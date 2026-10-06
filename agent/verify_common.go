// ABOUTME: Portable verify-after-edit helpers shared by every platform — pure logic
// ABOUTME: with no POSIX dependency (command detection, repair prompt, output capping).
package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	// verifyOutputCap is the maximum bytes of verification output fed back to the LLM.
	// The tail is kept (most relevant errors appear at the end).
	verifyOutputCap = 16384
)

// verifyRepairPrompt returns the repair prompt injected when verification fails.
func verifyRepairPrompt(command string, exitCode int, output string) string {
	return fmt.Sprintf(`Verification failed after your edits.

Command: %s
Exit code: %d
Output (truncated to %dKB):
%s

Please fix the failing test/lint issue, then I'll re-verify.`, command, exitCode, verifyOutputCap/1024, output)
}

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

// verifyResult holds the result of a verification run, including which command
// was executed so callers can attribute failures correctly.
type verifyResult struct {
	Passed   bool
	ExitCode int
	Output   string
	Command  string // the command that produced this result
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
