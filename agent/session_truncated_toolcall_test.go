// ABOUTME: Tests the fail-closed guard for length-truncated tool calls (#507).
// ABOUTME: A turn cut off by the token limit must not execute its (possibly
// incomplete) tool calls; recovery routes via a re-issue continuation prompt.
package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/2389-research/tracker/llm"
)

// TestTruncatedToolCallNotExecuted asserts that a turn whose response was cut
// off by the token limit (FinishReason length) is NOT dispatched even though it
// carries a tool call — the arguments may be incomplete. Recovery routes via a
// continuation prompt instructing the model to re-issue the complete call.
func TestTruncatedToolCallNotExecuted(t *testing.T) {
	client := &mockCompleter{
		responses: []*llm.Response{
			{ // turn 1: truncated — a tool call with (incomplete) args, cut off by length.
				Message: llm.Message{
					Role: llm.RoleAssistant,
					Content: []llm.ContentPart{{
						Kind: llm.KindToolCall,
						ToolCall: &llm.ToolCallData{
							ID:        "call_1",
							Name:      "mutate",
							Arguments: json.RawMessage(`{"path":"/etc/imp`), // truncated mid-JSON
						},
					}},
				},
				FinishReason: llm.FinishReason{Reason: "length"},
			},
			{ // turn 2: plain text, ends the session
				Message:      llm.AssistantMessage("done"),
				FinishReason: llm.FinishReason{Reason: "stop"},
			},
		},
	}

	var events []Event
	sess := mustNewSession(t, client, DefaultConfig(),
		WithEventHandler(EventHandlerFunc(func(evt Event) {
			events = append(events, evt)
		})))
	tool := &recordingTool{name: "mutate"}
	sess.registry.Register(tool)

	if _, err := sess.Run(context.Background(), "go"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if got := tool.calls; got != 0 {
		t.Fatalf("truncated tool call was executed %d time(s); want 0 (fail-closed)", got)
	}

	// No dispatch should have started for the truncated turn.
	for _, evt := range events {
		if evt.Type == EventToolCallStart {
			t.Errorf("EventToolCallStart emitted for a truncated turn; want none")
		}
	}

	// The rejection must be surfaced (never swallowed) — an EventError naming
	// truncation.
	var surfaced bool
	for _, evt := range events {
		if evt.Type == EventError && evt.Err != nil && strings.Contains(evt.Err.Error(), "truncat") {
			surfaced = true
		}
	}
	if !surfaced {
		t.Errorf("expected an EventError surfacing the truncated-tool-call rejection")
	}

	// Recovery must reject the truncated call with an error tool-result that
	// tells the model to re-issue — and it must reference the call's ID so the
	// provider's tool_use→tool_result contract stays satisfied.
	var reissued bool
	for _, m := range sess.messages {
		if m.Role != llm.RoleTool {
			continue
		}
		for _, part := range m.Content {
			if part.Kind == llm.KindToolResult && part.ToolResult != nil &&
				part.ToolResult.ToolCallID == "call_1" && part.ToolResult.IsError &&
				strings.Contains(strings.ToLower(part.ToolResult.Content), "truncated") {
				reissued = true
			}
		}
	}
	if !reissued {
		t.Errorf("expected an error tool-result re-issuing the truncated call")
	}
}

// TestCompleteToolCallStillExecutes pins that a normal, non-truncated tool call
// is unaffected by the #507 guard.
func TestCompleteToolCallStillExecutes(t *testing.T) {
	client := &mockCompleter{
		responses: []*llm.Response{
			{ // turn 1: a complete tool call.
				Message: llm.Message{
					Role: llm.RoleAssistant,
					Content: []llm.ContentPart{{
						Kind: llm.KindToolCall,
						ToolCall: &llm.ToolCallData{
							ID:        "call_1",
							Name:      "mutate",
							Arguments: json.RawMessage(`{"path":"x.txt"}`),
						},
					}},
				},
				FinishReason: llm.FinishReason{Reason: "tool_calls"},
			},
			{ // turn 2: plain text, ends the session
				Message:      llm.AssistantMessage("done"),
				FinishReason: llm.FinishReason{Reason: "stop"},
			},
		},
	}

	sess := mustNewSession(t, client, DefaultConfig())
	tool := &recordingTool{name: "mutate"}
	sess.registry.Register(tool)

	if _, err := sess.Run(context.Background(), "go"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if got := tool.calls; got != 1 {
		t.Fatalf("complete tool call executed %d time(s); want 1", got)
	}
}
