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
	lines := strings.Split(content, "\n")

	// Extract the command (first line, capped at 80 chars).
	cmd := lines[0]
	if len(cmd) > 80 {
		cmd = cmd[:80]
	}

	// Look for pass/fail signal in the last 10 lines.
	var signal string
	searchLines := lines
	if len(searchLines) > 10 {
		searchLines = searchLines[len(searchLines)-10:]
	}
	for _, line := range searchLines {
		lower := strings.ToLower(line)
		trimmed := strings.TrimSpace(lower)
		// Check failure keywords first — "3 passed, 1 failed" is a failure.
		// Use word-boundary-aware patterns to avoid matching "error_handler", "failover", etc.
		if strings.Contains(lower, "failed") ||
			strings.HasPrefix(trimmed, "error:") || strings.HasPrefix(trimmed, "error ") ||
			strings.Contains(lower, " error") || strings.HasSuffix(trimmed, "error") ||
			strings.Contains(lower, "failure") {
			signal = " (failed)"
			break
		}
		if strings.Contains(lower, "passed") ||
			strings.HasPrefix(trimmed, "ok ") || strings.HasPrefix(trimmed, "ok\t") || trimmed == "ok" {
			signal = " (passed)"
			break
		}
	}

	return fmt.Sprintf("[previously ran: %s%s — %d lines output. Re-run if needed.]", cmd, signal, len(lines))
}

// compactMessages returns a new message slice with old tool results replaced by
// summaries. The last protectedTurns turns (counted by assistant messages from
// the END) are preserved verbatim; non-error tool results before that are
// compacted. Counting from the end — rather than comparing an assistant tally
// to the loop's currentTurn — keeps the cutoff correct regardless of extra
// assistant messages from the planning turn and repair turns, which otherwise
// drift the tally ahead of currentTurn and progressively disable compaction
// (#541). The original slice is never modified.
func compactMessages(messages []llm.Message, protectedTurns int) []llm.Message {
	if protectedTurns <= 0 {
		return messages
	}
	cutoff, ok := oldestProtectedTurnStart(messages, protectedTurns)
	if !ok {
		return messages // fewer than protectedTurns turns exist — nothing is old enough
	}
	result := make([]llm.Message, len(messages))
	for i, msg := range messages {
		if i < cutoff && msg.Role == llm.RoleTool {
			result[i] = compactToolMessage(msg)
		} else {
			result[i] = msg
		}
	}
	return result
}

// oldestProtectedTurnStart returns the index of the assistant message that opens
// the oldest of the last protectedTurns turns (tool messages at or after it are
// recent and preserved), and ok=false when fewer than protectedTurns turns exist.
func oldestProtectedTurnStart(messages []llm.Message, protectedTurns int) (int, bool) {
	seen := 0
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == llm.RoleAssistant {
			seen++
			if seen == protectedTurns {
				return i, true
			}
		}
	}
	return 0, false
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
func (s *Session) compactIfNeeded(tracker *ContextWindowTracker) {
	if s.config.ContextCompaction != CompactionAuto {
		return
	}
	if tracker.Utilization() < s.config.CompactionThreshold {
		return
	}
	s.messages = compactMessages(s.messages, defaultProtectedTurns)
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
