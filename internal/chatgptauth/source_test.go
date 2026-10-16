// ABOUTME: Tests for TokenSource: refreshing near or past expiry, the eight-day fallback, keeping an
// ABOUTME: unrotated refresh token, and one refresh — or one permanent failure — per rejected token.
package chatgptauth

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/2389-research/tracker/internal/testutil"
)

// signedInTokens is a complete sign-in for account acct-1 holding access
// and r-1, the refresh token the fake auth server accepts first.
func signedInTokens(access string, lastRefresh time.Time) Tokens {
	return Tokens{
		IDToken:      fakeIDToken("acct-1", "plus"),
		AccessToken:  access,
		RefreshToken: "r-1",
		AccountID:    "acct-1",
		LastRefresh:  lastRefresh,
	}
}

// accessTokenExpiring mints an access-token JWT whose exp claim is exp.
func accessTokenExpiring(exp time.Time) string {
	return fakeJWT(map[string]any{"exp": exp.Unix()})
}

// newTestSource saves tok as a new token file and returns a TokenSource for
// it whose client talks to srv and whose clock stands still at now, plus
// the token file's path.
func newTestSource(t *testing.T, srv *fakeAuthServer, tok Tokens, now time.Time) (*TokenSource, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), FileName)
	if err := Save(path, tok); err != nil {
		t.Fatalf("Save: %v", err)
	}
	client := NewAuthClient()
	client.Issuer = srv.URL()
	src := NewTokenSource(path, client)
	src.now = func() time.Time { return now }
	return src, path
}

// loadTokens reads the token file back, failing the test if it can't.
func loadTokens(t *testing.T, path string) Tokens {
	t.Helper()
	tok, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return tok
}

// tokenOrFail calls src.Token and fails the test on an error.
func tokenOrFail(t *testing.T, src *TokenSource) (accessToken, accountID string) {
	t.Helper()
	accessToken, accountID, err := src.Token(context.Background())
	if err != nil {
		t.Fatalf("Token: %v", err)
	}
	return accessToken, accountID
}

func TestTokenSourceReturnsFreshTokenWithoutRefresh(t *testing.T) {
	srv := newFakeAuthServer(t)
	now := time.Now()
	access := accessTokenExpiring(now.Add(time.Hour))
	src, _ := newTestSource(t, srv, signedInTokens(access, now.Add(-time.Hour)), now)

	gotAccess, gotAccount := tokenOrFail(t, src)

	if gotAccess != access || gotAccount != "acct-1" {
		t.Errorf("Token() = %q, %q; want the stored access token and acct-1", gotAccess, gotAccount)
	}
	if n := srv.refreshCallCount(); n != 0 {
		t.Errorf("refresh calls = %d, want 0 for a token an hour from expiry", n)
	}
}

func TestTokenSourceRefreshesNearExpiry(t *testing.T) {
	srv := newFakeAuthServer(t)
	now := time.Now()
	src, path := newTestSource(t, srv, signedInTokens(accessTokenExpiring(now.Add(4*time.Minute)), now.Add(-time.Hour)), now)

	gotAccess, gotAccount := tokenOrFail(t, src)

	if gotAccess != "a-2" || gotAccount != "acct-1" {
		t.Errorf("Token() = %q, %q; want a-2 and acct-1", gotAccess, gotAccount)
	}
	if n := srv.refreshCallCount(); n != 1 {
		t.Errorf("refresh calls = %d, want 1 for a token four minutes from expiry", n)
	}
	saved := loadTokens(t, path)
	if saved.AccessToken != "a-2" || saved.RefreshToken != "r-2" {
		t.Errorf("token file holds access %q, refresh %q; want the rotated a-2, r-2", saved.AccessToken, saved.RefreshToken)
	}
	if !saved.LastRefresh.Equal(now) {
		t.Errorf("token file LastRefresh = %v, want the refresh time %v", saved.LastRefresh, now)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat token file: %v", err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Errorf("token file mode = %04o after refresh, want 0600", perm)
		}
	}
}

// TestTokenSourceRefreshesExpiredTokenBeforeFirstUse is Review Focus 1: a
// laptop asleep for days wakes with an access token that has already
// expired, and the first Token call refreshes instead of handing it out.
func TestTokenSourceRefreshesExpiredTokenBeforeFirstUse(t *testing.T) {
	srv := newFakeAuthServer(t)
	now := time.Now()
	src, _ := newTestSource(t, srv, signedInTokens(accessTokenExpiring(now.Add(-time.Hour)), now.Add(-72*time.Hour)), now)

	gotAccess, _ := tokenOrFail(t, src)

	if gotAccess != "a-2" {
		t.Errorf("Token() access = %q, want a-2", gotAccess)
	}
	if n := srv.refreshCallCount(); n != 1 {
		t.Errorf("refresh calls = %d, want exactly 1", n)
	}
}

func TestTokenSourceKeepsRefreshTokenWhenNotRotated(t *testing.T) {
	srv := newFakeAuthServer(t)
	srv.setRefreshKeepsToken(true)
	now := time.Now()
	src, path := newTestSource(t, srv, signedInTokens(accessTokenExpiring(now.Add(-time.Minute)), now.Add(-time.Hour)), now)

	gotAccess, _ := tokenOrFail(t, src)

	saved := loadTokens(t, path)
	if gotAccess != "a-2" || saved.AccessToken != "a-2" {
		t.Errorf("Token() access = %q, file access = %q; want a-2 for both", gotAccess, saved.AccessToken)
	}
	if saved.RefreshToken != "r-1" {
		t.Errorf("token file refresh token = %q, want r-1 kept when the response carries none", saved.RefreshToken)
	}
}

func TestTokenSourceEightDayFallback(t *testing.T) {
	noExp := fakeJWT(map[string]any{"sub": "user-1"})
	cases := []struct {
		name       string
		age        time.Duration
		wantAccess string
		wantCalls  int
	}{
		{name: "nine days since the last refresh", age: 9 * 24 * time.Hour, wantAccess: "a-2", wantCalls: 1},
		{name: "one day since the last refresh", age: 24 * time.Hour, wantAccess: noExp, wantCalls: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newFakeAuthServer(t)
			now := time.Now()
			src, _ := newTestSource(t, srv, signedInTokens(noExp, now.Add(-tc.age)), now)

			gotAccess, _ := tokenOrFail(t, src)

			if gotAccess != tc.wantAccess {
				t.Errorf("Token() access = %q, want %q", gotAccess, tc.wantAccess)
			}
			if n := srv.refreshCallCount(); n != tc.wantCalls {
				t.Errorf("refresh calls = %d, want %d", n, tc.wantCalls)
			}
		})
	}
}

func TestForceRefreshSkipsWhenFileAlreadyNewer(t *testing.T) {
	srv := newFakeAuthServer(t)
	now := time.Now()
	// A recent LastRefresh makes the exp-less a-1 count as fresh.
	src, path := newTestSource(t, srv, signedInTokens("a-1", now), now)
	if gotAccess, _ := tokenOrFail(t, src); gotAccess != "a-1" {
		t.Fatalf("first Token() access = %q, want a-1", gotAccess)
	}

	// Another process refreshes first and rotates the file to a-2 / r-2.
	rotated := signedInTokens("a-2", now)
	rotated.RefreshToken = "r-2"
	if err := Save(path, rotated); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if err := src.ForceRefresh(context.Background(), "a-1"); err != nil {
		t.Fatalf("ForceRefresh: %v", err)
	}
	if n := srv.refreshCallCount(); n != 0 {
		t.Errorf("refresh calls = %d, want 0: the file already holds a newer token", n)
	}
	if gotAccess, _ := tokenOrFail(t, src); gotAccess != "a-2" {
		t.Errorf("Token() access = %q after ForceRefresh, want the file's a-2", gotAccess)
	}
}

// forceRefreshResult is one racing caller's outcome: ForceRefresh's error
// and, when that was nil, the access token Token returned next.
type forceRefreshResult struct {
	err    error
	access string
}

// raceForceRefresh releases n goroutines at once into
// src.ForceRefresh(ctx, "a-1"); each that succeeds then asks Token for the
// token it would retry with. The fake server holds the refresh until all n
// goroutines have entered ForceRefresh and a refresh is waiting at the
// server — witnessed, or the test fails — so the other callers are queued
// behind the refresh rather than arriving after it ended.
func raceForceRefresh(t *testing.T, srv *fakeAuthServer, src *TokenSource, n int) []forceRefreshResult {
	t.Helper()
	release := srv.holdRefreshes(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)

	start := make(chan struct{})
	var entered atomic.Int32
	var wg sync.WaitGroup
	results := make([]forceRefreshResult, n)
	for i := range n {
		wg.Go(func() {
			<-start
			entered.Add(1)
			err := src.ForceRefresh(ctx, "a-1")
			results[i].err = err
			if err == nil {
				results[i].access, _, results[i].err = src.Token(ctx)
			}
		})
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	close(start)

	witnessed, _ := testutil.Overlap(t, 5*time.Second, func() bool {
		if int(entered.Load()) == n && srv.refreshInFlightCount() >= 1 {
			return true
		}
		time.Sleep(time.Millisecond)
		return false
	})
	release()
	if !witnessed {
		t.Fatalf("race not witnessed within 5s: %d of %d callers entered ForceRefresh, %d refresh(es) waiting at the server, %d received",
			entered.Load(), n, srv.refreshInFlightCount(), srv.refreshCallCount())
	}
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatalf("callers still running 10s after the refresh was released; %d refresh(es) received", srv.refreshCallCount())
	}
	return results
}

// TestForceRefreshSameRejectedTokenRefreshesOnce is Review Focus 5: parallel
// branches all get a 401 for a-1 and call ForceRefresh at once. One refresh
// call serves them all, and every branch retries with the new token; a
// permanent failure reaches every branch from that one call.
func TestForceRefreshSameRejectedTokenRefreshesOnce(t *testing.T) {
	const callers = 8

	t.Run("every caller retries with the rotated token", func(t *testing.T) {
		srv := newFakeAuthServer(t)
		now := time.Now()
		src, _ := newTestSource(t, srv, signedInTokens("a-1", now), now)

		results := raceForceRefresh(t, srv, src, callers)

		retried := 0
		for _, r := range results {
			if r.err == nil && r.access == "a-2" {
				retried++
			}
		}
		if calls := srv.refreshCallCount(); calls != 1 || retried != callers {
			t.Errorf("refresh calls = %d (want 1); callers left holding a-2 = %d (want %d); results: %+v",
				calls, retried, callers, results)
		}
	})

	t.Run("a permanent failure reaches every caller", func(t *testing.T) {
		srv := newFakeAuthServer(t)
		srv.setRefreshFailure(http.StatusBadRequest, `{"error":{"code":"refresh_token_invalidated"}}`)
		now := time.Now()
		src, _ := newTestSource(t, srv, signedInTokens("a-1", now), now)

		results := raceForceRefresh(t, srv, src, callers)

		signInAgain := 0
		for _, r := range results {
			var rerr *RefreshError
			if errors.As(r.err, &rerr) && rerr.Permanent && strings.Contains(r.err.Error(), "tracker setup") {
				signInAgain++
			}
		}
		if calls := srv.refreshCallCount(); calls != 1 || signInAgain != callers {
			t.Errorf("refresh calls = %d (want 1); callers told to sign in again = %d (want %d); results: %+v",
				calls, signInAgain, callers, results)
		}
	})
}

// TestTokenSourcePermanentFailureHoldsUntilSignedInAgain pins the memory of
// a dead sign-in: while the file still holds the refresh token that failed,
// the source answers with that failure and no network call; once setup has
// written a new refresh token, refreshing works again.
func TestTokenSourcePermanentFailureHoldsUntilSignedInAgain(t *testing.T) {
	srv := newFakeAuthServer(t)
	srv.setRefreshFailure(http.StatusUnauthorized, `{"error":{"code":"refresh_token_expired"}}`)
	now := time.Now()
	src, path := newTestSource(t, srv, signedInTokens("a-1", now), now)
	ctx := context.Background()

	first := src.ForceRefresh(ctx, "a-1")
	var rerr *RefreshError
	if !errors.As(first, &rerr) || !rerr.Permanent {
		t.Fatalf("first ForceRefresh = %v, want a permanent *RefreshError", first)
	}
	again := src.ForceRefresh(ctx, "a-1")
	if again == nil || again.Error() != first.Error() {
		t.Errorf("second ForceRefresh = %v, want the same failure: %v", again, first)
	}
	if n := srv.refreshCallCount(); n != 1 {
		t.Fatalf("refresh calls = %d after two ForceRefresh calls, want 1: a dead refresh token is never sent twice", n)
	}

	// Setup signs in again: a new refresh token r-9 the server accepts,
	// and an access token that is itself due (no exp, refreshed 9 days ago).
	srv.setRefreshFailure(0, "")
	srv.setLiveRefreshToken("r-9")
	resigned := signedInTokens("a-8", now.Add(-9*24*time.Hour))
	resigned.RefreshToken = "r-9"
	if err := Save(path, resigned); err != nil {
		t.Fatalf("Save: %v", err)
	}

	if err := src.ForceRefresh(ctx, "a-1"); err != nil {
		t.Fatalf("ForceRefresh after signing in again: %v", err)
	}
	if n := srv.refreshCallCount(); n != 2 {
		t.Errorf("refresh calls = %d, want 2: the new refresh token goes to the server", n)
	}
	if gotAccess, _ := tokenOrFail(t, src); gotAccess != "a-2" {
		t.Errorf("Token() access = %q, want a-2 from the new sign-in's refresh", gotAccess)
	}
}

// TestTokenSourceRetriesAfterTransientFailure pins that only permanent
// failures are remembered: after a 5xx the next call goes back to the
// network and succeeds.
func TestTokenSourceRetriesAfterTransientFailure(t *testing.T) {
	srv := newFakeAuthServer(t)
	srv.setRefreshFailure(http.StatusInternalServerError, `{"error":"server_error"}`)
	now := time.Now()
	src, _ := newTestSource(t, srv, signedInTokens("a-1", now), now)
	ctx := context.Background()

	err := src.ForceRefresh(ctx, "a-1")
	var rerr *RefreshError
	if !errors.As(err, &rerr) || !rerr.Temporary() {
		t.Fatalf("ForceRefresh during a 500 = %v, want a temporary *RefreshError", err)
	}

	srv.setRefreshFailure(0, "")
	if err := src.ForceRefresh(ctx, "a-1"); err != nil {
		t.Fatalf("ForceRefresh after the outage: %v", err)
	}
	if n := srv.refreshCallCount(); n != 2 {
		t.Errorf("refresh calls = %d, want 2", n)
	}
	if gotAccess, _ := tokenOrFail(t, src); gotAccess != "a-2" {
		t.Errorf("Token() access = %q, want a-2", gotAccess)
	}
}

// TestTokenSourceRefreshSurvivesCallerCancel: the caller's context is
// cancelled while its refresh waits at the server. The server rotates the
// refresh token whether or not anyone reads the reply, so the exchange and
// the save must finish: losing the reply loses the new refresh token, and
// the next refresh is refused as reused, forcing a fresh sign-in.
func TestTokenSourceRefreshSurvivesCallerCancel(t *testing.T) {
	srv := newFakeAuthServer(t)
	release := srv.holdRefreshes(t)
	now := time.Now()
	src, path := newTestSource(t, srv, signedInTokens("a-1", now), now)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- src.ForceRefresh(ctx, "a-1") }()

	testutil.Eventually(t, 5*time.Second, time.Millisecond, func() bool {
		return srv.refreshInFlightCount() == 1
	}, "the refresh never reached the server")
	cancel()
	if ctx.Err() == nil {
		t.Fatal("the caller's context is not cancelled")
	}
	release()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("ForceRefresh = %v after a cancel mid-refresh, want nil: the refresh itself finished", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("ForceRefresh still running 10s after the refresh was released; %d refresh(es) received", srv.refreshCallCount())
	}
	saved := loadTokens(t, path)
	if saved.AccessToken != "a-2" || saved.RefreshToken != "r-2" {
		t.Errorf("token file holds access %q, refresh %q; want the rotated a-2, r-2", saved.AccessToken, saved.RefreshToken)
	}
	if calls, reused := srv.refreshCallCount(), srv.refreshReusedCount(); calls != 1 || reused != 0 {
		t.Errorf("refresh calls = %d, refresh_token_reused answers = %d; want 1 and 0", calls, reused)
	}
}

// TestTokenSourceCancelledCallerSendsNoRefresh: a caller whose context is
// already cancelled when its refresh would start gets the cancellation and
// spends nothing; only an exchange already under way outlives a cancel.
func TestTokenSourceCancelledCallerSendsNoRefresh(t *testing.T) {
	srv := newFakeAuthServer(t)
	now := time.Now()
	src, path := newTestSource(t, srv, signedInTokens("a-1", now), now)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := src.ForceRefresh(ctx, "a-1"); !errors.Is(err, context.Canceled) {
		t.Fatalf("ForceRefresh with a cancelled context = %v, want context.Canceled", err)
	}
	if n := srv.refreshCallCount(); n != 0 {
		t.Errorf("refresh calls = %d, want 0 for a caller that was already cancelled", n)
	}
	if saved := loadTokens(t, path); saved.RefreshToken != "r-1" {
		t.Errorf("token file refresh token = %q, want the untouched r-1", saved.RefreshToken)
	}
}
