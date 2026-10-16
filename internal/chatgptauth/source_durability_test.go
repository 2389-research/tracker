// ABOUTME: Tests for the token source's refresh-chain durability (#700): a rotation whose save fails
// ABOUTME: stays in memory until a save works, and a transient refresh failure keeps the current token.
package chatgptauth

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/2389-research/tracker/internal/diag"
)

// captureWarnings sends diag output to a buffer for the rest of the test.
func captureWarnings(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	diag.SetLogger(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { diag.SetLogger(nil) })
	return &buf
}

// blockSaves makes the token file's directory read-only, so Save can't
// create its temp file, and returns a func that makes it writable again. It
// creates the lock file first, since the source opens it before each
// refresh, and skips the test where a read-only directory doesn't stop
// writes (Windows, or a root user).
func blockSaves(t *testing.T, path string) (unblock func()) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("needs a read-only directory")
	}
	dir := filepath.Dir(path)
	if err := os.WriteFile(filepath.Join(dir, LockFileName), nil, 0o600); err != nil {
		t.Fatalf("create lock file: %v", err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("make %s read-only: %v", dir, err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	if probe, err := os.CreateTemp(dir, "probe"); err == nil {
		_ = probe.Close()
		t.Skip("a read-only directory doesn't stop writes here (root?)")
	}
	return func() {
		if err := os.Chmod(dir, 0o700); err != nil {
			t.Fatalf("make %s writable: %v", dir, err)
		}
	}
}

// assertNoTokens fails the test if any token value appears in the warnings.
func assertNoTokens(t *testing.T, warnings string, tokens ...string) {
	t.Helper()
	for _, tok := range tokens {
		if strings.Contains(warnings, tok) {
			t.Errorf("a warning carries token %q:\n%s", tok, warnings)
		}
	}
}

func TestTokenSourceKeepsRotatedTokensWhenSaveFails(t *testing.T) {
	srv := newFakeAuthServer(t)
	warnings := captureWarnings(t)
	now := time.Now()
	src, path := newTestSource(t, srv, signedInTokens(accessTokenExpiring(now.Add(4*time.Minute)), now.Add(-time.Hour)), now)
	unblock := blockSaves(t, path)

	// The server spends r-1 as soon as it answers, so the rotated tokens are
	// the only working sign-in even though they couldn't be saved.
	if gotAccess, _ := tokenOrFail(t, src); gotAccess != "a-2" {
		t.Fatalf("Token() = %q after a refresh whose save failed; want the rotated a-2", gotAccess)
	}
	if saved := loadTokens(t, path); saved.RefreshToken != "r-1" {
		t.Fatalf("token file refresh token = %q; the save should have failed", saved.RefreshToken)
	}
	if !strings.Contains(warnings.String(), "couldn't save the refreshed ChatGPT sign-in") {
		t.Fatalf("no warning about the failed save:\n%s", warnings)
	}

	// The file still holds the spent r-1; the next refresh must send r-2.
	if err := src.ForceRefresh(context.Background(), "a-2"); err != nil {
		t.Fatalf("ForceRefresh while the rotation is unsaved: %v", err)
	}
	if n := srv.refreshReusedCount(); n != 0 {
		t.Fatalf("server refused %d refreshes as reused: the source sent the file's spent r-1", n)
	}

	unblock()
	if gotAccess, _ := tokenOrFail(t, src); gotAccess != "a-3" {
		t.Fatalf("Token() = %q, want a-3", gotAccess)
	}
	if saved := loadTokens(t, path); saved.AccessToken != "a-3" || saved.RefreshToken != "r-3" {
		t.Fatalf("token file holds %q / %q once saves work; want a-3 / r-3", saved.AccessToken, saved.RefreshToken)
	}
	assertNoTokens(t, warnings.String(), "r-1", "r-2", "r-3", "a-2", "a-3")
}

func TestTokenSourceKeepsCurrentTokenWhileRefreshFailsTransiently(t *testing.T) {
	srv := newFakeAuthServer(t)
	srv.setRefreshFailure(http.StatusInternalServerError, `{"error":"server_error"}`)
	warnings := captureWarnings(t)
	now := time.Now()
	access := accessTokenExpiring(now.Add(4 * time.Minute))
	src, _ := newTestSource(t, srv, signedInTokens(access, now.Add(-time.Hour)), now)

	if gotAccess, _ := tokenOrFail(t, src); gotAccess != access {
		t.Fatalf("Token() during an auth-server failure = %q; want the current token, valid for 4 more minutes", gotAccess)
	}
	if !strings.Contains(warnings.String(), "couldn't refresh the ChatGPT sign-in") {
		t.Fatalf("no warning about the failed refresh:\n%s", warnings)
	}
	tokenOrFail(t, src)
	if n := srv.refreshCallCount(); n != 1 {
		t.Fatalf("refresh calls = %d after a second call inside the retry wait, want 1", n)
	}

	srv.setRefreshFailure(0, "")
	src.now = func() time.Time { return now.Add(refreshRetryAfter) }
	if gotAccess, _ := tokenOrFail(t, src); gotAccess != "a-2" {
		t.Fatalf("Token() after the retry wait = %q, want the refreshed a-2", gotAccess)
	}
	assertNoTokens(t, warnings.String(), access, "r-1")
}

func TestTokenSourceFailsWhenRefreshFailsAfterExpiry(t *testing.T) {
	srv := newFakeAuthServer(t)
	srv.setRefreshFailure(http.StatusInternalServerError, `{"error":"server_error"}`)
	now := time.Now()
	src, _ := newTestSource(t, srv, signedInTokens(accessTokenExpiring(now.Add(-time.Minute)), now.Add(-time.Hour)), now)

	if _, _, err := src.Token(context.Background()); err == nil {
		t.Fatal("Token() handed out an expired access token after its refresh failed")
	}
}

func TestTokenSourceReturnsPermanentFailureBeforeExpiry(t *testing.T) {
	srv := newFakeAuthServer(t)
	srv.setRefreshFailure(http.StatusUnauthorized, `{"error":{"code":"refresh_token_expired"}}`)
	now := time.Now()
	src, _ := newTestSource(t, srv, signedInTokens(accessTokenExpiring(now.Add(4*time.Minute)), now.Add(-time.Hour)), now)

	_, _, err := src.Token(context.Background())
	var rerr *RefreshError
	if !errors.As(err, &rerr) || !rerr.Permanent {
		t.Fatalf("Token() = %v, want the permanent *RefreshError: the sign-in has ended, so the user must sign in again", err)
	}
}
