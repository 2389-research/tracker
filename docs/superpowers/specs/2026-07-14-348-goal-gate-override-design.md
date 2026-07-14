# #348 Defect 2 — Wire human "accept" overrides to the goal-gate exit check — Design

**Date:** 2026-07-14
**Status:** design approved (Approach A) — **revised after 5-expert squad review** (engine control-flow, checkpoint/resume, audit-integrity, simplicity, test-coverage)
**Issue:** [#348](https://github.com/2389-research/tracker/issues/348) (Engine correctness milestone), Defect 2. Defect 1 shipped via #360/#361.
**Scope:** `pipeline/checkpoint.go`, `pipeline/engine.go`, `pipeline/engine_run.go`, `pipeline/engine_checkpoint.go`, plus tests. No dippin-lang changes (uses the existing `override: true` edge attribute, carried in `ir.Edge.Override` and validated by the adapter to `wait.human`-source edges only).

## Problem

A `goal_gate: true` node (e.g. `FinalSpecCheck`) that returns `outcome: fail` routes to an escalation (`fallback_target: EscalateReview`). A human who chooses **accept** there means "I have judged this acceptable — complete the run." Today that intent is lost:

- `recordOverrideIfPresent` (engine.go:691) fires on the `override: true` edge and records an `OverrideDetail` against the **escalation node**, appends to the sticky `validationOverrides` list, and (if the run reaches the success exit) `buildSuccessResult` flips the terminal status to `validation_overridden`.
- **But** `checkGoalGateNode` (engine_checkpoint.go:200) keys purely on `nodeOutcomes[gate]`. The gate is still `fail`, so the exit-time check marks it unsatisfied, re-enters via the recheck/retry machinery, and eventually **drains the retry budget and fails the run** — the exact #348 symptom.

The override subsystem and the goal-gate exit check are **not connected**. Every existing override test (`TestEngine_OverrideEdge_*`) uses a *plain* human gate that returns **success**; none exercises a **failed goal gate** whose escalation is overridden.

### Why the pending flag is the wrong link

`GateRecheckPending` is set only inside `handleExitNode` (engine_run.go:1041/1071) and cleared the instant the gate re-executes (`applyOutcome`, engine_run.go:766). While the human is at the escalation taking the override edge on the **first** pass, the flag is not set. The durable signal is `nodeOutcomes[gate]` (persists as `fail`) plus the static graph relationship between the gate and its escalation node.

## Approach A — covering override (keyed on outcome + adjacency + human actor)

When an `override: true` edge is traversed from node `N` **by a human**, resolve every goal gate this escalation "covers" as override-satisfied, so the exit check treats it as done and the run completes `validation_overridden` with a durable audit record.

**A goal gate `G` is covered by an override edge from `N` iff ALL hold:**
1. `G` is a goal gate (`isGoalGate(G)`).
2. **`G` actually executed and failed: `nodeOutcomes[G] == string(OutcomeFail)`.** *(Squad C1 — "not success" wrongly matches a never-run gate, e.g. `FinalSpecCheck` before it runs when the human accepts an earlier `FinalBuild` failure at the shared `EscalateReview`. Requiring the explicit `fail` outcome scopes coverage to the gate the human is actually on.)*
3. `G` routes to `N`: a direct edge `G → N` exists (`graph.OutgoingEdges(G)` has `To == N`), **or** `findFallbackTarget(G) == N`. *(Squad I2 — `findFallbackTarget` (engine_checkpoint.go:251) resolves all four fallback sources — node/graph `fallback_target` and `fallback_retry_target` — so a gate reaching `N` via a graph-level `on_failure` default is also covered. Do NOT add a fresh raw `node.Attrs["fallback_target"]` read; there is no typed accessor and the neighboring goal-gate code already reads it via `findFallbackTarget`.)*

**And the override actor must be human:** the covering step runs only when `s.lastOutcome.OverrideActor == ActorHuman`. *(Squad I1 — without this, `--auto-approve` / `--autopilot lax` / webhook could deterministically accept and auto-satisfy a **failed** quality gate with no human judgment. A non-human actor still records a plain `OverrideDetail` as today, but does **not** resolve a failed goal gate; autopilot's existing, safe behavior — an unsatisfied goal gate fails the run — is unchanged. This is faithful to the issue's framing ("a human who chooses accept") and adds no new flag.)*

This is audit-sound (an override only resolves the specific failed gate whose escalation a human is on) and needs no new syntax.

**Out of scope (documented limitation):** a multi-hop escalation chain (`G → X → EscalationHuman`, override edge from `EscalationHuman`, no direct `G→EscalationHuman` edge and no matching fallback) is **not** covered — the gate re-enters and the run fails (fail-*safe*, the original symptom, no false success). Workflows keep the escalation adjacent to the goal gate (the existing norm). `graph.IncomingEdges(N)` cannot replace the node scan because `fallback_target` synthesizes no graph edge, so a reverse index is impossible without a scan; the scan runs only on override-edge traversal (rare) over graphs of dozens of nodes.

## Components

### 1. `pipeline/checkpoint.go` — persisted overridden-gates set

Mirror the existing `GateRecheckPending` field and methods — including a `Clear` (see §"stickiness" below):

```go
// OverriddenGates records goal-gate node IDs whose last (failed) outcome a
// human resolved by traversing an override edge from the gate's escalation.
// An overridden gate is treated as satisfied by the exit check and is not
// re-entered; the run completes validation_overridden. Cleared when the gate
// re-executes so a fresh failure on new work re-prompts the human. Persisted.
OverriddenGates map[string]bool `json:"overridden_gates,omitempty"`

func (cp *Checkpoint) MarkGateOverridden(nodeID string)  { /* lazy-init, set true */ }
func (cp *Checkpoint) ClearGateOverridden(nodeID string) { delete(cp.OverriddenGates, nodeID) }
func (cp *Checkpoint) IsGateOverridden(nodeID string) bool { return cp.OverriddenGates[nodeID] }
```

`IsGateOverridden` on a nil map returns `false` (safe, matches `IsGateRecheckPending`); `MarkGateOverridden` lazy-inits exactly like `SetGateRecheckPending`.

### 2. `OverrideDetail` (`pipeline/override.go`) — record the covered gate(s)

```go
// CoveredGates lists the goal-gate node IDs this override resolved as
// satisfied. Empty for a plain override edge that covers no failed goal gate.
CoveredGates []string `json:"covered_gates,omitempty"`
```

`omitempty` keeps pre-existing checkpoints byte-identical. `OverrideDetail` already rides `EventValidationOverridden` and persists in `cp.ValidationOverrides` and `EngineResult.ValidationOverrides`, so the covered gates surface in `activity.jsonl` / `tracker diagnose` with no extra plumbing. *(Squad noted this is the one audit-only addition beyond the strict functional fix; kept because the issue names "no durable record that a human overrode it" as a gap, and `OverriddenGates` alone — a flat global set — cannot tie a gate to the specific override event.)*

### 3. `pipeline/engine.go` — `recordOverrideIfPresent` marks covered gates (before dedup)

Extend the flip-point (engine.go:691). **Compute and mark the covered gates BEFORE the `overrideAlreadyRecorded` early-return** *(Squad I4 — the `(escalation, label)` dedup can otherwise skip covering on a legitimate re-accept; `MarkGateOverridden` is idempotent, so moving it above the dedup is safe. Only the `OverrideDetail` append stays deduped.)*:

1. Guard: only cover when `s.lastOutcome.OverrideActor == ActorHuman` (§I1).
2. `covered := e.coveredGoalGates(s, currentNodeID)` — the set satisfying rules 1–3.
3. For each `G` in `covered`: `s.cp.MarkGateOverridden(G)`, `s.cp.ClearGateRecheckPending(G)` (defensive), collect into `detail.CoveredGates`.
4. Then the existing dedup early-return, `appendOverride`, `emit`, and synchronous `saveCheckpointWithTag` (the same single JSON write persists `OverriddenGates`, the override, and the escalation node's completion — no lost-override window).

`coveredGoalGates` and its adjacency predicate must stay under the 8/8 complexity gate: extract a `gateRoutesTo(node, escalationID) bool` helper (wraps `findFallbackTarget` + one `OutgoingEdges` loop) so `coveredGoalGates` stays ~6 cyclo and the predicate ~4. *(Squad I5 — `checkGoalGateNode` is already at exactly 7/7 and the short-circuit takes it to 8/8; the covering logic must live in new helpers, not inline.)*

### 4. `pipeline/engine_run.go` — clear override when the gate re-executes

In `applyOutcome`, alongside the existing goal-gate `ClearGateRecheckPending` (engine_run.go:765-767), also clear the override:

```go
if isGoalGate(e.nodeOrDefault(currentNodeID)) {
    s.cp.ClearGateRecheckPending(currentNodeID)
    s.cp.ClearGateOverridden(currentNodeID) // a re-executed gate re-judges fresh work; a stale human override must not auto-pass it
}
```

*(Squad I3 — otherwise the override is sticky forever: a looping workflow that re-runs the gate on new, unreviewed work would still auto-complete `validation_overridden`. Clearing on re-execution makes a fresh failure re-prompt the human. build_product's forward-to-Done accept path never re-runs the gate, so it is unaffected.)*

### 5. `pipeline/engine_checkpoint.go` — `checkGoalGateNode` honors overrides

Add one short-circuit, immediately after the `success`/`partial_success` early-return and **before** the `IsGateRecheckPending` / retry-budget / fallback branches:

```go
if cp.IsGateOverridden(nodeID) {
    return goalGateCheckResult{}, false // resolved by human override; not unsatisfied
}
```

*(Squad I5/engine-I2 — this ordering is load-bearing. It must precede the pending branch (a gate can be both pending and overridden), and it is the **only** guard on resume: `nodeOutcomes` is rebuilt empty on resume (engine_run.go:385, populated only on execution), so a resumed failed gate has status `""` and the success early-return does NOT fire — only `IsGateOverridden`, read straight from the reloaded checkpoint, prevents a re-run. Placing it after the budget branch reintroduces the #348 fail on resume.)*

The run then reaches the success exit, and because `validationOverrides` is non-empty, `buildSuccessResult` (engine.go:300) already flips the terminal status to `validation_overridden` — no change there.

## Data flow (build_product case)

1. `FinalSpecCheck` (goal_gate) executes → `outcome: fail`; `nodeOutcomes[FinalSpecCheck] = "fail"`.
2. Fail routing → `EscalateReview` (human) via the real `when ctx.outcome = fail` edge (no goal-gate exit machinery yet; `GateRecheckPending` unset).
3. Human picks **accept** (`OverrideActor = human`); the engine selects the `EscalateReview --accept--> Cleanup` edge with `Override: true`.
4. `recordOverrideIfPresent`: actor is human → `coveredGoalGates` finds `FinalSpecCheck` (goal gate ✓, `nodeOutcomes == "fail"` ✓, `findFallbackTarget == EscalateReview` and/or direct edge ✓) → `MarkGateOverridden`, `CoveredGates = ["FinalSpecCheck"]`; dedup; append; emit; persist.
5. `Cleanup → FinalCommit → Done`.
6. `checkGoalGateNode(FinalSpecCheck)`: `IsGateOverridden` → not-unsatisfied → no retry.
7. `buildSuccessResult`: `validationOverrides` non-empty → `validation_overridden`; `ValidationOverrides[0].CoveredGates == ["FinalSpecCheck"]`, `Actor == human`.

## Error handling / edge cases

- **Plain override (no failed goal gate, or non-human actor):** `coveredGoalGates` returns empty / the human guard skips → unchanged (regression-guarded by `TestEngine_OverrideEdge_*`).
- **Human picks a non-override edge (retry/reject):** no override recorded, no gate marked → gate still rechecks/fails as today. The fix does not weaken gates.
- **Earlier non-goal-gate failure at a shared escalation** (build_product `FinalBuild → EscalateReview`): `FinalSpecCheck` has not run (`nodeOutcomes == ""`, not `"fail"`) → **not** covered (§C1 fix). Only a gate that actually ran and failed is resolved.
- **Multiple failed goal gates adjacent to `N`:** all covered and recorded in `CoveredGates` — intended (the human accepts at their shared escalation).
- **Gate re-executes after override (looping workflow):** override cleared on re-execution → fresh failure re-prompts.
- **Checkpoint resume:** `OverriddenGates` persists; the exit-check short-circuit reads it directly (no runState rehydration needed) → run still completes `validation_overridden`.
- **clearDownstream after override:** overridden gates live in a dedicated map, not `CompletedNodes`, so downstream clearing cannot lose the state (same rationale as `GateRecheckPending` in #360).

## Testing

- **Unit (`checkpoint_test.go`):** near-copy of `TestCheckpoint_GateRecheckPending_Roundtrip` — `Mark`/`Clear`/`Is` + JSON round-trip of `overridden_gates` + **nil-map safety** (`(&Checkpoint{}).IsGateOverridden("x")` returns false, no panic).
- **Engine integration (`engine_goal_gate_override_test.go`, new) — the #348 reproduction.** Compose `recheckTestGraph`'s gate (box/codergen, `goal_gate:"true"`, `fallback_target:"escalate"`) with an `escalate` hexagon (`wait.human`) whose `testHandler` returns `{Status: success, PreferredLabel:"accept", OverrideActor: ActorHuman}`; mark `escalate --accept--> cleanup` `Override: true`. Gate handler returns `OutcomeFail` **unconditionally** with a `gateAttempts` counter. Assert **all** of *(Squad C1/C2/I1/I4)*:
  - `result.Status == OutcomeValidationOverridden` **and `gateAttempts == 1`** (status alone doesn't prove it — it flips from any override; the re-run count is the discriminator).
  - `cp.RetryCount("gate") == 0` (budget untouched; via `WithCheckpointPath` + `LoadCheckpoint`).
  - `cp.OverriddenGates["gate"] == true`.
  - captured `EventValidationOverridden.Override.CoveredGates == ["gate"]` **and `.Actor == ActorHuman`** (audit trail, not just the in-memory result).
- **Direct-edge arm (`I2`):** a variant where the gate reaches `escalate` via a **direct conditional edge, no `fallback_target`** — proves rule 3's first arm independently.
- **Pending+overridden ordering (`I5`):** a gate both recheck-pending and overridden does **not** re-enter (locks the short-circuit-before-pending invariant).
- **Non-human actor (`I1`):** same graph but `OverrideActor: ActorAutopilot` → gate **not** covered, run fails / stays unsatisfied (autopilot behavior unchanged).
- **Negative (`I3`):** human takes a non-override `retry` edge → run fails, **and** `cp.OverriddenGates["gate"]` absent **and** `len(result.ValidationOverrides) == 0`.
- **Resume (`I5`):** build a checkpoint with `OverriddenGates["gate"]=true` + gate in `CompletedNodes`, `SaveCheckpoint`, resume via `WithCheckpointPath`, run → **`gateAttempts == 0`** (gate never executes) and `Status == validation_overridden`.
- **Multi-hop negative (out-of-scope pin):** `gate → X → escalateHuman`, override from `escalateHuman`, no direct edge / no matching fallback → gate **not** marked overridden.
- **Regression:** existing `TestEngine_OverrideEdge_*` (plain success gates, no `goal_gate`) unchanged — `CoveredGates` nil, behavior byte-for-byte identical.
- **Repo green:** `go build ./...`, `go test ./... -short`, `make complexity` (new helpers ≤ 8/8; `coveredGoalGates` + `gateRoutesTo` extracted), pipeline coverage ≥ 80% (`Makefile:13`), `dippin doctor` core pipelines A.

## Threat notes (audit)

- **Exit-code conflation:** `validation_overridden.IsSuccess() == true`, so a covered run exits 0 by default. With the human-actor gate (§I1) this means "a human accepted a failed gate," which is the intended, recorded outcome. Operators wanting a hard stop use the existing `--fail-on-override` (exit 2). Documented, not changed here.
- **Checkpoint is not tamper-evident:** `OverriddenGates` and `OverrideDetail.Actor` persist in `checkpoint.json`, which (unlike `activity.jsonl`) carries no sentinel. A party who can write the checkpoint (already trusted for resume) could forge an override. Consistent with the documented "sentinel is detection, not authentication" threat model; the human-override claim rests on checkpoint integrity, not proof.
- **Mid-upgrade resume:** a checkpoint written by the pre-fix binary has `ValidationOverrides` but no `OverriddenGates`; resumed under the new binary it will not retroactively gain coverage (the resume-skip path never re-fires `recordOverrideIfPresent`). Not a regression — such a run was already failing (the bug being fixed). No action.

## Out of scope / follow-ups

- **Wiring `build_product_with_superspec`'s `EscalateReview` accept edge with `override: true`** — makes the shipped workflow *use* the capability, but it is a **behavior change** to the approval flow needing separate author sign-off with `dippin doctor`/`simulate`. The engine plan ships the reusable mechanism; the example opts in separately.
- **Surfacing covered gate IDs in the human-gate prompt** (audit F3 — informed consent when one accept covers multiple gates). Nice-to-have; separate.
- **dippin-lang lint** (warn when a goal gate's fallback can reach `Done` without a path back through the gate) — softened by the engine re-entry; file as authoring guidance if desired.
- **#279** (rescope `overrideAlreadyRecorded` to checkpoint generation) — related override-dedup follow-up, independent.
