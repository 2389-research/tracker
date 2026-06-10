// ABOUTME: Security checks for tool_command execution: denylist and allowlist pattern matching.
// ABOUTME: Built-in denylist patterns are always active; .dip files can extend but not shrink them. Allowlist is opt-in.
package handlers

import (
	"fmt"
	"regexp"
	"strings"
)

// defaultDenyPatterns are blocked in all tool_command executions by default.
// Built-in patterns cannot be removed — .dip files (via the `tool_denylist_add`
// graph attr) and operators (via `--tool-denylist-add`) can *extend* the list,
// but never shrink it. The only switch that disables the defaults is the
// all-or-nothing `--bypass-denylist` CLI flag, which also disables any
// user-added patterns (it is the intentional escape hatch for sandboxed
// environments). Patterns use * as wildcard. Matching is case-insensitive,
// per-statement.
var defaultDenyPatterns = []string{
	"eval *",
	"exec *",
	"source *",
	". ./*",
	". /*",
	"curl * | *",
	"wget * | *",
	"* | sh",
	"* | sh *",
	"* | bash",
	"* | bash *",
	"* | zsh",
	"* | zsh *",
	"* | /bin/sh",
	"* | /bin/sh *",
	"* | /bin/bash",
	"* | /bin/bash *",
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

// wsCollapseRe collapses runs of whitespace for denylist matching, so a tab
// between words (e.g. "exec\t/bin/sh") can't evade space-separated patterns.
var wsCollapseRe = regexp.MustCompile(`\s+`)

// execRedir* recognize the POSIX fd-redirection token shapes that make an
// exec statement fd-only (no process replacement). See isExecFdRedirectOnly.
var (
	// [n]>&N / [n]<&N / [n]>&- / [n]<&- — fd duplication or close.
	execRedirDupRe = regexp.MustCompile(`^[0-9]*(?:>&|<&)(?:[0-9]+|-)$`)
	// [n]> / [n]>> / [n]< / [n]<> fused with a target word.
	execRedirFusedRe = regexp.MustCompile("^[0-9]*(?:>>|<>|>|<)([^&|;<>\\\\`()]+)$")
	// &> / &>> (bash) fused with a target word.
	execRedirAmpFusedRe = regexp.MustCompile("^&>>?([^&|;<>\\\\`()]+)$")
	// Operator alone — the target is the next token.
	execRedirOpOnlyRe = regexp.MustCompile(`^(?:[0-9]*(?:>>|<>|>|<)|&>>?)$`)
)

// isExecRedirTarget reports whether tok is acceptable as a redirection
// target word: non-empty and free of shell metacharacters that could smuggle
// a command (&, |, ;, <, >, backslash, backtick, parens). Quotes and $VAR /
// ${VAR} expansions are fine — they can only ever name a file here.
//
// Tokenization is whitespace-based (strings.Fields), so a quoted target
// containing whitespace (`exec >"log file"`) splits into multiple tokens and
// the fragment after the space falls to the bare-word path — denied. That is
// the fail-closed direction by design: the exemption recognizes only targets
// that are provably a single redirection word without a shell-quoting parser.
func isExecRedirTarget(tok string) bool {
	return tok != "" && !strings.ContainsAny(tok, "&|;<>\\`()")
}

// isExecFdRedirectOnly reports whether stmt is an `exec` whose remaining
// tokens are exclusively fd redirections (`exec 3>"$tmp"`, `exec 3>&-`,
// `exec <file`) — the POSIX idiom for opening/closing file descriptors,
// which does NOT replace the process. Such statements are exempt from the
// built-in "exec *" deny pattern (#333).
//
// This is a security boundary: the statement must be PROVABLY fd-only, not
// merely "doesn't look like a command". Fail closed on anything ambiguous —
// command substitution ($(, backtick), unbalanced quotes, or any bare word
// after exec that is not a redirection token.
func isExecFdRedirectOnly(stmt string) bool {
	// Fail closed on substitution or quoting we can't reason about.
	if strings.Contains(stmt, "$(") || strings.Contains(stmt, "`") {
		return false
	}
	if strings.Count(stmt, `"`)%2 != 0 || strings.Count(stmt, `'`)%2 != 0 {
		return false
	}

	fields := strings.Fields(stmt)
	if len(fields) < 2 || !strings.EqualFold(fields[0], "exec") {
		return false
	}
	rest := fields[1:]
	for i := 0; i < len(rest); i++ {
		tok := rest[i]
		switch {
		case execRedirDupRe.MatchString(tok):
			// complete: fd dup/close
		case execRedirFusedRe.MatchString(tok) || execRedirAmpFusedRe.MatchString(tok):
			// complete: operator fused with target
		case execRedirOpOnlyRe.MatchString(tok):
			// operator alone consumes exactly one following target word
			i++
			if i >= len(rest) || !isExecRedirTarget(rest[i]) {
				return false
			}
		default:
			// bare word — a command for exec to replace the process with
			return false
		}
	}
	return true
}

// checkCommandDenylist checks each statement against the default deny
// patterns plus any user-supplied patterns (from --tool-denylist-add or
// the tool_denylist_add graph attr). User patterns are additive — they
// cannot remove a default pattern; they can only add more. Returns
// (denied, matchedPattern) for the first match.
//
// Statements are whitespace-normalized before matching so tab-separated
// commands can't evade space-separated patterns. The built-in "exec *"
// pattern exempts fd-only redirect statements (see isExecFdRedirectOnly);
// user-supplied patterns get no exemption.
func checkCommandDenylist(cmd string, extraDenyPatterns []string) (bool, string) {
	for _, stmt := range splitCommandStatements(cmd) {
		stmt = wsCollapseRe.ReplaceAllString(stmt, " ")
		for _, pattern := range defaultDenyPatterns {
			if globMatch(pattern, stmt) {
				if pattern == "exec *" && isExecFdRedirectOnly(stmt) {
					continue
				}
				return true, pattern
			}
		}
		for _, pattern := range extraDenyPatterns {
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

// CheckToolCommand validates a command against the denylist (built-in +
// user-added) and optional allowlist. Returns an error if the command is
// blocked. --bypass-denylist disables both the built-in and user-added
// denylist patterns — by design, since the flag is the intentional
// escape hatch for sandboxed environments. The allowlist is still
// enforced when bypass is set.
func CheckToolCommand(cmd, nodeID string, allowlist, extraDenyPatterns []string, bypassDenylist bool) error {
	if !bypassDenylist {
		if denied, pattern := checkCommandDenylist(cmd, extraDenyPatterns); denied {
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
