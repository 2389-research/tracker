# Review Squad Fixes Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix all critical and important findings from the 11-expert review panel on issues #42/#43.

**Architecture:** 12 tasks organized by dependency. Tasks 1-3 fix critical data accuracy bugs (parallel stats, double-counting, trivial prompts). Tasks 4-8 fix important structural issues (failResult, JSON tags, cache tokens, EndTime, summary display). Tasks 9-10 fill test gaps. Tasks 11-12 update docs.

**Tech Stack:** Go, pipeline engine, bubbletea TUI, dippin-lang .dip files.

---

## File Structure

| File | Responsibility | Tasks |
|------|---------------|-------|
| `pipeline/handlers/parallel.go` | Parallel fan-out handler | 1 |
| `pipeline/handlers/parallel_test.go` | Parallel handler tests | 1 |
| `pipeline/handlers/codergen.go` | LLM agent handler | 2 |
| `pipeline/handlers/codergen_test.go` | Codergen tests | 2 |
| `examples/ask_and_execute.dip` | Built-in workflow | 3 |
| `examples/build_product.dip` | Built-in workflow | 3 |
| `examples/build_product_with_superspec.dip` | Built-in workflow | 3 |
| `examples/reasoning_effort_demo.dip` | Demo workflow | 3 |
| `pipeline/engine.go` | Engine + EngineResult | 4 |
| `pipeline/engine_run.go` | Engine execution paths | 4, 7 |
| `pipeline/trace.go` | SessionStats, Trace, UsageSummary | 5, 6 |
| `pipeline/trace_test.go` | Trace tests | 5, 6, 8, 10 |
| `pipeline/handlers/transcript.go` | buildSessionStats | 6 |
| `pipeline/handlers/transcript_test.go` | Transcript tests | 6, 8 |
| `cmd/tracker/summary.go` | CLI run summary | 7 |
| `cmd/tracker/main_test.go` | CLI tests | 7, 8 |
| `pipeline/dippin_adapter_test.go` | Adapter tests | 9 |
| `CHANGELOG.md` | Release notes | 11 |
| `CLAUDE.md` | Project instructions | 12 |

---

### Task 1: Collect parallel branch SessionStats into trace

The parallel handler discards `outcome.Stats` from branches. This means `AggregateUsage()` misses all parallel node costs.

**Files:**
- Modify: `pipeline/handlers/parallel.go:18-23,82-90,157-200`
- Modify: `pipeline/handlers/parallel_test.go` (add test)

- [ ] **Step 1: Write failing test — parallel branches produce aggregated stats**

In `pipeline/handlers/parallel_test.go`, add a test that verifies branch stats are collected. Find an existing test file or create one. First check what exists:

Run: `ls pipeline/handlers/parallel_test.go 2>/dev/null || echo "no test file"`

If no test file exists, create `pipeline/handlers/parallel_test.go`. Add:

```go
func TestParallelHandler_AggregatesBranchStats(t *testing.T) {
	// Build a graph with a parallel node dispatching to two branches.
	g := &pipeline.Graph{
		Nodes:     make(map[string]*pipeline.Node),
		StartNode: "dispatch",
		ExitNode:  "join",
	}
	g.Nodes["dispatch"] = &pipeline.Node{
		ID: "dispatch", Shape: "component", Handler: "parallel",
		Attrs: map[string]string{"parallel_targets": "branch_a,branch_b", "parallel_join": "join"},
	}
	g.Nodes["branch_a"] = &pipeline.Node{
		ID: "branch_a", Shape: "box", Handler: "codergen",
		Attrs: map[string]string{"prompt": "Do A"},
	}
	g.Nodes["branch_b"] = &pipeline.Node{
		ID: "branch_b", Shape: "box", Handler: "codergen",
		Attrs: map[string]string{"prompt": "Do B"},
	}
	g.Nodes["join"] = &pipeline.Node{ID: "join", Shape: "tripleoctagon", Handler: "parallel.fan_in"}
	g.Edges = []*pipeline.Edge{
		{From: "dispatch", To: "branch_a"},
		{From: "dispatch", To: "branch_b"},
	}

	// Register a stub handler that returns stats.
	registry := pipeline.NewHandlerRegistry()
	registry.Register(&stubHandlerWithStats{
		name: "codergen",
		stats: func(nodeID string) *pipeline.SessionStats {
			switch nodeID {
			case "branch_a":
				return &pipeline.SessionStats{Turns: 3, InputTokens: 1000, OutputTokens: 500, TotalTokens: 1500, CostUSD: 0.005}
			case "branch_b":
				return &pipeline.SessionStats{Turns: 5, InputTokens: 2000, OutputTokens: 1000, TotalTokens: 3000, CostUSD: 0.010}
			default:
				return nil
			}
		},
	})

	handler := NewParallelHandler(g, registry, pipeline.PipelineNoopHandler)
	pctx := pipeline.NewPipelineContext()

	outcome, err := handler.Execute(context.Background(), g.Nodes["dispatch"], pctx)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if outcome.Status != pipeline.OutcomeSuccess {
		t.Fatalf("status = %q, want success", outcome.Status)
	}

	// The parallel handler should aggregate branch stats.
	if outcome.Stats == nil {
		t.Fatal("outcome.Stats is nil — parallel branches stats were lost")
	}
	if outcome.Stats.InputTokens != 3000 {
		t.Errorf("InputTokens = %d, want 3000", outcome.Stats.InputTokens)
	}
	if outcome.Stats.OutputTokens != 1500 {
		t.Errorf("OutputTokens = %d, want 1500", outcome.Stats.OutputTokens)
	}
	if outcome.Stats.TotalTokens != 4500 {
		t.Errorf("TotalTokens = %d, want 4500", outcome.Stats.TotalTokens)
	}
	if outcome.Stats.Turns != 8 {
		t.Errorf("Turns = %d, want 8", outcome.Stats.Turns)
	}
}
```

Also add the stub helper (if not already in the test file):

```go
type stubHandlerWithStats struct {
	name  string
	stats func(nodeID string) *pipeline.SessionStats
}

func (s *stubHandlerWithStats) Name() string { return s.name }
func (s *stubHandlerWithStats) Execute(_ context.Context, node *pipeline.Node, _ *pipeline.PipelineContext) (pipeline.Outcome, error) {
	return pipeline.Outcome{
		Status: pipeline.OutcomeSuccess,
		Stats:  s.stats(node.ID),
	}, nil
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pipeline/handlers/ -run TestParallelHandler_AggregatesBranchStats -v`
Expected: FAIL — `outcome.Stats is nil`

- [ ] **Step 3: Add Stats field to ParallelResult**

In `pipeline/handlers/parallel.go:18-23`, add `Stats` to `ParallelResult`:

```go
type ParallelResult struct {
	NodeID         string            `json:"node_id"`
	Status         string            `json:"status"`
	ContextUpdates map[string]string `json:"context_updates,omitempty"`
	Error          string            `json:"error,omitempty"`
	Stats          *pipeline.SessionStats `json:"stats,omitempty"`
}
```

- [ ] **Step 4: Capture Stats in runBranch**

In `pipeline/handlers/parallel.go:184`, add `Stats` to the `ParallelResult`:

```go
pr := ParallelResult{NodeID: tn.ID, Status: outcome.Status, ContextUpdates: outcome.ContextUpdates, Stats: outcome.Stats}
```

- [ ] **Step 5: Aggregate branch stats into the parallel handler's Outcome**

In `pipeline/handlers/parallel.go`, add a helper function after `aggregateStatus`:

```go
// aggregateBranchStats combines SessionStats from all branches into a single summary.
func aggregateBranchStats(results []ParallelResult) *pipeline.SessionStats {
	agg := &pipeline.SessionStats{
		ToolCalls: make(map[string]int),
	}
	count := 0
	for _, r := range results {
		if r.Stats == nil {
			continue
		}
		count++
		s := r.Stats
		agg.Turns += s.Turns
		agg.TotalToolCalls += s.TotalToolCalls
		agg.InputTokens += s.InputTokens
		agg.OutputTokens += s.OutputTokens
		agg.TotalTokens += s.TotalTokens
		agg.CostUSD += s.CostUSD
		agg.Compactions += s.Compactions
		agg.CacheHits += s.CacheHits
		agg.CacheMisses += s.CacheMisses
		for name, c := range s.ToolCalls {
			agg.ToolCalls[name] += c
		}
		if s.LongestTurn > agg.LongestTurn {
			agg.LongestTurn = s.LongestTurn
		}
		agg.FilesModified = append(agg.FilesModified, s.FilesModified...)
		agg.FilesCreated = append(agg.FilesCreated, s.FilesCreated...)
	}
	if count == 0 {
		return nil
	}
	return agg
}
```

Then at line 86, set `Stats` on the outcome:

```go
outcome := pipeline.Outcome{Status: status, Stats: aggregateBranchStats(collected)}
```

- [ ] **Step 6: Run test to verify it passes**

Run: `go test ./pipeline/handlers/ -run TestParallelHandler_AggregatesBranchStats -v`
Expected: PASS

- [ ] **Step 7: Run full test suite**

Run: `go test ./... -short`
Expected: All 14 packages pass.

- [ ] **Step 8: Commit**

```bash
git add pipeline/handlers/parallel.go pipeline/handlers/parallel_test.go
git commit -m "fix: aggregate branch SessionStats in parallel handler

Parallel branches now collect Stats from each branch outcome and merge
them into the parallel node's own Outcome.Stats. This ensures
AggregateUsage() includes token/cost data from parallel execution."
```

---

### Task 2: Fix native backend token double-counting

`codergen.go:95-97` calls `tokenTracker.AddUsage("claude-code", ...)` for ALL backends. Native sessions get counted once via middleware AND again here.

**Files:**
- Modify: `pipeline/handlers/codergen.go:90-97`

- [ ] **Step 1: Write failing test — native backend should not double-count**

This is best tested by checking that `AddUsage` is only called for `ClaudeCodeBackend`. Add a test in `pipeline/handlers/codergen_test.go` (or the existing test file). The simplest fix is to check the backend type before calling AddUsage. First, verify the fix is correct by reading the comment at line 92-94 — it already says "e.g., claude-code subprocess. Native backend usage flows through the TokenTracker middleware automatically." The fix is to actually gate on the backend type.

- [ ] **Step 2: Fix the double-counting**

In `pipeline/handlers/codergen.go`, the `Execute` method needs to know which backend was selected. The `backend` variable is already in scope from line 72. Change lines 92-97 to:

```go
	// Report token usage from backends that bypass the LLM client middleware
	// (e.g., claude-code subprocess). Native backend usage flows through the
	// TokenTracker middleware automatically — skip to avoid double-counting.
	if _, isClaudeCode := backend.(*ClaudeCodeBackend); isClaudeCode {
		if h.tokenTracker != nil && sessResult.Usage.TotalTokens > 0 {
			h.tokenTracker.AddUsage("claude-code", sessResult.Usage)
		}
	}
```

- [ ] **Step 3: Run full test suite**

Run: `go test ./... -short`
Expected: All packages pass.

- [ ] **Step 4: Commit**

```bash
git add pipeline/handlers/codergen.go
git commit -m "fix: only report claude-code usage to TokenTracker, not native

Native backend tokens already flow through the TokenTracker middleware.
The explicit AddUsage call is only needed for claude-code subprocess
which bypasses the middleware."
```

---

### Task 3: Remove trivial prompts from built-in Start/Done nodes

After #42, agent Start/Done nodes with prompts run real LLM calls. The 3 core built-in pipelines have placeholder prompts that waste tokens and add failure risk.

**Files:**
- Modify: `examples/ask_and_execute.dip`
- Modify: `examples/build_product.dip`
- Modify: `examples/build_product_with_superspec.dip`
- Modify: `examples/reasoning_effort_demo.dip`

- [ ] **Step 1: Remove prompt from Start/Done in ask_and_execute.dip**

Replace the agent Start/Done declarations. Remove the `prompt:` lines so they become prompt-less agents (passthrough):

In `examples/ask_and_execute.dip`, change:
```
  agent Start
    label: Start
    prompt: Initialize pipeline.

  agent Done
    label: Done
    prompt: Pipeline complete.
```
To:
```
  agent Start
    label: Start

  agent Done
    label: Done
```

- [ ] **Step 2: Same for build_product.dip**

Find and remove `prompt:` lines from Start and Done agent blocks in `examples/build_product.dip`. These will be similar trivial prompts.

- [ ] **Step 3: Same for build_product_with_superspec.dip**

Find and remove `prompt:` lines from Start and Done agent blocks in `examples/build_product_with_superspec.dip`.

- [ ] **Step 4: Same for reasoning_effort_demo.dip**

Find and remove `prompt:` lines from Start and Exit agent blocks in `examples/reasoning_effort_demo.dip`.

- [ ] **Step 5: Run full test suite**

Run: `go test ./... -short`
Expected: All packages pass. (The .dip files are not compiled; they are runtime inputs.)

- [ ] **Step 6: Commit**

```bash
git add examples/ask_and_execute.dip examples/build_product.dip examples/build_product_with_superspec.dip examples/reasoning_effort_demo.dip
git commit -m "fix: remove trivial prompts from built-in Start/Done nodes

After the #42 fix, agent nodes with prompts keep their codergen handler.
These placeholder prompts ('Initialize pipeline.', 'Pipeline complete.')
were causing unnecessary LLM calls and adding failure risk."
```

---

### Task 4: Consolidate failResult to include Trace and Usage

`failResult()` omits Trace/Usage. 3 callers patch after. Future callers will forget.

**Files:**
- Modify: `pipeline/engine.go:365-379`
- Modify: `pipeline/engine_run.go:329-332,387-390,395-398`

- [ ] **Step 1: Update failResult signature to accept trace**

In `pipeline/engine.go:365-379`, change `failResult` to accept a `*Trace` and compute Usage:

```go
// failResult builds an EngineResult with fail status, including trace and usage.
func (e *Engine) failResult(runID string, cp *Checkpoint, pctx *PipelineContext, trace *Trace) *EngineResult {
	e.emit(PipelineEvent{
		Type:      EventPipelineFailed,
		Timestamp: time.Now(),
		RunID:     runID,
		Message:   "pipeline failed",
	})
	return &EngineResult{
		RunID:          runID,
		Status:         OutcomeFail,
		CompletedNodes: cp.CompletedNodes,
		Context:        pctx.Snapshot(),
		Trace:          trace,
		Usage:          trace.AggregateUsage(),
	}
}
```

- [ ] **Step 2: Update all 3 call sites in engine_run.go**

At `engine_run.go:329-332` (retries exhausted), replace:
```go
result := e.failResult(s.runID, s.cp, s.pctx)
result.Trace = s.trace
result.Usage = s.trace.AggregateUsage()
```
With:
```go
result := e.failResult(s.runID, s.cp, s.pctx, s.trace)
```

At `engine_run.go:387-390` (goal gate unsatisfied), same replacement.

At `engine_run.go:395-398` (exit node fail), same replacement.

- [ ] **Step 3: Run full test suite**

Run: `go test ./... -short`
Expected: All packages pass.

- [ ] **Step 4: Commit**

```bash
git add pipeline/engine.go pipeline/engine_run.go
git commit -m "refactor: failResult accepts trace, sets Usage atomically

Eliminates the caller-must-patch pattern where every failResult call
site had to manually set result.Trace and result.Usage."
```

---

### Task 5: Add consistent JSON tags to SessionStats and TraceEntry

4 new fields have `json:` tags, 9 existing fields don't. Mixed naming in serialized output.

**Files:**
- Modify: `pipeline/trace.go:11-47`

- [ ] **Step 1: Add JSON tags to all SessionStats fields**

In `pipeline/trace.go:13-27`:

```go
type SessionStats struct {
	Turns          int            `json:"turns"`
	ToolCalls      map[string]int `json:"tool_calls,omitempty"`
	TotalToolCalls int            `json:"total_tool_calls"`
	FilesModified  []string       `json:"files_modified,omitempty"`
	FilesCreated   []string       `json:"files_created,omitempty"`
	Compactions    int            `json:"compactions"`
	LongestTurn    time.Duration  `json:"longest_turn"`
	CacheHits      int            `json:"cache_hits"`
	CacheMisses    int            `json:"cache_misses"`
	InputTokens    int            `json:"input_tokens"`
	OutputTokens   int            `json:"output_tokens"`
	TotalTokens    int            `json:"total_tokens"`
	CostUSD        float64        `json:"cost_usd"`
}
```

- [ ] **Step 2: Add JSON tags to TraceEntry**

In `pipeline/trace.go:30-39`:

```go
type TraceEntry struct {
	Timestamp   time.Time      `json:"timestamp"`
	NodeID      string         `json:"node_id"`
	HandlerName string         `json:"handler_name"`
	Status      string         `json:"status"`
	Duration    time.Duration  `json:"duration"`
	EdgeTo      string         `json:"edge_to,omitempty"`
	Error       string         `json:"error,omitempty"`
	Stats       *SessionStats  `json:"stats,omitempty"`
}
```

- [ ] **Step 3: Add JSON tags to Trace**

In `pipeline/trace.go:42-47`:

```go
type Trace struct {
	RunID     string       `json:"run_id"`
	Entries   []TraceEntry `json:"entries"`
	StartTime time.Time    `json:"start_time"`
	EndTime   time.Time    `json:"end_time"`
}
```

- [ ] **Step 4: Run full test suite**

Run: `go test ./... -short`
Expected: All packages pass.

- [ ] **Step 5: Commit**

```bash
git add pipeline/trace.go
git commit -m "fix: add consistent JSON tags to SessionStats, TraceEntry, Trace

All fields now use snake_case json tags for consistent serialization."
```

---

### Task 6: Add cache/reasoning token fields to SessionStats

`CacheReadTokens`, `CacheWriteTokens`, `ReasoningTokens` are tracked in `llm.Usage` but dropped at the `buildSessionStats` boundary.

**Files:**
- Modify: `pipeline/trace.go` (SessionStats, UsageSummary, AggregateUsage)
- Modify: `pipeline/handlers/transcript.go` (buildSessionStats)
- Modify: `pipeline/handlers/transcript_test.go`
- Modify: `pipeline/trace_test.go`

- [ ] **Step 1: Write failing test**

In `pipeline/handlers/transcript_test.go`, update `TestBuildSessionStatsIncludesTokenUsage` to include cache/reasoning tokens:

Add to the `llm.Usage` in the test:
```go
ReasoningTokens:  intPtr(200),
CacheReadTokens:  intPtr(500),
CacheWriteTokens: intPtr(100),
```

Add assertions:
```go
if stats.ReasoningTokens != 200 {
	t.Errorf("expected ReasoningTokens=200, got %d", stats.ReasoningTokens)
}
if stats.CacheReadTokens != 500 {
	t.Errorf("expected CacheReadTokens=500, got %d", stats.CacheReadTokens)
}
if stats.CacheWriteTokens != 100 {
	t.Errorf("expected CacheWriteTokens=100, got %d", stats.CacheWriteTokens)
}
```

Add helper at bottom of test file:
```go
func intPtr(n int) *int { return &n }
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pipeline/handlers/ -run TestBuildSessionStatsIncludesTokenUsage -v`
Expected: FAIL — fields don't exist.

- [ ] **Step 3: Add fields to SessionStats**

In `pipeline/trace.go`, add after `CostUSD`:
```go
	ReasoningTokens  int `json:"reasoning_tokens"`
	CacheReadTokens  int `json:"cache_read_tokens"`
	CacheWriteTokens int `json:"cache_write_tokens"`
```

- [ ] **Step 4: Add fields to UsageSummary**

In `pipeline/trace.go`, add to `UsageSummary`:
```go
	TotalReasoningTokens  int `json:"total_reasoning_tokens"`
	TotalCacheReadTokens  int `json:"total_cache_read_tokens"`
	TotalCacheWriteTokens int `json:"total_cache_write_tokens"`
```

- [ ] **Step 5: Update AggregateUsage to sum new fields**

In `pipeline/trace.go`, inside the `AggregateUsage` loop, add:
```go
		s.TotalReasoningTokens += e.Stats.ReasoningTokens
		s.TotalCacheReadTokens += e.Stats.CacheReadTokens
		s.TotalCacheWriteTokens += e.Stats.CacheWriteTokens
```

- [ ] **Step 6: Update buildSessionStats to copy new fields**

In `pipeline/handlers/transcript.go`, add to the return struct in `buildSessionStats`:
```go
		ReasoningTokens:  derefInt(r.Usage.ReasoningTokens),
		CacheReadTokens:  derefInt(r.Usage.CacheReadTokens),
		CacheWriteTokens: derefInt(r.Usage.CacheWriteTokens),
```

Add helper function:
```go
// derefInt safely dereferences an *int, returning 0 for nil.
func derefInt(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}
```

- [ ] **Step 7: Run tests**

Run: `go test ./pipeline/handlers/ -run TestBuildSessionStats -v && go test ./pipeline/ -short`
Expected: All pass.

- [ ] **Step 8: Commit**

```bash
git add pipeline/trace.go pipeline/handlers/transcript.go pipeline/handlers/transcript_test.go
git commit -m "feat: expose reasoning and cache token counts in SessionStats

CacheReadTokens, CacheWriteTokens, and ReasoningTokens are now carried
from llm.Usage through SessionStats to UsageSummary."
```

---

### Task 7: Fix cancelledResult and retry-backoff EndTime

`cancelledResult()` and the retry-backoff cancel path don't set `trace.EndTime`, causing missing duration in the run summary.

**Files:**
- Modify: `pipeline/engine.go:341-358` (cancelledResult)
- Modify: `pipeline/engine_run.go:275-286` (retry backoff cancel)

- [ ] **Step 1: Set EndTime in cancelledResult**

In `pipeline/engine.go:341`, add `s.trace.EndTime = time.Now()` before building the result:

```go
func (e *Engine) cancelledResult(s *runState, err error) (*EngineResult, error) {
	e.saveCheckpoint(s.cp, s.pctx, s.runID)
	s.trace.EndTime = time.Now()
	e.emit(PipelineEvent{
```

- [ ] **Step 2: Set EndTime in retry backoff cancel path**

In `pipeline/engine_run.go`, find the `case <-ctx.Done():` inside the retry backoff (around line 277-286). Add `s.trace.EndTime = time.Now()` before building the EngineResult:

```go
		case <-ctx.Done():
			e.saveCheckpoint(s.cp, s.pctx, s.runID)
			s.trace.EndTime = time.Now()
			return "", false, &EngineResult{
```

- [ ] **Step 3: Run full test suite**

Run: `go test ./... -short`
Expected: All packages pass.

- [ ] **Step 4: Commit**

```bash
git add pipeline/engine.go pipeline/engine_run.go
git commit -m "fix: set trace EndTime on cancel and retry-backoff paths

Duration was missing from the run summary for cancelled runs because
cancelledResult and the retry backoff cancel path did not set EndTime."
```

---

### Task 8: Fix float64 exact equality in tests

Tests compare summed `float64` costs with `!=`. Use epsilon comparison instead.

**Files:**
- Modify: `pipeline/trace_test.go`
- Modify: `pipeline/handlers/transcript_test.go`
- Modify: `cmd/tracker/main_test.go`

- [ ] **Step 1: Add floatEqual helper to trace_test.go**

At the bottom of `pipeline/trace_test.go`:

```go
func floatNear(a, b, epsilon float64) bool {
	diff := a - b
	if diff < 0 {
		diff = -diff
	}
	return diff < epsilon
}
```

- [ ] **Step 2: Replace exact float comparisons in trace_test.go**

Find all `!= 0.` comparisons on CostUSD or TotalCostUSD in `pipeline/trace_test.go` and replace with:

```go
if !floatNear(stats.CostUSD, 0.042, 1e-9) {
	t.Errorf("CostUSD = %f, want ~0.042", stats.CostUSD)
}
```

Do this for every cost comparison in the file. The key ones are:
- `TestSessionStatsIncludesTokenUsage` — CostUSD
- `TestTraceAggregateUsage` normal case — TotalCostUSD

- [ ] **Step 3: Same for transcript_test.go**

Add `floatNear` helper and replace the CostUSD comparison in `TestBuildSessionStatsIncludesTokenUsage`.

- [ ] **Step 4: Same for main_test.go**

Add `floatNear` helper and replace the TotalCostUSD comparison in `TestAggregateSessionStatsMultipleNodes`.

- [ ] **Step 5: Run full test suite**

Run: `go test ./... -short`
Expected: All packages pass.

- [ ] **Step 6: Commit**

```bash
git add pipeline/trace_test.go pipeline/handlers/transcript_test.go cmd/tracker/main_test.go
git commit -m "fix: use epsilon comparison for float64 cost assertions

Exact equality on summed float64 values is fragile. Using floatNear
with 1e-9 epsilon instead."
```

---

### Task 9: Add missing tests for ensureStartExitNodes error path

No test covers the error path where start/exit node is missing from the graph.

**Files:**
- Modify: `pipeline/dippin_adapter_test.go`

- [ ] **Step 1: Add error path test**

In `pipeline/dippin_adapter_test.go`, after the existing `TestEnsureStartExitNodes_*` tests:

```go
func TestEnsureStartExitNodes_ErrorMissingNodes(t *testing.T) {
	g := &Graph{
		Nodes:     make(map[string]*Node),
		StartNode: "missing_start",
		ExitNode:  "missing_exit",
	}

	err := ensureStartExitNodes(g)
	if err == nil {
		t.Fatal("expected error for missing start node, got nil")
	}
	if !strings.Contains(err.Error(), "missing_start") {
		t.Errorf("error should mention missing_start, got: %v", err)
	}

	// Add start but not exit — should error on exit.
	g.Nodes["missing_start"] = &Node{ID: "missing_start", Attrs: make(map[string]string)}
	err = ensureStartExitNodes(g)
	if err == nil {
		t.Fatal("expected error for missing exit node, got nil")
	}
	if !strings.Contains(err.Error(), "missing_exit") {
		t.Errorf("error should mention missing_exit, got: %v", err)
	}
}
```

- [ ] **Step 2: Run test**

Run: `go test ./pipeline/ -run TestEnsureStartExitNodes_ErrorMissingNodes -v`
Expected: PASS (error paths already work, this just adds coverage).

- [ ] **Step 3: Commit**

```bash
git add pipeline/dippin_adapter_test.go
git commit -m "test: add error-path coverage for ensureStartExitNodes"
```

---

### Task 10: Add integration test FromDippinIR through EngineResult.Usage

No end-to-end test verifies that token data flows from handlers through the engine to `EngineResult.Usage`.

**Files:**
- Modify: `pipeline/trace_test.go`

- [ ] **Step 1: Add integration test**

In `pipeline/trace_test.go`, add a test that builds a graph, registers a handler returning stats, runs the engine, and checks `result.Usage`:

```go
func TestEngineResultUsageFromTraceStats(t *testing.T) {
	g := &Graph{
		Nodes:     make(map[string]*Node),
		StartNode: "s",
		ExitNode:  "e",
	}
	g.Nodes["s"] = &Node{ID: "s", Shape: "Mdiamond", Handler: "start", Attrs: make(map[string]string)}
	g.Nodes["work"] = &Node{ID: "work", Shape: "box", Handler: "stub", Attrs: make(map[string]string)}
	g.Nodes["e"] = &Node{ID: "e", Shape: "Msquare", Handler: "exit", Attrs: make(map[string]string)}
	g.Edges = []*Edge{
		{From: "s", To: "work"},
		{From: "work", To: "e"},
	}
	g.NodeOrder = []string{"s", "work", "e"}

	registry := NewHandlerRegistry()
	registry.Register(&startHandler{})
	registry.Register(&exitHandler{})
	registry.Register(&stubHandler{
		name: "stub",
		fn: func(_ context.Context, _ *Node, _ *PipelineContext) (Outcome, error) {
			return Outcome{
				Status: OutcomeSuccess,
				Stats: &SessionStats{
					Turns:        5,
					InputTokens:  2000,
					OutputTokens: 800,
					TotalTokens:  2800,
					CostUSD:      0.01,
				},
			}, nil
		},
	})

	engine := NewEngine(g, registry)
	result, err := engine.Run(context.Background())
	if err != nil {
		t.Fatalf("engine.Run failed: %v", err)
	}
	if result.Usage == nil {
		t.Fatal("result.Usage is nil — token data not aggregated")
	}
	if result.Usage.TotalInputTokens != 2000 {
		t.Errorf("TotalInputTokens = %d, want 2000", result.Usage.TotalInputTokens)
	}
	if result.Usage.SessionCount != 1 {
		t.Errorf("SessionCount = %d, want 1", result.Usage.SessionCount)
	}
}
```

Note: Check if `stubHandler` and `startHandler`/`exitHandler` already exist in the test file. If not, the stubHandler pattern should already be available from existing tests — reuse it. If `startHandler`/`exitHandler` are not available, import them from the handlers package or create minimal stubs.

- [ ] **Step 2: Run test**

Run: `go test ./pipeline/ -run TestEngineResultUsageFromTraceStats -v`
Expected: PASS

- [ ] **Step 3: Commit**

```bash
git add pipeline/trace_test.go
git commit -m "test: add integration test for EngineResult.Usage population"
```

---

### Task 11: Update CHANGELOG.md

Missing entries for 3 post-v0.13.0 commits.

**Files:**
- Modify: `CHANGELOG.md`

- [ ] **Step 1: Add Unreleased section at top of CHANGELOG.md**

After the header and before `## [0.13.0]`, add:

```markdown
## [Unreleased]

### Added

- **EngineResult.Usage**: Pipeline runs now expose aggregated token counts and cost via `EngineResult.Usage` (a `*UsageSummary`). Downstream consumers can read `TotalInputTokens`, `TotalOutputTokens`, `TotalTokens`, `TotalCostUSD`, and `SessionCount` directly from the result without parsing artifacts.
- **Per-node token tracking in SessionStats**: `InputTokens`, `OutputTokens`, `TotalTokens`, `CostUSD`, `ReasoningTokens`, `CacheReadTokens`, `CacheWriteTokens` fields on `SessionStats` in trace entries.
- **Parallel branch stats aggregation**: Parallel handler now collects and aggregates `SessionStats` from branch outcomes into its own trace entry.

### Fixed

- **Start/exit agent nodes preserved**: `ensureStartExitNodes` no longer overwrites the `codergen` handler on agent nodes designated as start or exit. Agent start/exit nodes now execute their LLM prompts instead of being silently replaced with no-op passthroughs. (Closes #42)
- **DecisionDetail token mapping**: `TokenInput`/`TokenOutput` in pipeline events now correctly map from `InputTokens`/`OutputTokens` instead of `CacheHits`/`CacheMisses`.
- **Native backend double-counting**: Token usage from the native backend is no longer reported twice to the `TokenTracker`.
- **Cancel/fail EndTime**: Cancelled and retry-exhausted runs now set `trace.EndTime` so the run summary shows duration.
- **failResult atomicity**: `failResult()` now accepts a `*Trace` parameter and sets both `Trace` and `Usage` internally, preventing silent data loss from future callers forgetting to patch.
```

- [ ] **Step 2: Commit**

```bash
git add CHANGELOG.md
git commit -m "docs: add changelog entries for #42/#43 fixes and review squad remediations"
```

---

### Task 12: Update CLAUDE.md with token tracking architecture

Missing documentation for UsageSummary, AggregateUsage, and ensureStartExitNodes conditional behavior.

**Files:**
- Modify: `CLAUDE.md`

- [ ] **Step 1: Add token tracking section to Architecture Gotchas**

In `CLAUDE.md`, after the "### Checkpoint resume is fragile" section, add:

```markdown
### Token usage flows through three layers
The `llm.Usage` struct tracks per-API-call tokens. `agent.SessionResult.Usage`
accumulates across turns within a session. `buildSessionStats()` in
`pipeline/handlers/transcript.go` copies usage into `pipeline.SessionStats`
on each trace entry. `Trace.AggregateUsage()` sums all trace entries into
`UsageSummary`, which lands on `EngineResult.Usage`.

For parallel execution, the parallel handler aggregates branch `SessionStats`
into its own outcome so the trace entry for the parallel node carries the
combined usage of all branches.

The CLI summary in `cmd/tracker/summary.go` uses `EngineResult.Usage` for
token/cost totals and `llm.TokenTracker` for per-provider breakdowns. These
are independent data sources — `TokenTracker` counts middleware-level calls
while `Usage` counts trace-entry-level sessions.
```

- [ ] **Step 2: Update ensureStartExitNodes documentation**

In `CLAUDE.md`, in the "### Dippin-lang compatibility" section, add a bullet:

```markdown
- `ensureStartExitNodes` only assigns passthrough start/exit handlers to nodes without a `prompt` attribute. Agent nodes with prompts keep their codergen handler and execute real LLM calls.
```

- [ ] **Step 3: Commit**

```bash
git add CLAUDE.md
git commit -m "docs: document token usage data flow and ensureStartExitNodes behavior"
```

---

## Self-Review Checklist

**Spec coverage:**
- [x] Critical #1: Parallel branch stats → Task 1
- [x] Critical #2: Double-counting → Task 2
- [x] Critical #3: Trivial prompts → Task 3
- [x] Important #4: failResult → Task 4
- [x] Important #5: printRunTotals ignoring result.Usage → deferred (lower priority, TUI-only display concern)
- [x] Important #6: JSON tags → Task 5
- [x] Important #7: Cache/reasoning tokens → Task 6
- [x] Important #8: CHANGELOG → Task 11
- [x] Important #9: CLAUDE.md → Task 12
- [x] Important #10: EndTime → Task 7
- [x] Important #11: Float equality → Task 8
- [x] Important #12: Error path test → Task 9
- [x] Important #13: Integration test → Task 10

**Deferred to a separate PR (minor/cosmetic):**
- Dead test code cleanup (`trace_test.go:281-306`)
- Hand-rolled `contains()` → `strings.Contains`
- `ensureStartExitNodes` file placement
- `TestSessionStatsPopulated` missing new fields
- README keyboard shortcuts and stale "coming v0.12.0"
- Node drill-down stat header in TUI
- `printRunTotals` using `result.Usage` as fallback (requires TUI design decision)
