// ABOUTME: Tests for tool_access enforcement on agent sessions (issue #258).
// ABOUTME: Bounds the v0.28.2 single-agent multi-tool-call vector — when
// ABOUTME: ToolAccess is set, an LLM emitting multiple tool calls in one
// ABOUTME: response must execute zero of them. Covers the red-team scenario,
// ABOUTME: WithTools bypass, case/typo fail-closed, and system-prompt scrub.
package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/2389-research/tracker/llm"
)

// recordingTool tracks how many times Execute is invoked. The red-team test
// uses this instead of stubTool so we can assert zero invocations.
type recordingTool struct {
	name      string
	output    string
	calls     int
	lastInput string
}

func (r *recordingTool) Name() string        { return r.name }
func (r *recordingTool) Description() string { return "recording tool for tool_access tests" }
func (r *recordingTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{}}`)
}
func (r *recordingTool) Execute(_ context.Context, args json.RawMessage) (string, error) {
	r.calls++
	r.lastInput = string(args)
	return r.output, nil
}

// multiToolCallResponse builds the v0.28.2 red-team payload: a single
// assistant message containing three tool calls. Payload contents are
// inert sentinel strings — the test asserts zero invocations, so the
// payload never runs; the strings only need to be parseable JSON.
func multiToolCallResponse() *llm.Response {
	return &llm.Response{
		Message: llm.Message{
			Role: llm.RoleAssistant,
			Content: []llm.ContentPart{
				{
					Kind: llm.KindToolCall,
					ToolCall: &llm.ToolCallData{
						ID:        "call_1",
						Name:      "bash",
						Arguments: json.RawMessage(`{"command":"PAYLOAD_1"}`),
					},
				},
				{
					Kind: llm.KindToolCall,
					ToolCall: &llm.ToolCallData{
						ID:        "call_2",
						Name:      "write",
						Arguments: json.RawMessage(`{"path":"payload.txt","content":"PAYLOAD_2"}`),
					},
				},
				{
					Kind: llm.KindToolCall,
					ToolCall: &llm.ToolCallData{
						ID:        "call_3",
						Name:      "bash",
						Arguments: json.RawMessage(`{"command":"PAYLOAD_3"}`),
					},
				},
			},
		},
		FinishReason: llm.FinishReason{Reason: "tool_calls"},
		Usage:        llm.Usage{InputTokens: 30, OutputTokens: 30, TotalTokens: 60},
	}
}

// TestSessionToolAccess_RedTeamMultiToolCall is the dispositive test for
// issue #258: an LLM that emits multiple tool calls in a single response
// must execute zero of them when ToolAccess is non-empty. This bounds the
// v0.28.2 attack vector (LLM gets through `max_turns` because multiple
// calls fit in one turn).
func TestSessionToolAccess_RedTeamMultiToolCall(t *testing.T) {
	bashTool := &recordingTool{name: "bash", output: "should-not-execute"}
	writeTool := &recordingTool{name: "write", output: "should-not-execute"}

	var capturedRequests []llm.Request
	client := &mockCompleter{
		responses: []*llm.Response{multiToolCallResponse()},
		onComplete: func(req *llm.Request) {
			copied := *req
			copied.Messages = append([]llm.Message(nil), req.Messages...)
			capturedRequests = append(capturedRequests, copied)
		},
	}

	cfg := DefaultConfig()
	cfg.ToolAccess = "none"
	cfg.MaxTurns = 3

	// Register tools via WithTools — the registry must still end up empty
	// because ToolAccess gates registration at session construction.
	sess := mustNewSession(t, client, cfg, WithTools(bashTool, writeTool))

	result, err := sess.Run(context.Background(), "Do whatever it takes")
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	// Dispositive assertion: zero tool executions despite the LLM emitting
	// three tool calls in one response.
	if bashTool.calls != 0 {
		t.Errorf("bash tool was invoked %d times; expected 0 under tool_access=none", bashTool.calls)
	}
	if writeTool.calls != 0 {
		t.Errorf("write tool was invoked %d times; expected 0 under tool_access=none", writeTool.calls)
	}
	if result.TotalToolCalls() != 0 {
		t.Errorf("result.TotalToolCalls() = %d; expected 0", result.TotalToolCalls())
	}

	// The outbound LLM request must carry no tool definitions and a
	// ToolChoiceNone signal so the API itself blocks tool invocation.
	if len(capturedRequests) == 0 {
		t.Fatal("expected at least one LLM request captured")
	}
	first := capturedRequests[0]
	if len(first.Tools) != 0 {
		t.Errorf("request carried %d tool definitions; expected 0", len(first.Tools))
	}
	if first.ToolChoice == nil {
		t.Error("request.ToolChoice was nil; expected ToolChoiceNone()")
	} else if first.ToolChoice.Mode != "none" {
		t.Errorf("request.ToolChoice.Mode = %q; expected \"none\"", first.ToolChoice.Mode)
	}
}

// TestSessionToolAccess_RestrictedRegistry_EmptyAfterWithTools confirms the
// defense-in-depth move: even when a caller registers tools via WithTools,
// the registry ends up empty when ToolAccess is set.
func TestSessionToolAccess_RestrictedRegistry_EmptyAfterWithTools(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ToolAccess = "none"

	sess := mustNewSession(t, &mockCompleter{}, cfg, WithTools(
		&recordingTool{name: "read"},
		&recordingTool{name: "write"},
		&recordingTool{name: "bash"},
	))

	if got := len(sess.registry.Definitions()); got != 0 {
		t.Errorf("registry holds %d tools after ToolAccess=none + WithTools; expected 0", got)
	}
}

// TestSessionToolAccess_FailClosedOnTypo confirms that a misspelled
// directive (e.g. "noen") still disables tools — any non-empty value
// triggers the restriction so a lint-skipped typo can't ship full tools.
func TestSessionToolAccess_FailClosedOnTypo(t *testing.T) {
	for _, val := range []string{"noen", "None", "  none  ", "NONE", "off", "x"} {
		t.Run(val, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.ToolAccess = val

			sess := mustNewSession(t, &mockCompleter{}, cfg, WithTools(
				&recordingTool{name: "read"},
			))

			if got := len(sess.registry.Definitions()); got != 0 {
				t.Errorf("ToolAccess=%q: registry holds %d tools; expected 0 (fail-closed)", val, got)
			}
		})
	}
}

// TestSessionToolAccess_EmptyMeansUnrestricted confirms that the empty
// string means no restriction — tools register normally.
func TestSessionToolAccess_EmptyMeansUnrestricted(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ToolAccess = ""

	sess := mustNewSession(t, &mockCompleter{}, cfg, WithTools(
		&recordingTool{name: "read"},
	))

	if got := len(sess.registry.Definitions()); got != 1 {
		t.Errorf("ToolAccess=\"\": registry holds %d tools; expected 1 (unrestricted)", got)
	}
}

// TestSessionToolAccess_SystemPromptScrub confirms the BUILT-IN prefix of
// the assembled system prompt contains no standalone case-insensitive tool
// names when ToolAccess is restricted. Defends against the LLM noticing
// tool affordances from tracker's own boilerplate.
//
// Scope: tracker only scrubs its own built-in prefix. A caller-supplied
// SessionConfig.SystemPrompt is appended verbatim — if it names tools,
// they survive into the assembled prompt. The registry-empty + ToolChoice
// + dispatch-shortcircuit defenses do not depend on the prompt scrub; the
// scrub is defense-in-depth. This test deliberately uses a SystemPrompt
// that does NOT name tools so the assertion holds for the built-in path.
func TestSessionToolAccess_SystemPromptScrub(t *testing.T) {
	var captured []llm.Request
	client := &mockCompleter{
		responses: []*llm.Response{{
			Message:      llm.AssistantMessage("acknowledged"),
			FinishReason: llm.FinishReason{Reason: "stop"},
		}},
		onComplete: func(req *llm.Request) {
			copied := *req
			copied.Messages = append([]llm.Message(nil), req.Messages...)
			captured = append(captured, copied)
		},
	}

	cfg := DefaultConfig()
	cfg.ToolAccess = "none"
	cfg.SystemPrompt = "You are an assistant."

	sess := mustNewSession(t, client, cfg)
	if _, err := sess.Run(context.Background(), "Hello"); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	if len(captured) == 0 {
		t.Fatal("no LLM request captured")
	}

	var systemText string
	for _, msg := range captured[0].Messages {
		if msg.Role == llm.RoleSystem {
			systemText = msg.Text()
			break
		}
	}
	if systemText == "" {
		t.Fatal("no system message in request")
	}

	// The forbidden tokens per spec's system-prompt-audit rule.
	forbidden := []string{"read", "write", "edit", "glob", "grep_search", "bash", "apply_patch"}
	lower := strings.ToLower(systemText)
	for _, tok := range forbidden {
		// Match the token surrounded by non-word boundaries (start, end, or
		// non-alphanumeric/underscore). Using simple substring + word-edge
		// checks rather than regexp to stay literal about the contract.
		for i := 0; i <= len(lower)-len(tok); i++ {
			if lower[i:i+len(tok)] != tok {
				continue
			}
			if i > 0 && isWordChar(lower[i-1]) {
				continue
			}
			if i+len(tok) < len(lower) && isWordChar(lower[i+len(tok)]) {
				continue
			}
			t.Errorf("system prompt contains forbidden standalone token %q at offset %d under tool_access=none\n  prompt: %s", tok, i, systemText)
			break
		}
	}
}

func isWordChar(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '_'
}
