// ABOUTME: Tests for the AgentLog component.
// ABOUTME: Covers text coalescing, expand/collapse, thinking indicator, and tool formatting.
package tui

import (
	"strings"
	"testing"
)

func TestAgentLogTextCoalescing(t *testing.T) {
	store := NewStateStore(nil)
	tr := NewThinkingTracker()
	al := NewAgentLog(store, tr, 20)
	al.Update(MsgTextChunk{NodeID: "n1", Text: "Hello "})
	al.Update(MsgTextChunk{NodeID: "n1", Text: "world"})
	view := al.View()
	if !strings.Contains(view, "Hello world") {
		t.Errorf("expected coalesced text, got: %s", view)
	}
}

func TestAgentLogToolCallBreaksCoalescing(t *testing.T) {
	store := NewStateStore(nil)
	tr := NewThinkingTracker()
	al := NewAgentLog(store, tr, 20)
	al.Update(MsgTextChunk{NodeID: "n1", Text: "before"})
	al.Update(MsgToolCallStart{NodeID: "n1", ToolName: "exec"})
	al.Update(MsgTextChunk{NodeID: "n1", Text: "after"})
	view := al.View()
	if !strings.Contains(view, "before") || !strings.Contains(view, "after") {
		t.Errorf("expected both text segments, got: %s", view)
	}
	if !strings.Contains(view, "exec") {
		t.Errorf("expected tool name, got: %s", view)
	}
}

func TestAgentLogThinkingIndicator(t *testing.T) {
	store := NewStateStore(nil)
	store.SetNodes([]NodeEntry{{ID: "n1"}})
	store.Apply(MsgNodeStarted{NodeID: "n1"})
	tr := NewThinkingTracker()
	tr.Start("n1")
	al := NewAgentLog(store, tr, 20)
	al.SetFocusedNode("n1")
	view := al.View()
	if !strings.Contains(view, "Thinking") {
		t.Errorf("expected thinking indicator, got: %s", view)
	}
}

func TestAgentLogExpandCollapse(t *testing.T) {
	store := NewStateStore(nil)
	tr := NewThinkingTracker()
	al := NewAgentLog(store, tr, 20)
	al.Update(MsgToolCallEnd{NodeID: "n1", ToolName: "exec", Output: "line1\nline2\nline3\nline4\nline5\nline6"})
	collapsed := al.View()
	al.Update(MsgToggleExpand{})
	expanded := al.View()
	if len(expanded) < len(collapsed) {
		t.Error("expected expanded view to be >= collapsed view")
	}
}

func TestAgentLogReasoningChunk(t *testing.T) {
	store := NewStateStore(nil)
	tr := NewThinkingTracker()
	al := NewAgentLog(store, tr, 20)
	al.Update(MsgReasoningChunk{NodeID: "n1", Text: "I think that..."})
	view := al.View()
	if !strings.Contains(view, "I think that...") {
		t.Errorf("expected reasoning text, got: %s", view)
	}
}

func TestAgentLogVerboseTrace(t *testing.T) {
	store := NewStateStore(nil)
	tr := NewThinkingTracker()
	al := NewAgentLog(store, tr, 20)
	al.Update(MsgLLMProviderRaw{NodeID: "n1", Data: "raw data"})
	view := al.View()
	if strings.Contains(view, "raw data") {
		t.Error("non-verbose mode should hide raw data")
	}
	al.SetVerboseTrace(true)
	al.Update(MsgLLMProviderRaw{NodeID: "n1", Data: "raw data 2"})
	view = al.View()
	if !strings.Contains(view, "raw data 2") {
		t.Errorf("verbose mode should show raw data, got: %s", view)
	}
}
