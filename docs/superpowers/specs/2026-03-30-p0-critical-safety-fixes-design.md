# P0 Critical Safety Fixes — Design Spec

**Date:** 2026-03-30
**Branch:** `fix/p0-critical-safety-fixes`
**Issues:** #15, #16, #20

## Problem Statement

Three P0 bugs in the pipeline engine:

1. **#20 — Parallel branch context updates lost on fan-in.** Handlers that call `pctx.Set()` as a side effect (rather than returning `ContextUpdates`) have their writes silently discarded after parallel execution.
2. **#15 — Goal-gate retry has no termination bound.** An unsatisfied goal gate with a `retry_target` loops forever — no retry budget, no escalation, no circuit breaker.
3. **#16 — tool_command has no protection against tainted interpolation.** `ExpandVariables` resolves `${ctx.last_response}` (raw LLM output) directly into a `sh -c` command string. A second expansion system (`ExpandGraphVariables`) runs beforehand with `strings.ReplaceAll` and no safety checks at all.

Additionally, checkpoint writes are not atomic (crash mid-write corrupts resume state).

## Design Principles

Informed by adversarial expert panel review:

- **No runtime taint tracking.** Zero existing pipelines interpolate `${ctx.*}` in `tool_command`. A static check at the expansion layer is sufficient. Runtime taint sets add API surface, require plumbing through snapshots/checkpoints/parallel branches, and still have laundering bypasses (human gates, autopilot).
- **No shell escaping.** Shell escaping is fundamentally unsound — it breaks grep patterns, jq filters, JSON payloads. There is no single escaping strategy that works for all shell contexts.
- **No command allowlist.** Prefix matching is trivially bypassable (`"ls"` matches `"ls; rm -rf /"`). Multi-line shell scripts start with `set -eu`, making prefix lists useless.
- **No resource limits (ulimit).** Breaks real workflows (`go build` needs >512MB, `go test` spawns >64 procs). ulimit is per-shell-session and easily circumvented. Wrong threat model — the pipeline author is the trust boundary, not the runtime.
- **Lint + block at expansion time.** The right layer to catch this is where variables are resolved into commands — block it there, warn about it in the linter, document the safe alternative (file-mediated passing).

---

## Fix 1: Parallel Branch Context Loss (#20)

### Status: Implemented (uncommitted)

### What Changed

**`pipeline/context.go` — `DiffFrom(baseline)`**

New method that compares current context values against a baseline snapshot. Returns all keys that were added or changed. Thread-safe (holds `RLock`). Does not detect deletions (PipelineContext has no delete operation, so this is correct).

**`pipeline/handlers/parallel.go` — `runBranch()`**

After branch execution, calls `branchCtx.DiffFrom(snapshot)` to capture all side-effect writes the handler made via `pctx.Set()`. Merges with explicit `outcome.ContextUpdates` (explicit wins). This ensures the `ParallelResult` contains all context mutations, not just those the handler explicitly returned.

### Tests Required

1. **`context_test.go` — DiffFrom unit tests** (implemented):
   - New keys captured
   - Changed values captured
   - Unchanged values excluded
   - Empty baseline → all keys
   - Empty context → empty diff

2. **`parallel_test.go` — Side-effect capture** (implemented):
   - `TestParallelHandlerCapturesSideEffectWrites`: handler only calls `pctx.Set()`, no `ContextUpdates` → value appears in results
   - `TestParallelHandlerExplicitOverridesSideEffects`: both `pctx.Set()` and `ContextUpdates` for same key → explicit wins
   - `TestParallelHandlerContextIsolation`: branches don't see each other's writes

3. **Engine-level integration test** (new — to implement):
   - Build a minimal graph: `Start → Parallel → [BranchA, BranchB] → FanIn → ReadContext → Done`
   - BranchA handler writes `pctx.Set("branch_a_key", "from_a")` as a side effect
   - BranchB handler returns `ContextUpdates{"branch_b_key": "from_b"}`
   - ReadContext handler asserts both keys are present in `pctx`
   - Assert `EngineResult.Context` contains both keys

### Not Doing

- **Parallel results namespacing** (`parallel.results.<nodeID>`). No existing pipeline has concurrent parallel fan-outs. Sequential fan-outs work correctly because each fan-in reads before the next parallel writes. Defer until needed.
- **Replace 5ms sleep** in `TestParallelHandlerContextIsolation`. The sleep is for race detector hinting, not correctness. Low priority.

---

## Fix 2: Goal-Gate Retry Termination (#15)

### Status: Partially implemented (uncommitted). Needs escalation loop fix.

### What Changed

**`pipeline/engine_checkpoint.go` — `goalGateRetryTarget()`**

Now checks `cp.RetryCount(gateNodeID) >= e.maxRetries(node)` before allowing retry. Returns a 4-tuple `(target, gateNodeID, shouldRetry, unsatisfied)` so the caller can track which gate triggered the retry.

When retries are exhausted, walks a separate fallback attribute list (`fallback_target`, `fallback_retry_target`) at node and graph level. If a fallback exists, routes there (escalation). If not, signals unsatisfied-without-retry → pipeline fails.

**`pipeline/engine_run.go` — `handleExitNode()`**

Calls `cp.IncrementRetry(gateNodeID)` when retrying. Emits `EventStageRetrying` with attempt counts. Emits `EventStageFailed` with exhaustion message when retries are exhausted and no fallback exists.

### Bug to Fix: Escalation Loop

**Problem:** When retries exhaust and the fallback path is taken, `goalGateRetryTarget` returns `retry=true`. This causes `handleExitNode` to call `IncrementRetry` and `clearDownstream` for the escalation target. If the escalation node also fails (or doesn't satisfy the goal gate), the pipeline bounces between exit and escalation forever — the retry counter goes past max but the `>=` check routes to fallback every time.

**Fix:** Track whether we've already taken the fallback path for a given gate. Add a `FallbackTaken map[string]bool` field to `Checkpoint`. When `goalGateRetryTarget` finds retries exhausted:
- If `cp.FallbackTaken[gateNodeID]` is true → return `(_, _, false, true)` (no retry, unsatisfied → pipeline fails)
- If false → set `cp.FallbackTaken[gateNodeID] = true`, return the fallback target

This guarantees at most one escalation attempt per gate. Serialized in checkpoint JSON for crash safety.

### Global Circuit Breaker

Add a hard iteration counter to the main engine loop (`engine.go:127`). Increment on every iteration. If it exceeds `maxIterations` (hardcoded 10,000 — not configurable via `.dip` attrs to prevent override by pipeline authors), emit a distinct `EventCircuitBreaker` event, save checkpoint, and return a failure result.

The event message includes the iteration count and last 10 visited nodes for diagnostics. `tracker diagnose` recognizes this event type.

This is 5 lines in the engine loop — belt-and-suspenders for any unbounded loop we haven't found yet.

```go
// In engine.go main loop
iterations++
if iterations > maxEngineIterations {
    e.emit(PipelineEvent{Type: EventCircuitBreaker, ...})
    return e.circuitBreakerResult(s, iterations)
}
```

### Tests Required

1. **`engine_goal_gate_test.go`** (partially implemented):
   - `TestGoalGateRetryTerminatesAtDefaultMax` ✓
   - `TestGoalGateRetryRespectsNodeMaxRetries` ✓
   - `TestGoalGateRetryFallsBackToFallbackTarget` — fix to assert final `EngineResult.Status`
   - `TestGoalGateFallbackTargetAttributeRecognized` ✓
   - **New: `TestGoalGateEscalationAlsoFailsTerminates`** — escalation node fails → pipeline terminates with failure, does not loop
   - **New: `TestGoalGateEscalationSurvivesCheckpointRestore`** — `FallbackTaken` persists through checkpoint round-trip

2. **Engine circuit breaker test** (new):
   - Build a graph that loops unconditionally (no restart detection, no goal gates — just a cycle the engine follows)
   - Assert engine returns failure after hitting iteration cap
   - Assert `EventCircuitBreaker` was emitted
   - Distinct from `TestEngineRestartMaxRestartsExceeded` which tests the *restart* counter (loop-back to completed node), not the *iteration* counter

---

## Fix 3: Tool Command Tainted Interpolation (#16)

### Status: Not started.

### Threat Model

The threat is **accidental footguns by pipeline authors**, not malicious pipeline authors. The `.dip` file is the trust boundary — whoever writes it has full shell access via `tool_command`. The danger is a well-meaning author writing `tool_command = "process ${ctx.last_response}"` without realizing the LLM's raw output is spliced unsanitized into a shell command.

### Two Expansion Systems Problem

There are two independent variable expansion paths, both running on `tool_command`:

1. **`ExpandGraphVariables()`** in `prepareExecNode()` — bare `$key` syntax, `strings.ReplaceAll`, runs on ALL node attributes BEFORE the handler sees the node. Sources: `graph.*` context keys.
2. **`ExpandVariables()`** in `ToolHandler.Execute()` — `${ctx.key}` syntax, single-pass parser, runs inside the handler. Sources: `ctx.*`, `params.*`, `graph.*`.

Both must be gated for tool_command safety.

### Design

**A. Block tainted keys in `ExpandVariables` for tool_command context**

Add an `opts` parameter to `ExpandVariables` (or a wrapper function `ExpandToolCommand`) that rejects expansion of keys from a hardcoded tainted-key set when expanding in a tool_command context:

```go
var taintedContextKeys = map[string]bool{
    "last_response":   true,
    "human_response":  true,
    "tool_stdout":     true,
    "tool_stderr":     true,
}
```

When `ExpandVariables` encounters `${ctx.<tainted_key>}` in tool_command mode:
- Return an error: `"tool_command at node %q references unsafe context key ${ctx.%s} which may contain LLM output. Write the value to a file instead: pctx.Set() → $ARTIFACT_DIR/input.txt → read in shell script. To override, add 'allow_unsafe_interpolation: <key1>,<key2>' to this node."`
- The error names the key, explains why, and shows the fix.

The `allow_unsafe_interpolation` attribute is a per-variable allowlist (comma-separated key names), matching the granularity of existing `reads`/`writes` attributes. A blanket `allow_unsafe_interpolation: all` is also accepted.

**B. Block tainted graph variables in `ExpandGraphVariables` for tool_command**

`prepareExecNode` already knows which attribute it's expanding (it iterates `execNode.Attrs` by key). For the `tool_command` key specifically, skip `ExpandGraphVariables` entirely — or apply the same tainted-key check to graph variables whose source key is in the tainted set.

Since `GraphVarMap` sources from `graph.*` context keys, and LLM output lands in `last_response`/`tool_stdout` (not `graph.*`), the risk is lower here. But an LLM node *could* write `pctx.Set("graph.foo", malicious)`. The fix: `GraphVarMap` should skip keys whose underlying context value matches a `graph.<tainted_key>` pattern. More practically: when building the var map for tool_command expansion, exclude vars whose source context key is in the tainted set.

Implementation: `prepareExecNode` builds a `taintedGraphVars` set from `GraphVarMap` entries whose source context key (`graph.<tainted_key>`) matches the tainted set. For the `tool_command` attribute only, these vars are excluded from expansion. Other attributes (e.g., `prompt`) expand normally. This is a ~10-line change in `prepareExecNode`.

**C. Lint rule DIP130: tainted interpolation in tool_command**

Static analysis in `LintDippinRules`:
- For each node with `tool_command` attribute, scan for `${ctx.last_response}`, `${ctx.human_response}`, `${ctx.tool_stdout}`, `${ctx.tool_stderr}`, and bare `$last_response`, `$human_response`, `$tool_stdout`, `$tool_stderr`.
- Emit warning: `"DIP130: node %q tool_command references %s which may contain LLM output. Consider using file-mediated passing instead."`
- No warning if node has `allow_unsafe_interpolation` attribute covering that key.

This catches the issue at `dippin doctor` time, before the pipeline runs.

**D. Document the safe alternative**

Add to CLAUDE.md under "Tool node safety":
```bash
### Safe pattern for LLM output in tool commands
Do NOT interpolate ${ctx.last_response} in tool_command. Instead:
1. Have the agent node write structured output to a file in $ARTIFACT_DIR
2. Read the file in the tool command: `cat "$ARTIFACT_DIR/agent_output.json" | jq ...`
This avoids shell injection and gives the tool script control over parsing.
```

### Tests Required

1. **Regression test FIRST** (prove the vulnerability exists before fixing):
   - Set `pctx.Set("last_response", "$(echo INJECTED)")`
   - Create a tool node with `tool_command: "echo ${ctx.last_response}"`
   - Execute through `ToolHandler` with a mock `ExecCommand` that captures the command string
   - Assert the command contains `$(echo INJECTED)` unsanitized (this test passes today, fails after fix)

2. **Block test** (after fix):
   - Same setup → `ExpandVariables` returns error mentioning `last_response`
   - Assert the tool handler does not call `ExecCommand`

3. **Allow override test**:
   - Same setup but node has `allow_unsafe_interpolation: last_response`
   - Expansion succeeds, command executes (pipeline author opted in)

4. **ExpandGraphVariables test**:
   - Set `pctx.Set("graph.payload", "$(evil)")`
   - Tool node with `tool_command: "echo $payload"`
   - Assert `prepareExecNode` blocks the expansion for tool_command
   - Assert it still works for `prompt` attribute (only tool_command is gated)

5. **Lint rule test**:
   - Graph with tool node containing `${ctx.last_response}` in tool_command → DIP130 warning
   - Graph with tool node containing `${ctx.last_response}` in prompt → no DIP130 warning
   - Graph with `allow_unsafe_interpolation: last_response` → no DIP130 warning

---

## Fix 4: Atomic Checkpoint Writes

### Status: Not started.

### What Changed

`SaveCheckpoint` currently calls `os.WriteFile(path, data, 0o600)` directly. A crash or OOM mid-write produces a truncated JSON file that `LoadCheckpoint` cannot parse, losing all resume state.

### Fix

Write-to-temp-then-rename (atomic on POSIX filesystems):

```go
func SaveCheckpoint(cp *Checkpoint, path string) error {
    dir := filepath.Dir(path)
    if err := os.MkdirAll(dir, 0o700); err != nil {
        return fmt.Errorf("create checkpoint directory: %w", err)
    }
    data, err := json.MarshalIndent(cp, "", "  ")
    if err != nil {
        return fmt.Errorf("marshal checkpoint: %w", err)
    }
    tmp := path + ".tmp"
    if err := os.WriteFile(tmp, data, 0o600); err != nil {
        return fmt.Errorf("write checkpoint temp: %w", err)
    }
    if err := os.Rename(tmp, path); err != nil {
        return fmt.Errorf("rename checkpoint: %w", err)
    }
    return nil
}
```

### Tests Required

1. **Atomic write test**: write checkpoint, verify file exists and is valid JSON
2. **No temp file left behind**: after successful save, `path + ".tmp"` does not exist
3. **Existing checkpoint_test.go** covers round-trip; add a test that simulates interrupted write (write a truncated file, verify `LoadCheckpoint` fails gracefully, then save again and verify success)

---

## Summary: What We're Building

| Item | Complexity | Status |
|------|-----------|--------|
| DiffFrom + parallel side-effect capture (#20) | Done | Uncommitted, needs integration test |
| Goal-gate retry budget + fallback (#15) | Done | Uncommitted, needs escalation loop fix |
| Escalation loop termination (`FallbackTaken`) | Small | New |
| Global circuit breaker (10k iterations) | Trivial | New |
| Block tainted keys in `ExpandVariables` for tool_command (#16) | Medium | New |
| Block tainted graph vars in `prepareExecNode` for tool_command | Small | New |
| Lint rule DIP130 | Small | New |
| Atomic checkpoint writes | Small | New |
| `allow_unsafe_interpolation` per-variable override | Small | New |
| Documentation update (CLAUDE.md safe pattern) | Trivial | New |

### What We're NOT Building

- Runtime taint tracking (`TaintedKeys` set on PipelineContext)
- Shell escaping for tainted values
- Command allowlist (prefix matching)
- Resource limits (ulimit)
- Parallel results namespacing
- DIP131, DIP132 lint rules
- Separate `safety_test.go` file

### Test Strategy

- Regression test for #16 written BEFORE the fix (TDD — prove vulnerability exists)
- Tests co-located with code they test (existing `*_test.go` files, not a separate safety suite)
- Engine-level integration test for parallel context propagation
- Escalation-also-fails termination test for goal gates
- Circuit breaker test distinct from restart test
- Lint rule positive and negative tests
