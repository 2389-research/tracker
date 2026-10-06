//go:build !windows

// ABOUTME: Verify-after-edit loop (POSIX): resolves a verifier and runs commands in a
// ABOUTME: process group so the whole shell tree is reaped on cancellation.
package agent

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"time"

	execpkg "github.com/2389-research/tracker/agent/exec"
)

// verifier holds configuration for the verify-after-edit loop and runs it.
type verifier struct {
	cmd      string // focused verification command (never empty)
	broadCmd string // optional broad regression command (empty = skip)
	workDir  string
}

// newVerifier resolves the verify command and returns a verifier ready to use,
// or nil if verification is disabled or no command can be resolved.
func newVerifier(cfg SessionConfig) *verifier {
	if !cfg.VerifyAfterEdit {
		return nil
	}
	cmd := cfg.VerifyCommand
	if cmd == "" {
		cmd = detectVerifyCommand(cfg.WorkingDir)
	}
	if cmd == "" {
		return nil // no build system detected; skip verification silently
	}
	// cfg.WorkingDir is set from s.env.WorkingDir in codergen (via SessionConfig.WorkingDir),
	// so the verifier runs in the same directory as tool executions. If the session has no
	// explicit WorkingDir it defaults to "." (process cwd), matching the tool handler default.
	return &verifier{
		cmd:      cmd,
		broadCmd: cfg.VerifyBroadCommand,
		workDir:  cfg.WorkingDir,
	}
}

// resolveBreachVerifier returns a verifier for the verify-on-breach pass (#303),
// or nil when no EXPLICIT verify command is configured. Unlike newVerifier it
// ignores the VerifyAfterEdit gate (a breach should be able to rescue green work
// even when in-loop verification was off) but it deliberately does NOT fall back
// to detectVerifyCommand: only an author-specified command may grant a breach
// success, so a coarse/auto-detected suite can never silently advance incomplete
// work. broadCmd is intentionally empty — the breach check is a single focused
// pass.
func resolveBreachVerifier(cfg SessionConfig) *verifier {
	cmd := strings.TrimSpace(cfg.VerifyCommand)
	if cmd == "" {
		return nil
	}
	return &verifier{cmd: cmd, workDir: cfg.WorkingDir}
}

// run executes the two-phase verification: focused test first, then optional broad
// regression test. If the focused test fails, the broad test is skipped and the
// focused result is returned immediately. If both pass, the broad result is returned.
// A non-zero exit code is not an error — it is returned as passed=false with the
// actual exit code and output. A real execution error (binary not found, etc.)
// is returned as error.
func (v *verifier) run(ctx context.Context) (verifyResult, error) {
	// Phase 1: focused test.
	res, err := v.runCommand(ctx, v.cmd)
	if err != nil || !res.Passed {
		return res, err
	}

	// Phase 2: broad regression test (optional).
	if v.broadCmd == "" {
		return res, nil
	}
	return v.runCommand(ctx, v.broadCmd)
}

// runCommand executes a single verification command and returns a verifyResult.
// Output is capped at verifyOutputCap (tail kept) to prevent feeding large test logs to the
// LLM repair prompt.
func (v *verifier) runCommand(ctx context.Context, command string) (verifyResult, error) {
	if strings.TrimSpace(command) == "" {
		return verifyResult{Command: command}, fmt.Errorf("empty verify command")
	}

	// Run via sh -c so the shell handles quoting and glob expansion, matching
	// how tool_command is executed elsewhere in tracker.
	//nolint:gosec // command comes from config/auto-detection, not user-controlled LLM output
	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	cmd.Dir = v.workDir
	// The command runs tests the model wrote, so it gets the credential-filtered
	// environment, never Tracker's own (which holds the provider keys).
	cmd.Env = execpkg.CommandEnv()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = 5 * time.Second

	// CombinedOutput merges stdout+stderr safely — exec.Cmd uses separate goroutines
	// for each stream and bytes.Buffer is not safe for concurrent writes.
	// The cap is applied post-execution so we keep the tail (errors appear at the end).
	out, runErr := cmd.CombinedOutput()

	// Reap any background processes spawned by the shell (e.g. ssh-agent).
	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}

	// Apply size cap: keep the tail where errors typically appear.
	outStr := truncateTail(string(out), verifyOutputCap)

	if runErr != nil {
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			// Non-zero exit code: verification failed but command ran fine.
			// Return the real exit code so the repair prompt is accurate.
			return verifyResult{Passed: false, ExitCode: exitErr.ExitCode(), Output: outStr, Command: command}, nil
		}
		return verifyResult{Passed: false, ExitCode: -1, Output: outStr, Command: command}, runErr
	}
	return verifyResult{Passed: true, ExitCode: 0, Output: outStr, Command: command}, nil
}
