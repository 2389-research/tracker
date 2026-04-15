# Token & Cost Governance Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Expose complete per-provider token/cost data from the tracker library in real time, and enforce configurable pipeline-level token/cost/wall-time ceilings that halt execution when breached.

**Architecture:** Part 1 promotes `llm.TokenTracker` from a CLI-summary helper to a first-class library output by surfacing its per-provider totals on `Result` / `EngineResult`, pricing each provider via `llm.EstimateCost`, and emitting a new `EventCostUpdated` event after every trace entry so consumers see streaming updates. Part 2 adds a `BudgetGuard` evaluated inside the engine loop after each node's outcome is applied; on breach it writes a terminal trace entry, emits `EventBudgetExceeded`, and returns an `EngineResult` with status `OutcomeBudgetExceeded`. Part 3 wires graph attrs (`max_total_tokens`, `max_cost_cents`, `max_wall_time`) and CLI flags that override them.

**Tech Stack:** Go 1.22+, existing `pipeline.Engine`, `llm.TokenTracker`, `llm.pricing`, `pipeline/events.go` event bus.

**Closes:** #62, #17

---

## File Structure

**New files**
- `llm/pricing.go` — extend (not new) with `EstimateCostDetailed(model, usage) CostBreakdown` and exported `Pricing(model)` lookup.
- `llm/token_tracker_cost.go` — `(*TokenTracker).CostByProvider(resolver)` helper that maps per-provider `Usage` to dollar cost using a provided model-resolver callback.
- `pipeline/budget.go` — `BudgetLimits` struct, `BudgetGuard` with `Check(usage *UsageSummary, started time.Time) BudgetBreach`, `BudgetBreach` enum.
- `pipeline/budget_test.go` — unit tests for guard boundary behavior.
- `tracker_budget_test.go` — integration test hitting `max_cost_cents: 1`.

**Modified files**
- `pipeline/trace.go` — add `TotalCacheCreationTokens` (if missing), add `ProviderTotals map[string]ProviderUsage` to `UsageSummary`.
- `pipeline/engine.go` — thread `BudgetGuard`, add `WithBudgetGuard` option, add `OutcomeBudgetExceeded` constant, add budget check in `processActiveNode` after `applyOutcome`, set `Status` on `EngineResult` accordingly.
- `pipeline/events.go` — add `EventCostUpdated` and `EventBudgetExceeded` event types; extend `PipelineEvent` with optional `Cost *CostSnapshot` field.
- `pipeline/engine_run.go` — after every `s.trace.AddEntry(...)` that corresponds to a completed node, emit `EventCostUpdated` with the fresh aggregate (helper `emitCostUpdate`).
- `tracker.go` — add `Result.Cost` (struct: Total, ByProvider, LimitsHit), add `Config.Budget BudgetLimits`, plumb into `buildEngineOpts`. Populate `result.Cost` in `Run()` from tracker + trace.
- `pipeline/dippin_adapter.go` — parse graph-level attrs `max_total_tokens`, `max_cost_cents`, `max_wall_time` into `Graph.Attrs` (pass-through — already supported since adapter copies `WorkflowConfig` attrs). Add adapter test for the new keys.
- `cmd/tracker/run.go` — add `--max-tokens`, `--max-cost` (cents), `--max-wall-time` flags; build `BudgetLimits`; attach via `Config.Budget`.
- `cmd/tracker/summary.go` — print "HALTED: budget exceeded (reason)" when `Result.Cost.LimitsHit` is non-empty.
- `cmd/tracker/diagnose.go` — detect `OutcomeBudgetExceeded` status and surface a distinct diagnostic section.
- `CHANGELOG.md` — Added section.
- `README.md` — new "Cost governance" subsection.
- `CLAUDE.md` — add note in "Token usage flows through three layers" about new per-provider breakdown and `BudgetGuard`.

---

## Task 1: Cost snapshot types and per-provider cost resolver

**Files:**
- Modify: `llm/pricing.go`
- Create: `llm/token_tracker_cost.go`
- Test: `llm/token_tracker_cost_test.go`

- [ ] **Step 1: Write failing test for `CostBreakdown` and resolver**

Create `llm/token_tracker_cost_test.go`:

```go
package llm

import (
	"testing"
)

func TestTokenTracker_CostByProvider(t *testing.T) {
	tr := NewTokenTracker()
	tr.AddUsage("anthropic", Usage{InputTokens: 1_000_000, OutputTokens: 500_000})
	tr.AddUsage("openai", Usage{InputTokens: 2_000_000, OutputTokens: 1_000_000})

	// 1M anthropic input @ $3 + 0.5M output @ $15 = 3 + 7.5 = 10.50
	// 2M openai input @ $2.50 + 1M output @ $10 = 5 + 10 = 15.00
	resolver := func(provider string) string {
		switch provider {
		case "anthropic":
			return "claude-sonnet-4-6"
		case "openai":
			return "gpt-4o"
		}
		return ""
	}

	breakdown := tr.CostByProvider(resolver)
	if got := breakdown["anthropic"].USD; got < 10.49 || got > 10.51 {
		t.Errorf("anthropic cost: got %.4f, want 10.50", got)
	}
	if got := breakdown["openai"].USD; got < 14.99 || got > 15.01 {
		t.Errorf("openai cost: got %.4f, want 15.00", got)
	}
}

func TestTokenTracker_CostByProvider_UnknownModel(t *testing.T) {
	tr := NewTokenTracker()
	tr.AddUsage("mystery", Usage{InputTokens: 10_000, OutputTokens: 5_000})

	breakdown := tr.CostByProvider(func(string) string { return "" })
	if breakdown["mystery"].USD != 0 {
		t.Errorf("unknown model should yield $0, got %.4f", breakdown["mystery"].USD)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./llm/ -run TokenTracker_CostByProvider -v`
Expected: FAIL — `CostByProvider` undefined.

- [ ] **Step 3: Add `ProviderCost` type and `CostByProvider` method**

Create `llm/token_tracker_cost.go`:

```go
// ABOUTME: Per-provider cost rollup for the TokenTracker middleware.
// ABOUTME: Maps accumulated Usage to dollar cost using a caller-provided model resolver.
package llm

// ProviderCost is the per-provider cost rollup returned by TokenTracker.CostByProvider.
type ProviderCost struct {
	Usage Usage
	Model string
	USD   float64
}

// ModelResolver returns the model name that should be used for cost estimation
// for a given provider. Return "" when unknown — the entry will still be
// included with USD=0.
type ModelResolver func(provider string) string

// CostByProvider returns a per-provider cost rollup, resolving each provider's
// model via the caller-supplied resolver and pricing it via EstimateCost.
func (t *TokenTracker) CostByProvider(resolve ModelResolver) map[string]ProviderCost {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make(map[string]ProviderCost, len(t.usage))
	for provider, usage := range t.usage {
		model := ""
		if resolve != nil {
			model = resolve(provider)
		}
		out[provider] = ProviderCost{
			Usage: usage,
			Model: model,
			USD:   EstimateCost(model, usage),
		}
	}
	return out
}

// TotalCostUSD is a convenience helper summing CostByProvider to a single dollar
// figure using the same resolver.
func (t *TokenTracker) TotalCostUSD(resolve ModelResolver) float64 {
	var total float64
	for _, pc := range t.CostByProvider(resolve) {
		total += pc.USD
	}
	return total
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./llm/ -run TokenTracker_CostByProvider -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add llm/token_tracker_cost.go llm/token_tracker_cost_test.go
git commit -m "feat(llm): add per-provider cost rollup on TokenTracker"
```

**Acceptance gate:**
- `go test ./llm/...` passes.
- `ProviderCost` exported, `CostByProvider` and `TotalCostUSD` callable.

---

## Task 2: Extend `UsageSummary` with per-provider totals

**Files:**
- Modify: `pipeline/trace.go`
- Test: `pipeline/trace_test.go`

- [ ] **Step 1: Add failing test**

Append to `pipeline/trace_test.go`:

```go
func TestAggregateUsage_PerProvider(t *testing.T) {
	tr := &Trace{Entries: []TraceEntry{
		{Stats: &SessionStats{InputTokens: 100, OutputTokens: 50, TotalTokens: 150, CostUSD: 0.01, Provider: "anthropic"}},
		{Stats: &SessionStats{InputTokens: 200, OutputTokens: 75, TotalTokens: 275, CostUSD: 0.02, Provider: "openai"}},
		{Stats: &SessionStats{InputTokens: 50, OutputTokens: 25, TotalTokens: 75, CostUSD: 0.005, Provider: "anthropic"}},
	}}

	s := tr.AggregateUsage()
	if s == nil {
		t.Fatal("AggregateUsage returned nil")
	}
	if s.TotalTokens != 500 {
		t.Errorf("TotalTokens = %d, want 500", s.TotalTokens)
	}
	if got := s.ProviderTotals["anthropic"].InputTokens; got != 150 {
		t.Errorf("anthropic input = %d, want 150", got)
	}
	if got := s.ProviderTotals["anthropic"].CostUSD; got < 0.014 || got > 0.016 {
		t.Errorf("anthropic cost = %.4f, want 0.015", got)
	}
	if got := s.ProviderTotals["openai"].InputTokens; got != 200 {
		t.Errorf("openai input = %d, want 200", got)
	}
}
```

- [ ] **Step 2: Run test, confirm fail**

Run: `go test ./pipeline/ -run TestAggregateUsage_PerProvider -v`
Expected: FAIL — `Provider` field and `ProviderTotals` missing.

- [ ] **Step 3: Extend `SessionStats` and `UsageSummary`**

Edit `pipeline/trace.go`:

```go
// SessionStats ... (add Provider field)
type SessionStats struct {
	// ... existing fields ...
	Provider         string         `json:"provider,omitempty"`
}

// ProviderUsage is the per-provider rollup embedded in UsageSummary.
type ProviderUsage struct {
	InputTokens      int     `json:"input_tokens"`
	OutputTokens     int     `json:"output_tokens"`
	TotalTokens      int     `json:"total_tokens"`
	CostUSD          float64 `json:"cost_usd"`
	ReasoningTokens  int     `json:"reasoning_tokens"`
	CacheReadTokens  int     `json:"cache_read_tokens"`
	CacheWriteTokens int     `json:"cache_write_tokens"`
	SessionCount     int     `json:"session_count"`
}

// UsageSummary ... (add ProviderTotals)
type UsageSummary struct {
	// ... existing fields ...
	ProviderTotals map[string]ProviderUsage `json:"provider_totals,omitempty"`
}
```

Update `AggregateUsage` so each trace-entry pass also accumulates into `ProviderTotals[entry.Stats.Provider]` when the provider is non-empty:

```go
func (tr *Trace) AggregateUsage() *UsageSummary {
	if tr == nil {
		return nil
	}
	s := &UsageSummary{ProviderTotals: make(map[string]ProviderUsage)}
	for _, e := range tr.Entries {
		if e.Stats == nil {
			continue
		}
		s.TotalInputTokens += e.Stats.InputTokens
		s.TotalOutputTokens += e.Stats.OutputTokens
		s.TotalTokens += e.Stats.TotalTokens
		s.TotalCostUSD += e.Stats.CostUSD
		s.TotalReasoningTokens += e.Stats.ReasoningTokens
		s.TotalCacheReadTokens += e.Stats.CacheReadTokens
		s.TotalCacheWriteTokens += e.Stats.CacheWriteTokens
		s.SessionCount++
		if p := e.Stats.Provider; p != "" {
			pt := s.ProviderTotals[p]
			pt.InputTokens += e.Stats.InputTokens
			pt.OutputTokens += e.Stats.OutputTokens
			pt.TotalTokens += e.Stats.TotalTokens
			pt.CostUSD += e.Stats.CostUSD
			pt.ReasoningTokens += e.Stats.ReasoningTokens
			pt.CacheReadTokens += e.Stats.CacheReadTokens
			pt.CacheWriteTokens += e.Stats.CacheWriteTokens
			pt.SessionCount++
			s.ProviderTotals[p] = pt
		}
	}
	if s.SessionCount == 0 {
		return nil
	}
	if len(s.ProviderTotals) == 0 {
		s.ProviderTotals = nil
	}
	return s
}
```

- [ ] **Step 4: Populate `SessionStats.Provider` at the transcript site**

Find the `buildSessionStats` function:

Run: `grep -n "buildSessionStats" pipeline/handlers/transcript.go`

Edit it to set `stats.Provider = sessionResult.Provider` (or equivalent — use whatever field the agent's `SessionResult` already exposes; if none, pull from the first LLM trace entry in the session).

- [ ] **Step 5: Run all trace tests**

Run: `go test ./pipeline/ -run TestAggregate -v`
Expected: PASS (new + existing).

- [ ] **Step 6: Commit**

```bash
git add pipeline/trace.go pipeline/trace_test.go pipeline/handlers/transcript.go
git commit -m "feat(pipeline): per-provider rollup in UsageSummary"
```

**Acceptance gate:**
- `go test ./pipeline/...` passes.
- A trace with two providers yields two entries in `UsageSummary.ProviderTotals`.

---

## Task 3: `Result.Cost` on the library API

**Files:**
- Modify: `tracker.go`
- Test: `tracker_test.go`

- [ ] **Step 1: Write failing test**

Append to `tracker_test.go` a test that runs a trivial passthrough pipeline with a mocked `Completer` returning a `Usage{InputTokens: 1000, OutputTokens: 500}` on a known model, then asserts:

```go
func TestRun_ResultCost_PopulatesProviderAndTotal(t *testing.T) {
	// Uses existing test harness — see nearby tests for stub Completer pattern.
	// Assertion:
	//   result.Cost.TotalUSD > 0
	//   result.Cost.ByProvider["anthropic"].USD > 0
	//   result.Cost.ByProvider["anthropic"].Usage.InputTokens == 1000
}
```

Model the stub after the nearest existing `tracker_test.go` test that injects `Config.LLMClient`.

- [ ] **Step 2: Confirm fail**

Run: `go test . -run TestRun_ResultCost -v`
Expected: FAIL — `Result.Cost` undefined.

- [ ] **Step 3: Add `Cost` type and populate in `Run`**

Edit `tracker.go`:

```go
// CostReport summarizes spend for a pipeline run.
type CostReport struct {
	TotalUSD   float64
	ByProvider map[string]llm.ProviderCost
	LimitsHit  []string // populated if BudgetGuard halted the run
}

// Result — add field:
type Result struct {
	// ... existing fields ...
	Cost *CostReport
}
```

In `(*Engine).Run` after `result := resultFromEngine(...)`:

```go
if e.tokenTracker != nil {
	resolver := func(provider string) string {
		return e.defaultModelFor(provider)
	}
	byProvider := e.tokenTracker.CostByProvider(resolver)
	total := 0.0
	for _, pc := range byProvider {
		total += pc.USD
	}
	result.Cost = &CostReport{
		TotalUSD:   total,
		ByProvider: byProvider,
	}
}
if engineResult != nil && engineResult.Status == pipeline.OutcomeBudgetExceeded {
	if result.Cost == nil {
		result.Cost = &CostReport{}
	}
	result.Cost.LimitsHit = engineResult.BudgetLimitsHit // added in Task 5
}
```

Add `defaultModelFor`: look up `graph.Attrs["llm_model"]` as a universal fallback, and optionally a future per-provider map. For this task, returning `graph.Attrs["llm_model"]` for every provider is sufficient.

- [ ] **Step 4: Run test, confirm pass**

Run: `go test . -run TestRun_ResultCost -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add tracker.go tracker_test.go
git commit -m "feat(tracker): expose CostReport on Result"
```

**Acceptance gate:**
- `Result.Cost.TotalUSD` and `Result.Cost.ByProvider` populated after every run that had any LLM activity.
- `go test ./...` passes.

---

## Task 4: Streaming `EventCostUpdated`

**Files:**
- Modify: `pipeline/events.go`
- Modify: `pipeline/engine_run.go`
- Modify: `pipeline/engine.go`
- Test: `pipeline/engine_test.go` (new test)

- [ ] **Step 1: Add failing test**

Add to `pipeline/engine_test.go` (or nearest engine test file):

```go
func TestEngine_EmitsCostUpdatedAfterEachNode(t *testing.T) {
	// Build a 3-node linear graph where each node's handler returns
	// SessionStats{InputTokens:100, OutputTokens:50, TotalTokens:150, CostUSD: 0.01}
	// Capture events via a PipelineEventHandlerFunc.
	// Assert: exactly 3 EventCostUpdated events; each carries a non-nil
	//         Cost snapshot whose TotalCostUSD is monotonically increasing.
}
```

- [ ] **Step 2: Confirm fail**

Run: `go test ./pipeline/ -run TestEngine_EmitsCostUpdated -v`
Expected: FAIL — `EventCostUpdated` undefined.

- [ ] **Step 3: Add event type and payload**

Edit `pipeline/events.go`:

```go
const (
	// ... existing constants ...
	EventCostUpdated    PipelineEventType = "cost_updated"
	EventBudgetExceeded PipelineEventType = "budget_exceeded"
)

// CostSnapshot is the payload of EventCostUpdated/EventBudgetExceeded events.
type CostSnapshot struct {
	TotalTokens    int
	TotalCostUSD   float64
	ProviderTotals map[string]ProviderUsage
	WallElapsed    time.Duration
}

// PipelineEvent — add field:
type PipelineEvent struct {
	// ... existing fields ...
	Cost *CostSnapshot
}
```

- [ ] **Step 4: Emit after trace entries**

In `pipeline/engine_run.go` add:

```go
func (e *Engine) emitCostUpdate(s *runState) {
	summary := s.trace.AggregateUsage()
	if summary == nil {
		return
	}
	e.emit(PipelineEvent{
		Type:      EventCostUpdated,
		Timestamp: time.Now(),
		RunID:     s.runID,
		Cost: &CostSnapshot{
			TotalTokens:    summary.TotalTokens,
			TotalCostUSD:   summary.TotalCostUSD,
			ProviderTotals: summary.ProviderTotals,
			WallElapsed:    time.Since(s.trace.StartTime),
		},
	})
}
```

Find each call site that ends a node's execution successfully and calls `s.trace.AddEntry(...)` for a user-facing node (not retry-bookkeeping entries). Call `e.emitCostUpdate(s)` immediately after. Use the call sites in `engine.go:279, 294, 319` and `engine_run.go:345, 365, 432, 449, 466, 472, 477`. Skip the retry-bookkeeping adds that already have a preceding add for the same node.

Guideline: add `emitCostUpdate` after any `AddEntry` where `traceEntry.Status` is `OutcomeSuccess`, `OutcomeFail`, or `OutcomeEscalate`. Do NOT emit after retry-scheduling entries.

- [ ] **Step 5: Run test, confirm pass**

Run: `go test ./pipeline/ -run TestEngine_EmitsCostUpdated -v`
Expected: PASS — three events, monotonically increasing cost.

- [ ] **Step 6: Commit**

```bash
git add pipeline/events.go pipeline/engine.go pipeline/engine_run.go pipeline/engine_test.go
git commit -m "feat(pipeline): emit EventCostUpdated after each completed node"
```

**Acceptance gate:**
- A 3-node run emits exactly 3 `EventCostUpdated` events.
- `PipelineEvent.Cost` is non-nil on those events; `nil` on all other event types.
- All existing pipeline tests pass.

---

## Task 5: `BudgetGuard` + `OutcomeBudgetExceeded`

**Files:**
- Create: `pipeline/budget.go`
- Create: `pipeline/budget_test.go`
- Modify: `pipeline/engine.go` (EngineResult, constants, option, Run loop)

- [ ] **Step 1: Write failing unit tests**

Create `pipeline/budget_test.go`:

```go
package pipeline

import (
	"testing"
	"time"
)

func TestBudgetGuard_UnderLimits(t *testing.T) {
	g := NewBudgetGuard(BudgetLimits{MaxTotalTokens: 1000, MaxCostCents: 100, MaxWallTime: time.Minute})
	breach := g.Check(&UsageSummary{TotalTokens: 500, TotalCostUSD: 0.25}, time.Now())
	if breach.Kind != BudgetOK {
		t.Errorf("got breach %v, want BudgetOK", breach.Kind)
	}
}

func TestBudgetGuard_TokenCeiling(t *testing.T) {
	g := NewBudgetGuard(BudgetLimits{MaxTotalTokens: 1000})
	breach := g.Check(&UsageSummary{TotalTokens: 1001}, time.Now())
	if breach.Kind != BudgetTokens {
		t.Errorf("got %v, want BudgetTokens", breach.Kind)
	}
}

func TestBudgetGuard_ExactTokenCeiling(t *testing.T) {
	g := NewBudgetGuard(BudgetLimits{MaxTotalTokens: 1000})
	if g.Check(&UsageSummary{TotalTokens: 1000}, time.Now()).Kind != BudgetOK {
		t.Errorf("exact limit should be OK (inclusive)")
	}
}

func TestBudgetGuard_CostCeiling(t *testing.T) {
	g := NewBudgetGuard(BudgetLimits{MaxCostCents: 50})
	breach := g.Check(&UsageSummary{TotalCostUSD: 0.51}, time.Now())
	if breach.Kind != BudgetCost {
		t.Errorf("got %v, want BudgetCost", breach.Kind)
	}
}

func TestBudgetGuard_WallTime(t *testing.T) {
	g := NewBudgetGuard(BudgetLimits{MaxWallTime: 10 * time.Millisecond})
	started := time.Now().Add(-time.Second)
	breach := g.Check(&UsageSummary{}, started)
	if breach.Kind != BudgetWallTime {
		t.Errorf("got %v, want BudgetWallTime", breach.Kind)
	}
}

func TestBudgetGuard_NilLimitsNoOp(t *testing.T) {
	var g *BudgetGuard
	if g.Check(&UsageSummary{TotalTokens: 999999}, time.Now()).Kind != BudgetOK {
		t.Errorf("nil guard should always return BudgetOK")
	}
}
```

- [ ] **Step 2: Confirm fail**

Run: `go test ./pipeline/ -run BudgetGuard -v`
Expected: FAIL — undefined.

- [ ] **Step 3: Implement `BudgetGuard`**

Create `pipeline/budget.go`:

```go
// ABOUTME: Pipeline-level token, cost, and wall-time ceilings enforced between nodes.
// ABOUTME: Halts execution with OutcomeBudgetExceeded when any configured limit is breached.
package pipeline

import "time"

// BudgetLimits configures hard ceilings for a pipeline run.
// A zero-value field means "no limit" for that dimension.
type BudgetLimits struct {
	MaxTotalTokens int
	MaxCostCents   int
	MaxWallTime    time.Duration
}

// IsZero reports whether any limit is configured.
func (l BudgetLimits) IsZero() bool {
	return l.MaxTotalTokens == 0 && l.MaxCostCents == 0 && l.MaxWallTime == 0
}

// BudgetBreachKind classifies which limit was hit.
type BudgetBreachKind int

const (
	BudgetOK BudgetBreachKind = iota
	BudgetTokens
	BudgetCost
	BudgetWallTime
)

// BudgetBreach describes the outcome of a guard check.
type BudgetBreach struct {
	Kind    BudgetBreachKind
	Message string
}

// BudgetGuard evaluates BudgetLimits against a UsageSummary snapshot.
type BudgetGuard struct {
	limits BudgetLimits
}

// NewBudgetGuard constructs a BudgetGuard with the given limits.
// If limits.IsZero(), returns nil so callers can skip the check entirely.
func NewBudgetGuard(limits BudgetLimits) *BudgetGuard {
	if limits.IsZero() {
		return nil
	}
	return &BudgetGuard{limits: limits}
}

// Check reports whether the given usage snapshot breaches any configured limit.
// A nil guard always returns BudgetOK.
func (g *BudgetGuard) Check(usage *UsageSummary, started time.Time) BudgetBreach {
	if g == nil {
		return BudgetBreach{Kind: BudgetOK}
	}
	if g.limits.MaxTotalTokens > 0 && usage != nil && usage.TotalTokens > g.limits.MaxTotalTokens {
		return BudgetBreach{
			Kind:    BudgetTokens,
			Message: "max_total_tokens exceeded",
		}
	}
	if g.limits.MaxCostCents > 0 && usage != nil {
		cents := int(usage.TotalCostUSD * 100)
		if cents > g.limits.MaxCostCents {
			return BudgetBreach{
				Kind:    BudgetCost,
				Message: "max_cost_cents exceeded",
			}
		}
	}
	if g.limits.MaxWallTime > 0 && time.Since(started) > g.limits.MaxWallTime {
		return BudgetBreach{
			Kind:    BudgetWallTime,
			Message: "max_wall_time exceeded",
		}
	}
	return BudgetBreach{Kind: BudgetOK}
}

// String returns a human-readable label for a breach kind.
func (k BudgetBreachKind) String() string {
	switch k {
	case BudgetTokens:
		return "tokens"
	case BudgetCost:
		return "cost"
	case BudgetWallTime:
		return "wall_time"
	default:
		return "ok"
	}
}
```

- [ ] **Step 4: Run unit tests**

Run: `go test ./pipeline/ -run BudgetGuard -v`
Expected: PASS.

- [ ] **Step 5: Add `OutcomeBudgetExceeded` and engine option**

Edit `pipeline/engine.go`:

```go
const OutcomeBudgetExceeded = "budget_exceeded"

// EngineResult — add:
type EngineResult struct {
	// ... existing fields ...
	BudgetLimitsHit []string
}

// WithBudgetGuard attaches a BudgetGuard evaluated between nodes.
func WithBudgetGuard(guard *BudgetGuard) EngineOption {
	return func(e *Engine) { e.budgetGuard = guard }
}

// Engine — add field:
type Engine struct {
	// ... existing fields ...
	budgetGuard *BudgetGuard
}
```

- [ ] **Step 6: Enforce in the run loop**

In `pipeline/engine_run.go`, right after each `emitCostUpdate(s)` call (added in Task 4), add:

```go
if breach := e.budgetGuard.Check(s.trace.AggregateUsage(), s.trace.StartTime); breach.Kind != BudgetOK {
	return e.haltForBudget(s, breach)
}
```

Add helper in `engine_run.go`:

```go
func (e *Engine) haltForBudget(s *runState, breach BudgetBreach) loopResult {
	s.trace.EndTime = time.Now()
	e.emit(PipelineEvent{
		Type:      EventBudgetExceeded,
		Timestamp: time.Now(),
		RunID:     s.runID,
		Message:   breach.Message,
	})
	return loopResult{
		action: loopReturn,
		result: &EngineResult{
			RunID:           s.runID,
			Status:          OutcomeBudgetExceeded,
			CompletedNodes:  s.cp.CompletedNodes,
			Context:         s.pctx.Snapshot(),
			Trace:           s.trace,
			Usage:           s.trace.AggregateUsage(),
			BudgetLimitsHit: []string{breach.Kind.String()},
		},
	}
}
```

The return value type of `processNode` / `processActiveNode` / `advanceToNextNode` is `loopResult`, which already supports `loopReturn`. Plumb `haltForBudget` through whichever function you put the check in — if it's in `advanceToNextNode`, return the `loopResult` directly; if it's in a callee, bubble it up.

Concretely: put the check in a new method `checkBudgetAfterNode` called at the top of `advanceToNextNode` *after* `s.trace.AddEntry(*traceEntry)` has run, and return its `loopResult` if non-OK. Because `advanceToNextNode` already adds the entry at line 294 before selecting the next edge, move the emit+check to after that `AddEntry`.

- [ ] **Step 7: Integration test for engine halt**

Add to `pipeline/engine_test.go`:

```go
func TestEngine_HaltsOnBudgetBreach(t *testing.T) {
	// 5-node linear graph. Each node's stub handler returns SessionStats{TotalTokens: 300}.
	// Guard: MaxTotalTokens = 700. Expect halt after node 3 (total 900 > 700), Status OutcomeBudgetExceeded.
}
```

- [ ] **Step 8: Run all pipeline tests**

Run: `go test ./pipeline/ -v`
Expected: PASS — including the new halt test and all existing ones.

- [ ] **Step 9: Commit**

```bash
git add pipeline/budget.go pipeline/budget_test.go pipeline/engine.go pipeline/engine_run.go pipeline/engine_test.go
git commit -m "feat(pipeline): BudgetGuard enforces token/cost/wall-time ceilings"
```

**Acceptance gate:**
- Budget tests pass.
- A deliberately over-budget run halts with `Status == OutcomeBudgetExceeded` and `BudgetLimitsHit` populated.
- No regressions in existing `./pipeline/...` tests.

---

## Task 6: Adapter passes budget attrs through

**Files:**
- Modify: `pipeline/dippin_adapter.go`
- Test: `pipeline/dippin_adapter_test.go`

- [ ] **Step 1: Failing test**

Add:

```go
func TestAdapter_BudgetAttrsPassThrough(t *testing.T) {
	src := `
workflow test {
	max_total_tokens: 50000
	max_cost_cents: 250
	max_wall_time: "10m"
	start -> done
	node start { prompt: "hi" }
	node done { }
}`
	// parse via dippin parser, convert, assert graph.Attrs has the three keys verbatim.
}
```

- [ ] **Step 2: Confirm fail or pass**

Run: `go test ./pipeline/ -run TestAdapter_BudgetAttrsPassThrough -v`
Expected: likely already passes if adapter copies all workflow attrs — if so, this test locks behavior. If it fails, add the keys to whatever attr-copy list the adapter uses.

- [ ] **Step 3: If failing, update adapter to copy the three keys into `graph.Attrs`**

(Editing guidance only; the exact location is the workflow-attr loop in `FromDippinIR`.)

- [ ] **Step 4: Commit**

```bash
git add pipeline/dippin_adapter.go pipeline/dippin_adapter_test.go
git commit -m "feat(adapter): pass through budget attrs (tokens/cost/walltime)"
```

**Acceptance gate:**
- Graph from a `.dip` file with budget attrs exposes them on `graph.Attrs` with the exact keys `max_total_tokens`, `max_cost_cents`, `max_wall_time`.

---

## Task 7: Library wiring — `Config.Budget` and graph-attr fallback

**Files:**
- Modify: `tracker.go`
- Test: `tracker_budget_test.go` (new)

- [ ] **Step 1: Failing integration test**

Create `tracker_budget_test.go`:

```go
package tracker

import (
	"context"
	"strings"
	"testing"

	"github.com/2389-research/tracker/llm"
	"github.com/2389-research/tracker/pipeline"
)

func TestRun_BudgetHalt_FromConfig(t *testing.T) {
	// Minimal .dip with two nodes that each produce stub usage 600 tokens.
	// Use a stub Completer; see existing tests for the pattern.
	// Config.Budget = BudgetLimits{MaxTotalTokens: 1000}
	// Expect: result.Status == "budget_exceeded", result.Cost.LimitsHit contains "tokens".
}

func TestRun_BudgetHalt_FromGraphAttrs(t *testing.T) {
	// Same stub, but budget only in the .dip source as max_total_tokens: 1000.
	// Config.Budget unset. Expect same halt.
}
```

- [ ] **Step 2: Confirm fail**

Run: `go test . -run TestRun_Budget -v`
Expected: FAIL.

- [ ] **Step 3: Wire through `Config` and graph attrs**

In `tracker.go`:

```go
type Config struct {
	// ... existing fields ...
	Budget pipeline.BudgetLimits
}
```

In `buildEngineOpts`:

```go
limits := resolveBudgetLimits(cfg, graph)
if !limits.IsZero() {
	opts = append(opts, pipeline.WithBudgetGuard(pipeline.NewBudgetGuard(limits)))
}
```

Note: `buildEngineOpts` currently does not receive `graph`. Thread it: change the signature to `buildEngineOpts(cfg Config, graph *pipeline.Graph)` and update the one caller in `buildEngine`.

Add `resolveBudgetLimits`:

```go
// resolveBudgetLimits returns Config.Budget merged with graph attrs.
// Config.Budget wins field-by-field; graph attrs fill in the gaps.
func resolveBudgetLimits(cfg Config, graph *pipeline.Graph) pipeline.BudgetLimits {
	limits := cfg.Budget
	if limits.MaxTotalTokens == 0 {
		limits.MaxTotalTokens = atoiAttr(graph.Attrs["max_total_tokens"])
	}
	if limits.MaxCostCents == 0 {
		limits.MaxCostCents = atoiAttr(graph.Attrs["max_cost_cents"])
	}
	if limits.MaxWallTime == 0 {
		if d, err := time.ParseDuration(graph.Attrs["max_wall_time"]); err == nil {
			limits.MaxWallTime = d
		}
	}
	return limits
}

func atoiAttr(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}
```

- [ ] **Step 4: Pass through in `Run`**

After `engineResult, err := e.inner.Run(ctx)`, if `engineResult.Status == pipeline.OutcomeBudgetExceeded`, copy `engineResult.BudgetLimitsHit` into `result.Cost.LimitsHit` (handled in Task 3 — verify the field name matches).

- [ ] **Step 5: Run tests**

Run: `go test . -run TestRun_Budget -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add tracker.go tracker_budget_test.go
git commit -m "feat(tracker): Config.Budget and graph-attr fallback wire BudgetGuard"
```

**Acceptance gate:**
- Library consumers can set `Config.Budget = pipeline.BudgetLimits{...}` and see the run halt.
- A `.dip` file with `max_total_tokens: 1000` halts without any CLI flag or Config field.
- `Result.Cost.LimitsHit` identifies which limit was hit.

---

## Task 8: CLI flags `--max-tokens --max-cost --max-wall-time`

**Files:**
- Modify: `cmd/tracker/run.go`
- Modify: `cmd/tracker/summary.go`
- Test: `cmd/tracker/main_test.go` (CLI smoke test)

- [ ] **Step 1: Add flags**

In `cmd/tracker/run.go`, add:

```go
var (
	maxTokensFlag   int
	maxCostCents    int
	maxWallTimeFlag time.Duration
)

// in flag registration:
runCmd.Flags().IntVar(&maxTokensFlag, "max-tokens", 0, "halt if total tokens exceed this value")
runCmd.Flags().IntVar(&maxCostCents, "max-cost", 0, "halt if total cost (cents) exceeds this value")
runCmd.Flags().DurationVar(&maxWallTimeFlag, "max-wall-time", 0, "halt if pipeline wall time exceeds this duration")
```

Wire into `Config.Budget` before calling `tracker.Run`.

- [ ] **Step 2: Summary output**

In `cmd/tracker/summary.go`, after the existing totals block, if `result.Cost != nil && len(result.Cost.LimitsHit) > 0`, print:

```
HALTED: budget exceeded (tokens)
  spent: 1,250 tokens, $0.04
  limit: 1,000 tokens
```

Keep it a single paragraph, styled consistently with existing error sections.

- [ ] **Step 3: Smoke test**

Add to `cmd/tracker/main_test.go` a subtest that invokes the CLI with `--max-tokens=10` against a stub pipeline (or skips if the harness doesn't support stub LLMs — fall back to `go test -run TestRun_Budget` in the library as the authoritative gate).

- [ ] **Step 4: Build and run**

Run: `go build ./... && go test ./cmd/tracker/... -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/tracker/run.go cmd/tracker/summary.go cmd/tracker/main_test.go
git commit -m "feat(cli): --max-tokens --max-cost --max-wall-time flags"
```

**Acceptance gate:**
- `tracker run foo.dip --max-tokens 10` halts and prints the budget-halt summary.
- `tracker run foo.dip` unchanged (no flags = no limits).

---

## Task 9: `tracker diagnose` surfaces budget halts

**Files:**
- Modify: `cmd/tracker/diagnose.go`
- Test: manual verification (diagnose reads from disk artifacts)

- [ ] **Step 1: Add branch**

In `diagnose.go`, find the status-based branching logic. Add a case for `status == "budget_exceeded"` that prints:

```
Budget halt detected
  reason: <first entry of BudgetLimitsHit>
  suggestion: raise the relevant --max-* flag or remove the graph attr
```

- [ ] **Step 2: Manual test**

Run: `tracker run examples/ask_and_execute.dip --max-tokens 10 || true` then `tracker diagnose`
Expected: The diagnose output names the budget halt.

- [ ] **Step 3: Commit**

```bash
git add cmd/tracker/diagnose.go
git commit -m "feat(diagnose): surface budget halts distinctly"
```

**Acceptance gate:**
- A run halted by budget yields a diagnose section that names the breach kind.

---

## Task 10: Docs + CHANGELOG + CLAUDE.md

**Files:**
- Modify: `CHANGELOG.md`
- Modify: `README.md`
- Modify: `CLAUDE.md`

- [ ] **Step 1: CHANGELOG**

Add under an `## [Unreleased]` → `### Added` section:

```
### Added
- `Result.Cost` on the library API with per-provider rollup and total USD (closes #62).
- `pipeline.BudgetGuard` enforcing `max_total_tokens`, `max_cost_cents`, `max_wall_time`. Configurable via `Config.Budget`, `.dip` graph attrs, or `--max-tokens / --max-cost / --max-wall-time` flags (closes #17).
- New pipeline events `cost_updated` and `budget_exceeded` for streaming consumers.
```

- [ ] **Step 2: README section**

Add a "Cost governance" subsection showing both consumer usage of `result.Cost.ByProvider` and a `.dip` snippet with `max_cost_cents: 500`.

- [ ] **Step 3: CLAUDE.md note**

Append to the "Token usage flows through three layers" section:

```
As of v0.17.0, `UsageSummary.ProviderTotals` carries the per-provider rollup, and `Result.Cost` on the library API exposes dollar cost. A `pipeline.BudgetGuard` evaluated between nodes halts the run with `OutcomeBudgetExceeded` when any configured limit is breached; see `pipeline/budget.go`.
```

- [ ] **Step 4: Commit**

```bash
git add CHANGELOG.md README.md CLAUDE.md
git commit -m "docs: cost governance — Result.Cost, BudgetGuard, and flags"
```

**Acceptance gate:**
- CHANGELOG entry references both issues.
- README shows at least one working snippet.
- CLAUDE.md mentions `BudgetGuard` and `ProviderTotals`.

---

## Task 11: Full pre-PR verification

- [ ] **Step 1: Build**

Run: `go build ./...`
Expected: no errors.

- [ ] **Step 2: Full test**

Run: `go test ./... -short`
Expected: all 14+ packages pass.

- [ ] **Step 3: Dippin doctor on all examples**

Run: `dippin doctor examples/ask_and_execute.dip examples/build_product.dip examples/build_product_with_superspec.dip`
Expected: A grade across the board.

- [ ] **Step 4: Smoke test a real run with a low budget**

Run: `tracker run examples/ask_and_execute.dip --max-tokens 1 || true`
Expected: Exits with budget halt; `tracker diagnose` names it.

- [ ] **Step 5: Smoke test a real run without budget**

Run: `tracker run examples/ask_and_execute.dip`
Expected: Normal completion; CLI summary shows per-provider cost rollup.

- [ ] **Step 6: Push branch**

```bash
git push -u origin feat/token-cost-governance
```

**Acceptance gate (hard blockers for PR):**
1. `go build ./...` — zero errors.
2. `go test ./... -short` — zero failures.
3. `dippin doctor` — A on every shipped example.
4. Hooks pass without `--no-verify`.
5. A budget-exceeded run halts with `OutcomeBudgetExceeded` and a populated `Result.Cost.LimitsHit`.
6. A normal run's `Result.Cost.ByProvider` is non-empty and `TotalUSD > 0` whenever any LLM call was made.
7. Three or more `EventCostUpdated` events are observable on a 3-node pipeline.
8. CHANGELOG, README, CLAUDE.md all updated.

---

## Task 12: Open the PR

- [ ] **Step 1: Create PR**

```bash
gh pr create --title "feat: token & cost governance (library exposure + pipeline ceilings)" \
  --body "$(cat <<'EOF'
## Summary
- Expose per-provider token and cost data on `Result.Cost` in the library API (closes #62)
- Enforce `max_total_tokens`, `max_cost_cents`, `max_wall_time` via `BudgetGuard` (closes #17)
- Stream `cost_updated` events for real-time consumer dashboards
- CLI flags `--max-tokens --max-cost --max-wall-time` and graph-attr equivalents

## Test plan
- [ ] `go test ./... -short`
- [ ] `dippin doctor` on all shipped examples
- [ ] Manual: `tracker run examples/ask_and_execute.dip --max-tokens 1` halts with budget error
- [ ] Manual: `tracker diagnose` after a budget halt surfaces the breach kind
- [ ] Manual: `tracker run examples/ask_and_execute.dip` prints per-provider cost rollup

Closes #62
Closes #17

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

- [ ] **Step 2: Report URL to user**

---

## Self-review notes

- Every task ships runnable code with tests and a commit.
- Types used after Task 2 (`ProviderUsage`, `UsageSummary.ProviderTotals`) are defined there.
- Types used after Task 5 (`BudgetLimits`, `BudgetGuard`, `OutcomeBudgetExceeded`, `EngineResult.BudgetLimitsHit`) are defined there.
- `Result.Cost` is introduced in Task 3 and extended in Task 7; field name `LimitsHit` is consistent.
- `EventCostUpdated` emission (Task 4) and `BudgetGuard` check (Task 5, Step 6) share the same call site — the check runs immediately after the emit to ensure the latest snapshot is evaluated.
- Pricing comes from `llm.EstimateCost` (already present) — no new pricing table is introduced; if models are missing from `llm/pricing.go`, that's a separate change and out of scope.
- #62 asked for real-time updates, per-provider breakdowns, and accurate cost; all three are addressed (Tasks 4, 2/3, and the reuse of `EstimateCost`).
- #17 asked for aggregate token cap, cost ceiling, wall-time timeout, and a circuit breaker; all four are in `BudgetLimits` + `BudgetGuard`.
