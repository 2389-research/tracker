# P1 Adapter & Engine Safety Fixes — Design Spec

**Date:** 2026-04-03
**Issues:** #38, #33, #8, #28, #25
**Scope:** Five independent P1 bug fixes, mostly in `pipeline/dippin_adapter.go`

---

## Fix 1: Nil pointer guards in adapter (#38)

**Problem:** `FromDippinIR` iterates `workflow.Nodes` and `workflow.Edges` without nil-checking slice elements. If dippin-lang emits a nil node/edge, tracker panics. Similarly, pointer config cases in `extractNodeAttrs` (e.g. `*ir.AgentConfig`) dereference without nil checks.

**Fix:**

In `FromDippinIR` (line 53–60): add `if irNode == nil { continue }` before `convertNode(irNode)`.

In `FromDippinIR` (line 63–66): add `if irEdge == nil { continue }` before `convertEdge(irEdge)`.

In `extractNodeAttrs` pointer cases (lines 148, 153, 158, 163, 168, 173): add `if cfg == nil { return nil }` before dereferencing.

**Test:** Add `TestFromDippinIR_NilNodeSkipped` — workflow with a nil element in `Nodes` slice should not panic and should produce a valid graph from the remaining nodes.

---

## Fix 2: Error wrapping with %w (#33)

**Problem:** Errors at lines 31, 34, 37, 113, 177 use plain `fmt.Errorf` strings. Callers cannot use `errors.Is` or `errors.As` to match them.

**Fix:**

Add sentinel errors:

```go
var (
    ErrNilWorkflow     = errors.New("nil workflow")
    ErrMissingStart    = errors.New("workflow missing Start node")
    ErrMissingExit     = errors.New("workflow missing Exit node")
    ErrUnknownNodeKind = errors.New("unknown node kind")
    ErrUnknownConfig   = errors.New("unknown config type")
)
```

Update call sites:
- Line 31: `return nil, ErrNilWorkflow`
- Line 34: `return nil, ErrMissingStart`
- Line 37: `return nil, ErrMissingExit`
- Line 113: `return nil, fmt.Errorf("%s: %w", irNode.Kind, ErrUnknownNodeKind)`
- Line 177: `return nil, fmt.Errorf("%T: %w", config, ErrUnknownConfig)`

**Test:** Add `TestFromDippinIR_SentinelErrors` — verify `errors.Is(err, ErrNilWorkflow)` etc.

---

## Fix 3: Deterministic map iteration in extractSubgraphAttrs (#8)

**Problem:** `extractSubgraphAttrs` iterates `cfg.Params` (a `map[string]string`) with non-deterministic order. Output differs across runs, breaking reproducibility and causing noisy diffs.

**Fix:** Sort keys before iterating (lines 332–336):

```go
keys := slices.Sorted(maps.Keys(cfg.Params))
for _, k := range keys {
    pairs = append(pairs, fmt.Sprintf("%s=%s", k, cfg.Params[k]))
}
```

Also apply the same fix to `serializeStylesheet` (line 440–442) which has the same issue with `rule.Properties`.

**Test:** Add `TestExtractSubgraphAttrs_DeterministicOrder` — call twice with same multi-key params, assert identical output.

---

## Fix 4: Build constraint for POSIX syscalls (#28)

**Problem:** `agent/exec/local.go` uses `syscall.SysProcAttr{Setpgid: true}` and `syscall.Kill()` which are POSIX-only. No build constraint exists.

**Fix:** Add `//go:build !windows` to line 1 of `agent/exec/local.go`. This documents the platform requirement without pretending to support Windows.

**Test:** None needed — this is a build constraint annotation.

---

## Fix 5: Map Workflow.Version to graph attrs (#25)

**Problem:** `ir.Workflow.Version` exists in the dippin-lang IR but is never mapped to graph attributes. NodeIO (reads/writes) is already handled by `extractNodeIO`.

**Fix:** In `FromDippinIR`, after the goal mapping (line 47), add:

```go
if workflow.Version != "" {
    g.Attrs["version"] = workflow.Version
}
```

**Test:** Add version to existing adapter test fixtures and assert `g.Attrs["version"]` is set.

---

## Files Changed

| File | Changes |
|------|---------|
| `pipeline/dippin_adapter.go` | Nil guards, sentinel errors, sorted map iteration, version mapping |
| `pipeline/dippin_adapter_test.go` | New tests for nil nodes, sentinel errors, deterministic order, version |
| `agent/exec/local.go` | Build constraint header |

## Non-Goals

- No Windows implementation (just the build constraint)
- No new sentinel errors beyond the 5 listed (other errors in the codebase are out of scope)
- No changes to `extractAgentAttrs` Params iteration (it writes to a map, order doesn't matter)
