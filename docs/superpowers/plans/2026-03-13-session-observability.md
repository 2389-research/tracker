# Session Observability Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add rich per-turn metrics, tool timing, cost estimation, and extended session results so callers can understand token spend, tool performance, and cost.

**Architecture:** Emit a structured `EventTurnMetrics` event after each LLM response with token breakdown and context utilization. Wrap tool execution with wall-clock timing. Add a fallback cost model in `llm/pricing.go` for when providers don't report cost. Extend `SessionResult` with aggregated timing and compaction stats. Wire the codergen handler to capture `SessionResult` instead of discarding it.

**Tech Stack:** Go, existing `agent/` and `llm/` packages

---

## File Structure

| Action | File | Responsibility |
|--------|------|----------------|
| Create | `llm/pricing.go` | Model pricing table and fallback cost calculation |
| Create | `llm/pricing_test.go` | Tests for pricing lookup and cost estimation |
| Modify | `agent/events.go` | Add `EventTurnMetrics` type, `TurnMetrics` struct, `ToolDuration` field |
| Modify | `agent/events_test.go` | Add `EventTurnMetrics` to event type coverage |
| Modify | `agent/result.go` | Add `ToolTimings`, `CompactionsApplied`, `LongestTurn` fields |
| Modify | `agent/result_test.go` | Test new fields in `String()` output |
| Modify | `agent/session.go` | Emit turn metrics, track tool timing, populate new result fields |
| Create | `agent/session_observability_test.go` | Integration tests for metrics emission, tool timing, cost |
| Modify | `pipeline/handlers/codergen.go` | Capture `SessionResult` and log key metrics |

---

## Chunk 1: Cost Model

### Task 1: Model Pricing Table

**Files:**
- Create: `llm/pricing.go`
- Create: `llm/pricing_test.go`

- [ ] **Step 1: Write the failing test for known model lookup**

In `llm/pricing_test.go`:

```go
// ABOUTME: Tests for model pricing lookup and fallback cost estimation.
// ABOUTME: Validates known models return correct prices and unknown models return zero.
package llm

import (
	"math"
	"testing"
)

func TestEstimateCost_KnownModel(t *testing.T) {
	cost := EstimateCost("claude-sonnet-4-5", Usage{
		InputTokens:  1_000_000,
		OutputTokens: 1_000_000,
	})
	// claude-sonnet-4-5: $3/MTok input + $15/MTok output = $18
	if math.Abs(cost-18.0) > 0.01 {
		t.Errorf("expected cost ~$18.00, got $%.2f", cost)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/harper/Public/src/2389/tracker && go test ./llm/ -run TestEstimateCost_KnownModel -v`
Expected: FAIL — `EstimateCost` undefined

- [ ] **Step 3: Write the pricing implementation**

In `llm/pricing.go`:

```go
// ABOUTME: Fallback cost estimation using a static model pricing table.
// ABOUTME: Used when LLM providers don't populate Usage.EstimatedCost.
package llm

// ModelPricing holds per-million-token prices for a model.
type ModelPricing struct {
	InputPerMTok  float64
	OutputPerMTok float64
}

// pricing maps model identifiers to their per-million-token costs.
var pricing = map[string]ModelPricing{
	"claude-sonnet-4-5":  {InputPerMTok: 3.00, OutputPerMTok: 15.00},
	"claude-sonnet-4-6":  {InputPerMTok: 3.00, OutputPerMTok: 15.00},
	"claude-opus-4-6":    {InputPerMTok: 15.00, OutputPerMTok: 75.00},
	"claude-haiku-4-5":   {InputPerMTok: 0.80, OutputPerMTok: 4.00},
	"gpt-4o":             {InputPerMTok: 2.50, OutputPerMTok: 10.00},
	"gpt-4o-mini":        {InputPerMTok: 0.15, OutputPerMTok: 0.60},
}

// EstimateCost calculates the cost of a request based on token counts and
// the model's pricing. Returns 0 for unknown models (no crash).
func EstimateCost(model string, usage Usage) float64 {
	p, ok := pricing[model]
	if !ok {
		return 0
	}
	inputCost := float64(usage.InputTokens) / 1_000_000 * p.InputPerMTok
	outputCost := float64(usage.OutputTokens) / 1_000_000 * p.OutputPerMTok
	return inputCost + outputCost
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /Users/harper/Public/src/2389/tracker && go test ./llm/ -run TestEstimateCost_KnownModel -v`
Expected: PASS

- [ ] **Step 5: Write additional tests**

Add to `llm/pricing_test.go`:

```go
func TestEstimateCost_UnknownModel(t *testing.T) {
	cost := EstimateCost("unknown-model-xyz", Usage{
		InputTokens:  1_000_000,
		OutputTokens: 1_000_000,
	})
	if cost != 0 {
		t.Errorf("expected 0 for unknown model, got $%.2f", cost)
	}
}

func TestEstimateCost_ZeroTokens(t *testing.T) {
	cost := EstimateCost("claude-sonnet-4-5", Usage{})
	if cost != 0 {
		t.Errorf("expected 0 for zero tokens, got $%.2f", cost)
	}
}

func TestEstimateCost_OpusModel(t *testing.T) {
	cost := EstimateCost("claude-opus-4-6", Usage{
		InputTokens:  100_000,
		OutputTokens: 10_000,
	})
	// 0.1M * $15 + 0.01M * $75 = $1.50 + $0.75 = $2.25
	if math.Abs(cost-2.25) > 0.01 {
		t.Errorf("expected cost ~$2.25, got $%.2f", cost)
	}
}

func TestEstimateCost_CacheTokensIgnored(t *testing.T) {
	// Cache tokens are not charged separately in our model — only input/output.
	cacheRead := 500
	cost := EstimateCost("claude-sonnet-4-5", Usage{
		InputTokens:    1_000_000,
		OutputTokens:   0,
		CacheReadTokens: &cacheRead,
	})
	// $3/MTok input = $3.00
	if math.Abs(cost-3.0) > 0.01 {
		t.Errorf("expected cost ~$3.00, got $%.2f", cost)
	}
}
```

- [ ] **Step 6: Run all pricing tests**

Run: `cd /Users/harper/Public/src/2389/tracker && go test ./llm/ -run TestEstimateCost -v`
Expected: All PASS

- [ ] **Step 7: Commit**

```bash
git add llm/pricing.go llm/pricing_test.go
git commit -m "feat(llm): add model pricing table and fallback cost estimation"
```

---

## Chunk 2: Turn Metrics Event and Tool Timing

### Task 2: TurnMetrics Struct and Event Type

**Files:**
- Modify: `agent/events.go:14-54`
- Modify: `agent/events_test.go:10-29`

- [ ] **Step 1: Write the failing test**

Add `EventTurnMetrics` to the event types list in `agent/events_test.go:10-29` (add it after `EventContextCompaction`):

```go
EventTurnMetrics,
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/harper/Public/src/2389/tracker && go test ./agent/ -run TestEventTypes -v`
Expected: FAIL — `EventTurnMetrics` undefined

- [ ] **Step 3: Add EventTurnMetrics and TurnMetrics struct**

In `agent/events.go`, add after `EventContextCompaction` (line 32):

```go
EventTurnMetrics      EventType = "turn_metrics"
```

Add `TurnMetrics` struct and `ToolDuration` field. After the `Event` struct (around line 54), add:

```go
// TurnMetrics captures per-turn token and performance data.
type TurnMetrics struct {
	InputTokens        int
	OutputTokens       int
	CacheReadTokens    int
	CacheWriteTokens   int
	ContextUtilization float64
	ToolCacheHits      int
	ToolCacheMisses    int
	TurnDuration       time.Duration
	EstimatedCost      float64
}
```

Add these fields to the `Event` struct:

```go
Metrics      *TurnMetrics
ToolDuration time.Duration
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /Users/harper/Public/src/2389/tracker && go test ./agent/ -run TestEventTypes -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add agent/events.go agent/events_test.go
git commit -m "feat(agent): add EventTurnMetrics type and TurnMetrics struct"
```

### Task 3: Emit Turn Metrics and Tool Timing in Session Loop

**Files:**
- Modify: `agent/session.go:128-373`
- Create: `agent/session_observability_test.go`

- [ ] **Step 1: Write the failing integration test for turn metrics**

Create `agent/session_observability_test.go`:

```go
// ABOUTME: Integration tests for session observability: turn metrics, tool timing, and cost.
// ABOUTME: Validates that EventTurnMetrics is emitted per turn with correct token data.
package agent

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/2389-research/tracker/llm"
)

func TestSession_EmitsTurnMetrics(t *testing.T) {
	client := &mockCompleter{
		responses: []*llm.Response{
			{
				Message:      llm.AssistantMessage("Hello!"),
				FinishReason: llm.FinishReason{Reason: "stop"},
				Usage:        llm.Usage{InputTokens: 100, OutputTokens: 20, TotalTokens: 120},
			},
		},
	}

	var events []Event
	handler := EventHandlerFunc(func(evt Event) {
		events = append(events, evt)
	})

	cfg := DefaultConfig()
	sess := mustNewSession(t, client, cfg, WithEventHandler(handler))
	_, err := sess.Run(context.Background(), "Hi")
	if err != nil {
		t.Fatal(err)
	}

	var metricsEvents []Event
	for _, e := range events {
		if e.Type == EventTurnMetrics {
			metricsEvents = append(metricsEvents, e)
		}
	}
	if len(metricsEvents) != 1 {
		t.Fatalf("expected 1 turn_metrics event, got %d", len(metricsEvents))
	}
	m := metricsEvents[0].Metrics
	if m == nil {
		t.Fatal("expected non-nil Metrics")
	}
	if m.InputTokens != 100 {
		t.Errorf("expected InputTokens=100, got %d", m.InputTokens)
	}
	if m.OutputTokens != 20 {
		t.Errorf("expected OutputTokens=20, got %d", m.OutputTokens)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/harper/Public/src/2389/tracker && go test ./agent/ -run TestSession_EmitsTurnMetrics -v`
Expected: FAIL — no turn_metrics events emitted

- [ ] **Step 3: Add turn metrics emission to session loop**

In `agent/session.go`, inside the `Run` method, after recording `result.Usage` and `result.Turns` (line 204-205), add a `turnStart` timer. Before the for-loop body (after line 162), add:

```go
turnStart := time.Now()
```

Then after the compaction block and before appending the response message (line 235), emit the turn metrics event:

```go
turnDuration := time.Since(turnStart)
if turnDuration > result.LongestTurn {
    result.LongestTurn = turnDuration
}

cacheHits, cacheMisses := 0, 0
if s.cache != nil {
    cacheHits = s.cache.hits
    cacheMisses = s.cache.misses
}

cacheRead, cacheWrite := 0, 0
if resp.Usage.CacheReadTokens != nil {
    cacheRead = *resp.Usage.CacheReadTokens
}
if resp.Usage.CacheWriteTokens != nil {
    cacheWrite = *resp.Usage.CacheWriteTokens
}

estimatedCost := resp.Usage.EstimatedCost
if estimatedCost == 0 {
    estimatedCost = llm.EstimateCost(s.config.Model, resp.Usage)
}

s.emit(Event{
    Type:      EventTurnMetrics,
    SessionID: s.id,
    Turn:      turn,
    Metrics: &TurnMetrics{
        InputTokens:        resp.Usage.InputTokens,
        OutputTokens:       resp.Usage.OutputTokens,
        CacheReadTokens:    cacheRead,
        CacheWriteTokens:   cacheWrite,
        ContextUtilization: tracker.Utilization(),
        ToolCacheHits:      cacheHits,
        ToolCacheMisses:    cacheMisses,
        TurnDuration:       turnDuration,
        EstimatedCost:      estimatedCost,
    },
})
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /Users/harper/Public/src/2389/tracker && go test ./agent/ -run TestSession_EmitsTurnMetrics -v`
Expected: PASS

- [ ] **Step 5: Write test for tool timing on EventToolCallEnd**

Add to `agent/session_observability_test.go`:

```go
func TestSession_ToolCallEndHasDuration(t *testing.T) {
	client := &mockCompleter{
		responses: []*llm.Response{
			{
				Message: llm.Message{
					Role: llm.RoleAssistant,
					Content: []llm.ContentPart{{
						Kind: llm.KindToolCall,
						ToolCall: &llm.ToolCallData{
							ID:        "call_1",
							Name:      "read",
							Arguments: json.RawMessage(`{"path":"test.go"}`),
						},
					}},
				},
				FinishReason: llm.FinishReason{Reason: "tool_calls"},
				Usage:        llm.Usage{InputTokens: 50, OutputTokens: 10},
			},
			{
				Message:      llm.AssistantMessage("done"),
				FinishReason: llm.FinishReason{Reason: "stop"},
				Usage:        llm.Usage{InputTokens: 80, OutputTokens: 5},
			},
		},
	}

	var events []Event
	handler := EventHandlerFunc(func(evt Event) {
		events = append(events, evt)
	})

	cfg := DefaultConfig()
	readTool := &stubTool{name: "read", output: "contents"}
	sess := mustNewSession(t, client, cfg, WithEventHandler(handler), WithTools(readTool))
	_, err := sess.Run(context.Background(), "Read")
	if err != nil {
		t.Fatal(err)
	}

	for _, e := range events {
		if e.Type == EventToolCallEnd {
			if e.ToolDuration <= 0 {
				t.Error("expected ToolDuration > 0 on EventToolCallEnd")
			}
			return
		}
	}
	t.Error("expected EventToolCallEnd event")
}
```

- [ ] **Step 6: Add tool timing to session loop**

In `agent/session.go`, in the tool execution block (around line 328-329), wrap the tool execution with timing:

```go
toolStart := time.Now()
toolResult = s.registry.Execute(ctx, call)
toolDuration := time.Since(toolStart)
```

Track cumulative tool timing per tool name. Add to the Session struct:

```go
toolTimings map[string]time.Duration
```

Initialize it in `NewSession` (after creating the Session struct):

```go
s.toolTimings = make(map[string]time.Duration)
```

After computing `toolDuration`, accumulate:

```go
s.toolTimings[call.Name] += toolDuration
```

In the `EventToolCallEnd` emission (around line 344-350), add `ToolDuration`:

```go
s.emit(Event{
    Type:         EventToolCallEnd,
    SessionID:    s.id,
    ToolName:     call.Name,
    ToolOutput:   toolResult.Content,
    ToolError:    boolToErrStr(toolResult.IsError),
    ToolDuration: toolDuration,
})
```

For cache hits, record zero duration and still emit `ToolDuration: 0`.

- [ ] **Step 7: Run test to verify it passes**

Run: `cd /Users/harper/Public/src/2389/tracker && go test ./agent/ -run TestSession_ToolCallEndHasDuration -v`
Expected: PASS

- [ ] **Step 8: Commit**

```bash
git add agent/session.go agent/session_observability_test.go
git commit -m "feat(agent): emit turn metrics and tool timing in session loop"
```

---

## Chunk 3: SessionResult Extensions and Codergen Integration

### Task 4: Extend SessionResult

**Files:**
- Modify: `agent/result.go:15-81`
- Modify: `agent/result_test.go`

- [ ] **Step 1: Write the failing test for new result fields**

Add to `agent/result_test.go`:

```go
func TestResultStringWithTimings(t *testing.T) {
	r := SessionResult{
		SessionID: "b4e1",
		Duration:  1*time.Minute + 12*time.Second,
		Turns:     8,
		ToolCalls: map[string]int{"read": 5, "bash": 3},
		Usage: llm.Usage{
			InputTokens:  50000,
			OutputTokens: 10000,
			TotalTokens:  60000,
			EstimatedCost: 1.25,
		},
		ToolTimings: map[string]time.Duration{
			"read": 2 * time.Second,
			"bash": 5 * time.Second,
		},
		CompactionsApplied: 2,
		LongestTurn:        15 * time.Second,
	}

	s := r.String()

	if !strings.Contains(s, "Compactions: 2") {
		t.Errorf("expected compactions in output: %s", s)
	}
	if !strings.Contains(s, "Longest turn: 15s") {
		t.Errorf("expected longest turn in output: %s", s)
	}
	if !strings.Contains(s, "$1.25") {
		t.Errorf("expected cost in output: %s", s)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/harper/Public/src/2389/tracker && go test ./agent/ -run TestResultStringWithTimings -v`
Expected: FAIL — `ToolTimings` and other fields undefined

- [ ] **Step 3: Add new fields to SessionResult and update String()**

In `agent/result.go`, add these fields to `SessionResult` after `ToolCacheMisses`:

```go
ToolTimings        map[string]time.Duration
CompactionsApplied int
LongestTurn        time.Duration
```

Update `String()` to include the new fields. After the tokens line, add:

```go
if r.CompactionsApplied > 0 || r.LongestTurn > 0 {
	var extras []string
	if r.CompactionsApplied > 0 {
		extras = append(extras, fmt.Sprintf("Compactions: %d", r.CompactionsApplied))
	}
	if r.LongestTurn > 0 {
		extras = append(extras, fmt.Sprintf("Longest turn: %s", r.LongestTurn.Round(time.Second)))
	}
	fmt.Fprintf(&b, "%s\n", strings.Join(extras, " | "))
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /Users/harper/Public/src/2389/tracker && go test ./agent/ -run TestResultStringWithTimings -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add agent/result.go agent/result_test.go
git commit -m "feat(agent): extend SessionResult with tool timings and compaction stats"
```

### Task 5: Populate New Result Fields in Session Loop

**Files:**
- Modify: `agent/session.go`
- Modify: `agent/session_observability_test.go`

- [ ] **Step 1: Write the failing test**

Add to `agent/session_observability_test.go`:

```go
func TestSession_ResultHasToolTimings(t *testing.T) {
	client := &mockCompleter{
		responses: []*llm.Response{
			{
				Message: llm.Message{
					Role: llm.RoleAssistant,
					Content: []llm.ContentPart{{
						Kind: llm.KindToolCall,
						ToolCall: &llm.ToolCallData{
							ID:        "call_1",
							Name:      "read",
							Arguments: json.RawMessage(`{"path":"a.go"}`),
						},
					}},
				},
				FinishReason: llm.FinishReason{Reason: "tool_calls"},
				Usage:        llm.Usage{InputTokens: 50, OutputTokens: 10},
			},
			{
				Message:      llm.AssistantMessage("done"),
				FinishReason: llm.FinishReason{Reason: "stop"},
				Usage:        llm.Usage{InputTokens: 80, OutputTokens: 5},
			},
		},
	}

	cfg := DefaultConfig()
	readTool := &stubTool{name: "read", output: "content"}
	sess := mustNewSession(t, client, cfg, WithTools(readTool))
	result, err := sess.Run(context.Background(), "Read file")
	if err != nil {
		t.Fatal(err)
	}

	if result.ToolTimings == nil {
		t.Fatal("expected ToolTimings to be non-nil")
	}
	if result.ToolTimings["read"] <= 0 {
		t.Error("expected read tool timing > 0")
	}
	if result.LongestTurn <= 0 {
		t.Error("expected LongestTurn > 0")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /Users/harper/Public/src/2389/tracker && go test ./agent/ -run TestSession_ResultHasToolTimings -v`
Expected: FAIL — `ToolTimings` not populated

- [ ] **Step 3: Populate result fields before returning**

In `agent/session.go`, in the `Run` method, before the final return (around line 370-372), add:

```go
result.ToolTimings = s.toolTimings
```

For `CompactionsApplied`, increment a counter in the compaction block (around line 224-232). Where `s.lastCompactTurn = turn` is set, also increment:

```go
result.CompactionsApplied++
```

The `LongestTurn` is already set per turn in the metrics emission block (from Task 3).

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /Users/harper/Public/src/2389/tracker && go test ./agent/ -run TestSession_ResultHasToolTimings -v`
Expected: PASS

- [ ] **Step 5: Write test for cost in SessionResult**

Add to `agent/session_observability_test.go`:

```go
func TestSession_ResultHasCostEstimate(t *testing.T) {
	client := &mockCompleter{
		responses: []*llm.Response{
			{
				Message:      llm.AssistantMessage("Hi"),
				FinishReason: llm.FinishReason{Reason: "stop"},
				Usage: llm.Usage{
					InputTokens:  100_000,
					OutputTokens: 10_000,
					TotalTokens:  110_000,
				},
			},
		},
	}

	cfg := DefaultConfig()
	cfg.Model = "claude-sonnet-4-5"
	sess := mustNewSession(t, client, cfg)
	result, err := sess.Run(context.Background(), "Hi")
	if err != nil {
		t.Fatal(err)
	}

	// 0.1M * $3 + 0.01M * $15 = $0.30 + $0.15 = $0.45
	if result.Usage.EstimatedCost < 0.40 || result.Usage.EstimatedCost > 0.50 {
		t.Errorf("expected cost ~$0.45, got $%.4f", result.Usage.EstimatedCost)
	}
}
```

- [ ] **Step 6: Add cost estimation to session result**

In `agent/session.go`, after `result.Usage = result.Usage.Add(resp.Usage)` (line 204), add fallback cost estimation:

```go
if resp.Usage.EstimatedCost == 0 {
    turnCost := llm.EstimateCost(s.config.Model, resp.Usage)
    result.Usage.EstimatedCost += turnCost
}
```

- [ ] **Step 7: Run test to verify it passes**

Run: `cd /Users/harper/Public/src/2389/tracker && go test ./agent/ -run TestSession_ResultHasCostEstimate -v`
Expected: PASS

- [ ] **Step 8: Run all agent tests**

Run: `cd /Users/harper/Public/src/2389/tracker && go test ./agent/ -v`
Expected: All PASS

- [ ] **Step 9: Commit**

```bash
git add agent/session.go agent/session_observability_test.go
git commit -m "feat(agent): populate tool timings and cost estimate in session result"
```

### Task 6: Codergen Handler Captures SessionResult

**Files:**
- Modify: `pipeline/handlers/codergen.go:101`

- [ ] **Step 1: Write a note about the change**

Currently `codergen.go:101` has:

```go
_, runErr := sess.Run(ctx, prompt)
```

The `SessionResult` is discarded. We need to capture it and include key metrics in the transcript and outcome context updates.

- [ ] **Step 2: Capture SessionResult and add metrics to outcome**

In `pipeline/handlers/codergen.go:101`, change to:

```go
sessResult, runErr := sess.Run(ctx, prompt)
```

After the successful outcome is constructed (around line 139-144), add session metrics to context updates:

```go
outcome := pipeline.Outcome{
    Status: status,
    ContextUpdates: map[string]string{
        pipeline.ContextKeyLastResponse: responseText,
    },
}
if sessResult.Usage.EstimatedCost > 0 {
    outcome.ContextUpdates["last_cost"] = fmt.Sprintf("%.4f", sessResult.Usage.EstimatedCost)
}
if sessResult.Turns > 0 {
    outcome.ContextUpdates["last_turns"] = strconv.Itoa(sessResult.Turns)
}
```

Also add metrics to the transcript collector output. Append session summary after the transcript:

```go
responseArtifact := collector.transcript()
if responseArtifact == "" {
    responseArtifact = responseText
}
responseArtifact += "\n\n" + sessResult.String()
```

For the error path (around line 112-126), also capture `sessResult`:

```go
sessResult, runErr := sess.Run(ctx, prompt)
```

And add the summary to the error-path artifact similarly.

- [ ] **Step 3: Run pipeline handler tests**

Run: `cd /Users/harper/Public/src/2389/tracker && go test ./pipeline/handlers/ -v`
Expected: All PASS

- [ ] **Step 4: Run all tests**

Run: `cd /Users/harper/Public/src/2389/tracker && go test ./... -count=1`
Expected: All PASS

- [ ] **Step 5: Commit**

```bash
git add pipeline/handlers/codergen.go
git commit -m "feat(pipeline): capture session result metrics in codergen handler"
```

---

## Final Verification

- [ ] **Run full test suite**

```bash
cd /Users/harper/Public/src/2389/tracker && go test ./... -count=1
```

- [ ] **Verify no regressions in existing tests**

All 15 packages should pass.
