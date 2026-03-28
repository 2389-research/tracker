// ABOUTME: Tests for the AgentLog component.
// ABOUTME: Covers text coalescing, expand/collapse, thinking indicator, and tool formatting.
package tui

import (
	"fmt"
	"strings"
	"testing"
)

// stripANSI removes ANSI escape sequences for text content checks.
func stripANSI(s string) string {
	var out strings.Builder
	inEscape := false
	for _, r := range s {
		if r == '\033' {
			inEscape = true
			continue
		}
		if inEscape {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
				inEscape = false
			}
			continue
		}
		out.WriteRune(r)
	}
	return out.String()
}

func TestAgentLogTextCoalescing(t *testing.T) {
	store := NewStateStore(nil)
	tr := NewThinkingTracker()
	al := NewAgentLog(store, tr, 20)
	al.Update(MsgTextChunk{NodeID: "n1", Text: "Hello "})
	al.Update(MsgTextChunk{NodeID: "n1", Text: "world"})
	view := al.View()
	plain := stripANSI(view)
	if !strings.Contains(plain, "Hello world") {
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
	plain := stripANSI(view)
	if !strings.Contains(plain, "before") || !strings.Contains(plain, "after") {
		t.Errorf("expected both text segments, got: %s", view)
	}
	if !strings.Contains(plain, "exec") {
		t.Errorf("expected tool name, got: %s", view)
	}
}

func TestAgentLogThinkingIndicator(t *testing.T) {
	store := NewStateStore(nil)
	store.SetNodes([]NodeEntry{{ID: "n1", Label: "Agent1"}})
	store.Apply(MsgNodeStarted{NodeID: "n1"})
	tr := NewThinkingTracker()
	tr.Start("n1")
	al := NewAgentLog(store, tr, 20)
	view := al.View()
	if !strings.Contains(view, "⟳") || !strings.Contains(view, "thinking") {
		t.Errorf("expected thinking indicator, got: %s", view)
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
	store.SetNodes([]NodeEntry{{ID: "n1", Label: "Agent1"}})
	store.Apply(MsgNodeStarted{NodeID: "n1"})
	tr := NewThinkingTracker()
	al := NewAgentLog(store, tr, 20)
	tr.StartTool("n1", "bash")
	al.Update(MsgToolCallStart{NodeID: "n1", ToolName: "bash", ToolInput: `{"command":"ls"}`})
	view := al.View()
	if !strings.Contains(view, "»") || !strings.Contains(view, "bash") {
		t.Errorf("expected tool running indicator with > and tool name, got: %s", view)
	}
	tr.StopTool("n1")
	al.Update(MsgToolCallEnd{NodeID: "n1", ToolName: "bash", Output: "file.go"})
	view = al.View()
	if strings.Contains(view, "»") {
		t.Error("tool running indicator should disappear after tool ends")
	}
}

func TestAgentLogThinkingOverToolIndicator(t *testing.T) {
	store := NewStateStore(nil)
	store.SetNodes([]NodeEntry{{ID: "n1", Label: "Agent1"}})
	store.Apply(MsgNodeStarted{NodeID: "n1"})
	tr := NewThinkingTracker()
	al := NewAgentLog(store, tr, 20)
	// Start thinking, then start a tool — tool should take priority
	tr.Start("n1")
	tr.StartTool("n1", "read")
	al.Update(MsgToolCallStart{NodeID: "n1", ToolName: "read", ToolInput: `{"path":"main.go"}`})
	view := al.View()
	if !strings.Contains(view, "»") {
		t.Errorf("tool indicator should take priority over thinking, got: %s", view)
	}
}

func TestAgentLogClipsToViewportHeight(t *testing.T) {
	store := NewStateStore(nil)
	tr := NewThinkingTracker()
	// Create a very short viewport (5 lines total, 4 usable after header).
	al := NewAgentLog(store, tr, 5)
	// Add more entries than fit in the viewport.
	for i := 0; i < 20; i++ {
		al.Update(MsgTextChunk{NodeID: "n1", Text: fmt.Sprintf("line-%d\n", i)})
	}
	view := al.View()
	lines := strings.Split(strings.TrimRight(view, "\n"), "\n")
	// Should be clipped to 5 lines (1 header + 4 content).
	if len(lines) > 5 {
		t.Errorf("expected at most 5 lines in viewport, got %d:\n%s", len(lines), view)
	}
	// Should show the latest entries (tail behavior), not the oldest.
	if !strings.Contains(view, "line-19") {
		t.Errorf("expected latest entry visible in clipped view, got:\n%s", view)
	}
}

func TestAgentLogViewportOptimizationMatchesFullRender(t *testing.T) {
	store := NewStateStore(nil)
	tr := NewThinkingTracker()
	viewportHeight := 10
	al := NewAgentLog(store, tr, viewportHeight)
	al.SetSize(80, viewportHeight)
	// Add many lines — more than fit in the viewport.
	// Each entry ends with \n so it becomes a finalized styled line.
	for i := 0; i < 100; i++ {
		al.Update(MsgTextChunk{NodeID: "n1", Text: fmt.Sprintf("entry-%d\n", i)})
	}
	optimizedView := al.View()
	// The view should clip to viewport height.
	lines := strings.Split(strings.TrimRight(optimizedView, "\n"), "\n")
	if len(lines) > viewportHeight {
		t.Errorf("expected at most %d lines, got %d", viewportHeight, len(lines))
	}
	// Should contain the latest entry (tail behavior).
	if !strings.Contains(optimizedView, "entry-99") {
		t.Errorf("expected latest entry visible, got:\n%s", optimizedView)
	}
	// Should NOT contain early entries that scrolled off.
	if strings.Contains(optimizedView, "entry-0") {
		t.Errorf("early entry should not be visible in clipped view, got:\n%s", optimizedView)
	}
}

func TestAgentLogLineStyleHeaders(t *testing.T) {
	store := NewStateStore(nil)
	tr := NewThinkingTracker()
	al := NewAgentLog(store, tr, 40)
	al.SetSize(80, 40)
	al.Update(MsgTextChunk{NodeID: "n1", Text: "# Big Header\n## Sub Header\nplain text\n"})
	view := al.View()
	if !strings.Contains(view, "Big Header") {
		t.Errorf("expected header text visible, got:\n%s", view)
	}
	if !strings.Contains(view, "Sub Header") {
		t.Errorf("expected sub header text visible, got:\n%s", view)
	}
	if !strings.Contains(view, "plain text") {
		t.Errorf("expected plain text visible, got:\n%s", view)
	}
}

func TestAgentLogStreamingTextAccumulates(t *testing.T) {
	store := NewStateStore(nil)
	tr := NewThinkingTracker()
	al := NewAgentLog(store, tr, 40)
	al.SetSize(80, 40)
	al.Update(MsgTextChunk{NodeID: "n1", Text: "Hello"})
	view1 := al.View()
	// Append more text (still on the same line, no newline yet).
	al.Update(MsgTextChunk{NodeID: "n1", Text: " world"})
	view2 := al.View()
	if view1 == view2 {
		t.Error("expected view to change after appending text")
	}
	if !strings.Contains(view2, "world") {
		t.Errorf("expected 'world' in updated view, got:\n%s", view2)
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
