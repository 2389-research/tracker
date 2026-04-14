// ABOUTME: Context compaction logic that replaces old tool results with short summaries.
// ABOUTME: Reduces context window consumption by summarizing stale tool outputs.
package agent

import (
	"fmt"
	"strings"

	"github.com/2389-research/tracker/llm"
)

const defaultProtectedTurns = 5

func compactSummary(toolName, content string) string {
	if content == "" {
		return fmt.Sprintf("[previous %s result — 0 chars. Re-run if needed.]", toolName)
	}
	switch toolName {
	case "read_file", "read":
		return compactReadSummary(toolName, content)
	case "grep_search", "grep":
		return compactGrepSummary(toolName, content)
	case "bash", "execute_command":
		return compactBashSummary(content)
	default:
		return fmt.Sprintf("[previous %s result — %d chars. Re-run if needed.]", toolName, len(content))
	}
}

func compactReadSummary(toolName, content string) string {
	lineCount := strings.Count(content, "\n")
	if lineCount == 0 && len(content) > 0 {
		lineCount = 1
	}
	unit := "lines"
	if lineCount == 1 {
		unit = "line"
	}
	return fmt.Sprintf("[previously read: %d %s. Re-read with %s if needed.]", lineCount, unit, toolName)
}

func compactGrepSummary(toolName, content string) string {
	matchCount := 0
	for _, line := range strings.Split(content, "\n") {
		if strings.TrimSpace(line) != "" {
			matchCount++
		}
	}
	return fmt.Sprintf("[previously searched: %d matches found. Re-run %s if needed.]", matchCount, toolName)
}

func compactBashSummary(content string) string {
	firstLine := content
	if idx := strings.Index(content, "\n"); idx >= 0 {
		firstLine = content[:idx]
	}
	if len(firstLine) > 30 {
		firstLine = firstLine[:30]
	}
	return fmt.Sprintf("[previously ran: %s — Re-run if needed.]", firstLine)
}

// compactMessages returns a new message slice with old tool results replaced by summaries.
// Turns older than currentTurn - protectedTurns have their non-error tool results compacted.
// The original slice is never modified.
func compactMessages(messages []llm.Message, currentTurn, protectedTurns int) []llm.Message {
	cutoffTurn := currentTurn - protectedTurns
	if cutoffTurn <= 0 {
		return messages
	}

	result := make([]llm.Message, len(messages))
	turnCounter := 0

	for i, msg := range messages {
		if msg.Role == llm.RoleAssistant {
			turnCounter++
		}
		if msg.Role == llm.RoleTool && turnCounter <= cutoffTurn {
			result[i] = compactToolMessage(msg)
		} else {
			result[i] = msg
		}
	}

	return result
}

// compactToolMessage replaces non-error tool results in a message with summary strings.
func compactToolMessage(msg llm.Message) llm.Message {
	newMsg := llm.Message{
		Role:    msg.Role,
		Content: make([]llm.ContentPart, len(msg.Content)),
	}
	for j, part := range msg.Content {
		if part.Kind == llm.KindToolResult && part.ToolResult != nil && !part.ToolResult.IsError {
			newResult := *part.ToolResult
			newResult.Content = compactSummary(newResult.Name, newResult.Content)
			newPart := part
			newPart.ToolResult = &newResult
			newMsg.Content[j] = newPart
		} else {
			newMsg.Content[j] = part
		}
	}
	return newMsg
}

// compactIfNeeded checks context utilization and compacts old tool results if needed.
func (s *Session) compactIfNeeded(tracker *ContextWindowTracker, currentTurn int) {
	if s.config.ContextCompaction != CompactionAuto {
		return
	}
	if tracker.Utilization() < s.config.CompactionThreshold {
		return
	}
	s.messages = compactMessages(s.messages, currentTurn, defaultProtectedTurns)
}

// totalToolResultBytes sums the content length of all tool result parts across all messages.
func totalToolResultBytes(messages []llm.Message) int {
	total := 0
	for _, msg := range messages {
		for _, part := range msg.Content {
			if part.Kind == llm.KindToolResult && part.ToolResult != nil {
				total += len(part.ToolResult.Content)
			}
		}
	}
	return total
}
