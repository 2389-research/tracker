# Phase 2 — Contract Hardening: Close the API Foot-Guns

**Date:** 2026-07-20
**Status:** proposed
**Ship:** 1 (typed-nil) / 2 (token guard, docs)
**Depends on:** nothing
**Related:** #478 (full unification), transport-boundary API/docs review

## Problem

The unified library surface is coherent and backward-compatible, but three sharp edges
put the correctness contract entirely on the caller. Each should be a code guard, not a
prose warning.

## Findings

### F2.1 — Typed-nil `*llm.Client` stored into the `Config.LLMClient` interface (CONFIRMED, MEDIUM)
`cmd/tracker/run.go:617`. `runTUI` assigns `llmClient` (a `*llm.Client`) unconditionally
into `cfg.LLMClient`, which is an `agent.Completer` **interface**. On the headless-backend
+ no-API-keys path, `resolveLLMClient` returns a nil `*llm.Client` (the author even guards
`if llmClient != nil` for `Close()` at `:580`). Assigning a nil pointer to the interface
yields a non-nil interface wrapping a nil pointer — the classic Go typed-nil trap.

`resolveCompleter` (`tracker.go:380`) then sees `cfg.LLMClient != nil` as **true**,
returns the typed-nil, and **skips the env-build fallback**. Inert for pure claude-code
runs (the completer is never dialed), but a per-node `backend: native` override under a
global `--backend claude-code` with no keys hands the typed-nil to the native backend and
nil-derefs on `Complete`. Narrow trigger, real trap.

### F2.2 — `TokenTracker` + `LLMClient` double-count foot-gun (CONFIRMED, MEDIUM)
`tracker.go:362-376`. `attachClientObservers` unconditionally calls
`c.AddMiddleware(tokenTracker)` on whichever `*llm.Client` backs the run. A caller who
supplies a `Config.LLMClient` that **already** has their tracker attached, and passes that
same tracker as `Config.TokenTracker`, double-counts usage/cost. The only guard is a prose
warning in the `TokenTracker` doc comment. The CLI sidesteps it correctly (builds the
client bare) — which is evidence the contract is subtle enough to deserve a code guard.

### F2.3 — Terminal-status guarantee overstated in docs (CONFIRMED, doc)
`transport-boundary.md:102-104`, the `events.go` `TerminalStatus` doc, and the CHANGELOG
claim the engine "guarantees exactly one terminal event carrying `TerminalStatus` on every
exit." Two ways this is currently false:
- nil-result invariant errors emit **none** (fixed by Phase 0 T0.3 — after that the claim
  holds per top-level engine);
- a subgraph / `manager_loop` child that trips the **shared** budget guard emits its own
  `budget_exceeded` (carrying `TerminalStatus`) through `NodeScopedPipelineHandler` (which
  filters `EventPipelineStarted/Completed/Failed` but **not** `EventBudgetExceeded`), so a
  top-level subscriber can see two terminal-status events.

### F2.4 — Minor API doc gaps (CONFIRMED, doc)
- `Config.LLMTrace` doesn't note it only attaches to an `*llm.Client` transport (auto-made
  or a `*llm.Client` passed as `LLMClient`); a custom `agent.Completer` silently gets no
  trace — mirror the note that `TokenTracker` already carries.
- `NewLLMClient(cfg Config)` reads only `Provider`/`GatewayURL`/`GatewayKind`; a one-line
  doc note prevents callers expecting `Model`/`LLMClient` to matter.

## Tasks

### T2.1 — Guard the typed-nil assignment
`if llmClient != nil { cfg.LLMClient = llmClient }` in `runTUI` (and audit the non-TUI
`run()` path for the same pattern), so a nil client leaves the field a true-nil interface
and `resolveCompleter` keeps its intended fallback semantics.
- Files: `cmd/tracker/run.go`.

### T2.2 — Middleware-identity guard in `attachClientObservers`
Before `AddMiddleware(tokenTracker)`, skip if that exact tracker instance is already
present on the client's middleware chain (identity check). Requires a way to introspect
or a `HasMiddleware`/idempotent-add on `*llm.Client`.
- Files: `tracker.go` (`attachClientObservers`), `llm/client.go` (introspection helper).

### T2.3 — Doc truth-up for the terminal-status guarantee
After Phase 0, reword to: the **top-level** engine emits exactly one terminal event for
its own run; a subgraph/`manager_loop` child that trips the shared budget guard emits its
own `budget_exceeded` through the scoped handler, so a subscriber should treat the terminal
event whose `NodeID` is **unscoped** (no `/`) as the run-level signal. Update
`transport-boundary.md`, `events.go` doc, CHANGELOG.
- **Optional code alternative:** filter/re-map `EventBudgetExceeded` in
  `NodeScopedPipelineHandler` so the invariant is literally true — deferred, since budget
  events are intentionally surfaced from children today; decide with the team.

### T2.4 — `LLMTrace` / `NewLLMClient` doc notes (F2.4)
Add the two one-line clarifications.
- Files: `tracker.go` doc comments.

## Verification / gates

- `go build ./...`, `go test ./... -short` green.
- New tests (also see Phase 3): a native-override-under-headless-no-keys config does not
  nil-deref (T2.1); passing a client that already has the tracker + the same
  `Config.TokenTracker` counts usage once (T2.2).
- Docs reviewed against code by re-grepping the claimed symbols.

## Out of scope

- The `handlers.Interviewer` / `ToolSafety` package leak into public `Config` — reviewed
  and accepted as pragmatic (same package callers already touch via `WebhookGate`); no
  change.
- Pointer-vs-value inconsistency across optional Config fields — each is individually
  defensible by its zero-value semantics; not worth churning.
