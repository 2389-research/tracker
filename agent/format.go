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

	if s := formatKnownToolStart(evt.ToolName, input); s != "" {
		return s
	}

	if summary := extractInputSummary(input); summary != "" {
		return "━ " + evt.ToolName + ": " + previewEventText(summary)
	}
	return "━ " + evt.ToolName
}

// formatKnownToolStart returns a formatted string for known tool types,
// or empty string if the tool is not recognized or has no relevant input.
func formatKnownToolStart(toolName string, input map[string]string) string {
	switch toolName {
	case "bash":
		return formatBashStart(input)
	case "read", "write", "edit":
		return formatPathTool("━ "+toolName+" ", input["path"])
	case "apply_patch":
		return formatPathTool("━ patch ", input["path"])
	case "grep":
		return formatGrepStart(input)
	case "glob":
		return "━ glob " + input["pattern"]
	case "spawn_agent":
		if task := input["task"]; task != "" {
			return "━ spawn_agent: " + previewEventText(task)
		}
	}
	return ""
}

func formatBashStart(input map[string]string) string {
	if cmd := input["command"]; cmd != "" {
		return "$ " + previewEventText(cmd)
	}
	return "━ bash"
}

func formatPathTool(prefix, path string) string {
	if path != "" {
		return prefix + path
	}
	return ""
}

func formatGrepStart(input map[string]string) string {
	s := "━ grep " + input["pattern"]
	if path := input["path"]; path != "" {
		s += " " + path
	}
	return s
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
