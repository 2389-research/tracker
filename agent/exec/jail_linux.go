// ABOUTME: Linux implementation of the writable_paths fs-jail (issue #272).
// ABOUTME: ProbeLandlock verifies kernel supports Landlock ABI v3 (6.7+).

//go:build linux

package exec

import (
	"fmt"
	"os"
	"os/exec"

	"golang.org/x/sys/unix"
)

// ProbeLandlock verifies the host kernel supports Landlock ABI v3 (kernel
// 6.7+, June 2023). Called eagerly at session setup. Failure = refuse-to-start.
//
// ABI v3 brings LANDLOCK_ACCESS_FS_REFER (hardlinks across rulesets) and
// LANDLOCK_ACCESS_FS_TRUNCATE; both are needed for the spec's "Bash + children
// bounded" contract. Strict — no BestEffort fallback.
//
// Uses the non-destructive landlock_create_ruleset(NULL, 0,
// LANDLOCK_CREATE_RULESET_VERSION) probe. The VERSION flag causes the kernel to
// return the highest supported ABI version number rather than creating a ruleset
// FD, so this call has no side effects on the calling process.
func ProbeLandlock() error {
	abi, _, errno := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET, 0, 0, unix.LANDLOCK_CREATE_RULESET_VERSION)
	if errno != 0 {
		return fmt.Errorf("%w: landlock_create_ruleset probe failed: %v", ErrLandlockUnavailable, errno)
	}
	if int(abi) < 3 {
		return fmt.Errorf("%w: kernel supports Landlock ABI %d, need >= 3 (kernel 6.7+)",
			ErrLandlockUnavailable, int(abi))
	}
	return nil
}

// WrapBashCmd returns cmd unchanged on Linux. The actual enforcement happens
// via RunJailExec (self-re-exec into a Landlock-restricted child), which is
// wired by Task 10. This stub keeps the package compiling while Tasks 10-13
// are implemented.
func WrapBashCmd(cmd *exec.Cmd, anchor string, writable []string) *exec.Cmd {
	return cmd
}

// OpenForWrite on Linux is the Landlock-aware file opener implemented in
// Task 13. This stub is a placeholder — it fails closed to match the
// non-Linux behaviour until Task 13 provides the real openat2 implementation.
func OpenForWrite(anchor, relPath string, perm os.FileMode) (*os.File, error) {
	return nil, fmt.Errorf("%w: OpenForWrite not yet implemented on Linux (Task 13)", ErrLandlockUnavailable)
}

// RunJailExec is the self-re-exec target that applies Landlock restrictions
// and then exec's the real command. Implemented in Task 11. This stub exits
// non-zero until that task is complete.
func RunJailExec(args []string) int {
	_, _ = os.Stderr.WriteString("tracker __jail-exec: not yet implemented (Task 11)\n")
	return 1
}
