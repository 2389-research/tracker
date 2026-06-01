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

// WrapBashCmd rewrites cmd's argv to invoke `/proc/self/exe __jail-exec` with
// the writable_paths jail rules, then the original command after a `--`
// separator.
//
// The wrapped command runs in three stages:
//  1. tracker re-execs itself as `tracker __jail-exec`.
//  2. The __jail-exec child applies Landlock ABI v3 with the static-prefix
//     ancestor directories of each writable_paths glob.
//  3. The child syscall.Exec's into the original command (e.g. `sh -c <agentCmd>`),
//     replacing its image. Landlock is preserved through exec; bash + all
//     descendants are bounded.
//
// Argv layout — the two `--` separators are unambiguous boundaries that
// RunJailExec's parseJailExecArgs (Task 11) splits on:
//
//	/proc/self/exe __jail-exec -- <anchor> <glob1> ... <globN> -- <origArgs...>
//
// All other Cmd fields (Dir, Env, Stdin/Stdout/Stderr, SysProcAttr, ctx) are
// preserved by in-place mutation — the wrapper returns the same *Cmd.
func WrapBashCmd(cmd *exec.Cmd, anchor string, writable []string) *exec.Cmd {
	newArgs := make([]string, 0, 4+len(writable)+len(cmd.Args))
	newArgs = append(newArgs, "/proc/self/exe", "__jail-exec", "--")
	newArgs = append(newArgs, anchor)
	newArgs = append(newArgs, writable...)
	newArgs = append(newArgs, "--")
	newArgs = append(newArgs, cmd.Args...)

	cmd.Path = "/proc/self/exe"
	cmd.Args = newArgs
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
