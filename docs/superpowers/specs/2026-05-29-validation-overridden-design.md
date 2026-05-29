# Gap 5.2 — `validation_overridden` Terminal Status (Issue #233)

**Status:** Design v1

**Author:** Claude (Opus 4.7) + Clint Ecker

**Date:** 2026-05-29

**Closes:** Gap 5.2 of [#233](https://github.com/2389-research/tracker/issues/233). Tracking issue: [#271](https://github.com/2389-research/tracker/issues/271).

**Likely release:** v0.35.0 (minor — library-API delta on `EngineResult.Status`, `tracker.Result.Status`, `AuditReport.Status`, `RunSummary.Status`).

---

## 1. Problem

When `examples/build_product.dip`'s post-build `FinalSpecCheck` reports compliance failures and the human gate `EscalateReview` is entered, the gate offers labels `accept` / `retry` / `abandon`. The `accept` branch routes to `Cleanup → FinalCommit → Done` and the run terminates with `EngineResult.Status = "success"`.

From `tracker audit` or `EngineResult.Status` there is no way to distinguish:

- **"workflow proved compliance"** — the automated checks passed end-to-end, or
- **"a non-workflow decision shipped a known-failing build"** — a check failed and a human, an autopilot persona, or a webhook callback accepted the failure.

Both rows read `Status: success`. The audit trail has silently lost the distinction. The same shape applies to `EscalateMilestone`'s `accept`/`mark done` edges in the same workflow, and to the equivalent acceptance edges in `build_product_with_superspec.dip`, `ask_and_execute.dip`, and `deep_review.dip`.

## 2. Goals

- Add a terminal `EngineResult.Status` value that distinguishes overridden runs from clean automated runs across audit, CLI, library API, and JSON surfaces.
- Make the new value **truthful across all override actors** — direct human input at the TUI, autopilot personas standing in for a human, and webhook callbacks from external services.
- Make the audit signal **durable** across lost activity logs, archived runs, and resumed runs.
- Make the audit signal **propagate across subgraph boundaries** so composed pipelines don't silently lose the override at the first run-boundary.
- Make `tracker run` exit code 0 by default (an override is a completed run), while giving CI operators an opt-in `--fail-on-override` knob in the same release.
- **Fix the `OutcomeBudgetExceeded` precedent** as part of the same work — that constant exists on the engine but `tracker_audit.classifyStatus` collapses it back to `"fail"` in audit/list surfaces, and `AuditReport.Status` / `RunSummary.Status` doc-comments say `"one of: success, fail"`. Don't repeat that mistake; fix the precedent.

## 3. Non-goals

- **Changing `ctx.outcome` routing inside a run.** The signal is audit-only. Existing workflow conditions (`when ctx.outcome = success`, `when ctx.outcome = fail`) keep working unchanged. Workflow authors do not opt in to anything except marking specific edges with `override: true`.
- **Adding `OutcomeValidationOverridden` to `pipeline/handler.go`'s `Outcome.Status` constants.** This is an engine-terminal-only status, sibling to `OutcomeBudgetExceeded`. Handlers never return it; the engine writes it post-loop based on whether the sticky list is non-empty.
- **Auto-marking existing override-shaped edges across the workflow corpus.** Migration is opt-in. A `dippin doctor` warning surfaces the opportunity (§9).
- **Per-line HMAC or stronger forgery defense.** Activity-log integrity is unchanged from #213 — the sentinel is detection, not authentication. The override event rides existing rails and adds no new authenticity claim.
- **A general `Result.ChildRuns []ChildRunSummary` programmatic API.** Subgraph propagation is solved by `ChildOverride` on `Outcome` and aggregated `ValidationOverrides` on the parent's result. A broader cross-run inspection API is a separate feature.
- **Tool nodes triggering overrides.** Tool nodes cannot originate `override: true` edges by construction (§7); changing that would create a forgery vector with the same shape as the #213 audit-log injection threat.

## 4. Decisions

| # | Decision | Rationale |
|---|----------|-----------|
| D1 | Terminal status string: **`validation_overridden`** | Past-tense state descriptor matching `budget_exceeded`. Truthful across human, autopilot, webhook, and any future automatic-policy trigger. Forward-compatible. Neutral — no "emergency rescue" undertone; the override path is a designed feature. |
| D2 | Edge attribute: **`override: true`** | Terse, matches existing `label:` / `when:` / `restart:` attribute style. Greps to the status root word. Consensus across DSL and Naming reviewers. |
| D3 | Default exit code: **0**. Add **`--fail-on-override`** flag (and `TRACKER_FAIL_ON_OVERRIDE=1` env) in the same release, exit code **2**. | Override is a completed run; default-0 doesn't break CI scripts that gate on `tracker run` success. Strict opt-in for CI operators who want builds to fail on overrides. Exit 2 is distinguishable from genuine fail=1. |
| D4 | **Subgraph propagation: parent's terminal Status propagates.** | A parent that composes pipelines via subgraph must not silently lose the override signal at the run boundary. `Outcome.ChildOverride *OverrideDetail` propagates from `SubgraphHandler` to the parent engine, which flips the parent's sticky list (with the gate path qualified). |
| D5 | **`override: true` + `restart: true`** is allowed; sticky persists across restarts. | Captures the audit reality: "this run reached success only because a non-workflow decision pushed it back into the pipeline after a failure." First override wins; sticky persists. |
| D6 | **`OverrideDetail.Actor`** records `human` \| `autopilot` \| `webhook`. | Status stays neutral; detail says who. Audit recommendation copy specializes by actor. Most operationally useful question in a fleet of autopilot-driven CI builds. |
| D7 | **Sticky storage is a slice, not a flag.** | Multiple overrides per run must be supported now — changing shape later is a public-API break. |
| D8 | **Sticky must persist on `Checkpoint`**, not only `runState`. | Without checkpoint persistence, resumed runs lose the override signal; archived runs whose activity log is lost or truncated silently misclassify as success or fail. |
| D9 | **`OutcomeValidationOverridden` is engine-terminal-only.** | Not a handler outcome. Sibling to `OutcomeBudgetExceeded` in `pipeline/engine.go`, not in `pipeline/handler.go`. |
| D10 | Introduce **`pipeline.TerminalStatus` type with `IsSuccess()` helper** as part of this PR. | Prevents `status == "success" \|\| status == "validation_overridden"` smearing across the codebase. CLI exit-code logic, audit recommendations, and JSON consumers all key off a single accessor. |
| D11 | **`override: true` is valid only on edges from `wait.human` or `webhook` nodes.** Adapter rejects at compile-time. | Tool/agent/parallel/manager_loop edges can't originate overrides — closes the forgery vector. Autopilot uses the same `wait.human` handler dispatch path, so autopilot edges remain syntactically valid. |
| D12 | **Fix the `budget_exceeded → "fail"` collapse** in `classifyStatus` as part of this PR. | The precedent we cite is itself broken. Widen the audit Status enum end-to-end (library, classifier, JSON contract, doc-comments) for both `budget_exceeded` and `validation_overridden`. |

## 5. Engine semantics

### 5.1 New constant

```go
// pipeline/engine.go (sibling to OutcomeBudgetExceeded)
const OutcomeValidationOverridden TerminalStatus = "validation_overridden"
```

`TerminalStatus` is a new named string type. `OutcomeSuccess`, `OutcomeFail`, `OutcomeBudgetExceeded`, and `OutcomeValidationOverridden` are all `TerminalStatus` values. The existing handler-level outcome constants in `pipeline/handler.go` (`OutcomeSuccess`/`OutcomeFail`/`OutcomeRetry`) remain plain strings — they coexist; `TerminalStatus("success")` and the handler-level `OutcomeSuccess` are the same underlying string and interop freely.

`TerminalStatus` carries one helper:

```go
// IsSuccess returns true for terminal statuses where the run completed without
// failure. Currently {success, validation_overridden}. Used by CLI exit-code
// logic, audit recommendations, and JSON consumers — call this instead of
// comparing strings directly.
func (s TerminalStatus) IsSuccess() bool
```

### 5.2 Sticky list

A new field on `runState`:

```go
// runState (pipeline/engine.go)
validationOverrides []OverrideDetail
```

The corresponding field on `Checkpoint`:

```go
// Checkpoint (pipeline/checkpoint.go)
ValidationOverrides []OverrideDetail `json:"validation_overrides,omitempty"`
```

`omitempty` ensures pre-existing checkpoints (nil slice) round-trip cleanly; absent = "no overrides happened."

### 5.3 OverrideDetail

```go
// pipeline/events.go
type OverrideDetail struct {
    GateNodeID   string    `json:"gate_node_id"`
    Label        string    `json:"label,omitempty"`         // edge label, empty if no label on the edge
    Actor        Actor     `json:"actor"`                   // ActorHuman | ActorAutopilot | ActorWebhook
    SubgraphPath []string  `json:"subgraph_path,omitempty"` // populated when propagated from a subgraph child
    Timestamp    time.Time `json:"timestamp"`
}

// Actor identifies who took the override edge. Defined as a named string type
// so the constant set is grep-able and JSON marshals as the bare string.
type Actor string

const (
    ActorHuman     Actor = "human"     // real wait.human interviewer (TUI)
    ActorAutopilot Actor = "autopilot" // AutopilotInterviewer or AutoApproveInterviewer
    ActorWebhook   Actor = "webhook"   // WebhookInterviewer (external callback)
)
```

`SubgraphPath` is empty for overrides taken in the run's own graph. When propagated from a child, the parent prepends the subgraph node ID(s) — `["RunChildPipeline"]` for one level, `["Outer", "Inner"]` for two.

### 5.4 Flip-point

The flip happens at edge selection, **not** at handler outcome application. Specifically in `advanceToNextNode` (immediately after `selectEdge` returns the selected edge, before `s.cp.SetEdgeSelection`):

1. If `edge.Override == true`:
   - Inspect the originating node's resolved handler. If `wait.human` (or `webhook`), determine `Actor` by inspecting the interviewer type currently bound: `WaitHumanInterviewer` → `"human"`, `AutopilotInterviewer` (any persona) → `"autopilot"`, `WebhookInterviewer` → `"webhook"`. (For `AutoApproveInterviewer` — the deterministic non-LLM auto-approver used in tests — the actor is `"autopilot"`; `AutoApproveInterviewer` is a flavor of autopilot for this attribution purpose.)
   - Build `OverrideDetail` with `GateNodeID = currentNodeID`, `Label = edge.Label`, `Actor`, `Timestamp = time.Now()`.
   - Append to `runState.validationOverrides` and `runState.cp.ValidationOverrides`.
   - Emit `EventValidationOverridden` (§6).
   - The next `saveCheckpoint` (already in the normal post-node accounting) persists the appended entry.
2. Continue normal edge traversal.

### 5.5 Terminal-status rule (failure dominates)

After the engine's main loop exits via the success path (current code at the end of `Run` and at the success branch of `handleExitNode` engine_run.go:805+), the rule is:

```
if len(s.validationOverrides) > 0:
    EngineResult.Status = OutcomeValidationOverridden
else:
    EngineResult.Status = OutcomeSuccess
```

Failure paths (`failResult`, `cancelledResult`, `handleLoopRestart` exhaustion) build `EngineResult` with `Status = OutcomeFail` (or `OutcomeBudgetExceeded`) and ignore the sticky list. This implements "failure dominates": any failure after an override produces `Status = fail`, but `runState.validationOverrides` is still persisted on the checkpoint and `EventValidationOverridden` is still in the activity log.

`EngineResult` gains a field:

```go
ValidationOverrides []OverrideDetail
```

Populated from `s.validationOverrides` for every terminal path (success, fail, budget, override) — failure paths still expose the list so post-hoc readers can see "this run had an override AND it failed."

### 5.6 Subgraph propagation

`Outcome` gains a new field:

```go
// pipeline/handler.go
ChildOverride []OverrideDetail // populated by SubgraphHandler / ManagerLoopHandler when a child run hit override edges
```

In `pipeline/subgraph.go`:

1. Extend the status mapping switch to treat `OutcomeValidationOverridden` like `OutcomeSuccess` for parent routing:
   ```go
   case OutcomeSuccess, OutcomeBudgetExceeded, OutcomeValidationOverridden:
       status = OutcomeSuccess
   ```
2. If `result.ValidationOverrides` is non-empty, copy it into `Outcome.ChildOverride`, prepending the current subgraph node ID to each entry's `SubgraphPath`.

In the engine's `applyOutcome` path (after the handler returns), if `outcome.ChildOverride != nil`, append each entry to `runState.validationOverrides` and `runState.cp.ValidationOverrides`. The parent's terminal-status rule then naturally lifts to `validation_overridden`.

Same wiring in `pipeline/handlers/manager_loop.go` (its existing `OutcomeBudgetExceeded` special-case at line 709 becomes an allowlist of `{success, budget, validation_overridden}`).

### 5.7 Override + restart + goal-gate retry

- **Override + restart on same edge**: sticky is set at edge traversal, persists across the restart loop. `clearDownstream` and `handleLoopRestart` MUST NOT clear `runState.validationOverrides` or `Checkpoint.ValidationOverrides`. Test coverage in §11.
- **Override followed by goal-gate retry**: same rule — sticky persists across the goal-gate jump.
- **Override followed by max-restart exhaustion**: terminal status is `fail` (failure dominates), `ValidationOverrides` still on the result for forensics.

### 5.8 What does NOT change

- Handlers' `Outcome.Status` taxonomy in `pipeline/handler.go` (still `success` / `fail` / `retry`).
- `ctx.outcome` value (still `success` / `fail`).
- Conditional edge evaluation in `pipeline/condition.go`.
- The strict-failure-edges rule.
- Parallel branch aggregation in `pipeline/handlers/parallel.go` (a branch handler doesn't select edges; overrides only fire at edge selection in the parent engine).

## 6. Event taxonomy

### 6.1 New event type

```go
// pipeline/events.go
const EventValidationOverridden PipelineEventType = "validation_overridden"
```

Stage-level event (`NodeID = gate node`), not pipeline-level. Matches the existing pattern of `EventConditionalFallthrough` and `EventToolMarkerMissing`. Carried via a new typed pointer on `PipelineEvent`:

```go
type PipelineEvent struct {
    // ...existing fields
    Override *OverrideDetail
    // ...
}
```

### 6.2 Reuse `DecisionDetail` for edge geometry

The `DecisionDetail` pattern already carries `EdgeFrom`, `EdgeTo`, `EdgePriority`, `EdgeCondition` for `EventDecisionEdge`. The override-edge selection continues to emit `EventDecisionEdge` with a new `EdgePriority` value `"override"` (sixth value, alongside the existing five). Then `EventValidationOverridden` rides alongside it, the same way `EventConditionalFallthrough` rides alongside `EventDecisionEdge`. The override event's payload (`OverrideDetail`) carries only what's *new*: gate identification, label, actor, subgraph path.

### 6.3 JSONL wire format

`pipeline/events_jsonl.go buildLogEntry` gets a new block analogous to the existing `Truncation` / `Marker` / `Route` blocks:

```go
if e.Override != nil {
    entry.OverrideGate = e.Override.GateNodeID
    entry.OverrideLabel = e.Override.Label
    entry.OverrideActor = e.Override.Actor
    if len(e.Override.SubgraphPath) > 0 {
        entry.OverrideSubgraphPath = strings.Join(e.Override.SubgraphPath, "/")
    }
}
```

Field naming: `override_gate`, `override_label`, `override_actor`, `override_subgraph_path` JSON keys.

### 6.4 `classifyStatus` algorithm

```go
// tracker_audit.go classifyStatus
//
// Algorithm: reverse-scan, failure dominates.
//   - On pipeline_failed: return "fail" immediately.
//   - On budget_exceeded: return "budget_exceeded".
//   - On pipeline_completed: remember it, keep scanning earlier for a
//     validation_overridden event; if found, return "validation_overridden";
//     otherwise return "success".
//   - On validation_overridden with no later completion event:
//     run halted at the override — return "validation_overridden".
//   - If no terminal event found and cp.CurrentNode != "": return "fail".
//   - Fallback: if len(cp.ValidationOverrides) > 0: return
//     "validation_overridden" (handles archived runs with missing log).
//   - Otherwise: return "success".
```

The fallback path closes the lost-activity-log gap: even if the activity log is missing or truncated, the checkpoint-persisted slice surfaces the audit signal.

**Resumed runs** (same runID, multiple `pipeline_started` markers in one log): the override should persist across resume. Don't anchor at the latest `pipeline_started` — the reverse scan walks past the resumed run's `pipeline_completed`, sees the original run's `validation_overridden`, and classifies `validation_overridden`. That's the desired answer ("an override anywhere in the audit history of this run-id classifies it as `validation_overridden`, unless a later failure or budget breach dominates"). Activity logs are per-runID (`SecureActivityLogPath` keys on runID), and `Audit` is scoped to one runDir, so cross-run contamination is impossible.

### 6.5 Fix the `budget_exceeded` collapse

In the same change, `classifyStatus` stops collapsing `budget_exceeded` to `"fail"`. `AuditReport.Status` / `RunSummary.Status` doc-comments update to enumerate `"success" | "fail" | "budget_exceeded" | "validation_overridden"` and explicitly mark the set as open. CLI surfaces (cmd/tracker/audit.go's run-list icons, summary.go's status header switch) gain a `budget` row and an `override` row.

## 7. Dippin-lang IR and adapter

### 7.1 dippin-lang change

Tagged release of `2389-research/dippin-lang`:

- Add `Override bool` to the `ir.Edge` struct.
- Parser accepts `override: true` (or `override: false` as default) as an edge attribute, position-flexible alongside `label:`, `when:`, `restart:`.
- JSON IR field name: `"override"`.

This lands first; tracker bumps the go module dependency to the tagged release in the same PR that ships the rest of this work.

### 7.2 Tracker adapter

`pipeline/dippin_adapter.go convertEdge` maps `IREdge.Override` → `pipeline.Edge.Override` (new first-class field on the existing `Edge` struct in `pipeline/graph.go`, symmetric to `Condition`, `Label`, `Restart`). NOT in `Edge.Attrs` map — engine reads the typed field.

### 7.3 Validation (adapter compile-time)

The adapter validates **at graph-construction time**:

1. `override: true` is rejected on any edge whose originating node's resolved handler is not `wait.human` or `webhook`. Error message: `edge X→Y has override: true but X is not a wait.human or webhook gate`. This closes the tool-node forgery vector (D11 / C2 from the DSL review).
2. `override + restart` is allowed (D5). No restriction.
3. `override + when` is allowed (conditional override edges are sensible — "only count as override if outcome=fail").

`dippin doctor` surfaces rule (1) as a hard error per its existing convention. The adapter and `dippin doctor` both call the same validation helper so the error message is identical in both contexts.

### 7.4 New `dippin doctor` lint rule: TRK1XX

Add a new lint code (next available in the TRK1XX series — likely `TRK102` if `TRK101` is taken; otherwise the next free number) that warns on **unmarked-but-looks-like-override** edges:

> "Edge from `wait.human` node `EscalateReview` to forward-progress node `Cleanup` via label `accept` is not marked `override: true`. If this edge represents accepting a failed validation, add `override: true` to record the audit signal."

Heuristic for the warning:

- Source node is a `wait.human` (or `webhook`).
- Label matches `accept`, `mark done`, `approve` (case-insensitive) — these are the empirically common override labels.
- Target is reachable from the run's exit node without passing through another gate.
- The edge is NOT marked `override: true`.

Warn, do not fail. False positives are expected for genuine `approve` paths (plan acceptance, spec approval) where no prior validation was overridden. The warning text suggests the one-line edit and explains the audit-status consequence in one sentence.

## 8. Library API delta

This is a **library-API change** — `EngineResult.Status` is part of the exported surface (CLAUDE.md release process). CHANGELOG entry goes under `Changed` with the explicit "library-API delta" call-out.

### 8.1 New / changed exported surfaces

| Symbol | Change |
|--------|--------|
| `pipeline.TerminalStatus` | **New** named string type. Existing constants (`OutcomeSuccess`, `OutcomeFail`, `OutcomeBudgetExceeded`) re-typed. |
| `pipeline.OutcomeValidationOverridden` | **New** constant, value `"validation_overridden"`. |
| `pipeline.TerminalStatus.IsSuccess()` | **New** method. Returns true for `{success, validation_overridden}`. |
| `pipeline.OverrideDetail` | **New** struct. |
| `pipeline.Edge.Override` | **New** bool field. |
| `pipeline.Outcome.ChildOverride` | **New** field, `[]OverrideDetail`. |
| `pipeline.EngineResult.ValidationOverrides` | **New** field, `[]OverrideDetail`. |
| `pipeline.Checkpoint.ValidationOverrides` | **New** field, `[]OverrideDetail` (JSON `validation_overrides`). |
| `pipeline.EventValidationOverridden` | **New** event constant. |
| `pipeline.PipelineEvent.Override` | **New** field, `*OverrideDetail`. |
| `tracker.Result.Status` | **Doc-comment added** enumerating `{success, fail, budget_exceeded, validation_overridden}`, marked as open enum. Today has no doc-comment at all. |
| `tracker.Result.ValidationOverrides` | **New** field, mirrored from `EngineResult.ValidationOverrides`. |
| `tracker.AuditReport.Status` | **Doc-comment fixed** to enumerate four values (currently lies as `"one of: success, fail"`). |
| `tracker.AuditReport.ValidationOverrides` | **New** field. |
| `tracker.RunSummary.Status` | **Doc-comment fixed** to enumerate four values. |
| `tracker.RunSummary.OverrideCount` | **New** field, `int` (count only — RunSummary stays thin for listing). |

### 8.2 CLI exit-code fixes

`cmd/tracker/run.go:313` (`interpretRunResult`) and `cmd/tracker/run.go:696` (`runPipelineAsync`) currently treat `result.Status != pipeline.OutcomeSuccess` as an error. Both rewrite to use `TerminalStatus.IsSuccess()`:

```go
if !pipeline.TerminalStatus(result.Status).IsSuccess() {
    return fmt.Errorf("pipeline finished with status: %s", result.Status)
}
```

When `--fail-on-override` is set, the wrapper after `interpretRunResult` translates `Status == OutcomeValidationOverridden` into exit code 2 (distinct from generic-fail exit 1), with a stderr line:

> `tracker: run completed via validation_overridden at EscalateReview (label "accept"); --fail-on-override caused non-zero exit`

`cmd/tracker/summary.go:447` (`printResumeHint`) uses the same `IsSuccess()` check so override runs don't print a misleading "Resume" hint.

### 8.3 Conformance JSON

`cmd/tracker-conformance/main.go:1001` continues to emit `"status": result.Status` raw. Add a sibling field for forward-compat:

```json
"status": "validation_overridden",
"status_class": "succeeded"
```

`status_class` is one of `{succeeded, failed}` and stays stable: `succeeded` for any `TerminalStatus.IsSuccess() == true`, `failed` otherwise. Downstream verifiers asserting `status_class == "succeeded"` survive future enum extensions.

## 9. CLI and audit surface

### 9.1 `tracker list` table

`cmd/tracker/audit.go:32-48` Status column widens from 8 to 10 chars. Icons:

| Status | Icon |
|--------|------|
| `success` | `ok` |
| `validation_overridden` | `override` |
| `budget_exceeded` | `budget` |
| `fail` | `FAIL` |

Note: `budget` and `override` are new — they replace the current behavior where both fall through to silent `fail`. Mixed casing distinguishes the "needs operator attention" cases without color (some operators pipe to `less` / `grep`).

### 9.2 `tracker audit <runID>` header

```
  Status:    validation_overridden (label "accept" at EscalateReview by human)
  Override:  fail at FinalSpecCheck → accept at EscalateReview → Cleanup
```

The first line renders gate + label + actor inline. The second line, shown only when `len(report.ValidationOverrides) > 0`, traces the routing: the failed node that triggered the gate → the override edge taken → the next non-gate target. Multiple overrides render one `Override:` line each.

For overrides propagated from a subgraph, the gate node renders as `subgraph_path/gate_node_id` (e.g., `RunChildPipeline/EscalateReview`).

### 9.3 Recommendations

`tracker_audit.go:269` drops the `sort.Strings(recs)` call — recommendations sort by importance, not alphabetically. Override notes go first.

Copy templates:

- **Summary entry** (always, when `len(ValidationOverrides) > 0`):
  > "This run terminated via a validation override. Workflow completion does not imply spec compliance — the override path bypassed at least one automated gate."

- **Per-override entry** (chronological):
  > "Validation override at gate `<gate>` routed to `<edge_target>` (label: `<label>`, actor: `<actor>`). Review the override decision to confirm it meets project policy."

No retry/budget recommendations on override-runs unless they're also relevant — override notes take priority.

### 9.4 `tracker diagnose`

Diagnose is for failure analysis. An overridden run is not a failure. Do NOT add a `SuggestionValidationOverridden` (would put it on equal footing with `SuggestionAuditLogInjection` etc. — wrong category).

Instead, add an informational field on `DiagnoseReport`:

```go
ValidationOverrides []OverrideDetail
OverrideCount       int
```

Populated from the activity log's `EventValidationOverridden` lines OR from `checkpoint.ValidationOverrides` (fallback). Rendered as a dedicated section between `BudgetHalt` and `Failures`:

```
─── Validation Override ─────
  Gate:        EscalateReview
  Label:       "accept"
  Actor:       human
  Routed to:   Cleanup
```

The triggering failure (e.g., `FinalSpecCheck` reporting compliance errors) still appears in the `Failures` section with an `(overridden)` suffix on the node row.

### 9.5 Summary printer

`cmd/tracker/summary.go:192` status header gets two new cases (override + budget). Override uses a dedicated yellow/amber color treatment (whatever lipgloss palette entry is closest to amber; reuse if one exists) + text glyph for `NO_COLOR`. Format: `● validation_overridden — at EscalateReview (label "accept")`.

`cmd/tracker/summary.go:164`'s `OutcomeBudgetExceeded` special-case stays; the override case is its sibling.

## 10. Workflow migration

### 10.1 Existing workflows — per-edge audit

| File | Edge | Mark? | Reason |
|------|------|-------|--------|
| build_product.dip:1314 | `ApprovePlan → PickNextMilestone label: "approve"` | No | Plan acceptance, no prior validation failure to override |
| build_product.dip:1334 | `EscalateMilestone → MarkMilestoneDone label: "mark done"` | **Yes** | Human accepts unfinished milestone |
| build_product.dip:1336 | `EscalateMilestone → Cleanup label: "accept"` | **Yes** | Human accepts unfinished build |
| build_product.dip:1370 | `EscalateReview → Cleanup label: "accept"` | **Yes** | Human accepts review-flagged issues |
| build_product_with_superspec.dip:940 | `ApprovePlan → SetupPhase1Worktrees label: "approve"` | No | Plan acceptance |
| build_product_with_superspec.dip:1016 | `EscalateToHuman → Cleanup label: "accept"` | **Yes** | Human accepts post-build issues |
| ask_and_execute.dip:398 | `ApproveSpec → SetupWorktrees label: "approve"` | No | Spec approval |
| ask_and_execute.dip:416 | `EscalateToHuman → CommitFinal label: "accept"` | **Yes** | Human accepts verification failure |
| deep_review.dip:366 | `ReviewPlan → ApplyFormat label: "approve"` | No | Plan approval, normal forward progress |

Five edges across four workflows get `override: true` added in the same PR. Each edge also gets a one-line `# audit: ...` comment explaining the override intent so future workflow authors don't blindly copy the pattern (or omit it).

### 10.2 Migration policy

Auto-marking is rejected (D7's flip side — changing every existing override-shaped run's terminal status overnight would break consumers comparing `Result.Status == "success"`). Workflow authors opt in per edge. The `dippin doctor TRK1XX` warning surfaces the opportunity for un-migrated workflows.

## 11. Testing strategy

### 11.1 Engine

- New override-edge fires sticky and emits `EventValidationOverridden`.
- Override-then-success-exit: `EngineResult.Status == OutcomeValidationOverridden`.
- Override-then-fail: `Status == OutcomeFail`, `ValidationOverrides` still populated.
- Override-then-budget-exceeded: `Status == OutcomeBudgetExceeded`, `ValidationOverrides` still populated.
- Override + restart: sticky persists across the restart loop; terminal status `validation_overridden` after retry-completes.
- Override + goal-gate retry: sticky persists across the goal-gate jump; terminal status `validation_overridden`.
- Override + max-restart exhaustion: `Status == fail`, `ValidationOverrides` on result.
- Multiple overrides in one run: all entries land on `ValidationOverrides` and `Checkpoint.ValidationOverrides`.
- Resume after override: load checkpoint, run continues, sticky preserved.

### 11.2 Subgraph / manager_loop

- Child run hits override → parent's `Outcome.ChildOverride` populated with subgraph_path-qualified detail → parent's terminal status `validation_overridden`.
- Two levels of subgraph nesting: `SubgraphPath` accumulates correctly (`["Outer", "Inner"]`).
- Child override + parent's own override: both land on parent's `ValidationOverrides`.
- Child override + parent's own subsequent failure: `Status == fail`, `ValidationOverrides` still populated (failure dominates).

### 11.3 Adapter

- `override: true` on a tool node edge → adapter rejects with specific error.
- `override: true` on a `wait.human` edge → accepted.
- `override: true` on a webhook edge → accepted.
- `override: true` on a manager_loop edge → rejected.
- `override + restart` → accepted, no warning.
- `override + when` → accepted.

### 11.4 Audit / classifyStatus

- override + complete → `validation_overridden`
- override + fail → `fail`
- override + budget → `budget_exceeded`
- 2x override + complete → `validation_overridden` (latest detail wins for the "headline")
- override at terminal (no later complete, `cp.CurrentNode != ""`) → `validation_overridden`
- resumed run with override on attempt 1, complete on attempt 2 → `validation_overridden`
- legacy run (no sentinel, no `ValidationOverrides` in checkpoint) → unchanged
- secure log lost / file gone but `ValidationOverrides` populated on checkpoint → `validation_overridden` (fallback path)
- `budget_exceeded` event in log → `budget_exceeded` status (NOT collapsed to fail)

### 11.5 JSONLEventHandler

- Round-trip: emit `EventValidationOverridden`, close handler, reload via `LoadActivityLog`, parse `override_*` fields correctly from both secure and snapshot paths.
- Sentinel applies to override events like every other event.

### 11.6 CLI

- `tracker list` row renders the new statuses without column overflow.
- `tracker audit <runID>` renders the `Override:` chain line.
- `tracker run` default exits 0 on override.
- `tracker run --fail-on-override` exits 2 on override and emits the stderr line.
- `TRACKER_FAIL_ON_OVERRIDE=1` has the same effect as the flag.

### 11.7 Library API

- `EngineResult.Status` returns the new value where appropriate.
- `tracker.Result.ValidationOverrides` mirrored correctly.
- `TerminalStatus("validation_overridden").IsSuccess() == true`.
- `TerminalStatus("budget_exceeded").IsSuccess() == false`.

### 11.8 Conformance

- `cmd/tracker-conformance` emits both `status` and `status_class`.
- Override-run conformance fixture: `status_class == "succeeded"`.

### 11.9 dippin doctor

- TRK1XX fires on the unmarked override-shaped edges in workflows the migration didn't touch (use a fixture).
- TRK1XX does NOT fire on `ApprovePlan` / `ApproveSpec` / `ReviewPlan` edges (plan-approval shape).

## 12. Verification gates (per CLAUDE.md)

- `go build ./...` clean
- `go test ./... -short` — all 17 packages pass
- `dippin doctor examples/{ask_and_execute,build_product,build_product_with_superspec,deep_review}.dip` — A grade on all four after the edge migrations in §10
- `dippin simulate -all-paths` on the three core pipelines — all paths terminate; override paths recognized
- `tracker audit` on a real run that exercises `EscalateReview` "accept" — confirm `Status: validation_overridden` with the gate / label / actor inline

## 13. Open questions

- **Color choice in `cmd/tracker/summary.go`.** Lipgloss palette — pick the closest amber/yellow to existing styles or introduce a new constant. Resolved at implementation time; not load-bearing on the design.
- **`dippin doctor` lint code number.** Next free in the TRK1XX series; check the current ordering at implementation time.

## 14. CHANGELOG entry shape

Under `Changed` (library-API delta call-out):

> **Added** terminal `EngineResult.Status` value `validation_overridden`. Runs that traverse a `wait.human` or `webhook` gate edge marked `override: true` in their `.dip` workflow now terminate with `Status == "validation_overridden"` instead of `"success"`, recording in the audit trail that a non-workflow decision accepted a result the automated checks flagged as failed. Default `tracker run` exit code remains 0 for override runs; `--fail-on-override` (or `TRACKER_FAIL_ON_OVERRIDE=1`) opts into exit code 2 for CI-strict use. Library-API delta: `pipeline.TerminalStatus` is a new named type; `pipeline.OutcomeValidationOverridden`, `pipeline.OverrideDetail`, `pipeline.Edge.Override`, `pipeline.Outcome.ChildOverride`, `pipeline.EngineResult.ValidationOverrides`, `pipeline.Checkpoint.ValidationOverrides`, `pipeline.EventValidationOverridden`, `pipeline.PipelineEvent.Override` are new exports. `tracker.Result`, `tracker.AuditReport`, `tracker.RunSummary` gain mirrored fields and have their `Status` doc-comments corrected to enumerate the open status set. **Fixed**: `tracker_audit.classifyStatus` previously collapsed `budget_exceeded` events to `"fail"` — audit and list surfaces now surface `budget_exceeded` correctly. **Requires** `dippin-lang vX.Y.Z` (tagged in the same release).

## 15. Out of scope (deferred)

- `OutcomeCancelled` for ctx-cancellation runs (currently `fail`). Sibling lossy-collapse worth a separate issue.
- `Result.ChildRuns []ChildRunSummary` — general programmatic cross-run inspection API.
- Per-line HMAC or stronger forgery defense for the activity log.
- TUI-specific override rendering (`tui/adapter.go`) — confirm needs at implementation time; may be a follow-up.
