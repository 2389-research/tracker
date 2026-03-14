// ABOUTME: Tests that all TUI message types are properly defined and satisfy tea.Msg.
// ABOUTME: Validates message field access patterns used throughout the TUI.
package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestPipelineMessagesAreTeaMsgs(t *testing.T) {
	msgs := []tea.Msg{
		MsgNodeStarted{NodeID: "n1"},
		MsgNodeCompleted{NodeID: "n1", Outcome: "success"},
		MsgNodeFailed{NodeID: "n1", Error: "boom"},
		MsgPipelineCompleted{},
		MsgPipelineFailed{Error: "fatal"},
	}
	for i, msg := range msgs {
		if msg == nil {
			t.Errorf("message %d is nil", i)
		}
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
