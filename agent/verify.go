// ABOUTME: Verify-after-edit loop: auto-detects build system, runs tests after file edits, and injects repair prompts.
// ABOUTME: Transparent inner loop — verification turns do not count against session MaxTurns.
package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	// verifyOutputCap is the maximum bytes of verification output fed back to the LLM.
	// The tail is kept (most relevant errors appear at the end).
	verifyOutputCap = 4096

	// verifyRepairPrompt is injected when verification fails after an edit turn.
	verifyRepairPrompt = `Verification failed after your edits.

Command: %s
Exit code: %d
Output (truncated to 4KB):
%s

Please fix the failing test/lint issue, then I'll re-verify.`
)

// editToolNames is the set of tool names that modify files on disk.
// A turn that calls any of these triggers the verify-after-edit loop.
var editToolNames = map[string]bool{
	"write":         true,
	"edit":          true,
	"apply_patch":   true,
	"notebook_edit": true,
}

// isEditTool reports whether the named tool modifies files.
func isEditTool(name string) bool {
	return editToolNames[name]
}

// detectVerifyCommand scans workDir for build system markers and returns the
// appropriate test command. Priority order:
//  1. go.mod → "go test ./..."
//  2. Cargo.toml → "cargo test"
//  3. package.json → "npm test"
//  4. Makefile with "test:" target → "make test"
//  5. pytest.ini / pyproject.toml with [tool.pytest] section → "pytest"
//  6. "" (no detection)
func detectVerifyCommand(workDir string) string {
	checks := []struct {
		file string
		cmd  string
		pred func(path string) bool
	}{
		{"go.mod", "go test ./...", nil},
		{"Cargo.toml", "cargo test", nil},
		{"package.json", "npm test", nil},
		{"Makefile", "make test", hasMakeTestTarget},
		{"pytest.ini", "pytest", nil},
		{"pyproject.toml", "pytest", hasPytestSection},
	}

	for _, c := range checks {
		path := filepath.Join(workDir, c.file)
		if _, err := os.Stat(path); err != nil {
			continue // file does not exist
		}
		if c.pred != nil && !c.pred(path) {
			continue
		}
		return c.cmd
	}
	return ""
}

// makeTestTargetRe matches a "test:" target at the start of a line, avoiding
// false positives on targets like "unittest:" or "integration_test:".
var makeTestTargetRe = regexp.MustCompile(`(?m)^test\s*:`)

// hasMakeTestTarget returns true if the Makefile at path contains a "test:" target
// at the start of a line. The full file is read — Makefiles are typically small
// config files and a valid target might appear anywhere in the file.
func hasMakeTestTarget(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return makeTestTargetRe.Match(data)
}

// hasPytestSection returns true if the pyproject.toml contains any [tool.pytest*] section
// header (e.g. [tool.pytest] or [tool.pytest.ini_options]).
// The full file is read so that sections appearing after the first 1 KB are not missed.
func hasPytestSection(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return strings.Contains(string(data), "[tool.pytest")
}

// verifier holds configuration for the verify-after-edit loop and runs it.
type verifier struct {
	cmd     string // resolved verification command (never empty)
	workDir string
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
	return &verifier{cmd: cmd, workDir: cfg.WorkingDir}
}

// run executes the verification command and returns (passed, exitCode, output, error).
// A non-zero exit code is not an error — it is returned as passed=false with the
// actual exit code and output. A real execution error (binary not found, etc.)
// is returned as error. Output is capped at verifyOutputCap (tail kept) to prevent
// feeding large test logs to the LLM repair prompt.
func (v *verifier) run(ctx context.Context) (passed bool, exitCode int, output string, err error) {
	if strings.TrimSpace(v.cmd) == "" {
		return false, 0, "", fmt.Errorf("empty verify command")
	}

	// Run via sh -c so the shell handles quoting and glob expansion, matching
	// how tool_command is executed elsewhere in tracker.
	//nolint:gosec // command comes from config/auto-detection, not user-controlled LLM output
	cmd := exec.CommandContext(ctx, "sh", "-c", v.cmd)
	cmd.Dir = v.workDir
	// TODO: add cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} for process-group
	// cleanup of long-running test suites (consistent with tool handler). Deferred
	// because verify commands are author-controlled and typically short-lived.

	// CombinedOutput merges stdout+stderr safely — exec.Cmd uses separate goroutines
	// for each stream and bytes.Buffer is not safe for concurrent writes.
	// The cap is applied post-execution so we keep the tail (errors appear at the end).
	out, runErr := cmd.CombinedOutput()

	// Apply size cap: keep the tail where errors typically appear.
	outStr := truncateTail(string(out), verifyOutputCap)

	if runErr != nil {
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			// Non-zero exit code: verification failed but command ran fine.
			// Return the real exit code so the repair prompt is accurate.
			return false, exitErr.ExitCode(), outStr, nil
		}
		return false, -1, outStr, runErr // real execution failure (e.g. binary not found)
	}
	return true, 0, outStr, nil
}

// truncateTail keeps the last n bytes of s.
// If len(s) <= n, returns s unchanged.
// The prefix ("...(truncated)\n") is counted inside the n-byte budget so the
// total returned string never exceeds n bytes.
func truncateTail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	const prefix = "...(truncated)\n"
	keep := n - len(prefix)
	if keep <= 0 {
		return s[len(s)-n:] // n is smaller than the prefix; just return a raw tail
	}
	return prefix + s[len(s)-keep:]
}
