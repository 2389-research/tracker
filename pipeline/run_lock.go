// ABOUTME: Exclusive per-run lock in the secure state dir, so two tracker
// ABOUTME: processes never resume (or answer) the same run at once.
package pipeline

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// ErrRunLocked is returned by AcquireRunLock when another tracker process
// holds the run's lock.
var ErrRunLocked = errors.New("run is in use by another tracker process")

// SecureRunLockPath returns <secureBase>/<runID>/run.lock, beside the run's
// secure checkpoint.
func SecureRunLockPath(runID string) (string, error) {
	if err := validateRunID(runID); err != nil {
		return "", fmt.Errorf("secure run lock path: %w", err)
	}
	base, err := secureActivityLogBase()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, runID, "run.lock"), nil
}

// AcquireRunLock takes the run's exclusive lock: an OS file lock (flock /
// LockFileEx) on <secure dir>/<runID>/run.lock, held until release. The OS
// drops it when the holding process exits, however it exits, so a crashed or
// killed run never leaves a lock behind and there is no stale lock to take
// over. The file holds the holder's PID for the error message only. release
// is safe to call more than once.
func AcquireRunLock(runID string) (release func(), err error) {
	path, err := SecureRunLockPath(runID)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("run lock: %w", err)
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|snapshotNoFollow, 0o600)
	if err != nil {
		return nil, fmt.Errorf("run lock: %w", err)
	}
	if err := lockFile(f); err != nil {
		holder := readLockHolder(f)
		_ = f.Close()
		if errors.Is(err, errLockHeld) {
			return nil, fmt.Errorf("%w (%s)", ErrRunLocked, holder)
		}
		return nil, fmt.Errorf("run lock: %w", err)
	}
	_ = f.Truncate(0)
	_, _ = f.WriteAt([]byte(strconv.Itoa(os.Getpid())), 0)
	var once sync.Once
	return func() {
		once.Do(func() {
			_ = f.Truncate(0)
			_ = unlockFile(f)
			_ = f.Close()
		})
	}, nil
}

// errLockHeld is what lockFile returns when another process holds the lock.
var errLockHeld = errors.New("lock held")

// readLockHolder describes the lock's holder from the PID it wrote.
func readLockHolder(f *os.File) string {
	buf := make([]byte, 32)
	n, _ := f.ReadAt(buf, 0)
	if pid := strings.TrimSpace(string(buf[:n])); pid != "" {
		return "pid " + pid
	}
	return "another process"
}
