//go:build !windows

// ABOUTME: processAlive for unix: signal 0 probes the PID without touching it.
// ABOUTME: EPERM means the process exists but belongs to another user.
package pipeline

import (
	"errors"
	"syscall"
)

func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
