# Engine P1 Fixes — Design Spec

**Date:** 2026-04-03
**Issues:** #24, #21, #26
**Scope:** Per-node context keys, condition parser hardening, consensus pipeline parallelization

---

## Fix 1: Per-node namespaced context keys (#24)

**Problem:** The codergen handler writes every node's output to `last_response`, overwriting the previous node's output. In a multi-step pipeline, downstream nodes can only see the immediately preceding node's output. The same issue affects `human_response`.

**Fix:** Alongside `last_response`, also write `response.<nodeID>` so downstream nodes can reference specific upstream outputs. `last_response` remains as a convenience alias for the most recent output.

### Changes

**`pipeline/context.go`** — Add a helper constant prefix:

```go
ContextKeyResponsePrefix = "response."
```

**`pipeline/handlers/codergen.go`** — In `buildSuccessOutcome` (line 338) and `buildFailureOutcome` (lines 287, 321), add a per-node key to `ContextUpdates`:

```go
outcome.ContextUpdates[pipeline.ContextKeyResponsePrefix + node.ID] = responseText
```

**`pipeline/handlers/human.go`** — In the freeform outcome (line 454) and interview outcome (line 561), add a per-node key alongside `ContextKeyHumanResponse`:

```go
outcome.ContextUpdates[pipeline.ContextKeyResponsePrefix + node.ID] = response
```

### Backward compatibility

- `last_response` and `human_response` still work exactly as before
- Existing pipelines are unaffected
- New pipelines can use `ctx.response.NodeName` in prompts and conditions
- The condition evaluator already resolves `ctx.response.NodeName` via `resolveVariable` (strips `ctx.` prefix, looks up `response.NodeName` in context)

### Tests

- `TestCodergenHandler_WritesPerNodeResponse` — verify both `last_response` and `response.<nodeID>` are set
- `TestHumanHandler_WritesPerNodeResponse` — same for human handler
- `TestPerNodeResponse_DownstreamAccess` — multi-node pipeline where node 3 reads `response.node1`

---

## Fix 2: Harden condition parser (#21)

**Problem:** The condition evaluator splits on `||` and `&&` using `strings.Split`, which is fragile. Additionally, `=` vs `==` is ambiguous, and values can't be quoted.

**Fix:** Three targeted hardening changes:

### 2a: Support `==` as alias for `=`

In `evaluateClause`, check for `==` before `=` (similar to how `!=` is checked first):

```go
// Check == before = (== contains = as substring)
if idx := strings.Index(clause, "=="); idx >= 0 {
    key := strings.TrimSpace(clause[:idx])
    expected := strings.TrimSpace(clause[idx+2:])
    actual := resolveAndWarnVar(key, ctx)
    return actual == expected, nil
}
```

### 2b: Strip quotes from values

After extracting `expected` in both `=` and `!=` handlers, strip surrounding double quotes:

```go
expected = strings.Trim(expected, `"`)
```

This allows conditions like `ctx.outcome = "success"` and `ctx.name = "has spaces"`.

### 2c: Document limitations

The `||`/`&&` splitting limitation (values containing these literals are misinterpreted) is a known constraint. The current approach works for all existing pipelines. A proper tokenizer/parser is a future enhancement (not this PR). Add a comment at the top of `condition.go` documenting this.

### Tests

- `TestConditionDoubleEquals` — `ctx.outcome == success` works
- `TestConditionQuotedValues` — `ctx.name = "hello world"` works
- `TestConditionQuotedWithOperator` — values in quotes aren't split on operators

---

## Fix 3: Parallelize consensus pipeline (#26)

**Problem:** `consensus_task.dip` runs multi-model agents sequentially (Gemini → GPT → Opus). Later models see earlier outputs via `last_response`, defeating independent evaluation. It's also slower than necessary.

**Fix:** Convert three sequential phases to parallel fan-out/fan-in. The consolidation nodes already serve as natural join points.

### Phase 1: DoD (Definition of Done)
```
RefineDoD → parallel DoDParallel → [DefineDoDGemini, DefineDoDGPT, DefineDoDOpus]
fan_in DoDJoin ← [DefineDoDGemini, DefineDoDGPT, DefineDoDOpus]
DoDJoin → ConsolidateDoD
```

### Phase 2: Planning
```
ConsolidateDoD → parallel PlanParallel → [PlanGemini, PlanGPT, PlanOpus]
fan_in PlanJoin ← [PlanGemini, PlanGPT, PlanOpus]
PlanJoin → DebateConsolidate
```

### Phase 3: Review
```
VerifyOutputs → parallel ReviewParallel → [ReviewGemini, ReviewGPT, ReviewOpus]
fan_in ReviewJoin ← [ReviewGemini, ReviewGPT, ReviewOpus]
ReviewJoin → ReviewConsensus
```

### Consolidation node prompts

Update ConsolidateDoD, DebateConsolidate, and ReviewConsensus prompts to reference per-node outputs (from Fix 1):

```
Merge the following DoD drafts:
- Gemini: ${ctx.response.DefineDoDGemini}
- GPT: ${ctx.response.DefineDoDGPT}
- Opus: ${ctx.response.DefineDoDOpus}
```

### What stays sequential

- `Start → RefineDoD` (seed input)
- `DebateConsolidate → Implement → VerifyOutputs` (must be sequential)
- `ReviewConsensus → Exit/Postmortem` (decision point)
- Retry loop: `Postmortem → PlanGemini` stays but reroutes to `PlanParallel`

### Tests

- `dippin doctor examples/consensus_task.dip` must pass with A grade
- `dippin simulate -all-paths examples/consensus_task.dip` must complete

---

## Files Changed

| File | Changes |
|------|---------|
| `pipeline/context.go` | Add `ContextKeyResponsePrefix` |
| `pipeline/handlers/codergen.go` | Write `response.<nodeID>` alongside `last_response` |
| `pipeline/handlers/human.go` | Write `response.<nodeID>` alongside `human_response` |
| `pipeline/condition.go` | Support `==`, quote stripping, document limitations |
| `pipeline/condition_test.go` | New tests for `==`, quotes |
| `pipeline/handlers/codergen_test.go` or `pipeline/handlers/handler_test.go` | Per-node response tests |
| `examples/consensus_task.dip` | Parallel fan-out/fan-in structure |

## Non-Goals

- Full tokenizer/parser for conditions (future work)
- Parentheses support in conditions
- Structured blackboard pattern (overkill for now)
- Breaking change to `last_response` behavior
