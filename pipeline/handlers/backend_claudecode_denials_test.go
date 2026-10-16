// ABOUTME: Tests for Claude Code permission denials (#676): parsing real NDJSON,
// ABOUTME: failing a node only when it did not ask for refusals, and the routable fail outcome.
package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/2389-research/tracker/agent"
	"github.com/2389-research/tracker/pipeline"
)

// Lines captured from Claude Code 2.1.300 refusing a Bash call under
// bypassPermissions with --disallowedTools "Bash(date:*)", trimmed to the
// fields Tracker reads.
const (
	realDeniedToolUse = `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_012Q","name":"Bash","input":{"command":"date > out.txt"}}]}}`
	realDeniedResult  = `{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"Permission to use Bash with command date > out.txt has been denied.","is_error":true,"tool_use_id":"toolu_012Q"}]},"parent_tool_use_id":null}`
	realDenialsResult = `{"type":"result","subtype":"success","is_error":false,"num_turns":2,"result":"I didn't run it because permission to use the Bash tool was denied.","permission_denials":[{"tool_name":"Bash","tool_use_id":"toolu_012Q","tool_input":{"command":"date > out.txt","description":"Write current date to out.txt"}}],"usage":{"input_tokens":10,"output_tokens":20}}`
)

// Claude Code nests a user message's tool_result blocks under message.content.
// Before #676 the parser read only a top-level content field, so every
// claude-code tool result (output and error alike) was dropped.
func TestParseNDJSONToolResult_NestedMessageContent(t *testing.T) {
	state := &runState{toolUseIDs: map[string]string{}}
	parseMessage(json.RawMessage(realDeniedToolUse), state)
	events := parseMessage(json.RawMessage(realDeniedResult), state)

	if len(events) != 1 || events[0].Type != agent.EventToolCallEnd {
		t.Fatalf("events = %+v, want one EventToolCallEnd", events)
	}
	if events[0].ToolName != "Bash" {
		t.Errorf("ToolName = %q, want Bash", events[0].ToolName)
	}
	if !strings.Contains(events[0].ToolError, "has been denied") {
		t.Errorf("ToolError = %q, want the denial text", events[0].ToolError)
	}
}

func TestStoreResult_RecordsPermissionDenials(t *testing.T) {
	state := &runState{toolUseIDs: map[string]string{}}
	parseMessage(json.RawMessage(realDenialsResult), state)

	if len(state.permissionDenials) != 1 {
		t.Fatalf("permissionDenials = %+v, want 1", state.permissionDenials)
	}
	d := state.permissionDenials[0]
	if d.ToolName != "Bash" || !strings.Contains(string(d.ToolInput), "date > out.txt") {
		t.Errorf("denial = %+v, want Bash with its input", d)
	}
}

func TestRefusalsExpected(t *testing.T) {
	bypass := func(mut func(*pipeline.ClaudeCodeConfig)) pipeline.AgentRunConfig {
		cc := &pipeline.ClaudeCodeConfig{PermissionMode: pipeline.PermissionBypassPermissions}
		if mut != nil {
			mut(cc)
		}
		return pipeline.AgentRunConfig{Extra: cc}
	}
	cases := []struct {
		name string
		cfg  pipeline.AgentRunConfig
		want bool
	}{
		{"default bypass, no tool lists", bypass(nil), false},
		{"disallowed tools", bypass(func(c *pipeline.ClaudeCodeConfig) { c.DisallowedTools = []string{"Bash(rm:*)"} }), true},
		// --allowedTools only pre-approves calls; it never refuses one, so it
		// must not mask a refusal the node did not ask for.
		{"allowed tools", bypass(func(c *pipeline.ClaudeCodeConfig) { c.AllowedTools = []string{"Read"} }), false},
		{"acceptEdits", bypass(func(c *pipeline.ClaudeCodeConfig) { c.PermissionMode = pipeline.PermissionAcceptEdits }), true},
		{"plan", bypass(func(c *pipeline.ClaudeCodeConfig) { c.PermissionMode = pipeline.PermissionPlan }), true},
		{"tool_access", pipeline.AgentRunConfig{ToolAccess: "read-only", Extra: &pipeline.ClaudeCodeConfig{PermissionMode: pipeline.PermissionBypassPermissions}}, true},
		{"no claude-code config (claude's own default mode)", pipeline.AgentRunConfig{}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := refusalsExpected(c.cfg); got != c.want {
				t.Errorf("refusalsExpected = %v, want %v", got, c.want)
			}
		})
	}
}

// fakeClaude writes a claude stand-in that prints lines and exits 0.
func fakeClaude(t *testing.T, lines ...string) *ClaudeCodeBackend {
	t.Helper()
	script := filepath.Join(t.TempDir(), "claude-fake")
	body := "#!/bin/sh\ncat <<'EOF'\n" + strings.Join(lines, "\n") + "\nEOF\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatalf("writing fake claude: %v", err)
	}
	return &ClaudeCodeBackend{claudePath: script}
}

func TestClaudeCodeRun_UnaskedDenialIsAnError(t *testing.T) {
	b := fakeClaude(t, realDeniedToolUse, realDeniedResult, realDenialsResult)
	cfg := pipeline.AgentRunConfig{Prompt: "go", Extra: &pipeline.ClaudeCodeConfig{PermissionMode: pipeline.PermissionBypassPermissions}}

	var ends int
	res, err := b.Run(context.Background(), cfg, func(e agent.Event) {
		if e.Type == agent.EventToolCallEnd && e.ToolError != "" {
			ends++
		}
	})
	var denied *permissionDeniedError
	if !errors.As(err, &denied) {
		t.Fatalf("err = %v (%T), want *permissionDeniedError", err, err)
	}
	if !strings.Contains(err.Error(), "Bash") || !strings.Contains(err.Error(), "date > out.txt") {
		t.Errorf("error %q should name the refused tool and its input", err)
	}
	if ends != 1 {
		t.Errorf("refused tool_result events = %d, want 1", ends)
	}
	if res.Turns != 2 || res.Usage.OutputTokens != 20 {
		t.Errorf("result should still carry the session's stats, got %+v", res)
	}
}

func TestClaudeCodeRun_AskedDenialOnlyWarns(t *testing.T) {
	b := fakeClaude(t, realDeniedToolUse, realDeniedResult, realDenialsResult)
	cfg := pipeline.AgentRunConfig{Prompt: "go", Extra: &pipeline.ClaudeCodeConfig{
		PermissionMode:  pipeline.PermissionBypassPermissions,
		DisallowedTools: []string{"Bash(date:*)"},
	}}
	if _, err := b.Run(context.Background(), cfg, func(agent.Event) {}); err != nil {
		t.Fatalf("a refusal the node asked for must not fail the run, got %v", err)
	}
}

// A refusal the node did not ask for is a routable, non-retryable fail: a
// headless run has nobody to approve the call, and retrying re-hits the same
// permission rule.
func TestHandleRunError_PermissionDeniedIsRoutableFail(t *testing.T) {
	h := NewCodergenHandler(nil, t.TempDir())
	node := &pipeline.Node{ID: "Build", Attrs: map[string]string{}}
	runErr := &permissionDeniedError{denials: []permissionDenial{{ToolName: "Bash", ToolInput: json.RawMessage(`{"command":"make"}`)}}}
	sess := agent.SessionResult{Turns: 3}

	outcome, err := h.handleRunError(runErr, node, "prompt", "", sess, &transcriptCollector{}, nil)
	if err != nil {
		t.Fatalf("handleRunError returned a handler error (would abort the run): %v", err)
	}
	if outcome.Status != pipeline.OutcomeFail {
		t.Fatalf("status = %s, want fail", outcome.Status)
	}
	if !strings.Contains(outcome.FailureReason, "Bash") {
		t.Errorf("FailureReason = %q, want the refused tool", outcome.FailureReason)
	}
	if outcome.Stats == nil || outcome.Stats.Turns != 3 {
		t.Errorf("Stats = %+v, want the session's stats kept", outcome.Stats)
	}
}

// A quoted input is cut on a rune boundary, so the failure reason stays valid
// UTF-8 however long the refused Write payload was.
func TestPermissionDenialString_TruncatesOnRuneBoundary(t *testing.T) {
	input := `{"content":"` + strings.Repeat("é", denialInputLimit) + `"}`
	got := permissionDenial{ToolName: "Write", ToolInput: json.RawMessage(input)}.String()
	if !utf8.ValidString(got) {
		t.Fatalf("truncated denial is not valid UTF-8: %q", got)
	}
	if len(got) > len("Write ")+denialInputLimit+len("…") {
		t.Errorf("denial not truncated: %d bytes", len(got))
	}
}

// A run that hits one rule over and over names the first few calls and counts
// the rest, so FailureReason stays readable.
func TestPermissionDeniedError_CapsListedCalls(t *testing.T) {
	denials := make([]permissionDenial, denialListLimit+5)
	for i := range denials {
		denials[i] = permissionDenial{ToolName: "Bash"}
	}
	msg := (&permissionDeniedError{denials: denials}).Error()
	if n := strings.Count(msg, "Bash"); n != denialListLimit {
		t.Errorf("listed %d calls, want %d", n, denialListLimit)
	}
	if !strings.Contains(msg, "and 5 more") {
		t.Errorf("message %q should count the calls it left out", msg)
	}
}
