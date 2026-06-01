// ABOUTME: Non-Linux passthrough stubs for the writable_paths fs-jail (issue #272).
// ABOUTME: ProbeLandlock returns ErrLandlockUnavailable; other stubs no-op or hard-error.

//go:build !linux

package exec

import (
	"os"
	"os/exec"
)

// ProbeLandlock on non-Linux always reports Landlock as unavailable.
// The codergen handler refuses to start a session with non-empty
// WritablePaths on this error — there is no silent fallback to
// unbounded behavior.
func ProbeLandlock() error {
	return ErrLandlockUnavailable
}

// WrapBashCmd on non-Linux is a passthrough. The codergen handler only
// installs CommandWrapper after ProbeLandlock returns nil, so this is
// effectively unreachable in production — we keep the symbol so the
// package compiles cross-platform and a stray caller doesn't trigger
// a build failure.
func WrapBashCmd(cmd *exec.Cmd, anchor string, writable []string) *exec.Cmd {
	return cmd
}

// OpenForWrite on non-Linux returns ErrLandlockUnavailable. Same
// reachability argument as WrapBashCmd — the codergen handler gates
// WriteOpener installation on ProbeLandlock success. If a stray caller
// invokes this, fail closed rather than silently bypass the jail.
func OpenForWrite(anchor, relPath string, perm os.FileMode) (*os.File, error) {
	return nil, ErrLandlockUnavailable
}

// RunJailExec on non-Linux is a hard error. The __jail-exec subcommand
// is dispatched by cmd/tracker/main.go before cobra parses argv; on a
// non-Linux host that path is unreachable in production because the
// codergen handler refuses to install the wrap that would invoke it.
// If a manual invocation somehow reaches it, exit non-zero with a clear
// message rather than no-op.
func RunJailExec(args []string) int {
	_, _ = os.Stderr.WriteString("tracker __jail-exec: Landlock not supported on this platform (issue #272)\n")
	return 1
}
