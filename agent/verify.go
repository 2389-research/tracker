// ABOUTME: Verify-after-edit loop: auto-detects build system, runs tests after file edits, and injects repair prompts.
// ABOUTME: Transparent inner loop — verification turns do not count against session MaxTurns.
package agent

import (
	"bytes"
	"context"
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

	// verifyBufferCap is the maximum bytes buffered from the verification command's
	// combined stdout+stderr. Real test runs can produce MBs; this prevents OOM.
	// We buffer more than verifyOutputCap so we can keep the tail after truncation.
	verifyBufferCap = 64 * 1024

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

// hasPytestSection returns true if the pyproject.toml contains a [tool.pytest] section.
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
	return &verifier{cmd: cmd, workDir: cfg.WorkingDir}
}

// run executes the verification command and returns (passed, exitCode, output, error).
// A non-zero exit code is not an error — it is returned as passed=false with the
// actual exit code and output. A real execution error (binary not found, etc.)
// is returned as error. Output is capped at verifyBufferCap to prevent OOM.
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

	// Cap combined output to prevent OOM on verbose test suites.
	// Keep the tail (errors usually appear at the end).
	var combined bytes.Buffer
	cmd.Stdout = &limitedWriter{buf: &combined, limit: verifyBufferCap}
	cmd.Stderr = &limitedWriter{buf: &combined, limit: verifyBufferCap}

	runErr := cmd.Run()
	out := combined.String()

	if runErr != nil {
		if ee, ok := runErr.(*exec.ExitError); ok {
			// Non-zero exit code: verification failed but command ran fine.
			// Return the real exit code so the repair prompt is accurate.
			return false, ee.ExitCode(), truncateTail(out, verifyOutputCap), nil
		}
		return false, 0, "", runErr // real execution failure (e.g. binary not found)
	}
	return true, 0, out, nil
}

// limitedWriter caps how many bytes are forwarded to buf at limit bytes.
// After the cap is reached, further writes are silently discarded.
// Write always returns (len(p), nil) to satisfy the io.Writer contract —
// returning a short write with err==nil would cause io.Copy to treat it as
// an error per the Go spec.
type limitedWriter struct {
	buf   *bytes.Buffer
	n     int64
	limit int64
}

func (lw *limitedWriter) Write(p []byte) (int, error) {
	if lw.n >= lw.limit {
		return len(p), nil // cap reached — accept but discard
	}
	space := lw.limit - lw.n
	if int64(len(p)) > space {
		lw.buf.Write(p[:space]) //nolint:errcheck // bytes.Buffer.Write never returns an error
		lw.n = lw.limit
		return len(p), nil // accept full write, truncate internally
	}
	lw.buf.Write(p) //nolint:errcheck // bytes.Buffer.Write never returns an error
	lw.n += int64(len(p))
	return len(p), nil
}

// truncateTail keeps the last n bytes of s.
// If len(s) <= n, returns s unchanged.
func truncateTail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "...(truncated)\n" + s[len(s)-n:]
}
