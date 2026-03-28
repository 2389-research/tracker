// ABOUTME: Tests for the verbosity filter feature.
// ABOUTME: Verifies cycling, view-level filtering, and interaction with search.
package tui

import (
	"strings"
	"testing"
)

func TestVerbosityCycling(t *testing.T) {
	store := NewStateStore(nil)
	tr := NewThinkingTracker()
	al := NewAgentLog(store, tr, 40)

	if al.Verbosity() != VerbosityAll {
		t.Fatalf("expected initial verbosity All, got %d", al.Verbosity())
	}
	al.CycleVerbosity()
	if al.Verbosity() != VerbosityTools {
		t.Errorf("expected Tools after first cycle, got %d", al.Verbosity())
	}
	al.CycleVerbosity()
	if al.Verbosity() != VerbosityErrors {
		t.Errorf("expected Errors after second cycle, got %d", al.Verbosity())
	}
	al.CycleVerbosity()
	if al.Verbosity() != VerbosityReasoning {
		t.Errorf("expected Reasoning after third cycle, got %d", al.Verbosity())
	}
	al.CycleVerbosity()
	if al.Verbosity() != VerbosityAll {
		t.Errorf("expected All after fourth cycle (wrap), got %d", al.Verbosity())
	}
}

func TestVerbosityLabel(t *testing.T) {
	tests := []struct {
		v    Verbosity
		want string
	}{
		{VerbosityAll, "all"},
		{VerbosityTools, "tools"},
		{VerbosityErrors, "errors"},
		{VerbosityReasoning, "reasoning"},
	}
	for _, tt := range tests {
		if got := tt.v.Label(); got != tt.want {
			t.Errorf("Verbosity(%d).Label() = %q, want %q", tt.v, got, tt.want)
		}
	}
}

func TestVerbosityFilterShowsOnlyTools(t *testing.T) {
	store := NewStateStore(nil)
	tr := NewThinkingTracker()
	al := NewAgentLog(store, tr, 40)
	al.SetSize(80, 40)

	// Add mixed content.
	al.Update(MsgTextChunk{NodeID: "n1", Text: "general text\n"})
	al.Update(MsgToolCallStart{NodeID: "n1", ToolName: "bash", ToolInput: `{"command":"ls"}`})
	al.Update(MsgToolCallEnd{NodeID: "n1", ToolName: "bash", Output: "ok"})
	al.Update(MsgReasoningChunk{NodeID: "n1", Text: "I should think"})
	al.Update(MsgAgentError{NodeID: "n1", Error: "something bad"})

	// Default: all visible.
	viewAll := al.View()
	if !strings.Contains(viewAll, "general text") {
		t.Error("expected general text in All mode")
	}

	// Switch to tools only.
	al.CycleVerbosity() // -> Tools
	viewTools := al.View()
	plain := stripANSI(viewTools)
	if !strings.Contains(plain, "$ ls") && !strings.Contains(plain, "bash") {
		t.Errorf("expected tool lines in Tools mode, got plain: %q", plain)
	}
	if strings.Contains(plain, "general text") {
		t.Error("general text should be hidden in Tools mode")
	}
	if strings.Contains(plain, "I should think") {
		t.Error("reasoning should be hidden in Tools mode")
	}
}

func TestVerbosityFilterShowsOnlyErrors(t *testing.T) {
	store := NewStateStore(nil)
	tr := NewThinkingTracker()
	al := NewAgentLog(store, tr, 40)
	al.SetSize(80, 40)

	al.Update(MsgTextChunk{NodeID: "n1", Text: "general text\n"})
	al.Update(MsgToolCallStart{NodeID: "n1", ToolName: "bash", ToolInput: ""})
	al.Update(MsgAgentError{NodeID: "n1", Error: "critical"})
	al.Update(MsgNodeFailed{NodeID: "n1", Error: "fatal error"})

	// Switch to errors.
	al.CycleVerbosity() // -> Tools
	al.CycleVerbosity() // -> Errors
	view := al.View()
	plain := stripANSI(view)
	if !strings.Contains(plain, "critical") {
		t.Error("expected error lines in Errors mode")
	}
	if !strings.Contains(plain, "fatal error") {
		t.Error("expected failed node in Errors mode")
	}
	if strings.Contains(plain, "general text") {
		t.Error("general text should be hidden in Errors mode")
	}
}

func TestVerbosityFilterShowsOnlyReasoning(t *testing.T) {
	store := NewStateStore(nil)
	tr := NewThinkingTracker()
	al := NewAgentLog(store, tr, 40)
	al.SetSize(80, 40)

	al.Update(MsgTextChunk{NodeID: "n1", Text: "general text\n"})
	al.Update(MsgReasoningChunk{NodeID: "n1", Text: "deep thought"})

	// Switch to reasoning.
	al.CycleVerbosity() // -> Tools
	al.CycleVerbosity() // -> Errors
	al.CycleVerbosity() // -> Reasoning
	view := al.View()
	plain := stripANSI(view)
	if !strings.Contains(plain, "deep thought") {
		t.Error("expected reasoning lines in Reasoning mode")
	}
	if strings.Contains(plain, "general text") {
		t.Error("general text should be hidden in Reasoning mode")
	}
}

func TestVerbosityAllStoresAllLines(t *testing.T) {
	store := NewStateStore(nil)
	tr := NewThinkingTracker()
	al := NewAgentLog(store, tr, 40)
	al.SetSize(80, 40)

	al.Update(MsgTextChunk{NodeID: "n1", Text: "before filter\n"})

	// Filter to errors (hides general text).
	al.CycleVerbosity() // Tools
	al.CycleVerbosity() // Errors

	// Add a general line while in errors mode — it should still be stored.
	al.Update(MsgTextChunk{NodeID: "n1", Text: "during filter\n"})

	// Switch back to All — both lines should be visible.
	al.CycleVerbosity() // Reasoning
	al.CycleVerbosity() // All
	view := al.View()
	plain := stripANSI(view)
	if !strings.Contains(plain, "before filter") {
		t.Error("expected 'before filter' visible after returning to All")
	}
	if !strings.Contains(plain, "during filter") {
		t.Error("expected 'during filter' visible after returning to All")
	}
}

func TestVerbosityCycleThroughMsgRoute(t *testing.T) {
	store := NewStateStore(nil)
	tr := NewThinkingTracker()
	al := NewAgentLog(store, tr, 40)

	al.Update(MsgCycleVerbosity{})
	if al.Verbosity() != VerbosityTools {
		t.Errorf("expected Tools after MsgCycleVerbosity, got %d", al.Verbosity())
	}
}

func TestVerbosityPlusFocusConjunction(t *testing.T) {
	store := NewStateStore(nil)
	store.SetNodes([]NodeEntry{{ID: "n1"}, {ID: "n2"}})
	tr := NewThinkingTracker()
	al := NewAgentLog(store, tr, 40)
	al.SetSize(80, 40)

	// Add mixed content from two nodes.
	al.Update(MsgTextChunk{NodeID: "n1", Text: "n1 general\n"})
	al.Update(MsgAgentError{NodeID: "n1", Error: "n1 error"})
	al.Update(MsgTextChunk{NodeID: "n2", Text: "n2 general\n"})
	al.Update(MsgAgentError{NodeID: "n2", Error: "n2 error"})

	// Focus on n1 + errors only.
	al.SetFocusNodeID("n1")
	al.CycleVerbosity() // Tools
	al.CycleVerbosity() // Errors

	view := al.View()
	plain := stripANSI(view)

	// Should show only n1 errors.
	if !strings.Contains(plain, "n1 error") {
		t.Error("expected n1 error visible with focus+errors filter")
	}
	if strings.Contains(plain, "n2 error") {
		t.Error("expected n2 error hidden with focus on n1")
	}
	if strings.Contains(plain, "n1 general") {
		t.Error("expected n1 general hidden in errors mode")
	}
}

func TestVisibleTextRespectsFilters(t *testing.T) {
	store := NewStateStore(nil)
	store.SetNodes([]NodeEntry{{ID: "n1"}, {ID: "n2"}})
	tr := NewThinkingTracker()
	al := NewAgentLog(store, tr, 40)
	al.SetSize(80, 40)

	al.Update(MsgTextChunk{NodeID: "n1", Text: "visible line\n"})
	al.Update(MsgTextChunk{NodeID: "n2", Text: "hidden line\n"})
	al.Update(MsgAgentError{NodeID: "n1", Error: "n1 err"})

	// Focus on n1, errors only.
	al.SetFocusNodeID("n1")
	al.CycleVerbosity() // Tools
	al.CycleVerbosity() // Errors

	text := al.VisibleText()
	if !strings.Contains(text, "n1 err") {
		t.Error("expected n1 error in visible text")
	}
	if strings.Contains(text, "hidden line") {
		t.Error("expected n2 content excluded from visible text")
	}
	if strings.Contains(text, "visible line") {
		t.Error("expected general text excluded in errors mode")
	}
}

func TestMaxLogLinesTrim(t *testing.T) {
	store := NewStateStore(nil)
	tr := NewThinkingTracker()
	al := NewAgentLog(store, tr, 20)
	al.SetSize(80, 20)

	// Add more than maxLogLines entries.
	for i := 0; i < maxLogLines+100; i++ {
		al.Update(MsgTextChunk{NodeID: "n1", Text: "x\n"})
	}

	if len(al.lines) > maxLogLines {
		t.Errorf("expected lines capped at %d, got %d", maxLogLines, len(al.lines))
	}

	// Verify view still renders without panic after trimming.
	view := al.View()
	if view == "" {
		t.Error("expected non-empty view after trimming")
	}
}
