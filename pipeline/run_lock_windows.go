//go:build windows

// ABOUTME: Run-lock primitives for Windows: a non-blocking exclusive LockFileEx,
// ABOUTME: which the OS releases when the holding process exits.
package pipeline

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// lockByteOffsetHigh locks the single byte at offset 1<<62, well past the holder
// PID that AcquireRunLock writes at offset 0. Windows denies a second handle's
// read of an exclusively locked range, so locking offset 0 would make
// readLockHolder (which reads from offset 0) see no PID and always report
// "another process" — breaking TestRunLock_ConcurrentRefusedAndReleased (#734
// review). lock and unlock must use the same offset.
const lockByteOffsetHigh = 0x40000000 // high dword of 1<<62

func lockRegion() *windows.Overlapped {
	return &windows.Overlapped{OffsetHigh: lockByteOffsetHigh}
}

func lockFile(f *os.File) error {
	err := windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, lockRegion())
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return errLockHeld
	}
	return err
}

func unlockFile(f *os.File) error {
	return windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, lockRegion())
}
