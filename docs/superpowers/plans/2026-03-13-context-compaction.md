# Context Compaction Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Reduce context consumption by summarizing old tool outputs when the context window fills up, extending effective session length.

**Architecture:** Fix the ContextWindowTracker to use latest-input-token tracking (not cumulative summation), then add a compaction pass that replaces old tool result content with short summaries when utilization crosses a threshold. Compaction is opt-in via DOT graph attributes.

**Tech Stack:** Go, agent session loop, llm message types

---

## Chunk 1: Fix ContextWindowTracker

### Task 1: Fix ContextWindowTracker double-counting bug

The current `ContextWindowTracker.Update()` cumulatively sums `InputTokens + OutputTokens`, but `InputTokens` from the provider already includes the full conversation context for that turn. This makes utilization wildly inaccurate (double/triple-counting inputs).

**Files:**
- Modify: `agent/context_window.go`
- Modify: `agent/context_window_test.go`

- [ ] **Step 1: Update tests first — change expectations for the new tracking model**

The tracker should now store `latestInputTokens` (latest turn's input tokens as context size proxy) instead of cumulative. `Utilization()` should return `latestInputTokens / Limit`.

In `agent/context_window_test.go`, rewrite `TestContextWindowTracker_Update` to verify:
- After first update with `InputTokens: 100, OutputTokens: 50`, the utilization should be `100 / 200000 = 0.0005` (not `150 / 200000`).
- After second update with `InputTokens: 200, OutputTokens: 100`, utilization should be `200 / 200000 = 0.001` (latest input, not cumulative).

Rewrite `TestContextWindowTracker_Utilization` to verify:
- After update with `InputTokens: 300, OutputTokens: 200` on a limit-1000 tracker, utilization should be `0.3` (not `0.5`).

Update `TestContextWindowTracker_ShouldWarn`:
- "below threshold": `InputTokens: 500, OutputTokens: 200` → utilization `0.5` < `0.8` → no warn.
- "at threshold": `InputTokens: 800, OutputTokens: 200` → utilization `0.8` = `0.8` → warn.
- "above threshold": `InputTokens: 900, OutputTokens: 200` → utilization `0.9` > `0.8` → warn.

Update `TestContextWindowTracker_WarnOnlyOnce`:
- Use `InputTokens: 900` to get above threshold.

Update `TestContextWindowTracker_ZeroTokens`:
- Remove assertion on `CurrentTokens` (field will be removed).

Update `TestContextWindowSession_UtilizationInResult`:
- With `InputTokens: 50, OutputTokens: 25`, expected utilization = `50.0 / 1000.0 = 0.05` (not `75.0 / 1000.0`).

Update `TestContextWindowSession_WarningEmitted`:
- First response `InputTokens: 400` → utilization `0.4` (no warn).
- Second response `InputTokens: 850` → utilization `0.85` (warn triggered).

```go
func TestContextWindowTracker_Update(t *testing.T) {
	tracker := NewContextWindowTracker(200000, 0.8)

	tracker.Update(llm.Usage{InputTokens: 100, OutputTokens: 50})
	if tracker.Utilization() != 100.0/200000.0 {
		t.Errorf("expected utilization %f, got %f", 100.0/200000.0, tracker.Utilization())
	}

	tracker.Update(llm.Usage{InputTokens: 200, OutputTokens: 100})
	if tracker.Utilization() != 200.0/200000.0 {
		t.Errorf("expected utilization %f after second update, got %f", 200.0/200000.0, tracker.Utilization())
	}
}
```

- [ ] **Step 2: Run tests to confirm they fail**

Run: `go test ./agent/ -run TestContextWindowTracker -v`
Expected: FAIL — tests expect new behavior but code still does cumulative tracking.

- [ ] **Step 3: Implement the fix in context_window.go**

Replace the `ContextWindowTracker` struct and methods:

```go
type ContextWindowTracker struct {
	Limit             int
	WarningThreshold  float64
	latestInputTokens int
	WarningEmitted    bool
}

func NewContextWindowTracker(limit int, threshold float64) *ContextWindowTracker {
	return &ContextWindowTracker{
		Limit:            limit,
		WarningThreshold: threshold,
	}
}

// Update records the latest turn's token usage. InputTokens from the provider
// represents the full context size this turn, so we store it directly rather
// than accumulating.
func (t *ContextWindowTracker) Update(usage llm.Usage) {
	t.latestInputTokens = usage.InputTokens
}

// Utilization returns the fraction of the context window currently consumed,
// based on the latest turn's input tokens (which represent actual context size).
func (t *ContextWindowTracker) Utilization() float64 {
	if t.Limit == 0 {
		return 0
	}
	return float64(t.latestInputTokens) / float64(t.Limit)
}
```

Keep `ShouldWarn()` and `MarkWarned()` unchanged — they use `Utilization()` already.

- [ ] **Step 4: Run tests to confirm they pass**

Run: `go test ./agent/ -run TestContextWindow -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add agent/context_window.go agent/context_window_test.go
git commit -m "fix(agent): use latest input tokens for context utilization instead of cumulative sum"
```

---

## Chunk 2: Compaction Config and Types

### Task 2: Add compaction config fields

**Files:**
- Modify: `agent/config.go`

- [ ] **Step 1: Write a failing test for config validation**

In `agent/context_window_test.go`, add:

```go
func TestSessionConfig_CompactionValidation(t *testing.T) {
	t.Run("valid compaction auto", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.ContextCompaction = CompactionAuto
		cfg.CompactionThreshold = 0.6
		if err := cfg.Validate(); err != nil {
			t.Errorf("expected valid config: %v", err)
		}
	})

	t.Run("invalid compaction threshold zero", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.ContextCompaction = CompactionAuto
		cfg.CompactionThreshold = 0
		if err := cfg.Validate(); err == nil {
			t.Error("expected error for CompactionThreshold = 0 when compaction is auto")
		}
	})

	t.Run("invalid compaction threshold above one", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.ContextCompaction = CompactionAuto
		cfg.CompactionThreshold = 1.1
		if err := cfg.Validate(); err == nil {
			t.Error("expected error for CompactionThreshold > 1.0")
		}
	})

	t.Run("none mode skips threshold validation", func(t *testing.T) {
		cfg := DefaultConfig()
		cfg.ContextCompaction = CompactionNone
		cfg.CompactionThreshold = 0
		if err := cfg.Validate(); err != nil {
			t.Errorf("CompactionNone should not validate threshold: %v", err)
		}
	})
}
```

- [ ] **Step 2: Run test to confirm it fails**

Run: `go test ./agent/ -run TestSessionConfig_CompactionValidation -v`
Expected: FAIL — `CompactionAuto` not defined.

- [ ] **Step 3: Add compaction fields to SessionConfig**

In `agent/config.go`, add type and fields:

```go
// CompactionMode controls whether context compaction is active.
type CompactionMode string

const (
	CompactionNone CompactionMode = "none"
	CompactionAuto CompactionMode = "auto"
)

// Add to SessionConfig struct:
// ContextCompaction  CompactionMode
// CompactionThreshold float64
```

Default: `ContextCompaction: CompactionNone` (opt-in). No default threshold needed when mode is none.

Add validation in `Validate()`:
```go
if c.ContextCompaction == CompactionAuto {
	if c.CompactionThreshold <= 0 || c.CompactionThreshold > 1.0 {
		return fmt.Errorf("CompactionThreshold must be > 0 and <= 1.0 when compaction is auto, got %f", c.CompactionThreshold)
	}
}
```

- [ ] **Step 4: Run tests to confirm they pass**

Run: `go test ./agent/ -run TestSessionConfig -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add agent/config.go agent/context_window_test.go
git commit -m "feat(agent): add compaction mode and threshold to session config"
```

---

## Chunk 3: Compaction Logic

### Task 3: Create compaction.go with summary generation

This is the core compaction logic: scan messages, identify old tool results, replace content with summaries.

**Files:**
- Create: `agent/compaction.go`
- Create: `agent/compaction_test.go`

- [ ] **Step 1: Write tests for summary generation**

Test each summary format independently:

```go
// agent/compaction_test.go
package agent

import "testing"

func TestCompactSummary_ReadTool(t *testing.T) {
	// Content in cat -n format (line-numbered output).
	content := "     1\tpackage main\n     2\t\n     3\tfunc main() {\n     4\t}\n"
	summary := compactSummary("read_file", content)
	expected := "[previously read: 4 lines. Re-read with read_file if needed.]"
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
```

- [ ] **Step 2: Run tests to confirm they fail**

Run: `go test ./agent/ -run TestCompactSummary -v`
Expected: FAIL — `compactSummary` not defined.

- [ ] **Step 3: Implement compactSummary**

In `agent/compaction.go`:

```go
// ABOUTME: Context compaction logic that replaces old tool results with short summaries.
// ABOUTME: Reduces context window consumption by summarizing stale tool outputs.
package agent

import (
	"fmt"
	"strings"
)

// compactSummary generates a short summary string for a tool result that has
// been compacted. The format varies by tool type to preserve the most useful
// metadata.
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
```

- [ ] **Step 4: Run tests to confirm they pass**

Run: `go test ./agent/ -run TestCompactSummary -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add agent/compaction.go agent/compaction_test.go
git commit -m "feat(agent): add compactSummary for tool result summarization"
```

### Task 4: Implement compactMessages and compactIfNeeded

**Files:**
- Modify: `agent/compaction.go`
- Modify: `agent/compaction_test.go`

- [ ] **Step 1: Write tests for compactMessages**

```go
func TestCompactMessages_PreservesRecentTurns(t *testing.T) {
	// Build a message list simulating 3 turns of tool calls.
	// Each turn: assistant message with tool call + tool result message.
	msgs := buildTestMessages(3)
	// currentTurn=3, protectedTurns=5 → all 3 turns are recent → nothing compacted.
	result := compactMessages(msgs, 3, 5)
	assertNoCompaction(t, result)
}

func TestCompactMessages_CompactsOldTurns(t *testing.T) {
	// 8 turns of tool calls.
	msgs := buildTestMessages(8)
	// currentTurn=8, protectedTurns=5 → turns 1-3 should be compacted.
	result := compactMessages(msgs, 8, 5)
	assertCompacted(t, result, 3) // first 3 turns' tool results compacted
}

func TestCompactMessages_PreservesErrors(t *testing.T) {
	msgs := buildTestMessagesWithError(6, 2) // error in turn 2
	result := compactMessages(msgs, 6, 5)
	// Turn 1 should be compacted, turn 2 has error → preserved.
	assertErrorPreserved(t, result, 2)
}

func TestCompactMessages_PreservesNonToolMessages(t *testing.T) {
	msgs := buildTestMessages(8)
	result := compactMessages(msgs, 8, 5)
	// System and user messages should never be modified.
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
```

Add helper functions:

```go
func buildTestMessages(turns int) []llm.Message {
	var msgs []llm.Message
	msgs = append(msgs, llm.SystemMessage("You are a helper."))
	msgs = append(msgs, llm.UserMessage("Do the task."))
	for i := 1; i <= turns; i++ {
		// Assistant message with a tool call.
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
		// Tool result message.
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
	// Find the tool result for errorTurn and mark it as error.
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

func assertNoCompaction(t *testing.T, msgs []llm.Message) {
	t.Helper()
	for _, msg := range msgs {
		if msg.Role == llm.RoleTool {
			for _, part := range msg.Content {
				if part.Kind == llm.KindToolResult && strings.HasPrefix(part.ToolResult.Content, "[previous") {
					t.Error("expected no compaction, but found compacted content")
				}
			}
		}
	}
}

func assertCompacted(t *testing.T, msgs []llm.Message, expectedCount int) {
	t.Helper()
	compacted := 0
	for _, msg := range msgs {
		if msg.Role == llm.RoleTool {
			for _, part := range msg.Content {
				if part.Kind == llm.KindToolResult && strings.HasPrefix(part.ToolResult.Content, "[previous") {
					compacted++
				}
			}
		}
	}
	if compacted != expectedCount {
		t.Errorf("expected %d compacted results, got %d", expectedCount, compacted)
	}
}

func assertErrorPreserved(t *testing.T, msgs []llm.Message, errorTurn int) {
	t.Helper()
	toolResultIdx := 0
	for _, msg := range msgs {
		if msg.Role == llm.RoleTool {
			toolResultIdx++
			if toolResultIdx == errorTurn {
				for _, part := range msg.Content {
					if part.Kind == llm.KindToolResult && part.ToolResult.IsError {
						if strings.HasPrefix(part.ToolResult.Content, "[previous") {
							t.Error("error result should not be compacted")
						}
						return
					}
				}
			}
		}
	}
}
```

- [ ] **Step 2: Run tests to confirm they fail**

Run: `go test ./agent/ -run TestCompactMessages -v`
Expected: FAIL — `compactMessages` not defined.

- [ ] **Step 3: Implement compactMessages and compactIfNeeded**

In `agent/compaction.go`, add:

```go
import "github.com/2389-research/tracker/llm"

const defaultProtectedTurns = 5

// compactMessages replaces old tool result content with short summaries.
// It protects the most recent `protectedTurns` tool-result-bearing turns
// and preserves error results. Returns a new slice (original is not modified).
func compactMessages(messages []llm.Message, currentTurn, protectedTurns int) []llm.Message {
	cutoffTurn := currentTurn - protectedTurns
	if cutoffTurn <= 0 {
		return messages
	}

	// Build a copy of messages. We need to identify which tool result
	// messages belong to which "turn". A turn boundary is an assistant
	// message (which precedes its tool results).
	result := make([]llm.Message, len(messages))
	copy(result, messages)

	turnCounter := 0
	for i, msg := range result {
		if msg.Role == llm.RoleAssistant {
			// Check if this assistant message contains tool calls.
			hasToolCall := false
			for _, part := range msg.Content {
				if part.Kind == llm.KindToolCall {
					hasToolCall = true
					break
				}
			}
			if hasToolCall {
				turnCounter++
			}
		}

		if msg.Role == llm.RoleTool && turnCounter <= cutoffTurn {
			// Compact tool results in this message.
			newContent := make([]llm.ContentPart, len(msg.Content))
			copy(newContent, msg.Content)
			for j, part := range newContent {
				if part.Kind == llm.KindToolResult && part.ToolResult != nil {
					if part.ToolResult.IsError {
						continue
					}
					compacted := *part.ToolResult
					compacted.Content = compactSummary(compacted.Name, compacted.Content)
					newContent[j].ToolResult = &compacted
				}
			}
			result[i] = llm.Message{
				Role:    msg.Role,
				Content: newContent,
			}
		}
	}

	return result
}

// compactIfNeeded checks context utilization and compacts messages if above threshold.
func (s *Session) compactIfNeeded(tracker *ContextWindowTracker, currentTurn int) {
	if s.config.ContextCompaction != CompactionAuto {
		return
	}
	if tracker.Utilization() < s.config.CompactionThreshold {
		return
	}
	s.messages = compactMessages(s.messages, currentTurn, defaultProtectedTurns)
}
```

- [ ] **Step 4: Run tests to confirm they pass**

Run: `go test ./agent/ -run TestCompactMessages -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add agent/compaction.go agent/compaction_test.go
git commit -m "feat(agent): implement context compaction for old tool results"
```

---

## Chunk 4: Session Integration

### Task 5: Wire compaction into the session loop

**Files:**
- Modify: `agent/session.go`
- Modify: `agent/events.go`
- Modify: `agent/session_test.go`

- [ ] **Step 1: Add EventContextCompaction event type**

In `agent/events.go`, add:

```go
EventContextCompaction EventType = "context_compaction"
```

- [ ] **Step 2: Write integration test for session compaction**

In `agent/session_test.go`, add a test that verifies compaction fires during a session:

```go
func TestSession_CompactsWhenAboveThreshold(t *testing.T) {
	// Use a small context window (1000 tokens) and a low threshold (0.3).
	// First LLM response triggers a tool call with high input tokens.
	// After tool execution, compaction should fire and replace old results.

	// 3 rounds of tool calls, then final text response.
	// Round 1: InputTokens: 100 (util 0.1 — no compact)
	// Round 2: InputTokens: 400 (util 0.4 — above 0.3, compact turn 1)
	// Round 3: InputTokens: 500 — tool call
	// Round 4: final text response

	responses := []*llm.Response{
		makeToolCallResponse("call_1", "read_file", `{"path":"a.go"}`, 100, 50),
		makeToolCallResponse("call_2", "read_file", `{"path":"b.go"}`, 400, 50),
		makeToolCallResponse("call_3", "read_file", `{"path":"c.go"}`, 500, 50),
		{
			Message:      llm.AssistantMessage("Done."),
			FinishReason: llm.FinishReason{Reason: "stop"},
			Usage:        llm.Usage{InputTokens: 600, OutputTokens: 10},
		},
	}

	client := &mockCompleter{responses: responses}

	cfg := DefaultConfig()
	cfg.ContextWindowLimit = 1000
	cfg.ContextCompaction = CompactionAuto
	cfg.CompactionThreshold = 0.3

	var events []Event
	handler := EventHandlerFunc(func(evt Event) {
		events = append(events, evt)
	})

	readTool := &stubTool{name: "read_file", output: "     1\tpackage main\n     2\t\n     3\tfunc main() {}\n"}
	sess := mustNewSession(t, client, cfg, WithEventHandler(handler), WithTools(readTool))
	_, err := sess.Run(context.Background(), "Read files")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Check that compaction event was emitted.
	compactionEvents := 0
	for _, evt := range events {
		if evt.Type == EventContextCompaction {
			compactionEvents++
		}
	}
	if compactionEvents == 0 {
		t.Error("expected at least one compaction event")
	}
}
```

Add helper:
```go
func makeToolCallResponse(id, name, args string, inputTokens, outputTokens int) *llm.Response {
	return &llm.Response{
		Message: llm.Message{
			Role: llm.RoleAssistant,
			Content: []llm.ContentPart{{
				Kind: llm.KindToolCall,
				ToolCall: &llm.ToolCallData{
					ID:        id,
					Name:      name,
					Arguments: json.RawMessage(args),
				},
			}},
		},
		FinishReason: llm.FinishReason{Reason: "tool_calls"},
		Usage:        llm.Usage{InputTokens: inputTokens, OutputTokens: outputTokens},
	}
}
```

- [ ] **Step 3: Run test to confirm it fails**

Run: `go test ./agent/ -run TestSession_CompactsWhenAboveThreshold -v`
Expected: FAIL — `CompactionAuto` may compile but session loop doesn't call compaction.

- [ ] **Step 4: Wire compactIfNeeded into session.go loop**

In `agent/session.go`, after `tracker.Update(resp.Usage)` and the warning check block (around line 215), add:

```go
// Check if context compaction is needed after updating utilization.
if s.config.ContextCompaction == CompactionAuto {
	prevLen := totalToolResultBytes(s.messages)
	s.compactIfNeeded(tracker, turn)
	newLen := totalToolResultBytes(s.messages)
	if newLen < prevLen {
		s.emit(Event{
			Type:               EventContextCompaction,
			SessionID:          s.id,
			Turn:               turn,
			ContextUtilization: tracker.Utilization(),
		})
	}
}
```

Add a helper function in `agent/compaction.go`:

```go
// totalToolResultBytes returns the total byte count of all tool result content
// in the message list. Used to detect whether compaction actually changed anything.
func totalToolResultBytes(messages []llm.Message) int {
	total := 0
	for _, msg := range messages {
		if msg.Role == llm.RoleTool {
			for _, part := range msg.Content {
				if part.Kind == llm.KindToolResult && part.ToolResult != nil {
					total += len(part.ToolResult.Content)
				}
			}
		}
	}
	return total
}
```

- [ ] **Step 5: Run tests to confirm they pass**

Run: `go test ./agent/ -run TestSession_Compacts -v`
Expected: PASS

- [ ] **Step 6: Run all agent tests**

Run: `go test ./agent/ -v`
Expected: PASS — no regressions.

- [ ] **Step 7: Commit**

```bash
git add agent/session.go agent/events.go agent/compaction.go agent/compaction_test.go agent/session_test.go
git commit -m "feat(agent): wire context compaction into session loop"
```

---

## Chunk 5: DOT Control and Pipeline Integration

### Task 6: Add DOT attributes for compaction and wire into codergen handler

**Files:**
- Modify: `pipeline/handlers/codergen.go`
- Modify: `pipeline/validate_semantic.go`
- Modify: `pipeline/validate_semantic_test.go`

- [ ] **Step 1: Write validation tests for new DOT attributes**

In `pipeline/validate_semantic_test.go`, add:

```go
func TestValidateNodeAttributes_ContextCompaction_Valid(t *testing.T) {
	g := NewGraph("test")
	g.Attrs["context_compaction"] = "auto"
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond"})
	g.AddNode(&Node{ID: "A", Shape: "box", Attrs: map[string]string{"context_compaction_threshold": "0.6"}})
	g.AddNode(&Node{ID: "e", Shape: "Msquare"})
	g.AddEdge(&Edge{From: "s", To: "A"})
	g.AddEdge(&Edge{From: "A", To: "e"})

	reg := NewHandlerRegistry()
	reg.Register(&semanticStubHandler{name: "codergen"})

	err := ValidateSemantic(g, reg)
	if err != nil {
		t.Errorf("expected valid, got: %v", err)
	}
}

func TestValidateNodeAttributes_ContextCompaction_InvalidMode(t *testing.T) {
	g := NewGraph("test")
	g.Attrs["context_compaction"] = "banana"
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond"})
	g.AddNode(&Node{ID: "e", Shape: "Msquare"})
	g.AddEdge(&Edge{From: "s", To: "e"})

	reg := NewHandlerRegistry()
	err := ValidateSemantic(g, reg)
	if err == nil {
		t.Error("expected error for invalid context_compaction value")
	}
}

func TestValidateNodeAttributes_ContextCompaction_InvalidThreshold(t *testing.T) {
	g := NewGraph("test")
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond"})
	g.AddNode(&Node{ID: "A", Shape: "box", Attrs: map[string]string{"context_compaction_threshold": "banana"}})
	g.AddNode(&Node{ID: "e", Shape: "Msquare"})
	g.AddEdge(&Edge{From: "s", To: "A"})
	g.AddEdge(&Edge{From: "A", To: "e"})

	reg := NewHandlerRegistry()
	reg.Register(&semanticStubHandler{name: "codergen"})

	err := ValidateSemantic(g, reg)
	if err == nil {
		t.Error("expected error for invalid threshold")
	}
}
```

- [ ] **Step 2: Run tests to confirm they fail**

Run: `go test ./pipeline/ -run TestValidateNodeAttributes_ContextCompaction -v`
Expected: FAIL — validation doesn't know about `context_compaction` or `context_compaction_threshold`.

- [ ] **Step 3: Add validation for compaction attributes**

In `pipeline/validate_semantic.go`, in `validateNodeAttributes()`:

```go
// Validate graph-level context_compaction.
if v, ok := g.Attrs["context_compaction"]; ok {
	if v != "auto" && v != "none" {
		ve.add(fmt.Sprintf("graph has invalid context_compaction %q: must be \"auto\" or \"none\"", v))
	}
}

// Inside the node loop, add:
if v, ok := node.Attrs["context_compaction_threshold"]; ok {
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || f <= 0 || f > 1.0 {
		ve.add(fmt.Sprintf("node %q has invalid context_compaction_threshold %q: must be a float > 0 and <= 1.0", node.ID, v))
	}
}
```

- [ ] **Step 4: Run tests to confirm they pass**

Run: `go test ./pipeline/ -run TestValidateNodeAttributes_ContextCompaction -v`
Expected: PASS

- [ ] **Step 5: Wire compaction config into codergen buildConfig**

In `pipeline/handlers/codergen.go`, in `buildConfig()`, add after the cache_tool_results block:

```go
// Context compaction: graph-level default, node-level override.
if v, ok := h.graphAttrs["context_compaction"]; ok && v == "auto" {
	config.ContextCompaction = agent.CompactionAuto
	config.CompactionThreshold = 0.6 // default threshold
}
if v, ok := node.Attrs["context_compaction"]; ok {
	if v == "auto" {
		config.ContextCompaction = agent.CompactionAuto
		if config.CompactionThreshold == 0 {
			config.CompactionThreshold = 0.6
		}
	} else {
		config.ContextCompaction = agent.CompactionNone
	}
}
if v, ok := node.Attrs["context_compaction_threshold"]; ok {
	if f, err := strconv.ParseFloat(v, 64); err == nil {
		config.CompactionThreshold = f
	}
}
```

- [ ] **Step 6: Run all tests**

Run: `go test ./... -count=1`
Expected: PASS — all packages green.

- [ ] **Step 7: Commit**

```bash
git add pipeline/validate_semantic.go pipeline/validate_semantic_test.go pipeline/handlers/codergen.go
git commit -m "feat(pipeline): add DOT attributes for context compaction control"
```

---

## Chunk 6: Event coverage and final polish

### Task 7: Update EventTypes test and add compaction to events_test.go

**Files:**
- Modify: `agent/events_test.go`

- [ ] **Step 1: Add EventContextCompaction to events_test.go**

Ensure the `TestEventTypes` test includes `EventContextCompaction` in its coverage list.

- [ ] **Step 2: Run all tests**

Run: `go test ./... -count=1`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add agent/events_test.go
git commit -m "test(agent): add EventContextCompaction to event type coverage"
```

### Task 8: Final integration test — full pipeline round trip

**Files:**
- Modify: `agent/session_test.go`

- [ ] **Step 1: Write test for compaction disabled (default)**

```go
func TestSession_NoCompactionWhenDisabled(t *testing.T) {
	// Default config has CompactionNone. Even with high utilization,
	// no compaction should occur.
	responses := []*llm.Response{
		makeToolCallResponse("call_1", "read_file", `{"path":"a.go"}`, 900, 50),
		{
			Message:      llm.AssistantMessage("Done."),
			FinishReason: llm.FinishReason{Reason: "stop"},
			Usage:        llm.Usage{InputTokens: 950, OutputTokens: 10},
		},
	}

	client := &mockCompleter{responses: responses}
	cfg := DefaultConfig()
	cfg.ContextWindowLimit = 1000
	// CompactionNone is default — do not set CompactionAuto.

	var events []Event
	handler := EventHandlerFunc(func(evt Event) {
		events = append(events, evt)
	})

	readTool := &stubTool{name: "read_file", output: "file content here"}
	sess := mustNewSession(t, client, cfg, WithEventHandler(handler), WithTools(readTool))
	_, err := sess.Run(context.Background(), "Read file")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, evt := range events {
		if evt.Type == EventContextCompaction {
			t.Error("should not emit compaction event when compaction is disabled")
		}
	}
}
```

- [ ] **Step 2: Run all tests one final time**

Run: `go test ./... -count=1`
Expected: PASS — all packages green, binary builds clean.

- [ ] **Step 3: Commit**

```bash
git add agent/session_test.go
git commit -m "test(agent): add session-level compaction integration tests"
```
