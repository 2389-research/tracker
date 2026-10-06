//go:build !windows

// ABOUTME: POSIX process-liveness probe for ownership-aware container cleanup (#598).
package main

import (
	"errors"
	"syscall"
)

// processAlive reports whether pid names a live process on this host. Signal 0
// probes for existence without delivering a signal; EPERM means the process
// exists but is owned by another user — still alive.
func processAlive(pid int) bool {
	killErr := syscall.Kill(pid, 0)
	return killErr == nil || errors.Is(killErr, syscall.EPERM)
}
