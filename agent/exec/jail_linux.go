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

// OpenForWrite opens (or creates + truncates) a file under anchor for writing,
// using openat2(2) with RESOLVE_BENEATH | RESOLVE_NO_SYMLINKS | RESOLVE_NO_MAGICLINKS.
// The kernel binds path resolution to anchorFD; symlink chains rejected at
// the syscall — no userspace TOCTOU window.
//
// Returns ErrPathEscape (wrapped) when the kernel returns EXDEV / ELOOP /
// EACCES indicating the resolved path is outside anchor.
//
// Used by LocalEnvironment.WriteOpener when SessionConfig.WritablePaths is
// non-empty. The codergen handler (Task 14) installs the configured
// OpenForWrite closure on the env. Closes the parallel-branch symlink race
// vector documented in spec D6.
func OpenForWrite(anchor, relPath string, perm os.FileMode) (*os.File, error) {
	// Reject absolute paths defensively — openat2's RESOLVE_BENEATH applies
	// to relative resolution after the anchor FD; an absolute path would
	// resolve to itself regardless of the anchor.
	if filepath.IsAbs(relPath) {
		return nil, fmt.Errorf("%w: absolute path %q rejected by OpenForWrite (use a workspace-relative path)",
			ErrPathEscape, relPath)
	}

	// Open the anchor dirfd.
	anchorFD, err := unix.Open(anchor, unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("open anchor %q: %w", anchor, err)
	}
	defer unix.Close(anchorFD)

	how := unix.OpenHow{
		Flags:   uint64(unix.O_WRONLY | unix.O_CREAT | unix.O_TRUNC | unix.O_CLOEXEC),
		Mode:    uint64(perm),
		Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS,
	}
	fd, err := unix.Openat2(anchorFD, relPath, &how)
	if err != nil {
		switch err {
		case unix.EXDEV, unix.ELOOP, unix.EACCES:
			return nil, fmt.Errorf("%w: openat2 %q under %q: %v",
				ErrPathEscape, relPath, anchor, err)
		}
		return nil, fmt.Errorf("openat2 %q under %q: %w", relPath, anchor, err)
	}
	return os.NewFile(uintptr(fd), filepath.Join(anchor, relPath)), nil
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

// SafeMkdirAll creates the directory tree rooted at anchor + relDir without
// following symlinks or procfs magic-links at any intermediate component.
// Each path component is resolved via
// openat2(RESOLVE_BENEATH | RESOLVE_NO_SYMLINKS | RESOLVE_NO_MAGICLINKS)
// against the running parent fd; missing components are created via mkdirat.
// A symlink at any intermediate path causes the resolution to fail with
// EXDEV/ELOOP, which surfaces as ErrPathEscape. Same Resolve flags as
// OpenForWrite so the in-process write seam and the on-the-side dir
// creation share one hardening contract (#275 review, Copilot jail_linux.go:282).
//
// Closes the #275 review gap where os.MkdirAll inside the WriteOpener
// closure would follow agent-placed symlinks before openat2 saw the leaf
// path (Copilot codergen_jail.go:92).
func SafeMkdirAll(anchor, relDir string, perm os.FileMode) error {
	anchorFD, err := unix.Open(anchor, unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("open anchor %q: %w", anchor, err)
	}
	defer unix.Close(anchorFD)

	parentFD := anchorFD
	cleanup := func() {
		if parentFD != anchorFD {
			unix.Close(parentFD)
		}
	}
	defer cleanup()

	for _, comp := range strings.Split(filepath.Clean(relDir), "/") {
		if comp == "" || comp == "." {
			continue
		}
		how := unix.OpenHow{
			Flags:   uint64(unix.O_PATH | unix.O_DIRECTORY | unix.O_CLOEXEC),
			Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS,
		}
		fd, err := unix.Openat2(parentFD, comp, &how)
		if err == nil {
			if parentFD != anchorFD {
				unix.Close(parentFD)
			}
			parentFD = fd
			continue
		}
		switch err {
		case unix.EXDEV, unix.ELOOP, unix.EACCES:
			return fmt.Errorf("%w: SafeMkdirAll %q under %q: %v",
				ErrPathEscape, relDir, anchor, err)
		case unix.ENOENT:
			// Component does not exist — create it then re-open.
		default:
			return fmt.Errorf("openat2 component %q under %q: %w", comp, anchor, err)
		}
		if err := unix.Mkdirat(parentFD, comp, uint32(perm.Perm())); err != nil && err != unix.EEXIST {
			return fmt.Errorf("mkdirat %q under %q: %w", comp, anchor, err)
		}
		// EEXIST: a concurrent creator won the race between the ENOENT
		// openat2 above and this mkdirat. Treat it like the ENOENT path and
		// re-open — the re-open below still uses RESOLVE_NO_SYMLINKS, so a
		// symlink planted by the racing creator is rejected, not followed.
		fd, err = unix.Openat2(parentFD, comp, &how)
		if err != nil {
			return fmt.Errorf("re-open %q under %q after mkdir: %w", comp, anchor, err)
		}
		if parentFD != anchorFD {
			unix.Close(parentFD)
		}
		parentFD = fd
	}
	return nil
}

// SafeRemove deletes the file at anchor + relPath without following symlinks
// or procfs magic-links at any intermediate path component. Uses openat2 to
// resolve the parent directory with
// RESOLVE_BENEATH | RESOLVE_NO_SYMLINKS | RESOLVE_NO_MAGICLINKS, then unlinkat
// on the final component. A symlink anywhere in the parent chain causes
// EXDEV/ELOOP which surfaces as ErrPathEscape. Same Resolve flags as
// OpenForWrite / SafeMkdirAll for consistent hardening across the jail's
// destructive operations (#275 review, Copilot jail_linux.go:346).
//
// Closes the #275 review gap where os.Remove inside env.Remover would follow
// agent-placed symlinks (Copilot codergen_jail.go:103).
func SafeRemove(anchor, relPath string) error {
	parentRel := filepath.Dir(relPath)
	name := filepath.Base(relPath)
	if name == "" || name == "." || name == "/" {
		return fmt.Errorf("%w: SafeRemove %q under %q: empty leaf",
			ErrPathEscape, relPath, anchor)
	}

	anchorFD, err := unix.Open(anchor, unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("open anchor %q: %w", anchor, err)
	}
	defer unix.Close(anchorFD)

	parentFD := anchorFD
	if parentRel != "." && parentRel != "" {
		how := unix.OpenHow{
			Flags:   uint64(unix.O_PATH | unix.O_DIRECTORY | unix.O_CLOEXEC),
			Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS,
		}
		fd, err := unix.Openat2(anchorFD, parentRel, &how)
		if err != nil {
			switch err {
			case unix.EXDEV, unix.ELOOP, unix.EACCES:
				return fmt.Errorf("%w: SafeRemove parent %q under %q: %v",
					ErrPathEscape, parentRel, anchor, err)
			}
			return fmt.Errorf("openat2 parent %q under %q: %w", parentRel, anchor, err)
		}
		defer unix.Close(fd)
		parentFD = fd
	}
	if err := unix.Unlinkat(parentFD, name, 0); err != nil {
		return fmt.Errorf("unlinkat %q under %q: %w", relPath, anchor, err)
	}
	return nil
}
