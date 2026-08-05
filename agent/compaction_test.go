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
	expected := "[previously ran: go test ./... -v (passed) — 4 lines output. Re-run if needed.]"
	if summary != expected {
		t.Errorf("expected %q, got %q", expected, summary)
	}
}

func TestCompactBashSummary_DetectsPass(t *testing.T) {
	content := "pytest -q\nsome output\n3 passed\n"
	summary := compactBashSummary(content)
	if !strings.Contains(summary, "(passed)") {
		t.Errorf("expected summary to contain '(passed)', got: %s", summary)
	}
}

func TestCompactBashSummary_DetectsFail(t *testing.T) {
	content := "go test ./...\nsome output\nFAILED\n"
	summary := compactBashSummary(content)
	if !strings.Contains(summary, "(failed)") {
		t.Errorf("expected summary to contain '(failed)', got: %s", summary)
	}
}

func TestCompactBashSummary_LongCommand(t *testing.T) {
	longCmd := strings.Repeat("a", 100)
	content := longCmd + "\nsome output\n"
	summary := compactBashSummary(content)
	// Command should be truncated to 80 chars
	if strings.Contains(summary, longCmd) {
		t.Errorf("expected long command to be truncated, got: %s", summary)
	}
	if !strings.Contains(summary, strings.Repeat("a", 80)) {
		t.Errorf("expected first 80 chars of command in summary, got: %s", summary)
	}
}

func TestCompactBashSummary_MixedPassFail(t *testing.T) {
	// "3 passed, 1 failed" should be detected as failed, not passed.
	content := "pytest -q\nsome output\n3 passed, 1 failed in 4.2s\n"
	summary := compactBashSummary(content)
	if !strings.Contains(summary, "(failed)") {
		t.Errorf("expected '(failed)' for mixed pass/fail output, got: %s", summary)
	}
	if strings.Contains(summary, "(passed)") {
		t.Errorf("should not contain '(passed)' when failures are present, got: %s", summary)
	}
}

func TestCompactBashSummary_OkSubstring(t *testing.T) {
	// Words containing "ok" as a substring should NOT trigger pass detection.
	content := "pip install\nLooking for cookbook\nchecking hooks\n"
	summary := compactBashSummary(content)
	if strings.Contains(summary, "(passed)") {
		t.Errorf("'cookbook'/'hooks' should not trigger pass detection, got: %s", summary)
	}
}

func TestCompactBashSummary_GoTestOk(t *testing.T) {
	// Go's "ok \t<pkg>" format should trigger pass detection.
	content := "go test ./...\nok  \tgithub.com/foo/bar\n"
	summary := compactBashSummary(content)
	if !strings.Contains(summary, "(passed)") {
		t.Errorf("expected '(passed)' for Go test 'ok' output, got: %s", summary)
	}
}

func TestCompactBashSummary_NoSignal(t *testing.T) {
	content := "ls -la\nfile1.txt\nfile2.txt\n"
	summary := compactBashSummary(content)
	if strings.Contains(summary, "(passed)") || strings.Contains(summary, "(failed)") {
		t.Errorf("expected no signal suffix for neutral output, got: %s", summary)
	}
	if !strings.Contains(summary, "lines output") {
		t.Errorf("expected 'lines output' in summary, got: %s", summary)
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
	result := compactMessages(msgs, 5)
	// Only 3 turns exist (< 5 protected) -> nothing is old enough to compact.
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
	result := compactMessages(msgs, 5)
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
	result := compactMessages(msgs, 5)
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
	result := compactMessages(msgs, 5)
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
	_ = compactMessages(msgs, 5)
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

	// protectedTurns=2, counting assistant messages from the end: turn 3 (tool
	// call) and turn 2 (text-only) are the two protected turns — the text-only
	// turn correctly counts as a turn — so the cutoff is turn 2's assistant.
	// Turn 1's tool result is before it → compacted; turn 3's is preserved.
	result := compactMessages(msgs, 2)

	compacted := 0
	preservedTurn3 := false
	for _, msg := range result {
		if msg.Role != llm.RoleTool {
			continue
		}
		for _, part := range msg.Content {
			if part.Kind != llm.KindToolResult {
				continue
			}
			if strings.HasPrefix(part.ToolResult.Content, "[previously") {
				compacted++
			}
			if part.ToolResult.ToolCallID == "call_2" && !strings.HasPrefix(part.ToolResult.Content, "[previously") {
				preservedTurn3 = true
			}
		}
	}
	if compacted != 1 {
		t.Errorf("expected 1 compacted result (turn 1, before the 2 protected turns), got %d", compacted)
	}
	if !preservedTurn3 {
		t.Error("turn 3's tool result should be preserved (within the 2 protected turns)")
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

// #541: compaction protects the last N turns counting from the END, so extra
// assistant messages from planning/repair turns (never removed from history)
// cannot drift the cutoff and progressively disable compaction.
func TestCompactMessages_ImmuneToExtraAssistantMessages(t *testing.T) {
	msgs := buildTestMessages(8) // 8 tool turns
	// Simulate 4 planning/repair assistant messages accumulated in history
	// (these have no tool results; under the old assistant-tally-vs-currentTurn
	// model they inflated the count and pushed old turns out of the cutoff).
	extra := make([]llm.Message, 0, 4)
	for i := 0; i < 4; i++ {
		extra = append(extra, llm.Message{Role: llm.RoleAssistant, Content: []llm.ContentPart{{Kind: llm.KindText, Text: "repair"}}})
	}
	// Interleave them near the front (as a planning turn + early repairs would be).
	withExtra := append(append(append([]llm.Message{}, msgs[:2]...), extra...), msgs[2:]...)

	result := compactMessages(withExtra, 5)
	compacted := 0
	for _, msg := range result {
		if msg.Role != llm.RoleTool {
			continue
		}
		for _, part := range msg.Content {
			if part.Kind == llm.KindToolResult && strings.HasPrefix(part.ToolResult.Content, "[previous") {
				compacted++
			}
		}
	}
	// 8 tool turns, protect the last 5 → the 3 oldest tool results still compact,
	// regardless of the 4 extra assistant messages.
	if compacted != 3 {
		t.Errorf("expected 3 compacted (oldest 3 of 8 turns), got %d — extra assistant messages drifted the cutoff", compacted)
	}
}
