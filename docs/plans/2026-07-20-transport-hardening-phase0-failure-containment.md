# Phase 0 — Failure Containment & Terminal-Signal Guarantee

**Date:** 2026-07-20
**Status:** proposed
**Ship:** 1 (highest priority)
**Depends on:** nothing (this is the keystone)
**Related:** #475 (event-stream completeness), #476 (N concurrent runs safe)

## Problem

The transport boundary's whole contract rests on two properties that are **documented
but not enforced**:

1. A run **never** takes down its host — so `RunManager` can drive many runs at once.
2. Every run exit emits **exactly one** terminal event carrying `TerminalStatus` — so a
   stream-only subscriber (NDJSON consumer, Slack notifier) always learns the run
   finished.

Both are violated today. These are the defects that would actually crash or hang a
live concurrent Slack bot.

## Findings (CONFIRMED, re-derived in source)

### F0.1 — `RunManager.execute` has no panic recovery; teardown is non-deferred
`tracker_runmanager.go:191-214`. `execute` calls `Run(...)` then does `cancel()` /
`rm.release()` / `close(m.done)` **inline** with **no `recover()`**. There is no
`recover()` anywhere on the engine main goroutine (`pipeline/engine.go`, `engine_run.go`,
`tracker.go`) — only parallel branches, subprocess readers, and `manager_loop` recover.

Consequence: a handler panic on one run's goroutine propagates through `Engine.Run`
into this un-recovered goroutine and **terminates the entire host process**, taking down
every other concurrent run the manager owns — defeating the component's stated purpose.
Short of a crash, a `runtime.Goexit` leaves the semaphore slot acquired forever and
`Done()`/`Result()` callers blocked. Note the asymmetry: the CLI's `runPipelineAsync`
(`cmd/tracker/run.go`) *does* recover; the service-oriented `RunManager` does not.

### F0.2 — terminal-status backstop skips nil-result error exits
`pipeline/terminal_emit.go:25`. `emitTerminalBackstop` early-returns on
`result == nil || s.terminalEmitted`. Several terminal paths return `(nil, err)` **after**
`pipeline_started` already fired and therefore emit no terminal event:

- node-not-found — `engine.go:319`
- no-outgoing-edges / select-edge error — `engine.go:643,653`
- resume select-edge — `engine_run.go:511`

Reachable at runtime, not just theoretical:
- a malformed / unresolvable `when` condition errors out of `selectEdge` → `engine.go:653`
  nil-result return, after start.
- `retry_target` is routed with **no graph-existence check** (unlike `restart_target`,
  which checks) → a typo'd `retry_target` sends the loop to a nonexistent node →
  `engine.go:319` nil-result return.

`tracker.go` then does `if engineResult == nil { return nil, err }`, so no terminal event
reaches the stream. A stream-only subscriber sees `pipeline_started` then silence — a
Slack thread hangs forever waiting for "done." Existing tests cover only success,
handler-error, and strict-fail (all carry a non-nil result), missing this class.

## Tasks

### T0.1 — Engine-level panic recovery (the general fix)
Wrap the `Engine.Run` main loop body in `defer func(){ recover() ... }()` that, on
panic, synthesizes a `fail` `EngineResult` (stamped `s.runID`), emits the terminal
`fail` event via the existing `emitFailed` path, and returns `(result, err)` describing
the panic. This is the **scalable** fix: one guard means no transport — RunManager, TUI,
web — can be crashed by a handler panic. Keep the panic detail in the error + activity
log (never silently swallow — CLAUDE.md).
- Files: `pipeline/engine.go` (Run loop), reuse `pipeline/terminal_emit.go:emitFailed`.

### T0.2 — Deferred, recovered teardown in RunManager (belt-and-suspenders)
Convert `execute`'s tail to `defer`, with its own `recover()` that records `RunFailed`,
so `cancel()` / `rm.release()` / `close(m.done)` and the state transition **always** run
even on `Goexit` or a panic that somehow escapes T0.1.
- Files: `tracker_runmanager.go:191-214`.

### T0.3 — Terminal-status completeness for nil-result exits
In `emitTerminalBackstop`, when `result == nil && err != nil && !s.terminalEmitted`,
synthesize and emit a `fail` terminal event (it has `s.runID`). Make the documented
guarantee literally true for every exit path.
- Files: `pipeline/terminal_emit.go`.

### T0.4 — `retry_target` graph-existence guard
Mirror `restart_target`'s existence check (`engine_run.go:1101`) on the `retry_target`
route (`engine_run.go:858-861`) so a typo fails loudly at authoring/route time rather
than routing into a nil-result invariant error.
- Files: `pipeline/engine_run.go`.

### T0.5 — Invariant test: exactly one terminal event, for every exit class
New `pipeline/terminal_invariant_test.go` that drives each terminal exit class and
asserts `len(terminalEvents) == 1` with the expected `TerminalStatus`:
success, handler-error, strict-fail, budget-exceeded, **nil-result invariant error**
(node-not-found via bad `retry_target`), condition-error, and cancel. Also assert the
process survives an injected handler panic (T0.1) and yields a `fail` terminal event.

## Verification / gates

- `go build ./...`, `go test ./... -short` green.
- New invariant test passes; a deliberately-injected panic in a fake handler is caught,
  the process stays alive, and a single `fail` terminal event is observed.
- `make complexity` green (the recover wrapper may need a small helper to stay under the
  cyclo/size ceiling — extract if so).
- No behavior change to the success path (regression: existing terminal-status tests).

## Out of scope (handled elsewhere)

- Atomic checkpoint writes and precise resume → **Durable Recovery** doc.
- The subgraph/`manager_loop` child budget event double-emit at the *doc* level →
  Phase 2 (doc truth-up); the code-level filter option is noted there.
