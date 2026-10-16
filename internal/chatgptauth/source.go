// ABOUTME: TokenSource hands out the current ChatGPT access token, refreshing it near expiry or after
// ABOUTME: a 401, one refresh at a time across goroutines (a mutex) and processes (a file lock).
package chatgptauth

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"time"
)

// refreshWindow is how close to its exp claim an access token gets
// refreshed (manager.rs:204).
const refreshWindow = 5 * time.Minute

// staleAfter is how old a refresh may get before an access token without a
// readable exp claim is refreshed anyway (manager.rs:203).
const staleAfter = 8 * 24 * time.Hour

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
	if needsRefresh(s.cached, s.now()) {
		if _, err := s.refreshLocked(ctx, s.cached.AccessToken); err != nil {
			return "", "", err
		}
	}
	return s.cached.AccessToken, s.cached.AccountID, nil
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
// lock and re-reads the token file, adopts the file's token when another
// holder has already replaced stale with a fresh one, and otherwise
// refreshes.
func (s *TokenSource) refreshLocked(ctx context.Context, stale string) (refreshed bool, err error) {
	unlock, err := lockFile(ctx, s.lockPath)
	if err != nil {
		return false, err
	}
	defer unlock()

	fresh, err := Load(s.path)
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
func (s *TokenSource) refreshFrom(ctx context.Context, fresh Tokens, now time.Time) error {
	if s.permanent != nil && fresh.RefreshToken == s.deadRefreshToken {
		return s.permanent
	}
	got, err := s.client.Refresh(ctx, fresh.RefreshToken)
	if err != nil {
		s.rememberIfPermanent(err, fresh.RefreshToken)
		return err
	}
	merged := fresh.overlay(got, now)
	if err := Save(s.path, merged); err != nil {
		return fmt.Errorf("save the refreshed ChatGPT sign-in: %w", err)
	}
	s.cached = merged
	return nil
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
