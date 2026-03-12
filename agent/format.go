// ABOUTME: Human-readable formatting helpers for live agent events.
// ABOUTME: Parses tool JSON input to show clean command/path displays instead of raw JSON.
package agent

import (
	"encoding/json"
	"fmt"
	"strings"
)

const eventPreviewLimit = 120

// FormatEventLine formats selected live agent events for console/TUI rendering.
// Parses tool input JSON to extract meaningful fields for a clean chat-like display.
func FormatEventLine(evt Event) string {
	switch evt.Type {
	case EventToolCallStart:
		return formatToolStart(evt)
	case EventToolCallEnd:
		return formatToolEnd(evt)
	case EventError:
		if evt.Err != nil {
			return "✖ " + evt.Err.Error()
		}
	}
	return ""
}

// formatToolStart renders tool invocations in a clean, human-readable way.
func formatToolStart(evt Event) string {
	input := parseToolInput(evt.ToolInput)

	switch evt.ToolName {
	case "bash":
		cmd := input["command"]
		if cmd == "" {
			return "━ bash"
		}
		return "$ " + previewEventText(cmd)

	case "read":
		path := input["path"]
		if path != "" {
			return "━ read " + path
		}

	case "write":
		path := input["path"]
		if path != "" {
			return "━ write " + path
		}

	case "edit":
		path := input["path"]
		if path != "" {
			return "━ edit " + path
		}

	case "apply_patch":
		path := input["path"]
		if path != "" {
			return "━ patch " + path
		}

	case "grep":
		pattern := input["pattern"]
		path := input["path"]
		s := "━ grep " + pattern
		if path != "" {
			s += " " + path
		}
		return s

	case "glob":
		pattern := input["pattern"]
		return "━ glob " + pattern

	case "spawn_agent":
		task := input["task"]
		if task != "" {
			return "━ spawn_agent: " + previewEventText(task)
		}
	}

	// Fallback: show tool name with best-effort summary
	if summary := extractInputSummary(input); summary != "" {
		return "━ " + evt.ToolName + ": " + previewEventText(summary)
	}
	return "━ " + evt.ToolName
}

// formatToolEnd renders tool results cleanly.
// Output preserves newlines so multiline results (bash, read) display properly
// in the scrollable viewport.
func formatToolEnd(evt Event) string {
	if evt.ToolError != "" {
		return fmt.Sprintf("✖ %s: %s", evt.ToolName, previewEventText(evt.ToolError))
	}

	output := strings.TrimSpace(evt.ToolOutput)
	if output == "" {
		return "✓ " + evt.ToolName
	}
	return "  " + output
}

// parseToolInput extracts string values from tool input JSON.
func parseToolInput(raw string) map[string]string {
	result := make(map[string]string)
	if raw == "" {
		return result
	}
	var parsed map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return result
	}
	for key, val := range parsed {
		var s string
		if err := json.Unmarshal(val, &s); err == nil {
			result[key] = s
		}
	}
	return result
}

// extractInputSummary picks the most useful field value from parsed input.
func extractInputSummary(input map[string]string) string {
	// Prefer common field names in order of usefulness
	for _, key := range []string{"path", "command", "pattern", "task", "query", "name", "url"} {
		if v := input[key]; v != "" {
			return v
		}
	}
	// Fall back to first non-empty value
	for _, v := range input {
		if v != "" {
			return v
		}
	}
	return ""
}

func previewEventText(text string) string {
	text = strings.TrimSpace(strings.ReplaceAll(text, "\n", " "))
	if len(text) <= eventPreviewLimit {
		return text
	}
	return text[:eventPreviewLimit-1] + "…"
}
