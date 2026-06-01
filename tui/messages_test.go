// ABOUTME: Tests that all TUI message types are properly defined and satisfy tea.Msg.
// ABOUTME: Validates message field access patterns used throughout the TUI.
package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/2389-research/tracker/pipeline"
)

func TestPipelineMessagesAreTeaMsgs(t *testing.T) {
	msgs := []tea.Msg{
		MsgNodeStarted{NodeID: "n1"},
		MsgNodeCompleted{NodeID: "n1", Outcome: "success"},
		MsgNodeFailed{NodeID: "n1", Error: "boom"},
		MsgNodeRetrying{NodeID: "n1", Message: "retrying in 5s"},
		MsgPipelineCompleted{},
		MsgPipelineCompleted{Status: pipeline.OutcomeValidationOverridden, Override: &pipeline.OverrideDetail{}},
		MsgPipelineFailed{Error: "fatal"},
		MsgValidationOverridden{NodeID: "Gate", Detail: pipeline.OverrideDetail{GateNodeID: "Gate"}},
	}
	for i, msg := range msgs {
		if msg == nil {
			t.Errorf("message %d is nil", i)
		}
	}
}

func TestMsgPipelineCompleted_StatusField(t *testing.T) {
	m := MsgPipelineCompleted{Status: pipeline.OutcomeValidationOverridden}
	if m.Status != pipeline.OutcomeValidationOverridden {
		t.Errorf("expected validation_overridden Status, got %q", m.Status)
	}
}

func TestMsgPipelineCompleted_OverridePopulated(t *testing.T) {
	detail := &pipeline.OverrideDetail{
		GateNodeID: "ApproveGate",
		Label:      "force-merge",
		Actor:      pipeline.ActorHuman,
	}
	m := MsgPipelineCompleted{Status: pipeline.OutcomeValidationOverridden, Override: detail}
	if m.Override == nil {
		t.Fatal("expected Override to be populated")
	}
	if m.Override.GateNodeID != "ApproveGate" {
		t.Errorf("expected gate ApproveGate, got %q", m.Override.GateNodeID)
	}
	if m.Override.Label != "force-merge" {
		t.Errorf("expected label force-merge, got %q", m.Override.Label)
	}
	if m.Override.Actor != pipeline.ActorHuman {
		t.Errorf("expected actor human, got %q", m.Override.Actor)
	}
}

func TestAgentMessagesAreTeaMsgs(t *testing.T) {
	msgs := []tea.Msg{
		MsgThinkingStarted{NodeID: "n1"},
		MsgThinkingStopped{NodeID: "n1"},
		MsgTextChunk{NodeID: "n1", Text: "hello"},
		MsgReasoningChunk{NodeID: "n1", Text: "thinking"},
		MsgToolCallStart{NodeID: "n1", ToolName: "exec"},
		MsgToolCallEnd{NodeID: "n1", ToolName: "exec", Output: "ok"},
		MsgAgentError{NodeID: "n1", Error: "fail"},
	}
	for i, msg := range msgs {
		if msg == nil {
			t.Errorf("message %d is nil", i)
		}
	}
}

func TestLLMMessagesAreTeaMsgs(t *testing.T) {
	msgs := []tea.Msg{
		MsgLLMRequestStart{NodeID: "n1", Provider: "anthropic", Model: "claude-sonnet-4-6"},
		MsgLLMFinish{NodeID: "n1"},
		MsgLLMProviderRaw{NodeID: "n1", Data: "raw"},
	}
	for i, msg := range msgs {
		if msg == nil {
			t.Errorf("message %d is nil", i)
		}
	}
}

func TestGateMessagesHaveReplyCh(t *testing.T) {
	ch := make(chan string, 1)
	choice := MsgGateChoice{NodeID: "n1", Prompt: "Pick one", Options: []string{"a", "b"}, ReplyCh: ch}
	if choice.ReplyCh == nil {
		t.Error("expected non-nil ReplyCh")
	}
	freeform := MsgGateFreeform{NodeID: "n1", Prompt: "Enter value", ReplyCh: ch}
	if freeform.ReplyCh == nil {
		t.Error("expected non-nil ReplyCh")
	}
}

func TestUIMessagesAreTeaMsgs(t *testing.T) {
	msgs := []tea.Msg{MsgThinkingTick{}, MsgHeaderTick{}, MsgToggleExpand{}}
	for i, msg := range msgs {
		if msg == nil {
			t.Errorf("message %d is nil", i)
		}
	}
}
