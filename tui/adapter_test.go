// ABOUTME: Tests for the event adapter layer.
// ABOUTME: Verifies correct mapping from pipeline/agent/LLM events to typed TUI messages.
package tui

import (
	"errors"
	"testing"

	"github.com/2389-research/tracker/agent"
	"github.com/2389-research/tracker/llm"
	"github.com/2389-research/tracker/pipeline"
)

func TestAdaptPipelineEvent(t *testing.T) {
	tests := []struct {
		name     string
		evt      pipeline.PipelineEvent
		wantType string
	}{
		{"stage started", pipeline.PipelineEvent{Type: pipeline.EventStageStarted, NodeID: "n1"}, "MsgNodeStarted"},
		{"stage completed", pipeline.PipelineEvent{Type: pipeline.EventStageCompleted, NodeID: "n1"}, "MsgNodeCompleted"},
		{"stage failed", pipeline.PipelineEvent{Type: pipeline.EventStageFailed, NodeID: "n1", Err: errors.New("boom")}, "MsgNodeFailed"},
		{"stage retrying", pipeline.PipelineEvent{Type: pipeline.EventStageRetrying, NodeID: "n1", Message: "retrying in 5s"}, "MsgNodeRetrying"},
		{"pipeline completed", pipeline.PipelineEvent{Type: pipeline.EventPipelineCompleted, TerminalStatus: string(pipeline.OutcomeSuccess)}, "MsgPipelineTerminated_Success"},
		{"pipeline failed", pipeline.PipelineEvent{Type: pipeline.EventPipelineFailed, Message: "fatal", TerminalStatus: string(pipeline.OutcomeFail)}, "MsgPipelineTerminated_Fail"},
		{"budget exceeded", pipeline.PipelineEvent{Type: pipeline.EventBudgetExceeded, Message: "over budget", TerminalStatus: string(pipeline.OutcomeBudgetExceeded)}, "MsgPipelineTerminated_Budget"},
		{"billing paused", pipeline.PipelineEvent{Type: pipeline.EventBillingPaused, NodeID: "n1", Message: "credit balance too low", TerminalStatus: string(pipeline.OutcomePausedBilling)}, "MsgPipelineTerminated_Paused"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := AdaptPipelineEvent(tt.evt)
			if msg == nil {
				t.Fatal("expected non-nil message")
			}
			switch tt.wantType {
			case "MsgNodeStarted":
				m, ok := msg.(MsgNodeStarted)
				if !ok {
					t.Errorf("expected MsgNodeStarted, got %T", msg)
				}
				if m.NodeID != "n1" {
					t.Errorf("expected NodeID n1, got %s", m.NodeID)
				}
			case "MsgNodeCompleted":
				m, ok := msg.(MsgNodeCompleted)
				if !ok {
					t.Errorf("expected MsgNodeCompleted, got %T", msg)
				}
				if m.Outcome != "success" {
					t.Errorf("expected outcome success, got %s", m.Outcome)
				}
			case "MsgNodeFailed":
				m, ok := msg.(MsgNodeFailed)
				if !ok {
					t.Errorf("expected MsgNodeFailed, got %T", msg)
				}
				if m.Error != "boom" {
					t.Errorf("expected error boom, got %s", m.Error)
				}
			case "MsgNodeRetrying":
				m, ok := msg.(MsgNodeRetrying)
				if !ok {
					t.Errorf("expected MsgNodeRetrying, got %T", msg)
				}
				if m.Message != "retrying in 5s" {
					t.Errorf("expected message 'retrying in 5s', got %s", m.Message)
				}
			case "MsgPipelineTerminated_Success":
				m, ok := msg.(MsgPipelineTerminated)
				if !ok {
					t.Errorf("expected MsgPipelineTerminated, got %T", msg)
				}
				if m.Status != pipeline.OutcomeSuccess {
					t.Errorf("expected status success, got %q", m.Status)
				}
				if m.Error != "" {
					t.Errorf("expected empty error for success, got %q", m.Error)
				}
			case "MsgPipelineTerminated_Fail":
				m, ok := msg.(MsgPipelineTerminated)
				if !ok {
					t.Errorf("expected MsgPipelineTerminated, got %T", msg)
				}
				if m.Status != pipeline.OutcomeFail {
					t.Errorf("expected status fail, got %q", m.Status)
				}
				if m.Error != "fatal" {
					t.Errorf("expected error fatal, got %s", m.Error)
				}
			case "MsgPipelineTerminated_Budget":
				m, ok := msg.(MsgPipelineTerminated)
				if !ok {
					t.Errorf("expected MsgPipelineTerminated, got %T", msg)
				}
				if m.Status != pipeline.OutcomeBudgetExceeded {
					t.Errorf("expected status budget_exceeded, got %q", m.Status)
				}
				if m.Error != "over budget" {
					t.Errorf("expected error 'over budget', got %q", m.Error)
				}
			case "MsgPipelineTerminated_Paused":
				m, ok := msg.(MsgPipelineTerminated)
				if !ok {
					t.Errorf("expected MsgPipelineTerminated, got %T", msg)
				}
				if m.Status != pipeline.OutcomePausedBilling {
					t.Errorf("expected status paused_billing, got %q", m.Status)
				}
				if m.Error != "credit balance too low" {
					t.Errorf("expected billing error, got %q", m.Error)
				}
			}
		})
	}
}

func TestAdaptPipelineEventUnknownReturnsNil(t *testing.T) {
	msg := AdaptPipelineEvent(pipeline.PipelineEvent{Type: "unknown_type"})
	if msg != nil {
		t.Errorf("expected nil for unknown event type, got %T", msg)
	}
}

func TestAdaptPipelineEvent_ValidationOverridden(t *testing.T) {
	detail := pipeline.OverrideDetail{
		GateNodeID: "ReviewGate",
		Label:      "force-merge",
		Actor:      pipeline.ActorHuman,
	}
	evt := pipeline.PipelineEvent{
		Type:     pipeline.EventValidationOverridden,
		NodeID:   "ReviewGate",
		Override: &detail,
	}
	msg := AdaptPipelineEvent(evt)
	m, ok := msg.(MsgValidationOverridden)
	if !ok {
		t.Fatalf("expected MsgValidationOverridden, got %T", msg)
	}
	if m.NodeID != "ReviewGate" {
		t.Errorf("expected NodeID ReviewGate, got %q", m.NodeID)
	}
	if m.Detail.Label != "force-merge" {
		t.Errorf("expected label force-merge, got %q", m.Detail.Label)
	}
}

func TestAdaptPipelineEvent_ValidationOverridden_NilOverrideReturnsNil(t *testing.T) {
	// Defensive: a malformed event without the Override payload yields nil
	// (no message) rather than a half-built MsgValidationOverridden.
	evt := pipeline.PipelineEvent{Type: pipeline.EventValidationOverridden, NodeID: "Gate"}
	if msg := AdaptPipelineEvent(evt); msg != nil {
		t.Errorf("expected nil for EventValidationOverridden with nil Override, got %T", msg)
	}
}

func TestPipelineAdapter_PassesThroughNonCompletionEvents(t *testing.T) {
	a := NewPipelineAdapter()
	evt := pipeline.PipelineEvent{Type: pipeline.EventStageStarted, NodeID: "n1"}
	msg := a.Adapt(evt)
	if _, ok := msg.(MsgNodeStarted); !ok {
		t.Errorf("expected MsgNodeStarted, got %T", msg)
	}
}

func TestPipelineAdapter_CompletionWithoutOverrides_IsSuccess(t *testing.T) {
	a := NewPipelineAdapter()
	a.Adapt(pipeline.PipelineEvent{Type: pipeline.EventStageStarted, NodeID: "n1"})
	a.Adapt(pipeline.PipelineEvent{Type: pipeline.EventStageCompleted, NodeID: "n1"})
	msg := a.Adapt(pipeline.PipelineEvent{
		Type: pipeline.EventPipelineCompleted, TerminalStatus: string(pipeline.OutcomeSuccess),
	})
	m, ok := msg.(MsgPipelineTerminated)
	if !ok {
		t.Fatalf("expected MsgPipelineTerminated, got %T", msg)
	}
	if m.Status != pipeline.OutcomeSuccess {
		t.Errorf("expected Status=success, got %q", m.Status)
	}
	if m.Override != nil {
		t.Errorf("expected nil Override for non-override completion, got %+v", m.Override)
	}
}

// TestTUIAdapter_BuildsMsgWithLatestOverride covers spec D5a: when multiple
// overrides fire during a run, the headline carried on MsgPipelineTerminated
// must be the LATEST entry (closest to the exit), not the first. The Status
// comes from the authoritative TerminalStatus stamped by the engine, not from
// the presence of overrides.
func TestTUIAdapter_BuildsMsgWithLatestOverride(t *testing.T) {
	a := NewPipelineAdapter()
	a.Adapt(pipeline.PipelineEvent{
		Type:   pipeline.EventValidationOverridden,
		NodeID: "EarlyGate",
		Override: &pipeline.OverrideDetail{
			GateNodeID: "EarlyGate",
			Label:      "lgtm",
			Actor:      pipeline.ActorHuman,
		},
	})
	a.Adapt(pipeline.PipelineEvent{
		Type:   pipeline.EventValidationOverridden,
		NodeID: "LateGate",
		Override: &pipeline.OverrideDetail{
			GateNodeID: "LateGate",
			Label:      "force-merge",
			Actor:      pipeline.ActorAutopilot,
		},
	})
	msg := a.Adapt(pipeline.PipelineEvent{
		Type: pipeline.EventPipelineCompleted, TerminalStatus: string(pipeline.OutcomeValidationOverridden),
	})
	m, ok := msg.(MsgPipelineTerminated)
	if !ok {
		t.Fatalf("expected MsgPipelineTerminated, got %T", msg)
	}
	if m.Status != pipeline.OutcomeValidationOverridden {
		t.Errorf("expected validation_overridden Status, got %q", m.Status)
	}
	if m.Override == nil {
		t.Fatal("expected Override to be populated for override completion")
	}
	if m.Override.GateNodeID != "LateGate" {
		t.Errorf("D5a violated: expected headline=LateGate (latest), got %q", m.Override.GateNodeID)
	}
	if m.Override.Label != "force-merge" {
		t.Errorf("expected label force-merge, got %q", m.Override.Label)
	}
	if m.Override.Actor != pipeline.ActorAutopilot {
		t.Errorf("expected actor autopilot, got %q", m.Override.Actor)
	}
}

func TestPipelineAdapter_FreeFunctionStillStateless(t *testing.T) {
	// Sanity: the stateless free function yields a MsgPipelineTerminated with a
	// nil Override (it doesn't observe override events across the run) but the
	// Status still comes from the authoritative TerminalStatus.
	msg := AdaptPipelineEvent(pipeline.PipelineEvent{
		Type: pipeline.EventPipelineCompleted, TerminalStatus: string(pipeline.OutcomeSuccess),
	})
	m, ok := msg.(MsgPipelineTerminated)
	if !ok {
		t.Fatalf("expected MsgPipelineTerminated, got %T", msg)
	}
	if m.Status != pipeline.OutcomeSuccess {
		t.Errorf("stateless adapter must carry authoritative Status, got %q", m.Status)
	}
	if m.Override != nil {
		t.Errorf("stateless adapter must yield nil Override, got %+v", m.Override)
	}
}

// TestAdaptTerminal_EmptyStatusYieldsNil pins the fail-safe: a malformed
// terminal event that carries no authoritative TerminalStatus produces no
// phantom terminal transition (the finding's "malformed empty status" risk).
func TestAdaptTerminal_EmptyStatusYieldsNil(t *testing.T) {
	if msg := AdaptPipelineEvent(pipeline.PipelineEvent{Type: pipeline.EventPipelineCompleted}); msg != nil {
		t.Errorf("expected nil for completed event with empty TerminalStatus, got %T", msg)
	}
	a := NewPipelineAdapter()
	if msg := a.Adapt(pipeline.PipelineEvent{Type: pipeline.EventPipelineFailed}); msg != nil {
		t.Errorf("expected nil for failed event with empty TerminalStatus, got %T", msg)
	}
}

// TestAdaptTerminal_ChildScopedTerminalIgnored pins the root-scope rule: a
// subgraph child's forwarded terminal event (scoped NodeID, e.g. from tripping
// the shared budget guard) must NOT drive the top-level completion row. Only
// the unscoped run-level terminal event does.
func TestAdaptTerminal_ChildScopedTerminalIgnored(t *testing.T) {
	scoped := pipeline.PipelineEvent{
		Type:           pipeline.EventBudgetExceeded,
		NodeID:         "Parent/Child",
		Message:        "child over budget",
		TerminalStatus: string(pipeline.OutcomeBudgetExceeded),
	}
	if msg := AdaptPipelineEvent(scoped); msg != nil {
		t.Errorf("expected nil for child-scoped terminal event, got %T", msg)
	}
	a := NewPipelineAdapter()
	if msg := a.Adapt(scoped); msg != nil {
		t.Errorf("stateful adapter must also drop child-scoped terminal event, got %T", msg)
	}
	// The unscoped run-level budget terminal is the one that drives the UI.
	root := pipeline.PipelineEvent{
		Type:           pipeline.EventBudgetExceeded,
		Message:        "run over budget",
		TerminalStatus: string(pipeline.OutcomeBudgetExceeded),
	}
	m, ok := AdaptPipelineEvent(root).(MsgPipelineTerminated)
	if !ok {
		t.Fatalf("expected MsgPipelineTerminated for unscoped budget terminal, got %T", AdaptPipelineEvent(root))
	}
	if m.Status != pipeline.OutcomeBudgetExceeded {
		t.Errorf("expected budget_exceeded, got %q", m.Status)
	}
}

func TestAdaptAgentEvent(t *testing.T) {
	tests := []struct {
		name     string
		evt      agent.Event
		wantType string
	}{
		{"text delta", agent.Event{Type: agent.EventTextDelta, Text: "hello"}, "MsgTextChunk"},
		{"tool call start", agent.Event{Type: agent.EventToolCallStart, ToolName: "bash"}, "MsgToolCallStart"},
		{"tool call end", agent.Event{Type: agent.EventToolCallEnd, ToolName: "bash", ToolOutput: "ok", ToolError: "err"}, "MsgToolCallEnd"},
		{"error", agent.Event{Type: agent.EventError, Err: errors.New("bad")}, "MsgAgentError"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := AdaptAgentEvent(tt.evt, "n1")
			if msg == nil {
				t.Fatal("expected non-nil message")
			}
			switch tt.wantType {
			case "MsgTextChunk":
				m, ok := msg.(MsgTextChunk)
				if !ok {
					t.Errorf("expected MsgTextChunk, got %T", msg)
				}
				if m.Text != "hello" {
					t.Errorf("expected text hello, got %s", m.Text)
				}
				if m.NodeID != "n1" {
					t.Errorf("expected NodeID n1, got %s", m.NodeID)
				}
			case "MsgToolCallStart":
				m, ok := msg.(MsgToolCallStart)
				if !ok {
					t.Errorf("expected MsgToolCallStart, got %T", msg)
				}
				if m.ToolName != "bash" {
					t.Errorf("expected tool bash, got %s", m.ToolName)
				}
			case "MsgToolCallEnd":
				m, ok := msg.(MsgToolCallEnd)
				if !ok {
					t.Errorf("expected MsgToolCallEnd, got %T", msg)
				}
				if m.Output != "ok" {
					t.Errorf("expected output ok, got %s", m.Output)
				}
				if m.Error != "err" {
					t.Errorf("expected error err, got %s", m.Error)
				}
			case "MsgAgentError":
				m, ok := msg.(MsgAgentError)
				if !ok {
					t.Errorf("expected MsgAgentError, got %T", msg)
				}
				if m.Error != "bad" {
					t.Errorf("expected error bad, got %s", m.Error)
				}
			}
		})
	}
}

func TestAdaptAgentEventUnknownReturnsNil(t *testing.T) {
	msg := AdaptAgentEvent(agent.Event{Type: "unknown_type"}, "n1")
	if msg != nil {
		t.Errorf("expected nil for unknown agent event type, got %T", msg)
	}
}

func TestAdaptLLMTraceEvent(t *testing.T) {
	evt := llm.TraceEvent{Kind: llm.TraceRequestStart, Provider: "anthropic", Model: "claude-sonnet-4-6"}
	msgs := AdaptLLMTraceEvent(evt, "n1", false)
	if len(msgs) == 0 {
		t.Fatal("expected at least one message")
	}
	var hasRequest bool
	for _, m := range msgs {
		if v, ok := m.(MsgLLMRequestStart); ok {
			hasRequest = true
			if v.Provider != "anthropic" {
				t.Errorf("expected provider anthropic, got %s", v.Provider)
			}
			if v.Model != "claude-sonnet-4-6" {
				t.Errorf("expected model claude-sonnet-4-6, got %s", v.Model)
			}
		}
	}
	if !hasRequest {
		t.Error("expected MsgLLMRequestStart")
	}
	// MsgThinkingStarted now comes from AdaptAgentEvent, not LLM trace.
}

func TestAdaptLLMTraceEventText(t *testing.T) {
	evt := llm.TraceEvent{Kind: llm.TraceText, Preview: "hello world"}
	msgs := AdaptLLMTraceEvent(evt, "n1", false)
	if len(msgs) < 1 {
		t.Fatalf("expected at least 1 message, got %d", len(msgs))
	}
	var hasText bool
	for _, m := range msgs {
		if v, ok := m.(MsgTextChunk); ok {
			hasText = true
			if v.Text != "hello world" {
				t.Errorf("expected text 'hello world', got %s", v.Text)
			}
		}
	}
	if !hasText {
		t.Error("expected MsgTextChunk")
	}
	// MsgThinkingStopped now comes from AdaptAgentEvent, not LLM trace.
}

func TestAdaptLLMTraceEventReasoning(t *testing.T) {
	evt := llm.TraceEvent{Kind: llm.TraceReasoning, Preview: "hmm"}
	msgs := AdaptLLMTraceEvent(evt, "n1", false)
	var hasReasoning bool
	for _, m := range msgs {
		if _, ok := m.(MsgReasoningChunk); ok {
			hasReasoning = true
		}
	}
	if !hasReasoning {
		t.Error("expected MsgReasoningChunk")
	}
}

func TestAdaptLLMTraceEventFinish(t *testing.T) {
	evt := llm.TraceEvent{Kind: llm.TraceFinish}
	msgs := AdaptLLMTraceEvent(evt, "n1", false)
	var hasFinish bool
	for _, m := range msgs {
		if _, ok := m.(MsgLLMFinish); ok {
			hasFinish = true
		}
	}
	if !hasFinish {
		t.Error("expected MsgLLMFinish")
	}
}

func TestAdaptLLMTraceEventToolPrepare(t *testing.T) {
	evt := llm.TraceEvent{Kind: llm.TraceToolPrepare, ToolName: "bash"}
	msgs := AdaptLLMTraceEvent(evt, "n1", false)
	// TraceToolPrepare now returns nil — thinking state is managed by agent events.
	if msgs != nil {
		t.Errorf("expected nil for TraceToolPrepare, got %d messages", len(msgs))
	}
}

func TestAdaptLLMTraceEventVerboseFilter(t *testing.T) {
	evt := llm.TraceEvent{Kind: llm.TraceProviderRaw, RawPreview: "raw"}
	msgs := AdaptLLMTraceEvent(evt, "n1", false)
	if len(msgs) != 0 {
		t.Errorf("expected no messages in non-verbose, got %d", len(msgs))
	}
	msgs = AdaptLLMTraceEvent(evt, "n1", true)
	if len(msgs) != 1 {
		t.Errorf("expected 1 message in verbose, got %d", len(msgs))
	}
	if m, ok := msgs[0].(MsgLLMProviderRaw); ok {
		if m.Data != "raw" {
			t.Errorf("expected data 'raw', got %s", m.Data)
		}
	} else {
		t.Errorf("expected MsgLLMProviderRaw, got %T", msgs[0])
	}
}
