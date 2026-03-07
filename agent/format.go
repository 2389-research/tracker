// ABOUTME: Human-readable formatting helpers for live agent events.
package agent

import (
	"fmt"
	"strings"
)

const eventPreviewLimit = 120

// FormatEventLine formats selected live agent events for console/TUI rendering.
func FormatEventLine(evt Event) string {
	switch evt.Type {
	case EventToolCallStart:
		line := "tool start"
		if evt.ToolName != "" {
			line += " name=" + evt.ToolName
		}
		if preview := previewEventText(evt.ToolInput); preview != "" {
			line += fmt.Sprintf(" input=%q", preview)
		}
		return line

	case EventToolCallEnd:
		line := "tool done"
		if evt.ToolName != "" {
			line += " name=" + evt.ToolName
		}
		if preview := previewEventText(evt.ToolOutput); preview != "" {
			line += fmt.Sprintf(" output=%q", preview)
		}
		if evt.ToolError != "" {
			line += " error=true"
		}
		return line

	case EventError:
		if evt.Err != nil {
			return "agent error=" + evt.Err.Error()
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
