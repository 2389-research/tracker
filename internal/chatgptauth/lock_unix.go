//go:build !windows

// ABOUTME: Cross-process refresh lock on Unix: an exclusive flock on chatgpt-auth.lock beside the
// ABOUTME: token file, polled without blocking so that a canceled context ends the wait.
package chatgptauth

import (
	"context"
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// lockRetryInterval is how often lockFile retries a lock another process
// holds.
const lockRetryInterval = 50 * time.Millisecond

// lockFile takes an exclusive flock on the lock file at path, creating it
// at 0600 if needed, and returns the func that releases it. It never
// deletes the file: a deleted lock file lets a newcomer lock a fresh file
// while an older holder still holds the old one, and both refresh. The
// lock lives on its own file, not the token file, because Save replaces
// the token file by rename, and a lock on the old file would not cover
// the new one.
func lockFile(ctx context.Context, path string) (unlock func(), err error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open the ChatGPT refresh lock: %w", err)
	}
	if err := waitForFlock(ctx, f); err != nil {
		_ = f.Close()
		return nil, err
	}
	return func() {
		_ = unix.Flock(int(f.Fd()), unix.LOCK_UN)
		_ = f.Close()
	}, nil
}

// waitForFlock tries a non-blocking exclusive flock on f every
// lockRetryInterval until it gets the lock or ctx is done. A blocking
// flock would ignore ctx: nothing interrupts it once it waits.
func waitForFlock(ctx context.Context, f *os.File) error {
	ticker := time.NewTicker(lockRetryInterval)
	defer ticker.Stop()
	for {
		err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			return nil
		}
		if !errors.Is(err, unix.EWOULDBLOCK) {
			return fmt.Errorf("lock the ChatGPT refresh lock: %w", err)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for the ChatGPT refresh lock: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}
