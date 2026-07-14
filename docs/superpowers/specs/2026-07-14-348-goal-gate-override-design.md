# #348 Defect 2 — Wire human "accept" overrides to the goal-gate exit check — Design

**Date:** 2026-07-14
**Status:** design approved (Approach A)
**Issue:** [#348](https://github.com/2389-research/tracker/issues/348) (Engine correctness milestone), Defect 2. Defect 1 shipped via #360/#361.
**Scope:** `pipeline/checkpoint.go`, `pipeline/engine.go`, `pipeline/engine_checkpoint.go`, plus tests. No dippin-lang changes (uses the existing `override: true` edge attribute already carried in `ir.Edge.Override` and validated by the adapter).

## Problem

A `goal_gate: true` node (e.g. `FinalSpecCheck`) that returns `outcome: fail` routes to an escalation (`fallback_target: EscalateReview`). A human who chooses **accept** there means "I have judged this acceptable — complete the run." Today that intent is lost:

- `recordOverrideIfPresent` (engine.go) fires on the `override: true` edge and records an `OverrideDetail` against the **escalation node**, appends to the sticky `validationOverrides` list, and (if the run reaches the success exit) `buildSuccessResult` flips the terminal status to `validation_overridden`.
- **But** `checkGoalGateNode` (engine_checkpoint.go) keys purely on `nodeOutcomes[gate]`. The gate is still `fail`, so the exit-time check marks it unsatisfied, re-enters via the recheck/retry machinery, and eventually **drains the retry budget and fails the run** — the exact #348 symptom (pipeline reports a terminal state with its final gate at `outcome: fail`).

The override subsystem and the goal-gate exit check are **not connected**. Every existing override test (`TestEngine_OverrideEdge_*`) uses a *plain* human gate that returns **success**; none exercises a **failed goal gate** whose escalation is overridden.

### Why the pending flag is the wrong link

`GateRecheckPending` is set only inside `handleExitNode` (when the run first reaches the exit with the gate unsatisfied) and cleared the instant the gate re-executes (`applyOutcome`). While the human is standing at the escalation taking the override edge, the flag is **not reliably set**. The durable signal is `nodeOutcomes[gate]` (the gate's last outcome, which persists as `fail`) plus the static graph relationship between the gate and its escalation node.

## Approach A — covering override (keyed on outcome + adjacency)

When an `override: true` edge is traversed from node `N`, resolve every goal gate that this escalation "covers" as **override-satisfied**, so the exit check treats it as done and the run completes `validation_overridden` with a durable audit record tying the override to the specific gate(s).

**A goal gate `G` is covered by an override edge from `N` iff all hold:**
1. `G` is a goal gate (`isGoalGate(G)`).
2. `G` is currently unsatisfied: `nodeOutcomes[G]` is neither `success` nor `partial_success`.
3. `G` routes to `N`: there is a direct edge `G → N`, **or** `G`'s `fallback_target` attribute equals `N`.

This is audit-sound (an override only resolves the gate whose escalation the human is actually on) and needs no new syntax. The common shape — `FinalSpecCheck --fail/fallback--> EscalateReview --accept(override:true)--> …` — satisfies all three directly.

**Out of scope (documented limitation):** a multi-hop escalation chain (`G → X → EscalationHuman`, override edge from `EscalationHuman`, no direct `G→EscalationHuman` edge and no matching `fallback_target`) is **not** covered. Workflows keep the escalation gate adjacent to the goal gate (the existing norm). A later enhancement could walk the fail-routing chain; not needed for #348.

## Components

### 1. `pipeline/checkpoint.go` — persisted overridden-gates set

Mirror the existing `GateRecheckPending` field and its three methods:

```go
// OverriddenGates records goal-gate node IDs whose last (failed) outcome a
// human resolved by traversing an override edge from the gate's escalation.
// An overridden gate is treated as satisfied by the exit check and is never
// re-entered; the run completes as validation_overridden. Survives resume.
OverriddenGates map[string]bool `json:"overridden_gates,omitempty"`

func (cp *Checkpoint) MarkGateOverridden(nodeID string) { /* lazy-init, set true */ }
func (cp *Checkpoint) IsGateOverridden(nodeID string) bool { return cp.OverriddenGates[nodeID] }
```

### 2. `OverrideDetail` — record the covered gate(s)

Add one field so the audit record ties the override to the gate it resolved (the "no durable record that a human overrode it" gap in the issue):

```go
// CoveredGates lists the goal-gate node IDs this override resolved as
// satisfied. Empty for a plain override edge that covers no failed goal gate.
CoveredGates []string `json:"covered_gates,omitempty"`
```

`EventValidationOverridden` carries the same `OverrideDetail`, so the covered gates surface in `activity.jsonl` and `tracker diagnose` with no extra event plumbing.

### 3. `pipeline/engine.go` — `recordOverrideIfPresent` marks covered gates

Extend the existing flip-point (engine.go:691). Before appending the detail, compute the covered gates via a new helper `e.coveredGoalGates(s, currentNodeID)` implementing the three-part rule above. For each covered gate `G`:
- `s.cp.MarkGateOverridden(G)`
- `s.cp.ClearGateRecheckPending(G)` (defensive — if a prior exit pass armed it)
- collect `G` into `detail.CoveredGates`

Then append, emit, and synchronously persist as today. When no gate is covered (a plain override), `CoveredGates` is empty and behavior is byte-for-byte unchanged — the existing `TestEngine_OverrideEdge_*` tests still pass.

### 4. `pipeline/engine_checkpoint.go` — `checkGoalGateNode` honors overrides

Add one short-circuit at the top of `checkGoalGateNode`, immediately after the `success`/`partial_success` early-return and **before** the `IsGateRecheckPending` / retry-budget / fallback branches:

```go
if cp.IsGateOverridden(nodeID) {
    return goalGateCheckResult{}, false // resolved by human override; not unsatisfied
}
```

An overridden gate is no longer an unsatisfied-gate problem: no retry, no fallback, no budget drain. The run proceeds to the success exit, and because `validationOverrides` is non-empty, `buildSuccessResult` already flips the terminal status to `validation_overridden` — no change needed there.

## Data flow (build_product case)

1. `FinalSpecCheck` (goal_gate) executes → `outcome: fail`; `nodeOutcomes[FinalSpecCheck] = "fail"`.
2. Fail routing → `EscalateReview` (human).
3. Human picks **accept**; the engine selects the `EscalateReview --accept--> Cleanup` edge with `Override: true`.
4. `recordOverrideIfPresent`: `coveredGoalGates` finds `FinalSpecCheck` (goal gate, unsatisfied, `fallback_target == EscalateReview`) → `MarkGateOverridden(FinalSpecCheck)`, `CoveredGates = ["FinalSpecCheck"]`; append detail; emit `EventValidationOverridden`; persist.
5. Run advances `Cleanup → FinalCommit → Done`.
6. `handleExitNode` → `checkGoalGateNode(FinalSpecCheck)`: `IsGateOverridden` → returns not-unsatisfied → no retry.
7. `buildSuccessResult`: `validationOverrides` non-empty → terminal status `validation_overridden`. `result.ValidationOverrides[0].CoveredGates == ["FinalSpecCheck"]`.

## Error handling / edge cases

- **Plain override (no failed goal gate):** `coveredGoalGates` returns empty → unchanged (regression-guarded).
- **Human picks a non-override edge (retry/reject):** no override recorded, no gate marked → gate still rechecks/fails as today (the fix does not weaken gates — regression-guarded).
- **Multiple failed goal gates both adjacent to `N`:** all are covered and recorded in `CoveredGates`. Intended: the human is accepting at their shared escalation.
- **Checkpoint resume:** `OverriddenGates` persists in checkpoint.json; a resumed run keeps the gate resolved.
- **`clearDownstream` after override:** overridden gates are keyed in a dedicated map, not `CompletedNodes`, so downstream clearing cannot lose the override state (same rationale as `GateRecheckPending` in #360).

## Testing

- **Unit (`checkpoint_test.go`):** `MarkGateOverridden`/`IsGateOverridden`; JSON round-trip of `overridden_gates`.
- **Engine integration (`engine_goal_gate_override_test.go`, new) — the #348 reproduction:** graph with a `goal_gate` node forced to `fail`, `fallback_target` to a `wait.human` escalation whose handler returns `PreferredLabel=accept, OverrideActor=human` on an `override: true` edge to the exit. Assert: `result.Status == OutcomeValidationOverridden`; the gate did **not** re-run (no second `EventStageStarted` for it, retry budget untouched); `result.ValidationOverrides[0].CoveredGates == [gate]`; `checkpoint.OverriddenGates[gate] == true`.
- **Negative:** same graph, human returns a non-override `retry` edge → gate rechecks and (budget exhausted) the run **fails**; no gate marked overridden.
- **Regression:** existing `TestEngine_OverrideEdge_*` (plain success gate) unchanged — `CoveredGates` empty, status still `validation_overridden` for the plain case, `success` for the no-override case.
- **Resume:** mark a gate overridden, checkpoint, resume → gate stays resolved, run completes `validation_overridden`.
- **Repo green:** `go build ./...`, `go test ./... -short`, `make complexity` (the new helper `coveredGoalGates` and the extended `recordOverrideIfPresent` must stay ≤ 8/8), `dippin doctor` on core pipelines A.

## Out of scope / follow-ups

- **Wiring `build_product_with_superspec`'s `EscalateReview` accept edge with `override: true`.** This makes the shipped workflow *use* the capability, but it is a **behavior change** to the approval flow (accept resolves the gate as overridden rather than re-running it) and belongs in a separate, author-signed-off change with `dippin doctor`/`simulate` verification. The engine plan ships the reusable mechanism; the example wiring is a follow-on decision.
- **dippin-lang lint** (warn when a goal gate's fallback can reach `Done` without a path back through the gate) — softened by the engine re-entry; file on dippin-lang as authoring guidance if desired.
- **#279** (rescope `overrideAlreadyRecorded` to checkpoint generation) — related override-dedup follow-up, independent.
