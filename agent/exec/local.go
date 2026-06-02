//go:build !windows

// ABOUTME: LocalEnvironment implements ExecutionEnvironment for local filesystem and process execution.
// ABOUTME: Enforces path containment within the working directory to prevent traversal attacks.
package exec

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

// LocalEnvironment runs commands and accesses files on the local machine,
// scoped to a specific working directory.
type LocalEnvironment struct {
	workDir string

	// CommandWrapper, when non-nil, is applied to every *exec.Cmd that
	// ExecCommand and ExecCommandWithLimit construct, after all standard
	// fields (Dir, SysProcAttr, Cancel, WaitDelay) are set but before
	// the command runs. The writable_paths fs-jail (issue #272) uses this
	// to rewrite Bash invocations through tracker's __jail-exec self-re-exec,
	// applying Linux Landlock before the agent command runs.
	// Default nil — the environment behaves as before.
	CommandWrapper func(*exec.Cmd) *exec.Cmd

	// WriteOpener, when non-nil, replaces the os.WriteFile call in WriteFile.
	// Receives the absolute path (already validated by safePath) and the
	// file mode; returns an *os.File for writing. The writable_paths fs-jail
	// sets this to an openat2-backed opener that enforces RESOLVE_BENEATH +
	// RESOLVE_NO_SYMLINKS against a session-root file descriptor — the
	// kernel atomic-checks the chain, closing the parallel-branch symlink
	// race vector (spec D6).
	// Default nil — WriteFile uses os.WriteFile as before.
	WriteOpener func(abs string, perm os.FileMode) (*os.File, error)

	// Remover, when non-nil, replaces the os.Remove call in RemoveFile.
	// Receives the absolute path (already validated by safePath). The
	// writable_paths fs-jail (#272) sets this with the same exact-glob
	// check WriteOpener uses, so destructive operations (apply_patch's
	// delete and move-cleanup paths) are bounded to the declared globs
	// just like writes are.
	// Default nil — RemoveFile uses os.Remove as before.
	Remover func(abs string) error
}

// NewLocalEnvironment creates a LocalEnvironment rooted at workDir.
// The path is resolved to an absolute path on creation.
func NewLocalEnvironment(workDir string) *LocalEnvironment {
	abs, err := filepath.Abs(workDir)
	if err != nil {
		abs = workDir
	}
	return &LocalEnvironment{workDir: abs}
}

// WorkingDir returns the absolute path of the environment root.
func (e *LocalEnvironment) WorkingDir() string {
	return e.workDir
}

// safePath validates that a relative path resolves inside the working directory.
func (e *LocalEnvironment) safePath(rel string) (string, error) {
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("absolute paths not allowed: %s (use a relative path like %q instead)", rel, filepath.Base(rel))
	}

	joined := filepath.Join(e.workDir, rel)
	abs, err := filepath.Abs(joined)
	if err != nil {
		return "", err
	}

	if !strings.HasPrefix(abs, e.workDir+string(filepath.Separator)) && abs != e.workDir {
		return "", fmt.Errorf("path escapes working directory: %s", rel)
	}

	return abs, nil
}

// ReadFile reads a file relative to the working directory and returns its contents.
func (e *LocalEnvironment) ReadFile(ctx context.Context, path string) (string, error) {
	abs, err := e.safePath(path)
	if err != nil {
		return "", err
	}

	data, err := os.ReadFile(abs)
	if err != nil {
		return "", err
	}

	return string(data), nil
}

// WriteFile writes content to a file relative to the working directory,
// creating intermediate directories as needed.
//
// When WriteOpener is non-nil, the opener is solely responsible for both
// policy (e.g. writable_paths glob check) AND mkdir+open. The opener
// performs the policy check BEFORE any filesystem mutation so rejected
// writes leave no empty intermediate directories behind (#272 review,
// codex P2). The unjailed path (WriteOpener nil) does mkdir then
// os.WriteFile as before.
func (e *LocalEnvironment) WriteFile(ctx context.Context, path string, content string) error {
	abs, err := e.safePath(path)
	if err != nil {
		return err
	}

	if e.WriteOpener != nil {
		return writeViaOpener(e.WriteOpener, abs, []byte(content))
	}
	dir := filepath.Dir(abs)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	return os.WriteFile(abs, []byte(content), 0644)
}

// writeViaOpener performs the open → write → close sequence with short-write
// and Close error propagation. Matches os.WriteFile's contract: a short write
// surfaces as io.ErrShortWrite, and a Close error returned post-write (delayed
// fsync, NFS commit, etc.) replaces a nil write error rather than being
// swallowed by `defer f.Close()` (#275 review, Copilot local.go:125).
func writeViaOpener(opener func(string, os.FileMode) (*os.File, error), abs string, data []byte) (err error) {
	f, err := opener(abs, 0644)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := f.Close(); err == nil {
			err = cerr
		}
	}()
	n, werr := f.Write(data)
	if werr != nil {
		return werr
	}
	if n < len(data) {
		return io.ErrShortWrite
	}
	return nil
}

// RemoveFile deletes a file relative to the working directory. The
// writable_paths fs-jail (#272) hooks here via Remover so destructive
// operations (apply_patch's delete and move-cleanup paths) are bounded
// to the declared globs.
func (e *LocalEnvironment) RemoveFile(ctx context.Context, path string) error {
	abs, err := e.safePath(path)
	if err != nil {
		return err
	}
	if e.Remover != nil {
		return e.Remover(abs)
	}
	return os.Remove(abs)
}

// ExecCommand runs a command with the given arguments and timeout.
// Non-zero exit codes are returned in CommandResult without an error.
// An error is returned only for timeouts or execution failures.
func (e *LocalEnvironment) ExecCommand(ctx context.Context, command string, args []string, timeout time.Duration) (CommandResult, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Dir = e.workDir
	// Start the command in its own process group so we can kill the entire
	// group on timeout, preventing orphaned child processes (e.g. long-running
	// servers started by the shell command).
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// Pdeathsig=SIGKILL on Linux: child dies when this process dies. Closes
	// the orphan-accumulation hole that fork-bombed dev hosts during #272
	// (132 live __jail-exec orphans, load avg 74). No-op on macOS — see
	// parent_death_other.go.
	applyParentDeathSig(cmd)
	// Override the default WaitDelay-based kill with process group kill.
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	// After killing, give pipes a few seconds to drain before force-closing.
	// Without this, cmd.Run() can block forever if a child process inherited
	// stdout/stderr and the SIGKILL didn't close them quickly enough.
	cmd.WaitDelay = 5 * time.Second

	if e.CommandWrapper != nil {
		// Enforce the in-place mutation contract: a wrapper that returns
		// a different *exec.Cmd would silently drop the Cancel,
		// WaitDelay, and SysProcAttr (including Pdeathsig + Setpgid)
		// fields the orphan-reaper defense (#272 commit e257d02) relies
		// on. The jail's WrapBashCmd mutates cmd in place; reject any
		// other shape rather than re-apply the fields on the new cmd
		// (which would re-open the very orphan-leak window the wrapper
		// is meant to preserve) — #275 review, Copilot local.go:178.
		wrapped := e.CommandWrapper(cmd)
		if wrapped != cmd {
			return CommandResult{}, fmt.Errorf("CommandWrapper must mutate cmd in place and return the same *exec.Cmd; got different pointer (would silently drop Cancel/WaitDelay/SysProcAttr including Pdeathsig)")
		}
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// Pin the calling goroutine to its OS thread for the lifetime of Run.
	// Defensive pairing with applyParentDeathSig (Linux-only). Modern
	// kernels deliver PDEATHSIG on parent process exit, so the lock is
	// not strictly required for correctness — see parent_death_linux.go
	// for the full rationale and historical context.
	unlock := pinCallingThreadForParentDeath()
	defer unlock()

	err := cmd.Run()
	reapProcessGroup(cmd)

	result := CommandResult{
		Stdout: stdout.String(),
		Stderr: stderr.String(),
	}

	if err != nil {
		if ctx.Err() != nil {
			return result, fmt.Errorf("command timed out after %v", timeout)
		}
		if exitErr, ok := err.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
			return result, nil
		}
		return result, err
	}

	return result, nil
}

// tailBuffer keeps the last `limit` bytes written to it. Excess bytes from
// the head of the stream are silently discarded; the tail is preserved.
// Used for capturing subprocess output where the trailing region carries
// the routing-relevant signal (a shell script that emits a routing marker
// at end of stream — see issue #208). Concurrent-safe via an internal
// mutex.
//
// Memory is bounded at `limit` bytes; per-byte amortized cost is O(1).
// The buffer is implemented as a fixed-size ring with a write index that
// wraps around once `limit` bytes have been written.
type tailBuffer struct {
	mu      sync.Mutex
	buf     []byte // fixed-size, allocated lazily on first Write
	limit   int
	pos     int   // next write index in [0, limit)
	wrapped bool  // true once total bytes written >= limit (head bytes dropped)
	total   int64 // total bytes ever Write'd, for accurate dropped-byte count
}

func newTailBuffer(limit int) *tailBuffer {
	return &tailBuffer{limit: limit}
}

func (tb *tailBuffer) Write(p []byte) (int, error) {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	n := len(p)
	if n == 0 {
		return 0, nil
	}
	if tb.limit <= 0 {
		// Defensive: callers should not construct a tailBuffer with non-positive
		// limit. Treat as discard so io.ErrShortWrite is not raised.
		tb.total += int64(n)
		return n, nil
	}
	if tb.buf == nil {
		tb.buf = make([]byte, tb.limit)
	}
	tb.total += int64(n)

	// Fast path for a single write larger than the ring: only the trailing
	// `limit` bytes of this write matter.
	if n >= tb.limit {
		copy(tb.buf, p[n-tb.limit:])
		tb.pos = 0
		tb.wrapped = true
		return n, nil
	}

	// Common path: copy `n` bytes starting at pos, wrapping if needed.
	end := tb.pos + n
	if end <= tb.limit {
		copy(tb.buf[tb.pos:], p)
		tb.pos = end
		if tb.pos == tb.limit {
			tb.pos = 0
			tb.wrapped = true
		}
	} else {
		first := tb.limit - tb.pos
		copy(tb.buf[tb.pos:], p[:first])
		copy(tb.buf, p[first:])
		tb.pos = n - first
		tb.wrapped = true
	}
	return n, nil
}

func (tb *tailBuffer) String() string {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	if tb.buf == nil || tb.total == 0 {
		return ""
	}
	if !tb.wrapped {
		return string(tb.buf[:tb.pos])
	}
	// Wrapped: oldest kept byte is at pos, newest is at pos-1 (mod limit).
	out := make([]byte, tb.limit)
	copy(out, tb.buf[tb.pos:])
	copy(out[tb.limit-tb.pos:], tb.buf[:tb.pos])
	return string(out)
}

// Truncated reports whether the buffer has elided any head bytes — i.e.
// whether more than `limit` bytes were ever written.
func (tb *tailBuffer) Truncated() bool {
	tb.mu.Lock()
	defer tb.mu.Unlock()
	return tb.total > int64(tb.limit)
}

// BytesDropped reports how many head bytes were elided. Zero when the
// total written did not exceed `limit`.
func (tb *tailBuffer) BytesDropped() int {
	tb.mu.Lock()
	defer tb.mu.Unlock()
	if tb.total <= int64(tb.limit) {
		return 0
	}
	return int(tb.total - int64(tb.limit))
}

// ExecCommandWithLimit runs a command with output capped at outputLimit bytes per stream.
// If outputLimit <= 0, output is unbounded (same as ExecCommand).
// Optional env parameter sets the subprocess environment (nil = inherit parent).
func (e *LocalEnvironment) ExecCommandWithLimit(ctx context.Context, command string, args []string, timeout time.Duration, outputLimit int, env ...[]string) (CommandResult, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Dir = e.workDir
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// Pdeathsig=SIGKILL on Linux: child dies when this process dies. Closes
	// the orphan-accumulation hole that fork-bombed dev hosts during #272
	// (132 live __jail-exec orphans, load avg 74). No-op on macOS — see
	// parent_death_other.go.
	applyParentDeathSig(cmd)
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = 5 * time.Second

	if len(env) > 0 && env[0] != nil {
		cmd.Env = env[0]
	}

	if e.CommandWrapper != nil {
		// Same in-place-mutation contract as ExecCommand — keep both
		// entry points consistent so a future wrapper can't drop
		// Cancel/WaitDelay/SysProcAttr on one path while preserving them
		// on the other (#275 review, Copilot local.go:344).
		wrapped := e.CommandWrapper(cmd)
		if wrapped != cmd {
			return CommandResult{}, fmt.Errorf("CommandWrapper must mutate cmd in place and return the same *exec.Cmd; got different pointer (would silently drop Cancel/WaitDelay/SysProcAttr including Pdeathsig)")
		}
	}

	if outputLimit <= 0 {
		return e.runUnlimited(ctx, cmd, timeout)
	}
	return e.runLimited(ctx, cmd, timeout, outputLimit)
}

// runUnlimited runs cmd with unbounded output buffers and translates the error.
func (e *LocalEnvironment) runUnlimited(ctx context.Context, cmd *exec.Cmd, timeout time.Duration) (CommandResult, error) {
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	unlock := pinCallingThreadForParentDeath()
	defer unlock()
	err := cmd.Run()
	reapProcessGroup(cmd)
	result := CommandResult{Stdout: stdout.String(), Stderr: stderr.String()}
	return result, translateExecError(ctx, err, &result, timeout)
}

// runLimited runs cmd with tail-window output buffers and translates the
// error. When either stream overflows the per-stream cap, the head is
// dropped and the truncation flags on CommandResult are set so callers
// (e.g. the tool handler) can emit a structured truncation event without
// pattern-matching on an in-band sentinel string.
func (e *LocalEnvironment) runLimited(ctx context.Context, cmd *exec.Cmd, timeout time.Duration, outputLimit int) (CommandResult, error) {
	stdoutBuf := newTailBuffer(outputLimit)
	stderrBuf := newTailBuffer(outputLimit)
	cmd.Stdout = stdoutBuf
	cmd.Stderr = stderrBuf
	unlock := pinCallingThreadForParentDeath()
	defer unlock()
	err := cmd.Run()
	reapProcessGroup(cmd)
	result := CommandResult{
		Stdout:             stdoutBuf.String(),
		Stderr:             stderrBuf.String(),
		StdoutTruncated:    stdoutBuf.Truncated(),
		StdoutBytesDropped: stdoutBuf.BytesDropped(),
		StderrTruncated:    stderrBuf.Truncated(),
		StderrBytesDropped: stderrBuf.BytesDropped(),
	}
	return result, translateExecError(ctx, err, &result, timeout)
}

// reapProcessGroup sends SIGKILL to the process group after a command completes.
// This catches background daemons (e.g. ssh-agent) spawned by the shell that
// survive after the foreground process exits. The kill is best-effort — the
// process group may already be gone.
func reapProcessGroup(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	// Negative PID targets the entire process group.
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}

// translateExecError maps a cmd.Run error to a CommandResult exit code or a timeout error.
// Returns nil if err is nil, a timeout error if ctx is done, populates result.ExitCode on ExitError,
// or returns the error as-is for other failure types.
func translateExecError(ctx context.Context, err error, result *CommandResult, timeout time.Duration) error {
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return fmt.Errorf("command timed out after %v", timeout)
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		result.ExitCode = exitErr.ExitCode()
		return nil
	}
	return err
}

// Glob returns file paths matching a pattern relative to the working directory.
func (e *LocalEnvironment) Glob(ctx context.Context, pattern string) ([]string, error) {
	fullPattern := filepath.Join(e.workDir, pattern)
	matches, err := filepath.Glob(fullPattern)
	if err != nil {
		return nil, err
	}

	var rel []string
	for _, m := range matches {
		// Filter out matches that escape the working directory.
		if !strings.HasPrefix(m, e.workDir+string(filepath.Separator)) && m != e.workDir {
			continue
		}
		r, err := filepath.Rel(e.workDir, m)
		if err != nil {
			continue
		}
		rel = append(rel, r)
	}

	return rel, nil
}
