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
	if !strings.Contains(view, "⟳ Thinking...") {
		t.Errorf("expected thinking indicator with ⟳ prefix, got: %s", view)
	}
}

func TestAgentLogExpandCollapse(t *testing.T) {
	store := NewStateStore(nil)
	tr := NewThinkingTracker()
	al := NewAgentLog(store, tr, 20)
	longOutput := "drwxr-xr-x  5 harper staff  160 Mar 14 10:00 src\n" +
		"-rw-r--r--  1 harper staff  4096 Mar 14 10:00 main.go\n" +
		"-rw-r--r--  1 harper staff  2048 Mar 14 10:00 go.mod\n" +
		"drwxr-xr-x  3 harper staff   96 Mar 14 10:00 tui\n" +
		"-rw-r--r--  1 harper staff  1024 Mar 14 10:00 README.md\n" +
		"drwxr-xr-x  2 harper staff   64 Mar 14 10:00 agent"
	al.Update(MsgToolCallEnd{NodeID: "n1", ToolName: "bash", Output: longOutput})
	collapsed := al.View()
	al.Update(MsgToggleExpand{})
	expanded := al.View()
	if len(expanded) < len(collapsed) {
		t.Error("expected expanded view to be >= collapsed view")
	}
}

func TestFormatToolDisplay(t *testing.T) {
	tests := []struct {
		name     string
		tool     string
		input    string
		contains string
	}{
		{"bash", "bash", `{"command":"ls -la"}`, "$ ls -la"},
		{"read", "read", `{"path":"src/main.go"}`, "read src/main.go"},
		{"write", "write", `{"path":"out.txt"}`, "write out.txt"},
		{"edit", "edit", `{"path":"main.go","old":"foo","new":"bar"}`, "edit main.go"},
		{"grep", "grep", `{"pattern":"TODO","path":"src/"}`, "grep TODO src/"},
		{"glob", "glob", `{"pattern":"**/*.go"}`, "glob **/*.go"},
		{"spawn", "spawn_agent", `{"task":"research the API"}`, "spawn: research the API"},
		{"unknown", "custom_tool", `{"query":"test"}`, "custom_tool test"},
		{"no input", "bash", "", "bash"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatToolDisplay(tt.tool, tt.input)
			if !strings.Contains(result, tt.contains) {
				t.Errorf("formatToolDisplay(%q, %q) = %q, want containing %q", tt.tool, tt.input, result, tt.contains)
			}
		})
	}
}

func TestAgentLogToolRunningIndicator(t *testing.T) {
	store := NewStateStore(nil)
	tr := NewThinkingTracker()
	al := NewAgentLog(store, tr, 20)
	al.SetFocusedNode("n1")
	// Simulate the app routing: StartTool on tracker + Update on agentlog
	tr.StartTool("n1", "bash")
	al.Update(MsgToolCallStart{NodeID: "n1", ToolName: "bash", ToolInput: `{"command":"ls"}`})
	view := al.View()
	if !strings.Contains(view, "⚡") || !strings.Contains(view, "bash") {
		t.Errorf("expected tool running indicator with ⚡ and tool name, got: %s", view)
	}
	// After tool ends, indicator should disappear
	tr.StopTool("n1")
	al.Update(MsgToolCallEnd{NodeID: "n1", ToolName: "bash", Output: "file.go"})
	view = al.View()
	if strings.Contains(view, "⚡") {
		t.Error("tool running indicator should disappear after tool ends")
	}
}

func TestAgentLogThinkingOverToolIndicator(t *testing.T) {
	store := NewStateStore(nil)
	tr := NewThinkingTracker()
	al := NewAgentLog(store, tr, 20)
	al.SetFocusedNode("n1")
	// Start thinking, then start a tool — tool should take priority
	tr.Start("n1")
	tr.StartTool("n1", "read")
	al.Update(MsgToolCallStart{NodeID: "n1", ToolName: "read", ToolInput: `{"path":"main.go"}`})
	view := al.View()
	if !strings.Contains(view, "⚡") {
		t.Errorf("tool indicator should take priority over thinking, got: %s", view)
	}
	if strings.Contains(view, "⟳ Thinking") {
		t.Error("should not show thinking indicator while tool is running")
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
