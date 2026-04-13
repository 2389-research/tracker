# Test Coverage, Retry Jitter, DOT Deprecation Docs — Design Spec

**Date:** 2026-04-03
**Issues:** #11, #29, #12 (also closes #37 as already-fixed)
**Scope:** Test coverage gaps, retry backoff jitter, DOT deprecation documentation

---

## Fix 1: Strengthen .dip test coverage (#11)

**Problem:** Several test gaps identified during review. Many were fixed in prior PRs (sentinel errors, nil configs, DippinValidated). Remaining gaps:

### 1a: Edge topology in e2e test

`dippin_adapter_e2e_test.go` only checks node count/shape. Add assertions for edge `From`/`To` pairs, `Weight`, `Restart` attrs, and `Condition` strings.

### 1b: Zero-value config fields absent from attrs

When a config field is its zero value (false bool, 0 int, empty string), it should NOT appear in attrs. Add a test that creates a workflow with all-zero-value AgentConfig and asserts no spurious keys.

### 1c: Subgraph params exact match

The existing subgraph params test uses `strings.Contains`. Now that we have deterministic ordering (PR #49), change to exact string match.

### 1d: .dip fixtures in validate/simulate tests

Add at least one `.dip` file test case to `validate_test.go` and `simulate_test.go` (currently DOT-only).

---

## Fix 2: Retry backoff jitter (#29)

**Problem:** `ExponentialBackoff` and `LinearBackoff` in `pipeline/retry_policy.go` produce deterministic delays. Multiple pipelines retrying simultaneously cause thundering herd.

**Fix:** Add ±25% jitter to the computed delay:

```go
jitter := 0.75 + rand.Float64()*0.5 // [0.75, 1.25)
return time.Duration(float64(delay) * jitter)
```

Use `math/rand/v2` (no seed needed — auto-seeded in Go 1.20+).

**Test:** `TestExponentialBackoffJitter` — call 100 times with same inputs, verify not all results are identical and all are within ±25% of the base value.

---

## Fix 3: DOT deprecation docs (#12)

### 3a: Deprecated annotation on ParseDOT

Add `// Deprecated: Use .dip format with FromDippinIR instead. DOT support will be removed in v1.0.` before `ParseDOT` in `pipeline/parser.go`.

### 3b: Package doc

Create `pipeline/doc.go` with package overview documenting dual-format support and the deprecation path.

### 3c: CHANGELOG entry

Add a "Deprecated" entry to CHANGELOG.md noting the DOT sunset timeline.

### Non-goals

- Migration tooling (`dot2dip` / `tracker migrate`) — out of scope
- Formal .dip grammar/EBNF — lives in dippin-lang repo, not tracker
