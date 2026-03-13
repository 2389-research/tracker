// ABOUTME: Context compaction logic that replaces old tool results with short summaries.
// ABOUTME: Reduces context window consumption by summarizing stale tool outputs.
package agent

import (
	"fmt"
	"strings"
)

func compactSummary(toolName, content string) string {
	if content == "" {
		return fmt.Sprintf("[previous %s result — 0 chars. Re-run if needed.]", toolName)
	}

	switch toolName {
	case "read_file", "read":
		lineCount := strings.Count(content, "\n")
		if lineCount == 0 && len(content) > 0 {
			lineCount = 1
		}
		return fmt.Sprintf("[previously read: %d lines. Re-read with %s if needed.]", lineCount, toolName)

	case "grep_search", "grep":
		matchCount := 0
		for _, line := range strings.Split(content, "\n") {
			if strings.TrimSpace(line) != "" {
				matchCount++
			}
		}
		return fmt.Sprintf("[previously searched: %d matches found. Re-run %s if needed.]", matchCount, toolName)

	case "bash", "execute_command":
		firstLine := content
		if idx := strings.Index(content, "\n"); idx >= 0 {
			firstLine = content[:idx]
		}
		if len(firstLine) > 30 {
			firstLine = firstLine[:30]
		}
		return fmt.Sprintf("[previously ran: %s — Re-run if needed.]", firstLine)

	default:
		return fmt.Sprintf("[previous %s result — %d chars. Re-run if needed.]", toolName, len(content))
	}
}
