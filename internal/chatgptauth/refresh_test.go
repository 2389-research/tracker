// ABOUTME: Tests for the refresh-token exchange (research §C): the JSON grant on the wire, and
// ABOUTME: classifying a failure as permanent or transient without ever repeating the token.
package chatgptauth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestRefreshSendsJSONGrant(t *testing.T) {
	srv := newFakeAuthServer(t)
	c := NewAuthClient()
	c.Issuer = srv.URL()

	got, err := c.Refresh(context.Background(), "r-1")
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}

	const wantBody = `{"grant_type":"refresh_token","client_id":"app_EMoamEEZ73f0CkXaXp7hrann","refresh_token":"r-1"}`
	if body := string(srv.lastRefreshRequestBody()); body != wantBody {
		t.Errorf("request body = %s, want %s", body, wantBody)
	}
	headers := srv.lastRequestHeaders()
	if ct := headers.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	if ua := headers.Get("User-Agent"); !strings.HasPrefix(ua, "tracker/") {
		t.Errorf("User-Agent = %q, want prefix %q", ua, "tracker/")
	}

	want := Tokens{IDToken: fakeIDToken("acct-1", "plus"), AccessToken: "a-2", RefreshToken: "r-2"}
	if got != want {
		t.Errorf("Refresh() = %+v, want only the fields the server returned: %+v", got, want)
	}
}

func TestRefreshClassifiesPermanentFailures(t *testing.T) {
	cases := []struct {
		name          string
		status        int
		body          string
		closeServer   bool // no response at all: a network failure
		wantPermanent bool
		wantCode      string
	}{
		// The three dead-token codes are permanent at 400 and at 401, in
		// each body shape the endpoint uses: "error" as a string, "error"
		// as an object with a code, and a top-level "code".
		{name: "expired 400, error string", status: 400, body: `{"error":"refresh_token_expired"}`, wantPermanent: true, wantCode: "refresh_token_expired"},
		{name: "reused 400, error object", status: 400, body: refreshReusedBody, wantPermanent: true, wantCode: "refresh_token_reused"},
		{name: "invalidated 400, top-level code", status: 400, body: `{"code":"refresh_token_invalidated"}`, wantPermanent: true, wantCode: "refresh_token_invalidated"},
		{name: "expired 401, error object", status: 401, body: `{"error":{"code":"refresh_token_expired"}}`, wantPermanent: true, wantCode: "refresh_token_expired"},
		{name: "reused 401, top-level code", status: 401, body: `{"code":"refresh_token_reused"}`, wantPermanent: true, wantCode: "refresh_token_reused"},
		{name: "invalidated 401, error string", status: 401, body: `{"error":"refresh_token_invalidated"}`, wantPermanent: true, wantCode: "refresh_token_invalidated"},
		{name: "401 without a code", status: 401, body: ``, wantPermanent: true},
		{name: "400 invalid_grant", status: 400, body: `{"error":"invalid_grant","error_description":"refresh token expired"}`, wantPermanent: true, wantCode: "invalid_grant"},
		{name: "code matched case-insensitively", status: 400, body: `{"error":"REFRESH_TOKEN_REUSED"}`, wantPermanent: true, wantCode: "refresh_token_reused"},
		{name: "blank error string falls through to code", status: 400, body: `{"error":" ","code":"refresh_token_expired"}`, wantPermanent: true, wantCode: "refresh_token_expired"},
		{name: "error object without a code falls through", status: 400, body: `{"error":{"message":"revoked"},"code":"refresh_token_invalidated"}`, wantPermanent: true, wantCode: "refresh_token_invalidated"},
		// A server that echoes the refresh token as its error code: the
		// unknown code is dropped, so the token never reaches Error().
		{name: "401 echoing the refresh token as its code", status: 401, body: `{"error":"r-1"}`, wantPermanent: true},
		{name: "400 with another code", status: 400, body: `{"error":"invalid_request"}`},
		{name: "500", status: 500, body: `{"error":"server_error"}`},
		{name: "200 with an unreadable body", status: 200, body: `<html>`},
		{name: "closed server", closeServer: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newFakeAuthServer(t)
			srv.setRefreshFailure(tc.status, tc.body)
			if tc.closeServer {
				srv.Close()
			}
			c := NewAuthClient()
			c.Issuer = srv.URL()

			_, err := c.Refresh(context.Background(), "r-1")

			var rerr *RefreshError
			if !errors.As(err, &rerr) {
				t.Fatalf("Refresh error = %v (%T), want a *RefreshError", err, err)
			}
			msg := err.Error()
			if strings.Contains(msg, "r-1") {
				t.Errorf("Error() = %q repeats the refresh token", msg)
			}
			if rerr.Permanent != tc.wantPermanent || rerr.Temporary() == tc.wantPermanent {
				t.Errorf("Permanent = %v, Temporary() = %v; want Permanent %v", rerr.Permanent, rerr.Temporary(), tc.wantPermanent)
			}
			if rerr.Code != tc.wantCode || rerr.Status != tc.status {
				t.Errorf("Code, Status = %q, %d; want %q, %d", rerr.Code, rerr.Status, tc.wantCode, tc.status)
			}
			checkRefreshMessage(t, msg, tc.wantPermanent, tc.wantCode, tc.status)
		})
	}
}

// TestRefreshNeverFollowsRedirects pins the fixed destination: the refresh
// token goes only to the issuer. Go re-sends a 307's request body to
// whatever host its Location names, so the client must not follow it; the
// 307 comes back as an ordinary transient failure instead.
func TestRefreshNeverFollowsRedirects(t *testing.T) {
	trapURL, trapHits := newTrapServer(t)
	srv := newFakeAuthServer(t)
	srv.setRefreshRedirect(http.StatusTemporaryRedirect, trapURL+"/oauth/token")
	c := NewAuthClient()
	c.Issuer = srv.URL()

	_, err := c.Refresh(context.Background(), "r-1")

	if n := trapHits.Load(); n != 0 {
		t.Errorf("redirect target received %d request(s), want 0: the refresh token left the issuer", n)
	}
	var rerr *RefreshError
	if !errors.As(err, &rerr) {
		t.Fatalf("Refresh error = %v (%T), want a *RefreshError", err, err)
	}
	if rerr.Permanent || rerr.Status != http.StatusTemporaryRedirect {
		t.Errorf("Permanent = %v, Status = %d; want a transient failure with status 307", rerr.Permanent, rerr.Status)
	}
	if strings.Contains(err.Error(), "r-1") {
		t.Errorf("Error() = %q repeats the refresh token", err.Error())
	}
}

// checkRefreshMessage checks that a permanent failure's message sends the
// user to `tracker setup` and names its reason — the code, or the HTTP
// status when there is none — and that a transient one does not, naming
// the HTTP error status when the server sent one.
func checkRefreshMessage(t *testing.T, msg string, permanent bool, code string, status int) {
	t.Helper()
	if !permanent {
		if !strings.HasPrefix(msg, "refreshing the ChatGPT sign-in failed: ") || strings.Contains(msg, "tracker setup") {
			t.Errorf("transient Error() = %q, want the refreshing-failed message without the sign-in-again advice", msg)
		}
		if status >= http.StatusBadRequest && !strings.Contains(msg, fmt.Sprintf("HTTP %d", status)) {
			t.Errorf("transient Error() = %q, want it to name HTTP %d", msg, status)
		}
		return
	}
	reason := code
	if reason == "" {
		reason = fmt.Sprintf("HTTP %d", status)
	}
	if !strings.Contains(msg, "tracker setup") || !strings.Contains(msg, "("+reason+")") {
		t.Errorf("permanent Error() = %q, want it to name (%s) and `tracker setup`", msg, reason)
	}
}
