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
		{"pipeline completed", pipeline.PipelineEvent{Type: pipeline.EventPipelineCompleted}, "MsgPipelineCompleted"},
		{"pipeline failed", pipeline.PipelineEvent{Type: pipeline.EventPipelineFailed, Message: "fatal"}, "MsgPipelineFailed"},
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
			case "MsgPipelineCompleted":
				if _, ok := msg.(MsgPipelineCompleted); !ok {
					t.Errorf("expected MsgPipelineCompleted, got %T", msg)
				}
			case "MsgPipelineFailed":
				m, ok := msg.(MsgPipelineFailed)
				if !ok {
					t.Errorf("expected MsgPipelineFailed, got %T", msg)
				}
				if m.Error != "fatal" {
					t.Errorf("expected error fatal, got %s", m.Error)
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
