# Parallel Concurrency & Human Gate Timeout — Design Spec

**Date:** 2026-04-03
**Issues:** #27, #30
**Scope:** Parallel concurrency limit + branch timeout, human gate timeout with default fallback

---

## Fix 1: Parallel concurrency limits and per-branch timeout (#27)

**Problem:** The parallel handler launches all branches simultaneously with no concurrency limit. 20 branches fire 20 concurrent API calls, likely hitting rate limits. One slow branch blocks fan-in indefinitely.

### max_concurrency

Add a semaphore (buffered channel of size N) in the parallel handler's dispatch loop. Before launching each branch goroutine, acquire from the semaphore. Release in defer inside `runBranch`.

- Attr: `node.Attrs["max_concurrency"]` — parsed as int
- Default: 0 (unlimited — no semaphore, current behavior)
- Implementation: `sem := make(chan struct{}, maxConcurrency)` before the dispatch loop. Each goroutine does `sem <- struct{}{}` before work and `<-sem` in defer.

### branch_timeout

Wrap the branch context with `context.WithTimeout` before calling `registry.Execute` inside `runBranch`.

- Attr: `node.Attrs["branch_timeout"]` — parsed as `time.Duration`
- Default: no timeout (inherits parent context)
- Implementation: In `runBranch`, if branch_timeout is set, `ctx, cancel := context.WithTimeout(ctx, timeout)` with defer cancel.

### Non-goals

- Configurable aggregation strategies (any/all/majority/quorum) — future enhancement
- Multi-node branch sub-pipelines — use subgraph composition instead

---

## Fix 2: Human gate timeout with default fallback (#30)

**Problem:** Human gate nodes block indefinitely. No timeout, no fallback.

### timeout

Wrap blocking interviewer calls in a goroutine+select pattern. The interviewer interface is NOT changed — the wrapper runs the call in a goroutine and selects between the result channel and `time.After`.

- Attr: `node.Attrs["timeout"]` — parsed as `time.Duration`
- Default: no timeout (blocks forever, current behavior)
- Applies to: freeform, choice, and interview modes

### timeout_action

What happens when timeout fires:

- `"default"` (default) — use `node.Attrs["default_choice"]` as the answer. If no default_choice, fail.
- `"fail"` — return `OutcomeFail`

Attr: `node.Attrs["timeout_action"]`

### Implementation pattern

```go
type timedResult struct {
    response string
    err      error
}

func withTimeout[T any](timeout time.Duration, fn func() (T, error)) (T, error) {
    ch := make(chan struct{ val T; err error }, 1)
    go func() {
        v, e := fn()
        ch <- struct{ val T; err error }{v, e}
    }()
    select {
    case r := <-ch:
        return r.val, r.err
    case <-time.After(timeout):
        var zero T
        return zero, errHumanTimeout
    }
}
```

Add a sentinel error `errHumanTimeout` in the human handler. The Execute method checks for this error and applies timeout_action logic.

### Non-goals

- Input validation (url/number/regex) — separate concern, future PR
- Undo/back mechanism — TUI concern, not handler-level
- Changing the Interviewer interface signature
