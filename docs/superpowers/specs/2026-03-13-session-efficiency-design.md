# Session Efficiency: Tool Cache, Context Compaction, and Observability

## Problem

Agent sessions waste tokens through redundant work and context bloat:

1. **Redundant tool calls** — the agent reads the same file multiple times, re-runs identical grep/glob queries. Each call costs tool execution time and adds duplicate content to the conversation.
2. **Context bloat** — every tool output stays in `s.messages` forever. A 500-line file read on turn 3 is still consuming context on turn 40, even if it's irrelevant by then.
3. **No visibility** — callers can't see per-turn token spend, tool timing, or cumulative cost. The only signal is a final `SessionResult` summary.

These are engine-level problems that affect every pipeline, not benchmark-specific issues.

## Design

Three phases, each independently shippable.

---

### Phase 1: Tool Result Cache

**Goal:** Eliminate redundant tool executions within a single session.

#### Behavior

A per-session cache keyed on `(tool_name, arguments_json)`. When a cached tool is called with previously-seen arguments, the cached result is returned without re-execution. The cached result is still appended to `s.messages` (the LLM expects a tool result), but no filesystem/command work happens.

#### Classification

Tools are classified by their cacheability. Names match the actual registered tool names in `agent/tools/`:

| Classification | Tools | Behavior |
|---|---|---|
| **Cacheable** | `read`, `glob`, `grep_search` | Cache result, return on hit |
| **Mutating** | `bash`, `write`, `edit`, `apply_patch`, `spawn_agent` | Never cache, invalidate all cached results on execution |
| **Uncacheable** | Any unclassified tool | Never cache, no side effects on cache |

#### Invalidation

Any mutating tool call clears the entire cache. This is conservative but correct — a `bash` or `write` call can change any file, so all cached reads are potentially stale.

When multiple tool calls arrive in a single LLM response, they execute sequentially (as they do today in session.go lines 260-288). A mutating call mid-batch clears the cache, so subsequent reads in the same batch will not get stale hits.

#### Interface

Use an optional interface so external tool implementations are not broken:

```go
type CachePolicy int

const (
    CachePolicyNone      CachePolicy = iota // default: don't cache, no side effects
    CachePolicyCacheable                     // safe to cache; identical args = identical result
    CachePolicyMutating                      // never cache; invalidates all cached results
)

// CachePolicyProvider is an optional interface tools can implement
// to declare their caching behavior. Tools that don't implement it
// default to CachePolicyNone.
type CachePolicyProvider interface {
    CachePolicy() CachePolicy
}
```

The existing `Tool` interface is unchanged:

```go
type Tool interface {
    Name() string
    Description() string
    Parameters() json.RawMessage
    Execute(ctx context.Context, input json.RawMessage) (string, error)
}
```

The registry checks for `CachePolicyProvider` via type assertion when consulting the cache. Built-in tools implement it. External tools that don't implement it get `CachePolicyNone` (no caching, no invalidation).

#### Cache struct

```go
type cacheKey struct {
    toolName string
    argsJSON string
}

type toolCache struct {
    results map[cacheKey]string // cached output string
    hits    int
    misses  int
}
```

Uses a struct key to avoid separator collision issues. Lives on `Session`. Consulted before `s.registry.Execute()`, populated after successful (non-error) executions.

#### DOT control

Graph-level attribute `cache_tool_results` controls whether caching is active. Flows through `graphAttrs` in codergen.go (same pattern as `default_fidelity`):

```dot
graph [cache_tool_results="true"]
```

Node-level attribute overrides graph-level. Default is `"false"` (opt-in, no behavior change for existing pipelines).

In `codergen.go`, `buildConfig` reads the attribute and sets `config.CacheToolResults`:

```go
if v, ok := h.graphAttrs["cache_tool_results"]; ok && v == "true" {
    config.CacheToolResults = true
}
if v, ok := node.Attrs["cache_tool_results"]; ok {
    config.CacheToolResults = (v == "true")
}
```

#### Event emission

New event type `EventToolCacheHit` emitted when a cache hit occurs:

```go
Event{
    Type:      EventToolCacheHit,
    SessionID: s.id,
    ToolName:  call.Name,
    ToolInput: string(call.Arguments),
}
```

---

### Phase 2: Context Compaction

**Goal:** Reduce context consumption by summarizing old tool outputs when the context window is filling up.

#### Prerequisite: Fix ContextWindowTracker

The current `ContextWindowTracker.Update()` sums `InputTokens + OutputTokens` cumulatively. But `InputTokens` from the provider already includes the entire conversation context for that turn — so cumulative addition double/triple/N-counts input tokens. This makes utilization wildly inaccurate.

**Fix:** Replace cumulative summation with tracking the latest turn's `InputTokens` (which represents actual context size) plus cumulative `OutputTokens` as a proxy:

```go
func (t *ContextWindowTracker) Update(usage llm.Usage) {
    // InputTokens from the provider = full context size this turn.
    // Use latest input (not cumulative) as the best estimate of current context.
    t.latestInputTokens = usage.InputTokens
    t.cumulativeOutputTokens += usage.OutputTokens
}

func (t *ContextWindowTracker) Utilization() float64 {
    if t.Limit == 0 {
        return 0
    }
    return float64(t.latestInputTokens) / float64(t.Limit)
}
```

This fix also benefits the existing 0.8 warning threshold, which currently fires too early.

#### Behavior

When context utilization crosses a compaction threshold (default: 0.6, distinct from the existing 0.8 warning threshold), the session scans `s.messages` for tool result messages older than N turns (where N = loop iterations, not individual messages) and replaces their content with a short summary.

Example replacement:
- Before: `{"content": "<500 lines of Go source code>", ...}`
- After: `{"content": "[previously read: main.go — 500 lines, Go source. Re-read if needed.]", ...}`

The LLM still sees that the tool was called and gets a hint about what was in it, but the full content is gone.

#### Interaction with Phase 1 cache

If compaction replaces a tool result summary and the LLM re-requests the same file, the cache returns the full cached result. This re-read will be compacted again on the next compaction pass. This is correct behavior — the LLM asked for the data, so it should get it, but it doesn't persist forever.

#### Rules

1. Only compact tool results, never user/assistant/system messages.
2. Never compact results from the most recent N turns (default: 5). A "turn" is one iteration of the session loop (which may contain multiple tool calls).
3. Compact oldest results first.
4. After compaction, recalculate context utilization. If still above threshold, compact more aggressively (shorter summaries).
5. Results from mutating tools (`bash`) get a different summary format that preserves the exit code and first/last few lines.
6. Error results (`IsError: true`) are preserved as-is (they are usually short and diagnostically important).

#### Compaction summary format

Compaction summaries are generated from the tool result content string. The logic parses known patterns:

For `read` tool results (content starts with line-numbered output):
```
[previously read: <path> — <line_count> lines. Re-read with read_file if needed.]
```
Path and line count are extracted from the content (line numbers in `cat -n` format).

For `grep_search` results:
```
[previously searched: <pattern> — <match_count> matches found. Re-run if needed.]
```

For `bash` results:
```
[previously ran: <first_30_chars_of_content> — Re-run if needed.]
```

For any other tool or unparseable content:
```
[previous <tool_name> result — <char_count> chars. Re-run if needed.]
```

#### DOT control

```dot
graph [context_compaction="auto"]  // "auto" | "none"
Agent [context_compaction_threshold="0.6"]
```

Default is `"none"` (opt-in). `context_compaction_threshold` is a node-level attribute (each codergen node gets its own session, so per-node thresholds work naturally).

#### Interface

```go
func (s *Session) compactIfNeeded(tracker *ContextWindowTracker, currentTurn int) {
    if s.config.ContextCompaction == CompactionNone {
        return
    }
    if tracker.Utilization() < s.config.CompactionThreshold {
        return
    }
    s.compactMessages(currentTurn)
}
```

---

### Phase 3: Session Observability

**Goal:** Rich per-turn metrics so callers can understand token spend, tool timing, and cost.

#### Per-turn metrics

After each LLM response, emit a structured metrics event:

```go
Event{
    Type:      EventTurnMetrics,
    SessionID: s.id,
    Turn:      turn,
    Metrics: &TurnMetrics{
        InputTokens:        resp.Usage.InputTokens,
        OutputTokens:       resp.Usage.OutputTokens,
        CacheReadTokens:    resp.Usage.CacheReadTokens,
        CacheWriteTokens:   resp.Usage.CacheWriteTokens,
        ContextUtilization: tracker.Utilization(),
        ToolCacheHits:      s.cache.hits,
        ToolCacheMisses:    s.cache.misses,
        TurnDuration:       turnDuration,
    },
}
```

#### Tool timing

Wrap tool execution to measure wall-clock time:

```go
toolStart := time.Now()
toolResult := s.registry.Execute(ctx, call)
toolDuration := time.Since(toolStart)
```

Include `ToolDuration time.Duration` in `EventToolCallEnd`.

#### Extending SessionResult

Rather than a separate `SessionMetrics` struct, extend `SessionResult` directly with new fields to avoid duplication (it already has `Usage` and `ToolCalls`):

```go
// New fields added to SessionResult:
ToolCacheHits      int
ToolCacheMisses    int
ToolTimings        map[string]time.Duration // total wall-clock time per tool
CompactionsApplied int
LongestTurn        time.Duration
```

#### Cost model

Fallback cost estimation when `Usage.EstimatedCost` is zero (providers may not always populate it). Stored in `llm/pricing.go`:

```go
var pricing = map[string]ModelPricing{
    "claude-sonnet-4-5":  {InputPerMTok: 3.00, OutputPerMTok: 15.00},
    "claude-sonnet-4-6":  {InputPerMTok: 3.00, OutputPerMTok: 15.00},
    "claude-opus-4-6":    {InputPerMTok: 15.00, OutputPerMTok: 75.00},
    "claude-haiku-4-5":   {InputPerMTok: 0.80, OutputPerMTok: 4.00},
    "gpt-4o":             {InputPerMTok: 2.50, OutputPerMTok: 10.00},
}
```

If `Usage.EstimatedCost > 0`, use that. Otherwise, look up the model in the pricing table. Unknown models return zero cost (no crash).

---

## Concurrency Note

The tool cache and compaction logic are accessed only from the session loop, which is single-goroutine. Tool calls within a turn execute sequentially. No synchronization is needed. If parallel tool execution is added in the future, the cache will need a mutex.

## Implementation Order

1. **Phase 1 (Tool Cache):** Optional `CachePolicyProvider` interface, cache struct, session integration, DOT attribute plumbing, event emission, tests.
2. **Phase 2 (Context Compaction):** Fix `ContextWindowTracker`, compaction logic, message rewriting, DOT attributes, `validate_semantic.go` updates, tests.
3. **Phase 3 (Observability):** Metrics event type, turn metrics, tool timing, cost model, `SessionResult` extensions, tests.

Each phase is a separate branch and PR.

## Files Changed

### Phase 1
- `agent/tools/registry.go` — add `CachePolicyProvider` optional interface, `CachePolicy` type
- `agent/tools/read.go`, `glob.go`, `grep.go` — implement `CachePolicyProvider` returning `CachePolicyCacheable`
- `agent/tools/bash.go`, `write.go`, `edit.go`, `apply_patch.go`, `spawn.go` — implement `CachePolicyProvider` returning `CachePolicyMutating`
- `agent/session.go` — add cache lookup/store/invalidation in tool execution loop
- `agent/config.go` — add `CacheToolResults bool` field
- `agent/events.go` — add `EventToolCacheHit`
- `pipeline/handlers/codergen.go` — read DOT attribute from `graphAttrs`/`node.Attrs`, set on config
- `pipeline/validate_semantic.go` — add `cache_tool_results` to known attributes
- New: `agent/tool_cache.go` — cache struct and logic
- Tests for all of the above

### Phase 2
- `agent/context_window.go` — fix token tracking (use latest input, not cumulative sum)
- `agent/session.go` — add compaction call in the loop
- `agent/config.go` — add `ContextCompaction string`, `CompactionThreshold float64` fields
- `pipeline/handlers/codergen.go` — read DOT attributes
- `pipeline/validate_semantic.go` — add `context_compaction`, `context_compaction_threshold` to known attributes
- New: `agent/compaction.go` — compaction logic and summary formatters
- Tests for compaction logic and tracker fix

### Phase 3
- `agent/events.go` — add `EventTurnMetrics`, `TurnMetrics` struct, `ToolDuration` to `EventToolCallEnd`
- `agent/session.go` — emit metrics per turn, track tool timing
- `agent/result.go` — add new fields to `SessionResult`
- New: `llm/pricing.go` — model pricing table and fallback cost calculation
- Tests for metrics and cost calculation

## What This Does NOT Change

- Pipeline engine (`pipeline/engine.go`) — untouched, these are session-level concerns
- `Tool` interface — unchanged, caching is via optional `CachePolicyProvider`
- LLM layer (`llm/`) — untouched except adding `pricing.go`
- DOT parsing — existing attribute parsing already handles arbitrary key-value pairs
- Existing behavior — all features are opt-in via DOT attributes, default is off
