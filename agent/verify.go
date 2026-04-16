// ABOUTME: Verify-after-edit loop: auto-detects build system, runs tests after file edits, and injects repair prompts.
// ABOUTME: Transparent inner loop — verification turns do not count against session MaxTurns.
package agent

import (
	"bytes"
	"context"
	"fmt"
	"io"
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
// at the start of a line. Only the first 1 KB is scanned to keep it fast.
func hasMakeTestTarget(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	buf := make([]byte, 1024)
	n, _ := f.Read(buf)
	return makeTestTargetRe.Match(buf[:n])
}

// hasPytestSection returns true if the pyproject.toml contains a [tool.pytest] section.
// Only the first 1 KB is scanned.
func hasPytestSection(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	buf := make([]byte, 1024)
	n, _ := f.Read(buf)
	return strings.Contains(string(buf[:n]), "[tool.pytest")
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
	limited := io.LimitReader(io.TeeReader(nopWriter{}, &combined), verifyBufferCap)
	_ = limited // limitreader applied via pipe below
	cmd.Stdout = &limitedWriter{buf: &combined, remaining: verifyBufferCap}
	cmd.Stderr = &limitedWriter{buf: &combined, remaining: verifyBufferCap}

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

// nopWriter discards all writes (used as the read side of TeeReader above — unused).
type nopWriter struct{}

func (nopWriter) Read(p []byte) (int, error) { return 0, io.EOF }

// limitedWriter is a bytes.Buffer wrapper that stops writing after remaining bytes.
type limitedWriter struct {
	buf       *bytes.Buffer
	remaining int
}

func (lw *limitedWriter) Write(p []byte) (int, error) {
	if lw.remaining <= 0 {
		return len(p), nil // silently discard — cap already reached
	}
	if len(p) > lw.remaining {
		p = p[:lw.remaining]
	}
	n, err := lw.buf.Write(p)
	lw.remaining -= n
	return len(p), err // report original len so cmd doesn't error on short write
}

// truncateTail keeps the last n bytes of s.
// If len(s) <= n, returns s unchanged.
func truncateTail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "...(truncated)\n" + s[len(s)-n:]
}
