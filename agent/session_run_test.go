// ABOUTME: Tests for tool dispatch inside a session turn.
// ABOUTME: Covers turn attribution on tool events so a call can be traced to the turn that issued it.
package agent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/2389-research/tracker/llm"
)

// TestToolEventsCarryTurn pins that tool_call_start / tool_call_end / the
// tool-result end of a dispatch are attributable to the turn that issued
// them. executeSingleTool did not receive the turn counter, so tool events
// reached the activity log with a session but no turn — which makes a tool
// call unattributable once a run has more than one turn, and unorderable
// once parallel branches interleave into one log.
func TestToolEventsCarryTurn(t *testing.T) {
	client := &mockCompleter{
		responses: []*llm.Response{
			{ // turn 1: ask for a tool
				Message: llm.Message{
					Role: llm.RoleAssistant,
					Content: []llm.ContentPart{{
						Kind: llm.KindToolCall,
						ToolCall: &llm.ToolCallData{
							ID:        "call_1",
							Name:      "probe",
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

	var events []Event
	sess := mustNewSession(t, client, DefaultConfig(),
		WithEventHandler(EventHandlerFunc(func(evt Event) {
			events = append(events, evt)
		})))
	sess.registry.Register(&stubTool{name: "probe", output: "probed"})

	if _, err := sess.Run(context.Background(), "go"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var sawStart, sawEnd bool
	for _, evt := range events {
		switch evt.Type {
		case EventToolCallStart:
			sawStart = true
			if evt.Turn != 1 {
				t.Errorf("tool_call_start Turn = %d, want 1", evt.Turn)
			}
			if evt.ToolInput != `{"path":"x.txt"}` {
				t.Errorf("tool_call_start ToolInput = %q, want the arguments", evt.ToolInput)
			}
		case EventToolCallEnd:
			sawEnd = true
			if evt.Turn != 1 {
				t.Errorf("tool_call_end Turn = %d, want 1", evt.Turn)
			}
			// End carries the input too, so a reader holding only the end
			// event can still tell what was asked for.
			if evt.ToolInput != `{"path":"x.txt"}` {
				t.Errorf("tool_call_end ToolInput = %q, want the arguments", evt.ToolInput)
			}
			if evt.ToolOutput != "probed" {
				t.Errorf("tool_call_end ToolOutput = %q, want probed", evt.ToolOutput)
			}
		}
		if evt.SessionID == "" && isSessionScoped(evt.Type) {
			t.Errorf("%s carries no SessionID", evt.Type)
		}
	}
	if !sawStart || !sawEnd {
		t.Fatalf("missing tool events: start=%v end=%v", sawStart, sawEnd)
	}
}

// isSessionScoped reports whether an event type is emitted from inside a
// session and should therefore always carry a SessionID.
func isSessionScoped(t EventType) bool {
	switch t {
	case EventSessionStart, EventSessionEnd, EventTurnStart, EventTurnEnd,
		EventToolCallStart, EventToolCallEnd, EventToolCacheHit, EventTurnMetrics:
		return true
	}
	return false
}
