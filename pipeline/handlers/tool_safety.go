// ABOUTME: Security checks for tool_command execution: denylist and allowlist pattern matching.
// ABOUTME: Denylist is always active and non-overridable by .dip files. Allowlist is opt-in.
package handlers

import (
	"fmt"
	"regexp"
	"strings"
)

// defaultDenyPatterns are blocked in all tool_command executions.
// Cannot be overridden by .dip graph attrs. Only --bypass-denylist CLI flag disables them.
// Patterns use * as wildcard. Matching is case-insensitive, per-statement.
var defaultDenyPatterns = []string{
	"eval *",
	"exec *",
	"source *",
	". /*",
	"curl * | *",
	"wget * | *",
	"* | sh",
	"* | bash",
	"* | zsh",
	"* | /bin/sh",
	"* | /bin/bash",
	"* | sh -",
	"* | bash -",
}

// splitStatementRe splits on ;, &&, ||, and newlines.
var splitStatementRe = regexp.MustCompile(`\s*(?:;|&&|\|\|)\s*`)

// splitCommandStatements splits a compound shell command into individual statements.
func splitCommandStatements(cmd string) []string {
	cmd = strings.ReplaceAll(cmd, "\n", ";")
	var stmts []string
	for _, part := range splitStatementRe.Split(cmd, -1) {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			stmts = append(stmts, trimmed)
		}
	}
	if len(stmts) == 0 {
		return []string{strings.TrimSpace(cmd)}
	}
	return stmts
}

// globMatch checks if s matches a glob pattern where * matches any characters.
// Case-insensitive.
func globMatch(pattern, s string) bool {
	pattern = strings.ToLower(pattern)
	s = strings.ToLower(s)
	escaped := regexp.QuoteMeta(pattern)
	escaped = strings.ReplaceAll(escaped, `\*`, `.*`)
	re, err := regexp.Compile("^" + escaped + "$")
	if err != nil {
		return false
	}
	return re.MatchString(s)
}

// checkCommandDenylist checks each statement against the default deny patterns.
// Returns (denied, matchedPattern) for the first match.
func checkCommandDenylist(cmd string) (bool, string) {
	for _, stmt := range splitCommandStatements(cmd) {
		for _, pattern := range defaultDenyPatterns {
			if globMatch(pattern, stmt) {
				return true, pattern
			}
		}
	}
	return false, ""
}

// checkCommandAllowlist returns true if every statement matches at least one allowlist pattern.
func checkCommandAllowlist(cmd string, allowlist []string) bool {
	for _, stmt := range splitCommandStatements(cmd) {
		matched := false
		for _, pattern := range allowlist {
			if globMatch(pattern, stmt) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

// CheckToolCommand validates a command against the denylist and optional allowlist.
// Returns an error if the command is blocked.
func CheckToolCommand(cmd, nodeID string, allowlist []string, bypassDenylist bool) error {
	if !bypassDenylist {
		if denied, pattern := checkCommandDenylist(cmd); denied {
			return fmt.Errorf(
				"tool_command for node %q matches denied pattern %q — "+
					"this command pattern is blocked for security. "+
					"Use --bypass-denylist if this is intentional, "+
					"or restructure the command to avoid the pattern",
				nodeID, pattern,
			)
		}
	}
	if len(allowlist) > 0 {
		if !checkCommandAllowlist(cmd, allowlist) {
			return fmt.Errorf(
				"tool_command %q for node %q is not in the allowlist. "+
					"Allowed patterns: %s",
				cmd, nodeID, strings.Join(allowlist, ", "),
			)
		}
	}
	return nil
}
