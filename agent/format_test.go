// ABOUTME: Tests for human-readable formatting of agent events.
package agent

import (
	"strings"
	"testing"
)

func TestFormatEventLineToolStart(t *testing.T) {
	line := FormatEventLine(Event{
		Type:      EventToolCallStart,
		ToolName:  "read",
		ToolInput: `{"path":"go.mod"}`,
	})

	if !strings.Contains(line, "tool start") {
		t.Fatalf("expected tool start prefix, got %q", line)
	}
	if !strings.Contains(line, "name=read") {
		t.Fatalf("expected tool name, got %q", line)
	}
}

func TestFormatEventLineToolDone(t *testing.T) {
	line := FormatEventLine(Event{
		Type:       EventToolCallEnd,
		ToolName:   "read",
		ToolOutput: "module github.com/example/project",
	})

	if !strings.Contains(line, "tool done") {
		t.Fatalf("expected tool done prefix, got %q", line)
	}
	if !strings.Contains(line, "name=read") {
		t.Fatalf("expected tool name, got %q", line)
	}
}
