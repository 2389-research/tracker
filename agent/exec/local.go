//go:build !windows

// ABOUTME: LocalEnvironment implements ExecutionEnvironment for local filesystem and process execution.
// ABOUTME: Enforces path containment within the working directory to prevent traversal attacks.
package exec

import (
	"bytes"
	"context"
	"fmt"
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
func (e *LocalEnvironment) WriteFile(ctx context.Context, path string, content string) error {
	abs, err := e.safePath(path)
	if err != nil {
		return err
	}

	dir := filepath.Dir(abs)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	return os.WriteFile(abs, []byte(content), 0644)
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
	// Override the default WaitDelay-based kill with process group kill.
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	// After killing, give pipes a few seconds to drain before force-closing.
	// Without this, cmd.Run() can block forever if a child process inherited
	// stdout/stderr and the SIGKILL didn't close them quickly enough.
	cmd.WaitDelay = 5 * time.Second

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

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

// limitedBuffer caps the amount of data that can be written. When the limit
// is reached, excess data is silently discarded and the truncated flag is set.
type limitedBuffer struct {
	mu        sync.Mutex
	buf       bytes.Buffer
	limit     int
	truncated bool
}

func (lb *limitedBuffer) Write(p []byte) (int, error) {
	lb.mu.Lock()
	defer lb.mu.Unlock()
	remaining := lb.limit - lb.buf.Len()
	if remaining <= 0 {
		lb.truncated = true
		return len(p), nil
	}
	if len(p) > remaining {
		lb.truncated = true
		lb.buf.Write(p[:remaining])
		return len(p), nil // report full length to avoid io.ErrShortWrite
	}
	return lb.buf.Write(p)
}

func (lb *limitedBuffer) String() string {
	lb.mu.Lock()
	defer lb.mu.Unlock()
	s := lb.buf.String()
	if lb.truncated {
		s += fmt.Sprintf("\n...(output truncated at %d bytes)", lb.limit)
	}
	return s
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
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = 5 * time.Second

	if len(env) > 0 && env[0] != nil {
		cmd.Env = env[0]
	}

	if outputLimit <= 0 {
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err := cmd.Run()
		result := CommandResult{Stdout: stdout.String(), Stderr: stderr.String()}
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

	stdoutBuf := &limitedBuffer{limit: outputLimit}
	stderrBuf := &limitedBuffer{limit: outputLimit}
	cmd.Stdout = stdoutBuf
	cmd.Stderr = stderrBuf

	err := cmd.Run()
	result := CommandResult{Stdout: stdoutBuf.String(), Stderr: stderrBuf.String()}
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
