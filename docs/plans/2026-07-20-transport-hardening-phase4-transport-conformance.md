# Phase 4 — Scalable to New Surfaces: Transport Conformance & Stream Stability

**Date:** 2026-07-20
**Status:** proposed
**Ship:** 4
**Depends on:** Phases 0–3 (the invariants a transport must satisfy exist and are enforced)
**Related:** #472 (UI-agnostic core epic), `docs/architecture/transport-boundary.md`

## Problem

The reason this session's bugs existed is that there was **no executable definition of "a
correct transport."** The boundary is documented in prose, but nothing forces a new
front-end (web, mobile, a second bot) to honor it — so each one can re-make Slack's
mistakes (uncontained panic, missed terminal signal, unvalidated source). This phase turns
the boundary contract into a **test any surface must pass** and stabilizes the streams so
new features don't break existing subscribers. This is the durable payoff — it makes
surfaces #4, #5, #6 cheap and safe.

## Goal

> A new transport is correct if it passes the conformance suite. Adding an event type or a
> gate mode is validated across every transport at once.

## Tasks

### T4.1 — Transport conformance harness (the keystone)
A reusable test kit that drives **any** `handlers.Interviewer` + event subscriber through
the full boundary contract against a fake in-memory workflow, asserting:
- **All four gate modes** — choice, yes/no, freeform, interview — round-trip and resolve.
- **Cancel / teardown** — `Engine.Close` → `Cancel()` unblocks a waiting gate; no
  goroutine leak (goroutine-count delta == 0).
- **Terminal-status received** — exactly one terminal event with a `TerminalStatus`
  reaches the subscriber, for success and failure (leans on Phase 0's invariant).
- **Cost attribution** — `cost_updated` carries a `NodeID`; snapshot at `pipeline_started`
  seeds a fresh subscriber.
- **Panic containment** — an injected handler panic yields a `fail` terminal event, not a
  crash (Phase 0).

Ship the TUI's `BubbleteaInterviewer` and trackerbot's `SlackInterviewer` (via fakes) both
run the suite, proving it's transport-neutral.

**Decision D-1:** does the harness live in the core module (transports import it as a
test-only dep) or a standalone `transport/conformance` package? Core-module keeps it close
to the invariants but adds a test-only surface; standalone keeps the core clean. Decide
before building.
- Files: new `transport/conformance/` (or `tracker_conformance_test_kit.go`), consumed by
  `tui/*_test.go` and `cmd/trackerbot/*_test.go`.

### T4.2 — Event-stream stability contract
- **Additive-only policy** for `PipelineEvent` / `agent.Event` / `StreamEvent` fields and
  types, documented in `transport-boundary.md`, so a subscriber compiled against an older
  shape keeps working.
- **Golden NDJSON snapshot test** — a canonical run serialized via `NewNDJSONWriter`,
  compared to a checked-in golden, so any wire-shape change is a conscious, reviewed diff.
- Files: `pipeline/events.go` doc, `tracker_events.go`, new `testdata/stream-golden.ndjson`.

### T4.3 — Boundary invariant checklist in the doc
Add to `transport-boundary.md` a "Building a new transport" gate listing the **enforced**
properties (one top-level terminal event; panic-contained; per-run isolation; source
containment; authz hook; bounded resources; durable resume) and pointing each at its
conformance test. Turn the prose contract into a checklist.

### T4.4 — RunManager admission policy (the deferred D-2)
Make queue-vs-reject-vs-preempt a pluggable strategy with backpressure metrics, so a
high-traffic surface can queue instead of dropping at capacity. Today `ErrAtCapacity` →
reject (mechanism, not policy). Emit an observable signal (depth, wait time) so operators
can size `WithMaxConcurrent`.
- Files: `tracker_runmanager.go` (+ options), `cmd/trackerbot/runner.go:handleAdmission`.

### T4.5 — Observability seam (forward-looking)
A structured run-lifecycle metrics hook (start/finish/duration/status/tokens/cost per run)
on `RunManager`, so every transport gets uniform operational telemetry without bespoke
wiring. Keep it an interface with a no-op default.
- Files: `tracker_runmanager.go`.

## Verification / gates

- `go test ./... -short` green; the conformance suite runs green for **both** shipped
  transports.
- The golden NDJSON test fails loudly on an unreviewed wire change.
- A deliberately-broken toy transport (drops the terminal event; panics in a handler)
  **fails** the conformance suite — proving the suite has teeth.
- `make complexity` green.

## Why this is the scalable investment

Phases 0–3 fix today's surfaces. Phase 4 makes the *next* surface safe by construction:
the web app, the mobile app, or a second bot inherits every invariant and must prove it
before shipping. The boundary stops being "trust the author read the doc" and becomes
"the compiler and the conformance suite won't let you violate it."
