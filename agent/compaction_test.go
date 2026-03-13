// ABOUTME: Tests for context compaction summary generation and message compaction.
// ABOUTME: Verifies per-tool-type summary formats, turn-based compaction, and edge cases.
package agent

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/2389-research/tracker/llm"
)

func TestCompactSummary_ReadTool(t *testing.T) {
	content := "     1\tpackage main\n     2\t\n     3\tfunc main() {\n     4\t}\n"
	summary := compactSummary("read_file", content)
	expected := "[previously read: 4 lines. Re-read with read_file if needed.]"
	if summary != expected {
		t.Errorf("expected %q, got %q", expected, summary)
	}
}

func TestCompactSummary_ReadToolAltName(t *testing.T) {
	content := "     1\tpackage main\n"
	summary := compactSummary("read", content)
	expected := "[previously read: 1 line. Re-read with read if needed.]"
	if summary != expected {
		t.Errorf("expected %q, got %q", expected, summary)
	}
}

func TestCompactSummary_GrepTool(t *testing.T) {
	content := "src/main.go:5:func main\nsrc/util.go:10:func helper\nsrc/lib.go:3:func init\n"
	summary := compactSummary("grep_search", content)
	expected := "[previously searched: 3 matches found. Re-run grep_search if needed.]"
	if summary != expected {
		t.Errorf("expected %q, got %q", expected, summary)
	}
}

func TestCompactSummary_BashTool(t *testing.T) {
	content := "go test ./... -v\nok  \tpackage1\nok  \tpackage2\n"
	summary := compactSummary("bash", content)
	expected := "[previously ran: go test ./... -v — Re-run if needed.]"
	if summary != expected {
		t.Errorf("expected %q, got %q", expected, summary)
	}
}

func TestCompactSummary_GenericTool(t *testing.T) {
	content := "some output that is 50 characters long or whateve"
	summary := compactSummary("list_files", content)
	expected := "[previous list_files result — 49 chars. Re-run if needed.]"
	if summary != expected {
		t.Errorf("expected %q, got %q", expected, summary)
	}
}

func TestCompactSummary_EmptyContent(t *testing.T) {
	summary := compactSummary("read_file", "")
	expected := "[previous read_file result — 0 chars. Re-run if needed.]"
	if summary != expected {
		t.Errorf("expected %q, got %q", expected, summary)
	}
}

func buildTestMessages(turns int) []llm.Message {
	var msgs []llm.Message
	msgs = append(msgs, llm.SystemMessage("You are a helper."))
	msgs = append(msgs, llm.UserMessage("Do the task."))
	for i := 1; i <= turns; i++ {
		msgs = append(msgs, llm.Message{
			Role: llm.RoleAssistant,
			Content: []llm.ContentPart{{
				Kind: llm.KindToolCall,
				ToolCall: &llm.ToolCallData{
					ID:        fmt.Sprintf("call_%d", i),
					Name:      "read_file",
					Arguments: json.RawMessage(fmt.Sprintf(`{"path":"file%d.go"}`, i)),
				},
			}},
		})
		msgs = append(msgs, llm.Message{
			Role: llm.RoleTool,
			Content: []llm.ContentPart{{
				Kind: llm.KindToolResult,
				ToolResult: &llm.ToolResultData{
					ToolCallID: fmt.Sprintf("call_%d", i),
					Name:       "read_file",
					Content:    fmt.Sprintf("     1\tpackage file%d\n     2\t\n     3\tfunc init() {}\n", i),
					IsError:    false,
				},
			}},
		})
	}
	return msgs
}

func buildTestMessagesWithError(turns, errorTurn int) []llm.Message {
	msgs := buildTestMessages(turns)
	toolResultIdx := 0
	for i, msg := range msgs {
		if msg.Role == llm.RoleTool {
			toolResultIdx++
			if toolResultIdx == errorTurn {
				msgs[i].Content[0].ToolResult.IsError = true
				msgs[i].Content[0].ToolResult.Content = "file not found"
				break
			}
		}
	}
	return msgs
}

func TestCompactMessages_PreservesRecentTurns(t *testing.T) {
	msgs := buildTestMessages(3)
	result := compactMessages(msgs, 3, 5)
	// All 3 turns are recent (3-5 = -2, cutoff <= 0) -> nothing compacted.
	for _, msg := range result {
		if msg.Role == llm.RoleTool {
			for _, part := range msg.Content {
				if part.Kind == llm.KindToolResult && strings.HasPrefix(part.ToolResult.Content, "[previous") {
					t.Error("expected no compaction")
				}
			}
		}
	}
}

func TestCompactMessages_CompactsOldTurns(t *testing.T) {
	msgs := buildTestMessages(8)
	result := compactMessages(msgs, 8, 5)
	// Turns 1-3 should be compacted (cutoff = 8-5 = 3).
	compacted := 0
	for _, msg := range result {
		if msg.Role == llm.RoleTool {
			for _, part := range msg.Content {
				if part.Kind == llm.KindToolResult && strings.HasPrefix(part.ToolResult.Content, "[previously") {
					compacted++
				}
			}
		}
	}
	if compacted != 3 {
		t.Errorf("expected 3 compacted results, got %d", compacted)
	}
}

func TestCompactMessages_PreservesErrors(t *testing.T) {
	msgs := buildTestMessagesWithError(8, 2) // error in turn 2
	result := compactMessages(msgs, 8, 5)
	// Turn 1 and 3 should be compacted, turn 2 has error -> preserved.
	toolResultIdx := 0
	for _, msg := range result {
		if msg.Role == llm.RoleTool {
			toolResultIdx++
			for _, part := range msg.Content {
				if part.Kind == llm.KindToolResult {
					if toolResultIdx == 2 && part.ToolResult.IsError {
						if strings.HasPrefix(part.ToolResult.Content, "[previous") {
							t.Error("error result should not be compacted")
						}
					}
				}
			}
		}
	}
	// Should still compact 2 (turns 1 and 3).
	compacted := 0
	for _, msg := range result {
		if msg.Role == llm.RoleTool {
			for _, part := range msg.Content {
				if part.Kind == llm.KindToolResult && strings.HasPrefix(part.ToolResult.Content, "[previously") {
					compacted++
				}
			}
		}
	}
	if compacted != 2 {
		t.Errorf("expected 2 compacted (1 error preserved), got %d", compacted)
	}
}

func TestCompactMessages_PreservesNonToolMessages(t *testing.T) {
	msgs := buildTestMessages(8)
	result := compactMessages(msgs, 8, 5)
	for _, msg := range result {
		if msg.Role == llm.RoleSystem || msg.Role == llm.RoleUser {
			for _, part := range msg.Content {
				if part.Kind == llm.KindText && strings.HasPrefix(part.Text, "[previous") {
					t.Error("system/user message should not be compacted")
				}
			}
		}
	}
}

func TestCompactMessages_DoesNotModifyOriginal(t *testing.T) {
	msgs := buildTestMessages(8)
	originalContent := msgs[3].Content[0].ToolResult.Content // Turn 1's tool result
	_ = compactMessages(msgs, 8, 5)
	if msgs[3].Content[0].ToolResult.Content != originalContent {
		t.Error("compactMessages should not modify original messages")
	}
}

func TestCompactMessages_MixedTextAndToolTurns(t *testing.T) {
	// Simulate a session with text-only turns (e.g., truncation continuations)
	// mixed with tool-call turns. Turn counting should count ALL assistant
	// messages as turns, matching the session loop's turn counter.
	var msgs []llm.Message
	msgs = append(msgs, llm.SystemMessage("You are a helper."))
	msgs = append(msgs, llm.UserMessage("Do the task."))

	// Turn 1: tool call
	msgs = append(msgs, llm.Message{
		Role: llm.RoleAssistant,
		Content: []llm.ContentPart{{
			Kind:     llm.KindToolCall,
			ToolCall: &llm.ToolCallData{ID: "call_1", Name: "read_file", Arguments: json.RawMessage(`{"path":"a.go"}`)},
		}},
	})
	msgs = append(msgs, llm.Message{
		Role: llm.RoleTool,
		Content: []llm.ContentPart{{
			Kind:       llm.KindToolResult,
			ToolResult: &llm.ToolResultData{ToolCallID: "call_1", Name: "read_file", Content: "     1\tpackage a\n     2\t\n"},
		}},
	})

	// Turn 2: text-only (truncation continuation)
	msgs = append(msgs, llm.Message{
		Role:    llm.RoleAssistant,
		Content: []llm.ContentPart{{Kind: llm.KindText, Text: "Continuing..."}},
	})

	// Turn 3: tool call
	msgs = append(msgs, llm.Message{
		Role: llm.RoleAssistant,
		Content: []llm.ContentPart{{
			Kind:     llm.KindToolCall,
			ToolCall: &llm.ToolCallData{ID: "call_2", Name: "read_file", Arguments: json.RawMessage(`{"path":"b.go"}`)},
		}},
	})
	msgs = append(msgs, llm.Message{
		Role: llm.RoleTool,
		Content: []llm.ContentPart{{
			Kind:       llm.KindToolResult,
			ToolResult: &llm.ToolResultData{ToolCallID: "call_2", Name: "read_file", Content: "     1\tpackage b\n     2\t\n"},
		}},
	})

	// currentTurn=8, protectedTurns=5 → cutoff=3. Turn 1 and turn 3 are both
	// within the cutoff. Turn 2 (text-only) counts as a turn, so turn 3 (the
	// second tool call) has turnCounter=3 which equals the cutoff → compacted.
	// Turn 1 has turnCounter=1 → compacted.
	result := compactMessages(msgs, 8, 5)

	compacted := 0
	for _, msg := range result {
		if msg.Role == llm.RoleTool {
			for _, part := range msg.Content {
				if part.Kind == llm.KindToolResult && strings.HasPrefix(part.ToolResult.Content, "[previously") {
					compacted++
				}
			}
		}
	}
	if compacted != 2 {
		t.Errorf("expected 2 compacted results (both tool-call turns before cutoff), got %d", compacted)
	}
}

func TestTotalToolResultBytes(t *testing.T) {
	msgs := buildTestMessages(3)
	total := totalToolResultBytes(msgs)
	if total == 0 {
		t.Error("expected non-zero total tool result bytes")
	}
	// Each tool result has content like "     1\tpackage file1\n     2\t\n     3\tfunc init() {}\n"
	// which is about 48 chars each.
	if total < 100 {
		t.Errorf("expected total > 100, got %d", total)
	}
}
