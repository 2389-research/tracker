// ABOUTME: TokenSource hands out the current ChatGPT access token, refreshing it near expiry or after
// ABOUTME: a 401, one refresh at a time across goroutines (a mutex) and processes (a file lock).
package chatgptauth

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"time"

	"github.com/2389-research/tracker/internal/diag"
)

// refreshWindow is how close to its exp claim an access token gets
// refreshed (manager.rs:204).
const refreshWindow = 5 * time.Minute

// staleAfter is how old a refresh may get before an access token without a
// readable exp claim is refreshed anyway (manager.rs:203).
const staleAfter = 8 * 24 * time.Hour

// refreshRetryAfter is how long Token keeps handing out a still-valid access
// token after a refresh failed transiently, before it tries again (#700).
const refreshRetryAfter = 30 * time.Second

// TokenSource hands out the access token and account ID from a token file,
// refreshing first when the token is due. ChatGPT refresh tokens are
// single-use and rotate, so exactly one caller may spend one: mu
// serializes callers inside this process, and on Unix a flock on
// LockFileName beside the token file serializes processes (lockFile is a
// no-op on Windows). Whoever takes both locks re-reads the file first and
// adopts a token another holder already refreshed instead of spending the
// same refresh token again. After a permanent failure (*RefreshError with
// Permanent set), every refresh returns that same error without a network
// call until the token file's refresh token changes — the user signed in
// again.
type TokenSource struct {
	path     string
	lockPath string
	client   *AuthClient
	now      func() time.Time

	mu     sync.Mutex
	cached Tokens
	// unsaved is set while cached holds a rotation the token file doesn't:
	// its save failed after the server had already spent the old refresh
	// token, so cached is the only working sign-in. Each call retries the
	// save, and refreshes start from cached, not the file (#700).
	unsaved bool
	// retryRefreshAt is when Token next tries a refresh after one failed
	// transiently while the access token was still valid (#700).
	retryRefreshAt time.Time
	// permanent is the last refresh failure that ended the sign-in, and
	// deadRefreshToken the refresh token it failed with: while the file
	// still holds that token, refreshing returns permanent without a
	// network call.
	permanent        *RefreshError
	deadRefreshToken string
}

// NewTokenSource returns a TokenSource for the token file at path whose
// refreshes go through client. It reads nothing until the first call.
func NewTokenSource(path string, client *AuthClient) *TokenSource {
	return &TokenSource{
		path:     path,
		lockPath: filepath.Join(filepath.Dir(path), LockFileName),
		client:   client,
		now:      time.Now,
	}
}

// Token returns the current access token and account ID, refreshing first
// when the token is due — so a token that expired while the machine slept
// is refreshed before its first use, not after a 401.
func (s *TokenSource) Token(ctx context.Context) (accessToken, accountID string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.cached.AccessToken == "" {
		tokens, err := Load(s.path)
		if err != nil {
			return "", "", err
		}
		s.cached = tokens
	}
	s.retrySaveLocked(ctx)
	if err := s.refreshIfDueLocked(ctx); err != nil {
		return "", "", err
	}
	return s.cached.AccessToken, s.cached.AccountID, nil
}

// refreshIfDueLocked refreshes the cached token when it is due; the caller
// holds s.mu. A transient failure while the access token is still valid is
// not an error: the token works until exp, so Token keeps handing it out and
// tries again after refreshRetryAfter, never later than exp (#700).
func (s *TokenSource) refreshIfDueLocked(ctx context.Context) error {
	now := s.now()
	if !needsRefresh(s.cached, now) || now.Before(s.retryRefreshAt) {
		return nil
	}
	_, err := s.refreshLocked(ctx, s.cached.AccessToken)
	if err == nil {
		return nil
	}
	exp, ok := usableUntil(s.cached, err, now)
	if !ok {
		return err
	}
	s.retryRefreshAt = now.Add(refreshRetryAfter)
	if exp.Before(s.retryRefreshAt) {
		s.retryRefreshAt = exp
	}
	diag.Warnf("[tracker] couldn't refresh the ChatGPT sign-in (%v); using the current access token, which expires at %s, and retrying in %s", err, exp.Format(time.TimeOnly), refreshRetryAfter)
	return nil
}

// usableUntil reports when t's access token expires and whether a refresh
// that failed with err leaves it usable until then: a transient failure
// does; a permanent one, a cancelled caller, an unreadable exp or an exp
// already past don't.
func usableUntil(t Tokens, err error, now time.Time) (time.Time, bool) {
	var rerr *RefreshError
	if errors.As(err, &rerr) && rerr.Permanent {
		return time.Time{}, false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return time.Time{}, false
	}
	exp, ok := AccessTokenExpiry(t.AccessToken)
	if !ok || !now.Before(exp) {
		return time.Time{}, false
	}
	return exp, true
}

// ForceRefresh replaces rejected, an access token the backend refused, with
// a new one. If the token file already holds a different, fresh token —
// another caller or process refreshed first — it adopts that token without
// a network call.
func (s *TokenSource) ForceRefresh(ctx context.Context, rejected string) error {
	_, err := s.forceRefresh(ctx, rejected)
	return err
}

// forceRefresh is ForceRefresh that also reports whether this call spent
// the refresh token (true) or adopted another holder's result (false).
func (s *TokenSource) forceRefresh(ctx context.Context, rejected string) (refreshed bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.refreshLocked(ctx, rejected)
}

// refreshLocked replaces stale; the caller holds s.mu. It takes the file
// lock and re-reads the token file (or, while a rotation is unsaved, uses
// s.cached), adopts that token when another holder has already replaced
// stale with a fresh one, and otherwise refreshes.
func (s *TokenSource) refreshLocked(ctx context.Context, stale string) (refreshed bool, err error) {
	unlock, err := lockFile(ctx, s.lockPath)
	if err != nil {
		return false, err
	}
	defer unlock()

	if s.unsaved {
		s.saveCachedLocked()
	}
	fresh, err := s.latestLocked()
	if err != nil {
		return false, err
	}
	now := s.now()
	if fresh.AccessToken != stale && !needsRefresh(fresh, now) {
		s.cached = fresh
		return false, nil
	}
	if err := s.refreshFrom(ctx, fresh, now); err != nil {
		return false, err
	}
	return true, nil
}

// refreshFrom spends fresh's refresh token and saves the merged result. A
// refresh token that already failed permanently is never sent again: the
// remembered failure comes back without a network call.
//
// A caller already cancelled sends nothing. Once sent, the exchange and the
// save ignore the caller's cancel (context.WithoutCancel; the AuthClient's
// own timeout still bounds the request): the server rotates the refresh
// token as soon as it answers, so a cancel that dropped the reply or
// skipped the save would lose the new token and force a fresh sign-in. For
// the same reason a save that fails keeps the rotation in memory (#700).
func (s *TokenSource) refreshFrom(ctx context.Context, fresh Tokens, now time.Time) error {
	if s.permanent != nil && fresh.RefreshToken == s.deadRefreshToken {
		return s.permanent
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	got, err := s.client.Refresh(context.WithoutCancel(ctx), fresh.RefreshToken)
	if err != nil {
		s.rememberIfPermanent(err, fresh.RefreshToken)
		return err
	}
	s.cached = fresh.overlay(got, now)
	s.saveCachedLocked()
	return nil
}

// retrySaveLocked writes a rotation whose save failed to the token file,
// under the file lock; the caller holds s.mu. A lock it can't take leaves
// the retry to the next call.
func (s *TokenSource) retrySaveLocked(ctx context.Context) {
	if !s.unsaved {
		return
	}
	unlock, err := lockFile(ctx, s.lockPath)
	if err != nil {
		return
	}
	defer unlock()
	s.saveCachedLocked()
}

// saveCachedLocked writes s.cached to the token file; the caller holds s.mu
// and the file lock. When the save fails, s.cached stays the working sign-in
// (the server has already spent the file's refresh token), marked unsaved
// so the next call tries again; the first failure warns (#700).
func (s *TokenSource) saveCachedLocked() {
	err := Save(s.path, s.cached)
	if err == nil {
		s.unsaved = false
		return
	}
	if !s.unsaved {
		diag.Warnf("[tracker] couldn't save the refreshed ChatGPT sign-in (%v); this process keeps using it and retries the save on each request, but if tracker exits before a save works, run `tracker setup` to sign in again", err)
	}
	s.unsaved = true
}

// latestLocked returns the newest tokens: s.cached while a rotation is
// unsaved, since the file still holds the spent refresh token; otherwise the
// token file, which another process may have refreshed.
func (s *TokenSource) latestLocked() (Tokens, error) {
	if s.unsaved {
		return s.cached, nil
	}
	return Load(s.path)
}

// rememberIfPermanent records err when it ends the sign-in, together with
// the refresh token that failed.
func (s *TokenSource) rememberIfPermanent(err error, refreshToken string) {
	var rerr *RefreshError
	if errors.As(err, &rerr) && rerr.Permanent {
		s.permanent, s.deadRefreshToken = rerr, refreshToken
	}
}

// overlay returns t with got's non-empty token fields laid over it and
// LastRefresh set to now. A refresh response may omit any field; an
// omitted refresh_token means the server did not rotate it. AccountID
// stays the one captured at sign-in, as in Codex (server.rs:856-861): t
// always has one, because Load refuses a token file without it.
func (t Tokens) overlay(got Tokens, now time.Time) Tokens {
	if got.IDToken != "" {
		t.IDToken = got.IDToken
	}
	if got.AccessToken != "" {
		t.AccessToken = got.AccessToken
	}
	if got.RefreshToken != "" {
		t.RefreshToken = got.RefreshToken
	}
	t.LastRefresh = now
	return t
}

// needsRefresh reports whether t's access token is due: its exp claim is
// at most refreshWindow away or already past, or — when the token has no
// readable exp — its last refresh is older than staleAfter
// (manager.rs:3001-3023).
func needsRefresh(t Tokens, now time.Time) bool {
	if exp, ok := AccessTokenExpiry(t.AccessToken); ok {
		return !now.Add(refreshWindow).Before(exp)
	}
	return now.Sub(t.LastRefresh) > staleAfter
}
