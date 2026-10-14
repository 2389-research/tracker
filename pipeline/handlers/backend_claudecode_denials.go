// ABOUTME: Claude Code permission denials (#676): the result message's permission_denials,
// ABOUTME: and the rule that fails a node only when it did not ask for refusals.
package handlers

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/2389-research/tracker/internal/diag"
	"github.com/2389-research/tracker/pipeline"
)

// permissionDenial is one entry of the stream-json result's permission_denials:
// a tool call Claude Code refused. A refusal does not mark the result as an
// error (subtype stays "success"), so this list is the only structured signal.
type permissionDenial struct {
	ToolName  string          `json:"tool_name"`
	ToolUseID string          `json:"tool_use_id"`
	ToolInput json.RawMessage `json:"tool_input"`
}

// denialInputLimit caps how much of a refused call's input an error message
// quotes, so a large Write payload doesn't flood the TUI and FailureReason;
// denialListLimit caps how many refused calls it names.
const (
	denialInputLimit = 200
	denialListLimit  = 10
)

func (d permissionDenial) String() string {
	input := string(d.ToolInput)
	if len(input) > denialInputLimit {
		// Back off to a rune boundary so the message stays valid UTF-8.
		cut := input[:denialInputLimit]
		for len(cut) > 0 && !utf8.ValidString(cut) {
			cut = cut[:len(cut)-1]
		}
		input = cut + "…"
	}
	if input == "" {
		return d.ToolName
	}
	return d.ToolName + " " + input
}

// permissionDeniedError reports tool calls Claude Code refused on a node that
// did not ask for any refusals. A headless run has nobody to approve them, so
// the node failed at its job even though claude exited 0. handleRunError turns
// it into a routable, non-retryable OutcomeFail: a retry re-hits the same rule.
type permissionDeniedError struct {
	denials []permissionDenial
}

func (e *permissionDeniedError) Error() string {
	var calls []string
	for i, d := range e.denials {
		if i == denialListLimit {
			calls = append(calls, fmt.Sprintf("and %d more", len(e.denials)-denialListLimit))
			break
		}
		calls = append(calls, d.String())
	}
	return fmt.Sprintf("Claude Code refused %d tool call(s) this node did not restrict, and a headless run cannot approve them: %s — check Claude Code's permission rules (deny/ask rules in its settings, permissions.blockReadsOutsideWorkingDirectories) and CLAUDE_CODE_SUBPROCESS_ENV_SCRUB (#676)",
		len(e.denials), strings.Join(calls, "; "))
}

// refusalsExpected reports whether the node's own config asks Claude Code to
// refuse some calls: tool_access, a disallowed tool list, or a permission mode
// other than bypassPermissions (with no claude-code config at all, buildArgs
// passes no mode and claude falls back to its own prompting default).
// allowed_tools doesn't count: --allowedTools only pre-approves calls and never
// refuses one. Only a node that asked for no refusals fails on one.
func refusalsExpected(cfg pipeline.AgentRunConfig) bool {
	if strings.TrimSpace(cfg.ToolAccess) != "" {
		return true
	}
	cc, _ := cfg.Extra.(*pipeline.ClaudeCodeConfig)
	if cc == nil {
		return true
	}
	return cc.PermissionMode != pipeline.PermissionBypassPermissions ||
		len(cc.DisallowedTools) > 0
}

// checkPermissionDenials logs every refused call and returns a
// permissionDeniedError when the node did not ask for refusals. The refused
// calls' tool_result errors already reach the TUI and transcript as tool
// errors; this adds the stderr line and the outcome.
func checkPermissionDenials(state *runState) error {
	if len(state.permissionDenials) == 0 {
		return nil
	}
	for _, d := range state.permissionDenials {
		diag.Warnf("[claude-code] Claude Code refused a tool call: %s", d)
	}
	if state.refusalsExpected {
		return nil
	}
	return &permissionDeniedError{denials: state.permissionDenials}
}
