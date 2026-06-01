// ABOUTME: Cross-platform pure Go helpers for the writable_paths fs-jail (issue #272).
// ABOUTME: ValidateWritablePaths runs at session setup before any syscall jail mechanism.
package exec

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"
)

// ValidateWritablePaths is the cross-platform gate that runs at session
// setup before the jail is wired. It catches three classes of refusal:
//
//  1. working_dir escapes tracker's process cwd (the "working_dir: /tmp/atk
//     relocation" attack described in the spec § 8.1).
//  2. The glob list is empty (fail-closed; a present-but-empty value is
//     already rejected by dippin's parser, but tracker backstops).
//  3. A glob entry is absolute, starts with ~, escapes the workspace via
//     parent traversal, or has unbalanced brace expansion. These are mostly
//     caught by dippin DIP142, but tracker is the runtime backstop.
//
// Returns ErrPathEscape-wrapped errors for class-1 and class-3; plain error
// for class-2.
func ValidateWritablePaths(workingDir string, globs []string, processCwd string) error {
	if err := validateWorkingDirEscape(workingDir, processCwd); err != nil {
		return err
	}
	if len(globs) == 0 {
		return fmt.Errorf("writable_paths is empty (fail-closed)")
	}
	for _, g := range globs {
		if err := validateGlobEntry(g); err != nil {
			return err
		}
	}
	return nil
}

// validateWorkingDirEscape rejects a working_dir that resolves outside
// processCwd. Catches both absolute paths (e.g. "/tmp/atk") and parent
// escapes (e.g. "../../etc").
func validateWorkingDirEscape(workingDir, processCwd string) error {
	cleanedCwd := filepath.Clean(processCwd)
	var resolved string
	if filepath.IsAbs(workingDir) {
		resolved = filepath.Clean(workingDir)
	} else {
		resolved = filepath.Clean(filepath.Join(cleanedCwd, workingDir))
	}
	if !isSubpathOf(resolved, cleanedCwd) {
		return fmt.Errorf("%w: working_dir %q resolves to %q which escapes the tracker process cwd %q",
			ErrPathEscape, workingDir, resolved, cleanedCwd)
	}
	return nil
}

// isSubpathOf reports whether child is the same as parent or a descendant.
// Both paths must be already cleaned + absolute.
func isSubpathOf(child, parent string) bool {
	if child == parent {
		return true
	}
	sep := string(filepath.Separator)
	if !strings.HasSuffix(parent, sep) {
		parent += sep
	}
	return strings.HasPrefix(child, parent)
}

// validateGlobEntry catches absolute, ~, parent-escape, and malformed-brace
// entries. dippin's DIP142 also catches these; we backstop at runtime.
func validateGlobEntry(g string) error {
	if g == "" {
		return fmt.Errorf("writable_paths entry is empty (fail-closed)")
	}
	if strings.HasPrefix(g, "/") || strings.HasPrefix(g, "~") {
		return fmt.Errorf("%w: writable_paths entry %q is absolute / ~ (must be workspace-relative)",
			ErrPathEscape, g)
	}
	if isWindowsAbsolute(g) {
		return fmt.Errorf("%w: writable_paths entry %q is a Windows absolute path", ErrPathEscape, g)
	}
	cleaned := filepath.Clean(g)
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return fmt.Errorf("%w: writable_paths entry %q escapes via parent traversal (cleaned: %q)",
			ErrPathEscape, g, cleaned)
	}
	if !balancedBraces(g) {
		return fmt.Errorf("malformed writable_paths entry %q: unbalanced braces (comma-split tore an expansion apart)", g)
	}
	// Probe with path.Match so malformed character classes (e.g. "foo[") and
	// other Match-rejected shapes are caught at session setup rather than
	// becoming opaque runtime denials (#272 review, Copilot jail.go:91).
	// "**" is tracker's own metasyntax — path.Match would reject it as a
	// bad pattern, so we strip it before probing.
	probe := strings.ReplaceAll(g, "**", "x")
	if _, err := path.Match(probe, "x"); err != nil {
		return fmt.Errorf("malformed writable_paths entry %q: %v", g, err)
	}
	return nil
}

// isWindowsAbsolute matches `C:\foo` or `\\share\foo` shapes build-OS-independently.
func isWindowsAbsolute(s string) bool {
	if len(s) >= 2 && s[1] == ':' && ((s[0] >= 'A' && s[0] <= 'Z') || (s[0] >= 'a' && s[0] <= 'z')) {
		return true
	}
	if strings.HasPrefix(s, `\\`) {
		return true
	}
	return false
}

// balancedBraces returns true when { and } counts match in s. Catches the
// case where a comma-split tore `*.{md,yaml}` into `*.{md` and `yaml}`.
func balancedBraces(s string) bool {
	openCount := strings.Count(s, "{")
	closeBrace := strings.Count(s, "}")
	return openCount == closeBrace
}
