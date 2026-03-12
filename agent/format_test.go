// ABOUTME: Tests for human-readable formatting of agent events.
package agent

import (
	"strings"
	"testing"
)

func TestFormatBashStartShowsDollarCommand(t *testing.T) {
	line := FormatEventLine(Event{
		Type:      EventToolCallStart,
		ToolName:  "bash",
		ToolInput: `{"command": "ls -la"}`,
	})
	if !strings.Contains(line, "$ ls -la") {
		t.Fatalf("expected '$ ls -la', got %q", line)
	}
}

func TestFormatBashEndShowsOutput(t *testing.T) {
	line := FormatEventLine(Event{
		Type:       EventToolCallEnd,
		ToolName:   "bash",
		ToolOutput: "total 8\ndrwxr-xr-x  5 harper",
	})
	// Should show output without the tool name repeated
	if !strings.Contains(line, "total 8") {
		t.Fatalf("expected output content, got %q", line)
	}
	// Should not contain raw JSON
	if strings.Contains(line, "{") {
		t.Fatalf("should not contain JSON, got %q", line)
	}
}

func TestFormatReadStartShowsPath(t *testing.T) {
	line := FormatEventLine(Event{
		Type:      EventToolCallStart,
		ToolName:  "read",
		ToolInput: `{"path":"src/main.go"}`,
	})
	if !strings.Contains(line, "read") {
		t.Fatalf("expected tool name, got %q", line)
	}
	if !strings.Contains(line, "src/main.go") {
		t.Fatalf("expected path, got %q", line)
	}
	// Should not show raw JSON
	if strings.Contains(line, `"path"`) {
		t.Fatalf("should not show raw JSON key, got %q", line)
	}
}

func TestFormatWriteStartShowsPath(t *testing.T) {
	line := FormatEventLine(Event{
		Type:      EventToolCallStart,
		ToolName:  "write",
		ToolInput: `{"path":"out.txt","content":"hello"}`,
	})
	if !strings.Contains(line, "write") {
		t.Fatalf("expected tool name, got %q", line)
	}
	if !strings.Contains(line, "out.txt") {
		t.Fatalf("expected path, got %q", line)
	}
}

func TestFormatEditStartShowsPath(t *testing.T) {
	line := FormatEventLine(Event{
		Type:      EventToolCallStart,
		ToolName:  "edit",
		ToolInput: `{"path":"main.go","old":"foo","new":"bar"}`,
	})
	if !strings.Contains(line, "edit") {
		t.Fatalf("expected tool name, got %q", line)
	}
	if !strings.Contains(line, "main.go") {
		t.Fatalf("expected path, got %q", line)
	}
}

func TestFormatGrepStartShowsPattern(t *testing.T) {
	line := FormatEventLine(Event{
		Type:      EventToolCallStart,
		ToolName:  "grep",
		ToolInput: `{"pattern":"TODO","path":"src/"}`,
	})
	if !strings.Contains(line, "grep") {
		t.Fatalf("expected tool name, got %q", line)
	}
	if !strings.Contains(line, "TODO") {
		t.Fatalf("expected pattern, got %q", line)
	}
}

func TestFormatGlobStartShowsPattern(t *testing.T) {
	line := FormatEventLine(Event{
		Type:      EventToolCallStart,
		ToolName:  "glob",
		ToolInput: `{"pattern":"**/*.go"}`,
	})
	if !strings.Contains(line, "glob") {
		t.Fatalf("expected tool name, got %q", line)
	}
	if !strings.Contains(line, "**/*.go") {
		t.Fatalf("expected pattern, got %q", line)
	}
}

func TestFormatToolEndError(t *testing.T) {
	line := FormatEventLine(Event{
		Type:      EventToolCallEnd,
		ToolName:  "bash",
		ToolError: "exit code 1",
	})
	if !strings.Contains(line, "✖") {
		t.Fatalf("expected error indicator, got %q", line)
	}
	if !strings.Contains(line, "exit code 1") {
		t.Fatalf("expected error message, got %q", line)
	}
}

func TestFormatToolEndNoOutput(t *testing.T) {
	line := FormatEventLine(Event{
		Type:       EventToolCallEnd,
		ToolName:   "bash",
		ToolOutput: "",
	})
	if !strings.Contains(line, "✓") {
		t.Fatalf("expected success indicator, got %q", line)
	}
}

func TestFormatToolEndWithOutput(t *testing.T) {
	line := FormatEventLine(Event{
		Type:       EventToolCallEnd,
		ToolName:   "read",
		ToolOutput: "package main\n\nimport \"fmt\"",
	})
	if !strings.Contains(line, "package main") {
		t.Fatalf("expected output preview, got %q", line)
	}
}

func TestFormatSpawnAgentStart(t *testing.T) {
	line := FormatEventLine(Event{
		Type:      EventToolCallStart,
		ToolName:  "spawn_agent",
		ToolInput: `{"task":"research the API"}`,
	})
	if !strings.Contains(line, "spawn_agent") {
		t.Fatalf("expected tool name, got %q", line)
	}
	if !strings.Contains(line, "research the API") {
		t.Fatalf("expected task content, got %q", line)
	}
}

func TestFormatUnknownToolFallsBackToPreview(t *testing.T) {
	line := FormatEventLine(Event{
		Type:      EventToolCallStart,
		ToolName:  "custom_tool",
		ToolInput: `{"foo":"bar","baz":123}`,
	})
	if !strings.Contains(line, "custom_tool") {
		t.Fatalf("expected tool name, got %q", line)
	}
}

func TestFormatErrorEvent(t *testing.T) {
	line := FormatEventLine(Event{
		Type: EventError,
		Err:  &testErr{msg: "connection timeout"},
	})
	if !strings.Contains(line, "✖") {
		t.Fatalf("expected error indicator, got %q", line)
	}
	if !strings.Contains(line, "connection timeout") {
		t.Fatalf("expected error message, got %q", line)
	}
}

type testErr struct{ msg string }

func (e *testErr) Error() string { return e.msg }
