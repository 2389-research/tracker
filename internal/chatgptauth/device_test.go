// ABOUTME: Tests for the device-code sign-in flow (design §1, research §B): the happy path, the
// ABOUTME: disabled-server and expiry/cancellation edges, interval parsing, and no token leakage.
package chatgptauth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestDeviceLoginHappyPath(t *testing.T) {
	srv := newFakeAuthServer(t)
	srv.setPendingPolls(2)

	c := NewAuthClient()
	c.Issuer = srv.URL()

	ctx := context.Background()
	dc, err := c.StartDeviceLogin(ctx)
	if err != nil {
		t.Fatalf("StartDeviceLogin: %v", err)
	}
	dc.Interval = 10 * time.Millisecond // R3: shorten the wire's whole-second wait for the test

	tokens, err := c.WaitForDeviceLogin(ctx, dc)
	if err != nil {
		t.Fatalf("WaitForDeviceLogin: %v", err)
	}

	if tokens.AccountID != "acct-1" {
		t.Errorf("AccountID = %q, want %q", tokens.AccountID, "acct-1")
	}
	if tokens.IDToken == "" || tokens.AccessToken == "" || tokens.RefreshToken == "" {
		t.Errorf("Tokens incomplete: %+v", tokens)
	}
	if tokens.LastRefresh.IsZero() {
		t.Error("LastRefresh not set")
	}

	form := srv.lastExchangeFormValues()
	if got := form.Get("client_id"); got != ClientID {
		t.Errorf("exchange client_id = %q, want %q", got, ClientID)
	}
	if got, want := form.Get("redirect_uri"), srv.URL()+"/deviceauth/callback"; got != want {
		t.Errorf("exchange redirect_uri = %q, want %q", got, want)
	}
	if got := form.Get("code_verifier"); got != "server-verifier-1" {
		t.Errorf("exchange code_verifier = %q, want %q", got, "server-verifier-1")
	}

	headers := srv.lastRequestHeaders()
	if ct := headers.Get("Content-Type"); ct != "application/x-www-form-urlencoded" {
		t.Errorf("exchange Content-Type = %q, want application/x-www-form-urlencoded", ct)
	}
	if ua := headers.Get("User-Agent"); !strings.HasPrefix(ua, "tracker/") {
		t.Errorf("exchange User-Agent = %q, want prefix %q", ua, "tracker/")
	}
}

func TestStartDeviceLoginDisabled(t *testing.T) {
	srv := newFakeAuthServer(t)
	srv.setUsercode404(true)

	c := NewAuthClient()
	c.Issuer = srv.URL()

	_, err := c.StartDeviceLogin(context.Background())
	if !errors.Is(err, ErrDeviceCodeDisabled) {
		t.Fatalf("StartDeviceLogin error = %v, want ErrDeviceCodeDisabled", err)
	}
}

func TestStartDeviceLoginParsesInterval(t *testing.T) {
	cases := []struct {
		name  string
		raw   *string // raw JSON fragment for "interval"; nil omits the field
		field string  // "" keeps the default "user_code" spelling
		want  time.Duration
	}{
		{name: "quoted string seconds", raw: strPtr(`"7"`), want: 7 * time.Second},
		{name: "bare numeric seconds", raw: strPtr(`3`), want: 3 * time.Second},
		{name: "absent defaults to five seconds", raw: nil, want: 5 * time.Second},
		{name: "usercode spelling is accepted", raw: strPtr(`4`), field: "usercode", want: 4 * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newFakeAuthServer(t)
			srv.setIntervalRaw(tc.raw)
			if tc.field != "" {
				srv.setUserCodeField(tc.field)
			}

			c := NewAuthClient()
			c.Issuer = srv.URL()

			dc, err := c.StartDeviceLogin(context.Background())
			if err != nil {
				t.Fatalf("StartDeviceLogin: %v", err)
			}
			if dc.Interval != tc.want {
				t.Errorf("Interval = %v, want %v", dc.Interval, tc.want)
			}
			if dc.UserCode != "WDJB-MJHT" {
				t.Errorf("UserCode = %q, want %q", dc.UserCode, "WDJB-MJHT")
			}
		})
	}
}

func TestWaitForDeviceLoginExpires(t *testing.T) {
	srv := newFakeAuthServer(t)
	srv.setPendingPolls(1 << 30) // always pending

	c := NewAuthClient()
	c.Issuer = srv.URL()
	c.DeviceTimeout = 50 * time.Millisecond

	dc, err := c.StartDeviceLogin(context.Background())
	if err != nil {
		t.Fatalf("StartDeviceLogin: %v", err)
	}
	dc.Interval = 10 * time.Millisecond // R3

	_, err = c.WaitForDeviceLogin(context.Background(), dc)
	if !errors.Is(err, ErrDeviceCodeExpired) {
		t.Fatalf("WaitForDeviceLogin error = %v, want ErrDeviceCodeExpired", err)
	}
}

func TestWaitForDeviceLoginHonorsCancel(t *testing.T) {
	srv := newFakeAuthServer(t)
	srv.setPendingPolls(1 << 30) // always pending; only cancellation ends this wait

	c := NewAuthClient()
	c.Issuer = srv.URL()
	// DeviceTimeout keeps NewAuthClient's 15-minute default so only
	// cancellation, never expiry, can end the wait within this test.

	dc, err := c.StartDeviceLogin(context.Background())
	if err != nil {
		t.Fatalf("StartDeviceLogin: %v", err)
	}
	dc.Interval = 10 * time.Millisecond // R3

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := c.WaitForDeviceLogin(ctx, dc)
		done <- err
	}()

	time.Sleep(50 * time.Millisecond) // let a few polls happen
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("WaitForDeviceLogin error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("WaitForDeviceLogin did not return within 1s of cancel")
	}

	countAtReturn := srv.pollCallCount()
	time.Sleep(100 * time.Millisecond)
	if got := srv.pollCallCount(); got != countAtReturn {
		t.Errorf("poll count rose after cancel: %d -> %d", countAtReturn, got)
	}
}

func TestDeviceLoginErrorsNeverCarryTokens(t *testing.T) {
	srv := newFakeAuthServer(t)
	srv.setExchangeOmit("id_token")

	c := NewAuthClient()
	c.Issuer = srv.URL()

	dc, err := c.StartDeviceLogin(context.Background())
	if err != nil {
		t.Fatalf("StartDeviceLogin: %v", err)
	}

	_, err = c.WaitForDeviceLogin(context.Background(), dc)
	if err == nil {
		t.Fatal("WaitForDeviceLogin: want an error for a response missing id_token, got nil")
	}

	msg := err.Error()
	for _, leaked := range []string{"fake-access-token-1", "fake-refresh-token-1"} {
		if strings.Contains(msg, leaked) {
			t.Errorf("error %q leaks token value %q", msg, leaked)
		}
	}
}

func strPtr(s string) *string { return &s }
