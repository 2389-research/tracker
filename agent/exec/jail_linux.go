// ABOUTME: Linux implementation of the writable_paths fs-jail (issue #272).
// ABOUTME: ProbeLandlock verifies kernel supports Landlock ABI v3 (6.7+).

//go:build linux

package exec

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/landlock-lsm/go-landlock/landlock"
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

// RunJailExec is the entry point for the `tracker __jail-exec` subcommand.
// Argv shape (already stripped of "__jail-exec" by cmd/tracker/main.go's
// dispatch in Task 12):
//
//	-- <anchor> <glob1> <glob2> ... -- <cmd> <args>...
//
// The function:
//  1. Parses argv into anchor + globs + command tail.
//  2. Computes Landlock RWDirs from the static-prefix ancestor of each glob
//     (per spec D2; Landlock is path-prefix on resolved paths, not glob-aware,
//     so we bound at the directory ancestor of each glob's literal prefix).
//  3. Applies Landlock ABI v3 to the current process. Strict — no BestEffort.
//  4. syscall.Exec's into the command tail with the parent's environment.
//     Landlock is preserved through exec; bash + all descendants are bounded.
//
// Returns the process exit code on failure; on success (post-exec), this
// function does not return. Exit codes:
//
//	2 = argv parse failure
//	3 = landlock_restrict_self failure
//	4 = exec failure
func RunJailExec(args []string) int {
	anchor, globs, cmdArgs, err := parseJailExecArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "tracker __jail-exec: %v\n", err)
		return 2
	}

	rwDirs := make([]string, 0, len(globs))
	for _, g := range globs {
		rwDirs = append(rwDirs, landlockDirForGlob(anchor, g))
	}

	// Resolve the binary path before applying Landlock so PATH lookup succeeds
	// while we still have unrestricted FS access. syscall.Exec does no PATH
	// lookup of its own.
	resolvedBin, err := exec.LookPath(cmdArgs[0])
	if err != nil {
		fmt.Fprintf(os.Stderr, "tracker __jail-exec: resolve %q: %v\n", cmdArgs[0], err)
		return 4
	}
	cmdArgs[0] = resolvedBin

	// Apply Landlock: read-only access to the entire filesystem (so the shell
	// binary, shared libs, etc. are accessible), plus read-write access to
	// the declared writable path roots. The RWDirs rule for the writable dirs
	// overrides the RODirs("/") restriction for those subtrees.
	if err := landlock.V3.RestrictPaths(
		landlock.RODirs("/"),
		landlock.RWDirs(rwDirs...),
	); err != nil {
		fmt.Fprintf(os.Stderr, "tracker __jail-exec: landlock_restrict_self: %v\n", err)
		return 3
	}

	// syscall.Exec replaces the process image. Landlock is preserved.
	if err := syscall.Exec(cmdArgs[0], cmdArgs, os.Environ()); err != nil {
		fmt.Fprintf(os.Stderr, "tracker __jail-exec: exec %q: %v\n", cmdArgs[0], err)
		return 4
	}
	return 0 // unreachable
}

// parseJailExecArgs splits argv into anchor, glob list, and command tail.
// Expected shape (WrapBashCmd's output, sans the binary path and __jail-exec):
//
//	-- <anchor> <glob1> ... <globN> -- <cmd> <args>...
//
// Returns descriptive errors so cmd/tracker/main.go's dispatch can surface
// argv malformations clearly during development.
func parseJailExecArgs(args []string) (anchor string, globs []string, cmdArgs []string, err error) {
	if len(args) < 1 || args[0] != "--" {
		return "", nil, nil, fmt.Errorf("invalid argv: missing leading -- separator")
	}
	args = args[1:] // drop leading --

	// Find the second -- separator.
	sep := -1
	for i, a := range args {
		if a == "--" {
			sep = i
			break
		}
	}
	if sep < 0 {
		return "", nil, nil, fmt.Errorf("invalid argv: missing command -- separator")
	}
	head := args[:sep]
	cmdArgs = args[sep+1:]
	if len(head) < 1 {
		return "", nil, nil, fmt.Errorf("invalid argv: missing anchor")
	}
	if len(cmdArgs) < 1 {
		return "", nil, nil, fmt.Errorf("invalid argv: missing command")
	}
	anchor = head[0]
	globs = head[1:]
	if len(globs) == 0 {
		return "", nil, nil, fmt.Errorf("invalid argv: missing globs")
	}
	return anchor, globs, cmdArgs, nil
}

// landlockDirForGlob returns the directory ancestor of the glob's static
// prefix, joined with the anchor. Per spec D2's two-tier semantic:
//
//	anchor=/run, glob="workspace/**"     → /run/workspace
//	anchor=/run, glob="workspace/out.md" → /run/workspace
//	anchor=/run, glob=".ai/sprints/**"   → /run/.ai/sprints
//	anchor=/run, glob="x.md"             → /run
//
// Landlock is path-prefix on directory resolutions, not glob-aware, so we
// must give it a directory. The in-process openat2 path (Task 13) enforces
// the exact glob semantics; this directory-level enforcement covers the
// Bash subprocess + descendants.
func landlockDirForGlob(anchor, g string) string {
	idx := strings.IndexAny(g, "*?[{")
	var prefix string
	if idx < 0 {
		prefix = g
	} else {
		prefix = g[:idx]
	}
	dir := filepath.Dir(prefix)
	if dir == "." {
		return anchor
	}
	return filepath.Join(anchor, dir)
}
