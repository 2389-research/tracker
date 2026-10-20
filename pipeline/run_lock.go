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

// ErrRunLocked is returned by AcquireRunLock when another live tracker process
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

// AcquireRunLock takes the run's exclusive lock: a file created with O_EXCL
// that holds this process's PID. A lock whose PID is no longer running (a
// crashed or killed process) is replaced. The returned release removes the
// lock; it is safe to call more than once.
func AcquireRunLock(runID string) (release func(), err error) {
	path, err := SecureRunLockPath(runID)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("run lock: %w", err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		if release, err := tryRunLock(path); release != nil || err != nil {
			return release, err
		}
	}
	return nil, fmt.Errorf("%w (lock %s keeps reappearing)", ErrRunLocked, path)
}

// tryRunLock makes one attempt at the lock: a release on success, ErrRunLocked
// when a live process holds it, or (nil, nil) after removing a stale lock so
// the caller can retry.
func tryRunLock(path string) (func(), error) {
	ok, err := createLockFile(path)
	if err != nil {
		return nil, err
	}
	if ok {
		var once sync.Once
		return func() { once.Do(func() { _ = os.Remove(path) }) }, nil
	}
	if pid, held := lockHolder(path); held {
		return nil, fmt.Errorf("%w (pid %d)", ErrRunLocked, pid)
	}
	_ = os.Remove(path) // stale: its process is gone
	return nil, nil
}

// createLockFile creates path exclusively with this process's PID. ok is
// false when the file already exists.
func createLockFile(path string) (ok bool, err error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|snapshotNoFollow, 0o600)
	if errors.Is(err, os.ErrExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("run lock: %w", err)
	}
	_, werr := f.WriteString(strconv.Itoa(os.Getpid()))
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		_ = os.Remove(path)
		return false, fmt.Errorf("run lock: %w", werr)
	}
	return true, nil
}

// lockHolder reads the lock's PID and reports whether that process is alive.
// An unreadable or malformed lock counts as stale.
func lockHolder(path string) (int, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 0 {
		return 0, false
	}
	return pid, processAlive(pid)
}
