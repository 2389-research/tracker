//go:build windows

// ABOUTME: Windows refresh lock: a no-op, so refreshes serialize only inside one process
// ABOUTME: (TokenSource.mu); two processes sharing one token file on Windows can still race.
package chatgptauth

import "context"

// lockFile does nothing on Windows and always succeeds. TokenSource.mu
// still allows one refresh at a time inside a process; across processes
// two refreshes can race, and the loser's refresh token is rejected as
// reused (design §1 "Refresh").
func lockFile(context.Context, string) (unlock func(), err error) {
	return func() {}, nil
}
