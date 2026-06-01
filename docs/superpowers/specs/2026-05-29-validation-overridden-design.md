# Gap 5.2 — `validation_overridden` Terminal Status (Issue #233)

**Status:** Design v2 (revised after second review-squad pass)

**Author:** Claude (Opus 4.7) + Clint Ecker

**Date:** 2026-05-29 (v1) → 2026-05-29 (v2, same day)

**Revision history:**

- **v1 (initial):** drafted from a 6-reviewer brainstorming pass.
- **v2 (this version):** drafted from a 4-reviewer spec-audit pass. Key changes — actor detection plumbing made explicit via a new `Interviewer.Actor()` method (closes the load-bearing gap); concrete interviewer type→Actor mapping table corrected (the v1 spec invented `WaitHumanInterviewer`); `webhook` removed as a node-type concept (it is an Interviewer flavor on the `wait.human` handler); D4 type corrected from `*OverrideDetail` to `[]OverrideDetail`; D5 split into D5+D5a (status flip is first-write-wins; the audit-header headline picks the latest entry); `classifyStatus` algorithm reordered so the checkpoint fallback is reachable; ParallelHandler `ChildOverride` aggregation added (closes the build_product.dip blind spot); §11 test list expanded to enumerate actor-mapping cases and engine-terminal-only invariant enforcement; TUI live rendering moved from §15 deferred to in-scope §9.6; CHANGELOG §14 reformatted to Keep a Changelog; new §16 release sequence; new §17 documentation surfaces enumerating the 12 doc edits required across README + site + CLI help.

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
- **Auto-marking existing override-shaped edges across the workflow corpus.** Migration is opt-in. A `dippin doctor` warning (TRK102, defined in §7.4) surfaces the opportunity. The five edges in the example corpus that should be marked are migrated explicitly in this PR (§10); they are NOT auto-marked by a tool.
- **Per-line HMAC or stronger forgery defense.** Activity-log integrity is unchanged from #213 — the sentinel is detection, not authentication. The override event rides existing rails and adds no new authenticity claim.
- **A general `Result.ChildRuns []ChildRunSummary` programmatic API.** Subgraph propagation in this PR is scoped to `Outcome.ChildOverride []OverrideDetail` (§5.6) and the aggregated `EngineResult.ValidationOverrides` slice — sufficient for the audit signal to propagate across composition boundaries. A broader cross-run inspection API is a separate future feature, deferred per §15.
- **Tool nodes triggering overrides.** Tool nodes cannot originate `override: true` edges by construction (§7.3); changing that would create a forgery vector with the same shape as the #213 audit-log injection threat.
- **Webhook as a distinct node type.** "Webhook" appears in user-facing copy and in the `OverrideDetail.Actor` taxonomy but is NOT a separate node type in the codebase. The `webhook` interviewer is one flavor of `Interviewer` plugged into the single `wait.human` handler. Adapter validation in §7.3 keys off "node's resolved handler is `wait.human`" — webhook eligibility is then determined at runtime by the bound interviewer type (§5.3.1). This is a v2 correction; v1 erroneously named webhook as a separate node type.

## 4. Decisions

| # | Decision | Rationale |
|---|----------|-----------|
| D1 | Terminal status string: **`validation_overridden`** | Past-tense state descriptor matching `budget_exceeded`. Truthful across human, autopilot, webhook, and any future automatic-policy trigger. Forward-compatible. Neutral — no "emergency rescue" undertone; the override path is a designed feature. |
| D2 | Edge attribute: **`override: true`** | Terse, matches existing `label:` / `when:` / `restart:` attribute style. Greps to the status root word. Consensus across DSL and Naming reviewers. |
| D3 | Default exit code: **0**. Add **`--fail-on-override`** flag (and `TRACKER_FAIL_ON_OVERRIDE=1` env) in the same release, exit code **2**. | Override is a completed run; default-0 doesn't break CI scripts that gate on `tracker run` success. Strict opt-in for CI operators who want builds to fail on overrides. Exit 2 is distinguishable from genuine fail=1. |
| D4 | **Subgraph propagation: parent's terminal Status propagates.** | A parent that composes pipelines via subgraph must not silently lose the override signal at the run boundary. `Outcome.ChildOverride []OverrideDetail` (a **slice**, v2 correction from v1's `*OverrideDetail` pointer) propagates from `SubgraphHandler` to the parent engine, which appends to the parent's sticky list with `SubgraphPath` qualified. ParallelHandler unions `ChildOverride` across branches (§5.6.1). |
| D5 | **Override + restart on the same edge is allowed; the sticky list persists across restarts.** | Captures the audit reality: "this run reached success only because a non-workflow decision pushed it back into the pipeline after a failure." Subsequent restarts neither clear the sticky list nor reset the terminal-status implication. |
| D5a | **Status-flip semantics: first override locks `validation_overridden` as the terminal-status implication; subsequent overrides append to the slice but do not change the implication. The audit-header headline picks the *latest* entry.** | The terminal status is set-once on the first override (idempotent under repeated traversals of the same edge during retries — re-traversal does NOT append a duplicate entry). The audit-row headline at §9.2 picks the most recent gate for "what most directly led to this override," which is the most useful single-line answer in an interactive `tracker audit`. The full chain is visible in the `Override:` chain line and `EngineResult.ValidationOverrides`. (v2: resolves v1's contradiction between D5 "first wins" and §11.4 "latest detail wins for the headline" — both rules are now stated; they target different surfaces.) |
| D6 | **`OverrideDetail.Actor`** records `human` \| `autopilot` \| `webhook` \| `unknown`. | Status stays neutral; detail says who. `unknown` is the sentinel for an `Interviewer` implementation whose `Actor()` method returns an unrecognized value (e.g., a future custom interviewer); the override still records, just without high-confidence attribution. (v2 addition: `unknown` sentinel for the third-party interviewer case.) |
| D7 | **Sticky storage is a slice, not a flag.** | Multiple overrides per run must be supported now — changing shape later is a public-API break. |
| D8 | **Sticky must persist on `Checkpoint`**, not only `runState`. Append+`saveCheckpoint` happen atomically at the flip-point in `advanceToNextNode`, before the next `selectEdge` call. | Without checkpoint persistence, resumed runs lose the override signal; archived runs whose activity log is lost or truncated silently misclassify as success or fail. The "atomically" qualifier protects against `kill -9` between sticky append and save: if the process dies before save, the override never fired (run resumes at the prior checkpoint), which is correct (the edge wasn't yet traversed from the persistence-layer's point of view). |
| D9 | **`OutcomeValidationOverridden` is engine-terminal-only.** Handlers that return `Outcome{Status: "validation_overridden"}` have the value silently overwritten to the engine's terminal-status rule (§5.5). | Not a handler outcome. Sibling to `OutcomeBudgetExceeded` in `pipeline/engine.go`, not in `pipeline/handler.go`. (v2 addition: explicit overwrite semantics so the invariant is enforceable by test.) |
| D10 | Introduce **`pipeline.TerminalStatus` type with `IsSuccess()` helper** as part of this PR. `EngineResult.Status` is re-typed from `string` to `TerminalStatus`. | Prevents `status == "success" \|\| status == "validation_overridden"` smearing across the codebase. CLI exit-code logic, audit recommendations, and JSON consumers all key off a single accessor. Re-typing is a public-API change but compatible: `TerminalStatus` is `type TerminalStatus string`, so existing string-literal comparisons (`result.Status == "success"`) continue to compile and produce the same answer. CHANGELOG calls this out explicitly. |
| D11 | **`override: true` is valid only on edges from `wait.human` nodes.** (v2 correction: dropped `webhook` from this rule — webhook is an interviewer flavor, not a node type.) Adapter rejects at compile-time. | Tool/agent/parallel/parallel.fan_in/conditional/subgraph/manager_loop edges can't originate overrides — closes the forgery vector. All `wait.human` modes (`freeform`, `yes_no`, `interview`, `choice`, `hybrid`) are valid. Backend overrides (`backend: claude-code`) do not affect this validation — handler dispatch is at the `Handler` interface; the resolved handler name is still `wait.human`. The webhook case is then runtime-detected via the bound interviewer's `Actor()` method (§5.3.1). |
| D12 | **Fix the `budget_exceeded → "fail"` collapse** in `classifyStatus` as part of this PR. | The precedent we cite is itself broken. Widen the audit Status enum end-to-end (library, classifier, JSON contract, doc-comments) for both `budget_exceeded` and `validation_overridden`. **User-visible behavior change**: scripts filtering on `status == "fail"` will see budget-halted runs move out of the `fail` bucket into a new `budget_exceeded` bucket. Called out in §14 CHANGELOG with explicit migration guidance. |
| D13 | **New `EdgePriority` value `"override"`** is added to the existing `DecisionDetail.EdgePriority` enum, emitted on the existing `EventDecisionEdge` when an override edge is traversed. The new `EventValidationOverridden` rides alongside, not in place. | Reuses existing wire-format machinery (§6.2). Symmetric with how `EventConditionalFallthrough` rides alongside `EventDecisionEdge`. NDJSON consumers that already key off `edge_priority` see the new value; consumers that don't get the dedicated event. |
| D14 | **`dippin doctor` lint rule TRK102** is added: warn-level, fires when an edge from a `wait.human` node with label matching `accept` / `mark done` / `approve` (case-insensitive) leads to a forward-progress target, the source gate is reachable via an `outcome = fail` edge from upstream, AND the edge lacks `override: true`. | Surfaces the migration opportunity for external workflow authors. The "reachable via `outcome = fail`" predicate is the key disambiguator that suppresses false positives on plan-approval gates (`ApprovePlan`, `ApproveSpec`, `ReviewPlan`) — those gates have no failed-validation upstream. (v2 strengthening of v1's heuristic, which would have false-positive on plan approvals.) Number TRK102 confirmed as next-available in `pipeline/lint_tracker.go`. |
| D15 | **`status_class` field** (one of `succeeded` / `failed`) is added to **all** JSON surfaces that today emit `status`: `tracker --json` (NDJSON `pipeline_completed` event), `tracker audit --json`, `tracker list --json`, `cmd/tracker-conformance` output. | v1 scoped `status_class` to conformance only; v2 widens. Otherwise downstream consumers parsing one JSON surface have to know whether `status_class` exists on it. Standardize across all surfaces. |
| D16 | **Drop the `sort.Strings(recs)` call** in `tracker_audit.buildAuditRecommendations`. Recommendations sort by priority, not alphabetically. Override notes go first; per-override entries follow chronologically; retries and budget notes come last. | v1 buried this in §9.3 prose. Elevated to a decision because it changes the order of an existing library-API surface (`AuditReport.Recommendations`). Downstream tooling that sorted on receipt is unaffected; tooling that displayed in receive-order will see a different order. |
| D17 | **TUI live rendering is in scope for v0.35.0**, not deferred. `MsgPipelineCompleted` gains a `Status TerminalStatus` field; the TUI completion row renders amber for `validation_overridden` (same color choice as the CLI summary). | v1 deferred TUI to "follow-up." v2 moves in-scope: the feature's audit-visibility purpose is defeated if the live screen shows green for an override. The user watching the run is the operationally most-important audit reader. (See §9.6.) |
| D18 | **Color choice: amber (`lipgloss.Color("#D97706")` or nearest existing palette entry).** Pick once, apply everywhere: TUI completion row, CLI summary header, `tracker list` row icon. | Closes v1's `§13` open question. Single source of truth for the visual treatment across all surfaces. The exact lipgloss color is fixed at impl time by looking at the existing palette; if there's no amber entry, add one named `colorOverride`. |

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
// failure. Currently {success, validation_overridden}. Any unrecognized value
// returns false (fail-closed) — CI scripts gating on IsSuccess() treat unknown
// future statuses as failures, which is conservative.
//
// Used by CLI exit-code logic, audit recommendations, and JSON consumers — call
// this instead of comparing strings directly.
func (s TerminalStatus) IsSuccess() bool {
    switch s {
    case OutcomeSuccess, OutcomeValidationOverridden:
        return true
    default:
        return false
    }
}
```

**Public-API note**: `EngineResult.Status` (and `tracker.Result.Status`, `tracker.AuditReport.Status`, `tracker.RunSummary.Status`) are re-typed from `string` to `TerminalStatus`. Because `TerminalStatus` is `type TerminalStatus string`, existing literal-string comparisons like `result.Status == "success"` continue to compile and produce the same answer (Go allows comparison between a named string type and an untyped string constant). Code that assigns from a `string` variable into `Status` needs an explicit cast — this is the one breaking change in the re-typing.

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
    Actor        Actor     `json:"actor"`                   // ActorHuman | ActorAutopilot | ActorWebhook | ActorUnknown
    SubgraphPath []string  `json:"subgraph_path,omitempty"` // populated when propagated from a subgraph child
    Timestamp    time.Time `json:"timestamp"`
}

// Actor identifies who took the override edge. Defined as a named string type
// so the constant set is grep-able and JSON marshals as the bare string.
type Actor string

const (
    ActorHuman     Actor = "human"     // a human-driven interviewer (TUI or non-TUI console)
    ActorAutopilot Actor = "autopilot" // any autopilot variant (LLM-backed or deterministic auto-approve)
    ActorWebhook   Actor = "webhook"   // external callback via WebhookInterviewer
    ActorUnknown   Actor = "unknown"   // third-party / future Interviewer with no recognized Actor() value
)
```

`SubgraphPath` is empty for overrides taken in the run's own graph. When propagated from a child, the parent prepends the subgraph node ID — see §5.6 for the recursive case at multi-level nesting. **The leaf gate node ID is carried in `GateNodeID`, not in `SubgraphPath`** — `SubgraphPath` contains only the intermediate subgraph-handler node IDs walked from outermost to innermost.

**JSONL Timestamp note**: in the JSONL wire format, `OverrideDetail.Timestamp` is **redundant** with the event-line timestamp `PipelineEvent.Timestamp`. Wire-format readers should prefer the event-line timestamp; `OverrideDetail.Timestamp` is included on the struct because it is also persisted on `Checkpoint.ValidationOverrides` where there is no enclosing event-line timestamp.

### 5.3.1 Actor detection mechanism

**This section closes the v1 "load-bearing gap"**: by the time edge selection runs at `advanceToNextNode`, the bound interviewer is not directly observable from the engine. Type-switching from the engine on concrete interviewer types is not feasible — TUI interviewer types live in `tui/` which already imports `pipeline/handlers/`, so a reverse import would create a cycle. The mechanism:

**Interface extension.** The existing `pipeline/handlers.Interviewer` interface (and its sibling `LabeledFreeformInterviewer`) gains one method:

```go
// pipeline/handlers/interviewer.go (existing file)
type Interviewer interface {
    // ...existing methods...

    // Actor returns the Actor classification for runs that traverse an override
    // edge via this interviewer. Implementations return ActorHuman, ActorAutopilot,
    // ActorWebhook, or ActorUnknown.
    Actor() pipeline.Actor
}
```

Every interviewer implementation in tracker adds this method:

| Implementation | File | Returns |
|---|---|---|
| `BubbleteaInterviewer` | `tui/interviewer.go` | `ActorHuman` |
| `ConsoleInterviewer` | `pipeline/handlers/human.go` | `ActorHuman` |
| `AutopilotInterviewer` | `pipeline/handlers/autopilot.go` | `ActorAutopilot` |
| `ClaudeCodeAutopilotInterviewer` | `pipeline/handlers/autopilot_claudecode.go` | `ActorAutopilot` |
| `AutopilotTUIInterviewer` | `tui/autopilot_interviewer.go` | `ActorAutopilot` |
| `AutoApproveInterviewer` | (existing test interviewer) | `ActorAutopilot` |
| `AutoApproveFreeformInterviewer` | (existing test interviewer) | `ActorAutopilot` |
| `WebhookInterviewer` | `pipeline/handlers/webhook_interviewer.go` | `ActorWebhook` |
| `CallbackInterviewer` | (test-only) | `ActorUnknown` |
| `QueueInterviewer` | (test-only) | `ActorUnknown` |
| third-party / future implementations | — | `ActorUnknown` (default if method omitted — see below) |

**Backward compatibility for external implementations.** Adding a method to an exported interface would break third-party implementations that satisfy the existing `Interviewer` shape. To avoid this, the engine queries `Actor()` via a defensive interface assertion:

```go
// pipeline/handlers/human.go (where HumanHandler resolves its interviewer)
func actorOf(i Interviewer) pipeline.Actor {
    if a, ok := i.(interface{ Actor() pipeline.Actor }); ok {
        return a.Actor()
    }
    return pipeline.ActorUnknown
}
```

This pattern keeps `Interviewer` unchanged at the interface-definition level; the `Actor()` method is queried opportunistically. Third-party interviewers that don't implement it classify as `ActorUnknown` — the override still records, just without high-confidence attribution. (Matches D6's `unknown` sentinel.)

**Channel from handler to engine.** `Outcome` (defined in `pipeline/handler.go`) gains one new field:

```go
type Outcome struct {
    // ...existing fields...

    // OverrideActor is the Actor classification of the interviewer that produced
    // this outcome, populated by HumanHandler from the bound interviewer's Actor()
    // method. The engine reads this field at edge-selection time to populate
    // OverrideDetail.Actor when an override edge is traversed from this node.
    // Empty for non-wait.human handlers (they cannot originate override edges).
    OverrideActor Actor
}
```

`HumanHandler.Execute` sets `outcome.OverrideActor = actorOf(h.interviewer)` before returning. The engine's `advanceToNextNode` reads `runState.lastOutcome.OverrideActor` when populating `OverrideDetail`.

This makes the actor classification a value carried on `Outcome`, not a separate runtime lookup. The data path is: `Interviewer.Actor()` → `Outcome.OverrideActor` → `OverrideDetail.Actor`. No circular imports; no engine-side type switching; backwards-compatible with third-party `Interviewer` implementations.

### 5.4 Flip-point

The flip happens at edge selection, **not** at handler outcome application. Specifically in `advanceToNextNode` (immediately after `selectEdge` returns the selected edge, before `s.cp.SetEdgeSelection`):

1. If `edge.Override == true`:
   - **Idempotency check**: if `runState.cp.ValidationOverrides` already contains an entry with `GateNodeID == currentNodeID && Label == edge.Label && len(SubgraphPath) == 0`, **do nothing** — this is a re-traversal of the same override edge during a restart or goal-gate retry, and the run is already marked overridden. Continue to step 2.
   - Otherwise: build `OverrideDetail` with `GateNodeID = currentNodeID`, `Label = edge.Label`, `Actor = runState.lastOutcome.OverrideActor` (populated by `HumanHandler.Execute`; defaults to `ActorUnknown` if absent), `Timestamp = time.Now()`, `SubgraphPath = nil`.
   - Append to `runState.validationOverrides` and `runState.cp.ValidationOverrides`.
   - Emit `EventValidationOverridden` (§6).
   - **Synchronously call `saveCheckpointWithTag`** (existing function at `engine_run.go:421`). This is load-bearing: a `kill -9` between the append and the save loses the override-fired state; resuming from the prior checkpoint produces a correct "edge was never traversed" replay. The save MUST happen before the next `SetEdgeSelection` to preserve this contract.
2. Continue normal edge traversal.

The idempotency check makes the "override + restart" case in D5 behave correctly: the second time the run traverses the override edge (after a `restart: true` loop-back), the sticky list does not gain a duplicate entry; the same `OverrideDetail` continues to anchor the status implication.

### 5.5 Terminal-status rule (failure dominates)

After the engine's main loop exits via the success path (current code at the end of `Run` and at the success branch of `handleExitNode` engine_run.go:805+), the rule is:

```
if len(s.validationOverrides) > 0:
    EngineResult.Status = OutcomeValidationOverridden
else:
    EngineResult.Status = OutcomeSuccess
```

Failure paths (`failResult`, `cancelledResult`, `handleLoopRestart` exhaustion) build `EngineResult` with `Status = OutcomeFail` (or `OutcomeBudgetExceeded`) and ignore the sticky list. This implements "failure dominates": any failure after an override produces `Status = fail`, but `runState.validationOverrides` is still persisted on the checkpoint and `EventValidationOverridden` is still in the activity log.

**D9 invariant enforcement.** `handleOutcomeStatus` (engine_run.go:704) currently treats any unknown `Outcome.Status` value as a hard failure. With this work the engine's terminal-status rule (§5.5) is the single authority for writing `validation_overridden` — a handler that returns `Outcome{Status: "validation_overridden"}` would have that value land in `ctx.outcome` (which is a separate routing surface) but the engine post-loop rule reads `runState.validationOverrides` length, not the last `ctx.outcome` value, to set the terminal status. The result: a handler returning the value has it silently overwritten when the engine writes `EngineResult.Status`. A test (§11.1) pins this behavior so a future contributor's misuse fails loud or is at least documented.

`EngineResult` gains a field:

```go
ValidationOverrides []OverrideDetail
```

Populated from `s.validationOverrides` for every terminal path (success, fail, budget, override) — failure paths still expose the list so post-hoc readers can see "this run had an override AND it failed."

### 5.6 Subgraph propagation

`Outcome` gains one new field (**v2 correction**: a slice, not a pointer):

```go
// pipeline/handler.go
ChildOverride []OverrideDetail // populated by SubgraphHandler / ManagerLoopHandler / ParallelHandler when a child run hit override edges
```

In `pipeline/subgraph.go`:

1. Extend the status mapping switch to treat `OutcomeValidationOverridden` like `OutcomeSuccess` for parent routing:
   ```go
   case OutcomeSuccess, OutcomeBudgetExceeded, OutcomeValidationOverridden:
       status = OutcomeSuccess
   ```
2. If `result.ValidationOverrides` is non-empty, copy it into `Outcome.ChildOverride`, **prepending the current subgraph node ID to each entry's `SubgraphPath`**.

**Recursive case.** The prepend rule produces the correct outermost-to-innermost ordering at arbitrary nesting depth:

- Innermost engine fires override at `EscalateReview` → `EngineResult.ValidationOverrides = [{GateNodeID: "EscalateReview", SubgraphPath: nil, ...}]`
- Middle subgraph handler (`Inner`) reads child result, prepends `"Inner"` → `Outcome.ChildOverride = [{GateNodeID: "EscalateReview", SubgraphPath: ["Inner"], ...}]`
- Outermost subgraph handler (`Outer`) reads its child's `ValidationOverrides` (which now carries `SubgraphPath: ["Inner"]`), prepends `"Outer"` → `Outcome.ChildOverride = [{GateNodeID: "EscalateReview", SubgraphPath: ["Outer", "Inner"], ...}]`

So at depth N, `SubgraphPath` has length N-1 (the chain of subgraph node IDs from outermost to innermost), and `GateNodeID` is the leaf gate. Audit-header rendering at §9.2 displays this as `Outer/Inner/EscalateReview`.

In the engine's `applyOutcome` path (after the handler returns), if `len(outcome.ChildOverride) > 0`, append each entry to `runState.validationOverrides` and `runState.cp.ValidationOverrides`. The parent's terminal-status rule then naturally lifts to `validation_overridden`. **This is the second sticky-write site** (the first is the flip-point at §5.4); they are intentionally distinct because they cover different sources (own-graph edges vs. child-propagated entries).

Same wiring in `pipeline/handlers/manager_loop.go`: the existing `OutcomeBudgetExceeded` special-case at line 709 becomes an allowlist of `{success, budget, validation_overridden}`. Manager_loop's child-success branch (line 667) also populates `Outcome.ChildOverride` when the child's `ValidationOverrides` is non-empty, mirroring the subgraph path. `stack.child.exit_status` context key continues to carry the underlying `EngineResult.Status` for operator introspection.

### 5.6.1 Parallel + override aggregation

`ParallelHandler` (`pipeline/handlers/parallel.go`) dispatches branches concurrently. Branches can themselves be subgraph nodes whose child engine fires an override edge. Without explicit handling, `ParallelResult` would drop the `ChildOverride` from each branch outcome.

**The aggregation rule**: `ParallelHandler.Execute` collects each branch's `Outcome.ChildOverride` (populated by the branch's own subgraph wiring), unions them in branch-result-order, and populates the parallel node's own returned `Outcome.ChildOverride`. The engine's `applyOutcome` then appends to the parent sticky list exactly as it would for a non-parallel propagation.

`ParallelResult` (the JSON-serializable per-branch result) does not need a `ChildOverride` field — the aggregation happens at the `Outcome` level after `ParallelResult` is assembled, before the parallel node's overall outcome returns to the engine. If `ParallelResult` is exposed to workflow authors via `ctx.parallel.results[N]`, the override information is also accessible via the parent's `EngineResult.ValidationOverrides` after the run completes.

**Fan-in note**: `FanInHandler` (`pipeline/handlers/fanin.go`) aggregates branch statuses for routing purposes. It does NOT propagate `ChildOverride` — that propagation already happened at the parallel-handler level. Fan-in is purely a routing-direction join.

### 5.7 Override + restart + goal-gate retry

- **Override + restart on same edge**: sticky is set at edge traversal, persists across the restart loop. `clearDownstream` and `handleLoopRestart` MUST NOT clear `runState.validationOverrides` or `Checkpoint.ValidationOverrides`. Test coverage in §11.
- **Override followed by goal-gate retry**: same rule — sticky persists across the goal-gate jump.
- **Override followed by max-restart exhaustion**: terminal status is `fail` (failure dominates), `ValidationOverrides` still on the result for forensics.

### 5.8 What does NOT change

- Handlers' `Outcome.Status` taxonomy in `pipeline/handler.go` (still `success` / `fail` / `retry`).
- `ctx.outcome` value (still `success` / `fail`).
- Conditional edge evaluation in `pipeline/condition.go`.
- The strict-failure-edges rule.
- Parallel branch *routing* in `pipeline/handlers/parallel.go` — branches still dispatch the same way; only the `ChildOverride` propagation is added (§5.6.1).
- `pipeline.Interviewer` interface signature — the `Actor()` method is queried via opportunistic interface assertion (§5.3.1), so third-party implementations remain compatible.

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
        // v2 note on shape: array, not slash-joined string. Earlier draft
        // proposed strings.Join with "/" but that's ambiguous if a subgraph
        // node ID contains "/". An array is the safe wire form.
        entry.OverrideSubgraphPath = append([]string(nil), e.Override.SubgraphPath...)
    }
}
```

Field naming on the JSONL `entry` struct: `override_gate`, `override_label`, `override_actor`, `override_subgraph_path` (JSON array of strings) — symmetric with the nested `OverrideDetail` shape so consumers don't have to switch on which JSON surface they're parsing.

The `Timestamp` field on `OverrideDetail` is intentionally NOT mirrored as `override_timestamp` on the JSONL flat fields — it would duplicate the event line's existing `Timestamp` (`ts`) field. Consumers that read JSONL line-by-line use `ts`; consumers that round-trip through `Checkpoint.ValidationOverrides` get the standalone timestamp (where no event-line timestamp exists).

### 6.4 `classifyStatus` algorithm

**v2 reordering**: the v1 algorithm had an unreachable fallback because the `cp.CurrentNode != ""` check returned before the `ValidationOverrides` fallback could run. The reordered algorithm:

```go
// tracker_audit.go classifyStatus
//
// Algorithm: reverse-scan activity for terminal events; on terminal
// events, "failure dominates" — fail and budget_exceeded short-circuit.
// If no terminal event found, fall back to checkpoint signals.
//
//   1. Reverse-scan activity entries:
//      - On pipeline_failed: return "fail" immediately.
//      - On budget_exceeded: return "budget_exceeded" immediately.
//      - On pipeline_completed: remember "saw completion," keep scanning
//        earlier for validation_overridden. If found before reaching the
//        start of the log, return "validation_overridden". If reached the
//        log start without finding override, return "success".
//      - On validation_overridden with no later completion event seen yet
//        in the reverse scan: continue scanning (a later completion may
//        still come up earlier in the scan; on reaching log start without
//        finding completion, return "validation_overridden" — run halted
//        at the override).
//   2. If no terminal activity event found:
//      a. If len(cp.ValidationOverrides) > 0 AND cp.CurrentNode == "":
//         return "validation_overridden" (run completed, log was lost,
//         but checkpoint preserved the override signal).
//      b. If cp.CurrentNode != "": return "fail" (run halted mid-graph).
//      c. Otherwise (no overrides, no current node): return "success".
```

**Resumed runs** (same runID, multiple `pipeline_started` markers in one log): the override should persist across resume. Don't anchor at the latest `pipeline_started` — the reverse scan walks past the resumed run's `pipeline_completed`, sees the original run's `validation_overridden`, and classifies `validation_overridden`. That's the desired answer ("an override anywhere in the audit history of this run-id classifies it as `validation_overridden`, unless a later failure or budget breach dominates"). Activity logs are per-runID (`SecureActivityLogPath` keys on runID), and `Audit` is scoped to one runDir, so cross-run contamination is impossible.

The fallback at step 2.a closes the lost-activity-log gap: even if the activity log is missing or truncated, the checkpoint-persisted slice surfaces the audit signal — but **only when `cp.CurrentNode == ""`** (the run actually completed). A halted run with overrides still classifies as `fail` per step 2.b (failure dominates over the override-fallback signal when the run never reached the exit).

**Resumed runs** (same runID, multiple `pipeline_started` markers in one log): the override should persist across resume. Don't anchor at the latest `pipeline_started` — the reverse scan walks past the resumed run's `pipeline_completed`, sees the original run's `validation_overridden`, and classifies `validation_overridden`. That's the desired answer ("an override anywhere in the audit history of this run-id classifies it as `validation_overridden`, unless a later failure or budget breach dominates"). Activity logs are per-runID (`SecureActivityLogPath` keys on runID), and `Audit` is scoped to one runDir, so cross-run contamination is impossible.

### 6.5 Fix the `budget_exceeded` collapse

In the same change, `classifyStatus` stops collapsing `budget_exceeded` to `"fail"`. `AuditReport.Status` / `RunSummary.Status` doc-comments update to enumerate `"success" | "fail" | "budget_exceeded" | "validation_overridden"` and explicitly mark the set as open. CLI surfaces (cmd/tracker/audit.go's run-list icons, summary.go's status header switch) gain a `budget` row and an `override` row.

## 7. Dippin-lang IR and adapter

### 7.1 dippin-lang change

Tagged release of `2389-research/dippin-lang` (must be a real version tag, NOT a pseudo-version pin — the v0.32.0 dippin release hit a transient-behavior window when tracker pinned a pseudo-version before the real tag landed; same pattern to avoid here):

- Add `Override bool` to the `ir.Edge` struct.
- Parser accepts `override: true` (or `override: false` as default-equivalent) as an edge attribute, position-flexible alongside `label:`, `when:`, `restart:`. Allowed in normal edges and inside `parallel: { … }` block syntax (for parallel-branch edges).
- JSON IR field name: `"override"` (boolean, peer of `restart`, `label`, `condition`).
- `override: false` round-trips: the IR omits the field when false; the parser tolerates explicit `override: false`.

This lands first; tracker bumps the go module dependency AND the `PinnedDippinVersion` constant (`tracker_doctor.go:26`) to the tagged release in the same PR that ships the rest of this work. `TestPinnedDippinVersionMatchesGoMod` enforces the lockstep.

### 7.2 Tracker adapter

`pipeline/dippin_adapter.go convertEdge` maps `IREdge.Override` → `pipeline.Edge.Override` (new first-class typed field on the `Edge` struct in `pipeline/graph.go`, symmetric to existing typed fields `Condition`, `Label`, `From`, `To`). NOT in `Edge.Attrs` map — engine reads the typed field. The existing `restart` semantic continues to live in `Edge.Attrs["restart"]` (it is NOT a typed field today; v1 spec incorrectly implied otherwise). `Override` joins the typed crew because it gates engine-terminal-status behavior and must be cheap to read in the hot edge-selection path.

### 7.3 Validation (adapter compile-time)

The adapter validates **at graph-construction time**:

1. `override: true` is rejected on any edge whose originating node's resolved handler is not `wait.human`. (**v2 correction**: v1 also allowed `webhook`. Removed — webhook is an Interviewer flavor on the `wait.human` handler, not a separate node type. Webhook eligibility is runtime-detected via `Interviewer.Actor()` per §5.3.1.) Error message: `edge X→Y has override: true but X is not a wait.human gate`.
2. `override + restart` is allowed (D5). No restriction.
3. `override + when` is allowed (conditional override edges are sensible — "only count as override if outcome=fail").
4. All `wait.human` modes are valid origin nodes for override edges: `freeform`, `yes_no`, `interview`, `choice`, `hybrid`. The validation rule keys off handler name, not mode.
5. Backend overrides (`backend: claude-code`) do not affect validation. Handler dispatch is at the `Handler` interface; the resolved handler name is still `wait.human`.

Rejected handler types (explicit): `tool`, `agent` (codergen), `parallel`, `parallel.fan_in`, `conditional`, `subgraph`, `stack.manager_loop`, exit-shape nodes, start nodes.

`dippin doctor` surfaces rule (1) as a hard error per its existing convention. The adapter and `dippin doctor` both call the same validation helper so the error message is identical in both contexts.

### 7.4 New `dippin doctor` lint rule: TRK102 (D14)

Add lint code `TRK102` (confirmed next-available in the TRK1XX series — TRK101 is the only existing entry in `pipeline/lint_tracker.go`). The rule warns on **unmarked-but-looks-like-override** edges:

> "TRK102: edge from `wait.human` node `EscalateReview` to forward-progress node `Cleanup` via label `accept` is not marked `override: true`. The gate is reachable from an upstream failure (e.g., `FinalSpecCheck -> EscalateReview when ctx.outcome = fail`), which suggests this edge represents a human accepting a failed validation. Add `override: true` to record the audit signal."

**Heuristic** (all four predicates must hold):

1. Source node is a `wait.human` handler.
2. Edge label matches `accept` / `mark done` / `approve` (case-insensitive).
3. Target node is reachable from the run's exit node without passing through another gate.
4. Source gate is reachable via at least one incoming edge predicated on `outcome = fail` (or marked with a `when ctx.outcome = fail` condition transitively upstream).

Predicate 4 is the disambiguator that suppresses false positives on plan-approval gates: `ApprovePlan`, `ApproveSpec`, `ReviewPlan` — those gates have only `outcome = success` upstream edges (or no conditional predicate at all), so the heuristic skips them. (**v2 strengthening**: v1's heuristic was 3 predicates and would have false-positive on plan-approval flows. The 4-predicate rule is testable; see §11.9.)

Warn, do not fail. The warning text suggests the one-line edit and explains the audit-status consequence in one sentence. External workflow authors who don't want the new noise can `// dippin-doctor: TRK102 silenced` in the workflow header (per dippin's existing convention).

## 8. Library API delta

This is a **library-API change** — `EngineResult.Status` is part of the exported surface (CLAUDE.md release process). CHANGELOG entry goes under `Changed` with the explicit "library-API delta" call-out.

### 8.1 New / changed exported surfaces

| Symbol | Change |
|--------|--------|
| `pipeline.TerminalStatus` | **New** named string type. Existing constants (`OutcomeSuccess`, `OutcomeFail`, `OutcomeBudgetExceeded`) re-typed. |
| `pipeline.OutcomeValidationOverridden` | **New** constant, value `"validation_overridden"`. |
| `pipeline.TerminalStatus.IsSuccess()` | **New** method. Returns true for `{success, validation_overridden}`. |
| `pipeline.OverrideDetail` | **New** struct. |
| `pipeline.Actor` | **New** named string type (v2 addition to table). |
| `pipeline.ActorHuman` / `ActorAutopilot` / `ActorWebhook` / `ActorUnknown` | **New** constants (v2 addition to table; four values per D6). |
| `pipeline.Outcome.OverrideActor` | **New** field, `Actor`. Populated by HumanHandler from its bound interviewer's `Actor()`. v2 addition: the channel that solves the actor-detection load-bearing gap. |
| `pipeline.Edge.Override` | **New** bool field. |
| `pipeline.Outcome.ChildOverride` | **New** field, **`[]OverrideDetail`** (slice, v2 correction from v1's pointer). |
| `pipeline.EngineResult.Status` | **Re-typed** from `string` to `TerminalStatus` (D10). Existing literal-string comparisons still work; assignments from a `string` variable need a cast. |
| `pipeline.EngineResult.ValidationOverrides` | **New** field, `[]OverrideDetail`. |
| `pipeline.Checkpoint.ValidationOverrides` | **New** field, `[]OverrideDetail` (JSON `validation_overrides`, `omitempty` for backwards compat with v0.34 checkpoints). |
| `pipeline.EventValidationOverridden` | **New** event constant. |
| `pipeline.PipelineEvent.Override` | **New** field, `*OverrideDetail`. |
| `pipeline.DecisionDetail.EdgePriority` | New value `"override"` added to the existing enum (D13). |
| `tracker.Result.Status` | **Doc-comment added** enumerating `{success, fail, budget_exceeded, validation_overridden}`, marked as open enum. Also re-typed to `pipeline.TerminalStatus`. Today has no doc-comment at all. |
| `tracker.Result.ValidationOverrides` | **New** field, mirrored from `EngineResult.ValidationOverrides`. |
| `tracker.AuditReport.Status` | **Doc-comment fixed** to enumerate four values (currently lies as `"one of: success, fail"`). Re-typed to `pipeline.TerminalStatus`. |
| `tracker.AuditReport.ValidationOverrides` | **New** field. |
| `tracker.RunSummary.Status` | **Doc-comment fixed** to enumerate four values. Re-typed to `pipeline.TerminalStatus`. |
| `tracker.RunSummary.OverrideCount` | **New** field, `int` (count only — RunSummary stays thin for listing). |
| `tracker.DiagnoseReport.ValidationOverrides` | **New** field (v2 addition; was missing from v1's §8.1 table despite being introduced in §9.4). |
| `tracker.DiagnoseReport.OverrideCount` | **New** field. |

### 8.2 CLI exit-code fixes

`cmd/tracker/run.go:313` (`interpretRunResult`) and `cmd/tracker/run.go:696` (`runPipelineAsync`) currently treat `result.Status != pipeline.OutcomeSuccess` as an error. Both rewrite to use `TerminalStatus.IsSuccess()`:

```go
if !result.Status.IsSuccess() {
    return fmt.Errorf("pipeline finished with status: %s", result.Status)
}
```

(`result.Status` is now typed `TerminalStatus` per D10, so the method call is direct.)

**Exit code wiring for `--fail-on-override`.** Cobra exits with code 1 on any returned error — there is no per-error-shape distinction today. To produce a distinct exit code 2 on override:

```go
// pipeline/engine.go (new exported sentinel)
var ErrValidationOverridden = errors.New("run completed via validation_overridden")

// cmd/tracker/run.go interpretRunResult (rewritten)
func interpretRunResult(result *pipeline.EngineResult, cfg *runConfig) error {
    if result.Status == pipeline.OutcomeValidationOverridden && cfg.FailOnOverride {
        head := headlineOverride(result.ValidationOverrides) // latest entry per D5a
        fmt.Fprintf(os.Stderr,
            "tracker: run completed via %s at %s (label %q); --fail-on-override caused non-zero exit\n",
            result.Status, head.GateNodeID, head.Label)
        return ErrValidationOverridden
    }
    if !result.Status.IsSuccess() {
        return fmt.Errorf("pipeline finished with status: %s", result.Status)
    }
    return nil
}

// cmd/tracker/main.go (cobra entry, near os.Exit)
if errors.Is(err, pipeline.ErrValidationOverridden) {
    os.Exit(2)
}
if err != nil {
    os.Exit(1)
}
```

`runPipelineAsync` (the TUI/async path) gets the same sentinel return — the cobra entry handles the exit-code branching in one place.

**Precedence**: when both `--fail-on-override` and a genuine failure or budget breach hit, failure dominates. Status is `fail` (or `budget_exceeded`), `IsSuccess()` returns false, exit code 1 (or whatever budget's exit becomes after D12 — currently 1). The `--fail-on-override` exit-2 path is reached only when `result.Status == OutcomeValidationOverridden`.

`cmd/tracker/summary.go:447` (`printResumeHint`) uses the same `IsSuccess()` check so override runs don't print a misleading "Resume" hint.

### 8.3 JSON `status_class` field — applied to ALL JSON surfaces (D15)

Every JSON surface that emits `status` adds `status_class` alongside:

| Surface | File | Behavior |
|---------|------|----------|
| `cmd/tracker-conformance` JSON | `cmd/tracker-conformance/main.go:1001` | `status` raw, `status_class` paired |
| `tracker --json` NDJSON `pipeline_completed` event | `pipeline/events_jsonl.go` | new `status` + `status_class` fields on the completion event entry |
| `tracker audit --json` (if a `--json` flag is added — currently not in the audit CLI but the underlying `AuditReport` is JSON-serializable via the library API) | `tracker_audit.go` | `AuditReport.Status` and a new `AuditReport.StatusClass` field |
| `tracker list --json` (same shape) | `tracker_audit.go RunSummary` | `RunSummary.Status` and a new `RunSummary.StatusClass` field |

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

The first line renders gate + label + actor inline. **When `len(ValidationOverrides) > 1`, the headline picks the *latest* entry** (per D5a) — most operationally useful, answers "what most directly led to this run being overridden." The full chain is visible in the `Override:` line(s) below and in `EngineResult.ValidationOverrides`.

The `Override:` line(s), shown only when `len(report.ValidationOverrides) > 0`, trace the routing: the failed node that triggered the gate → the override edge taken → the next non-gate target. Multiple overrides render one `Override:` line each, in chronological (append) order.

For overrides propagated from a subgraph, the gate node renders as `subgraph_path/gate_node_id` joined with `/` — e.g., `RunChildPipeline/EscalateReview` (one-level) or `Outer/Inner/EscalateReview` (two-level). The path is built from `OverrideDetail.SubgraphPath` (the array of subgraph node IDs) joined with `OverrideDetail.GateNodeID`.

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

`cmd/tracker/summary.go:192` status header gets two new cases (override + budget). Override uses the amber color treatment per D18 — if the lipgloss palette has no amber entry, add a new `colorOverride` constant set to `lipgloss.Color("#D97706")` (matches Tailwind's amber-600, distinct from green success and red fail).

Format: `● validation_overridden — at EscalateReview (label "accept")`. Where `●` is the existing color-bearing glyph, the dash separator is rendered in mutedStyle, and the location-detail picks the headline override per D5a.

For `NO_COLOR` environments, the same glyph (`●`) renders without color and the textual status string itself ("validation_overridden") is the unambiguous signal.

`cmd/tracker/summary.go:164`'s `OutcomeBudgetExceeded` special-case stays; the override case is its sibling.

### 9.6 TUI live rendering (D17)

`tui/messages.go` `MsgPipelineCompleted` gains a typed `Status` field:

```go
type MsgPipelineCompleted struct {
    Status pipeline.TerminalStatus
    // Override is non-nil when Status == OutcomeValidationOverridden, carrying
    // the headline override entry for display in the completion row.
    Override *pipeline.OverrideDetail
}
```

`tui/adapter.go` (line 25-26 currently maps `EventPipelineCompleted` to an empty `MsgPipelineCompleted{}`) reads `EngineResult.Status` and `EngineResult.ValidationOverrides[len-1]` (the headline) when constructing the message.

The TUI's completion row renders:

- `success`: green check ✓ + "Completed"
- `validation_overridden`: **amber** ● + "Completed — validation override at `<gate>` (`<label>` by `<actor>`)"
- `budget_exceeded`: red ✗ + "Budget exceeded"
- `fail`: red ✗ + "Failed at `<node>`"

This closes the v1 gap where the TUI displayed an identical green "Completed" badge for both `success` and `validation_overridden` — defeating the audit-visibility purpose of the entire feature for operators watching the run in real-time.

For `NO_COLOR` / monochrome terminals, the bullet character + textual status string provide the same signal without color.

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

Auto-marking is rejected (see §3 non-goals: "Auto-marking existing override-shaped edges across the workflow corpus"). Changing every existing override-shaped run's terminal status overnight would break consumers comparing `Result.Status == "success"`. Workflow authors opt in per edge. The `dippin doctor TRK102` warning (D14) surfaces the opportunity for un-migrated workflows.

(**v2 fix**: v1 cited "D7's flip side" as the rationale; D7 is about sticky storage shape, not migration policy. Corrected to reference §3 non-goals.)

### 10.3 Edges NOT marked — abandon, reject, and terminate-to-Done

Several existing edges look gate-shaped but should NOT be marked `override: true` because they route to `Done` as a deliberate abandonment, not a forward-progress acceptance:

| File | Edge | Mark? | Reason |
|------|------|-------|--------|
| build_product.dip:1316 | `ApprovePlan → Done label: "reject"` | No | Human rejects the plan; run terminates as `fail` (failure dominates, no override). |
| build_product.dip:1337 | `EscalateMilestone → Done label: "abandon"` | No | Human abandons; run terminates as `fail`. |
| build_product.dip:1372 | `EscalateReview → Done label: "abandon"` | No | Human abandons; run terminates as `fail`. |
| build_product_with_superspec.dip:1017 | `EscalateToHuman → Done label: "abandon"` | No | Same. |
| ask_and_execute.dip:417 | `EscalateToHuman → Done label: "abandon"` | No | Same. |

**The rule**: `override: true` marks edges that take a known-failed validation and route to **forward-progress** (eventually exiting via a clean success path). Edges that route to `Done` from an `abandon` / `reject` label are deliberate failure terminations — terminal status is `fail`, no override needed (and TRK102 should not warn on them because the heuristic predicate "target is reachable from exit without passing through another gate" still holds, but the label `abandon` / `reject` doesn't match the `accept` / `mark done` / `approve` heuristic-label list).

## 11. Testing strategy

Tests use inline graph fixtures (matching `pipeline/engine_test.go`'s existing pattern) unless explicitly noted. Where new `.dip` fixture files are introduced, they live in `pipeline/testdata/override/` for engine + lint tests and in `cmd/tracker-conformance/fixtures/override.dip` for conformance.

### 11.1 Engine

- New override-edge fires sticky and emits `EventValidationOverridden`.
- Override-then-success-exit: `EngineResult.Status == OutcomeValidationOverridden`.
- Override-then-fail: `Status == OutcomeFail`, `ValidationOverrides` still populated.
- Override-then-budget-exceeded: `Status == OutcomeBudgetExceeded`, `ValidationOverrides` still populated.
- Override + restart: sticky persists across the restart loop; **idempotency: re-traversal of the same override edge during restart does NOT append a duplicate `OverrideDetail`** (D5a / §5.4 idempotency check). Final `len(ValidationOverrides) == 1` even after N retraversals.
- Override + goal-gate retry: sticky persists across the goal-gate jump; terminal status `validation_overridden`.
- Override + max-restart exhaustion: `Status == fail`, `ValidationOverrides` on result.
- Multiple overrides in one run (two different override edges, in chronological order): both entries land on `ValidationOverrides` and `Checkpoint.ValidationOverrides`. Audit headline (per D5a) picks the second/latest entry.
- Resume after override (single-attempt): load checkpoint with populated `ValidationOverrides`, run continues, sticky preserved.
- **Crash-resume durability**: simulate `kill -9` between sticky append and `saveCheckpointWithTag`. Resume from prior checkpoint replays the edge selection; the override fires fresh on replay (no duplicate). Final state identical to single-clean-run.
- **Multiple resumes** (3+ attempts): override on attempt 1, resume to attempt 2 (clean exit), resume again to attempt 3 — `Status == validation_overridden`, override count == 1.
- **Actor enumeration**: one test per Actor mapping per §5.3.1 table. Test interviewer fixture returns each `Actor` value via the `Actor()` interface assertion; `OverrideDetail.Actor` matches.
  - `BubbleteaInterviewer` → `ActorHuman`
  - `ConsoleInterviewer` → `ActorHuman`
  - `AutopilotInterviewer` → `ActorAutopilot`
  - `ClaudeCodeAutopilotInterviewer` → `ActorAutopilot`
  - `AutoApproveInterviewer` → `ActorAutopilot`
  - `WebhookInterviewer` → `ActorWebhook`
  - Test-only interviewer with no `Actor()` method → `ActorUnknown`
- **D9 invariant**: a handler returning `Outcome{Status: "validation_overridden"}` has its value overwritten by the engine's terminal-status rule. After execution, `EngineResult.Status` reflects `len(runState.validationOverrides)`, not the handler's returned status.

### 11.2 Subgraph / manager_loop / parallel

- Child run hits override → parent's `Outcome.ChildOverride` populated with subgraph_path-qualified detail → parent's terminal status `validation_overridden`.
- **Two-level nesting**: `SubgraphPath` accumulates correctly (outermost-to-innermost: `["Outer", "Inner"]`).
- **Three-level nesting**: outer subgraph node `L1_node` contains middle subgraph `L2_node` contains inner pipeline with `EscalateReview` gate. After the override fires at the leaf, `SubgraphPath` at the outermost engine reads `["L1_node", "L2_node"]`, `GateNodeID == "EscalateReview"`. Tests the recursive prepend rule.
- Child override + parent's own override: both land on parent's `ValidationOverrides` (in chronological order).
- Child override + parent's own subsequent failure: `Status == fail`, `ValidationOverrides` still populated (failure dominates).
- **Parallel + override aggregation** (§5.6.1): parallel handler with two branches; one branch runs a subgraph that fires an override; the other branch runs cleanly. Parent's terminal status is `validation_overridden`; `ValidationOverrides` contains the one entry from the overriding branch.
- **Parallel + multiple overrides**: both branches' subgraphs fire overrides. Parent's `ValidationOverrides` contains both entries, union'd in branch-result-order.
- `manager_loop` child override: `stack.child.exit_status` context key carries the underlying child terminal status; parent's `ValidationOverrides` is populated.

### 11.3 Adapter

- `override: true` on a tool node edge → adapter rejects with specific error matching "edge X→Y has override: true but X is not a wait.human gate".
- `override: true` on a `wait.human` edge (each mode: `freeform`, `yes_no`, `interview`, `choice`, `hybrid`) → all five accepted.
- `override: true` on an `agent` (codergen) node edge → rejected.
- `override: true` on a `parallel` node edge → rejected.
- `override: true` on a `parallel.fan_in` node edge → rejected.
- `override: true` on a `conditional` node edge → rejected.
- `override: true` on a `subgraph` node edge → rejected (the override happens *inside* the child, not on the subgraph node's own edges).
- `override: true` on a `stack.manager_loop` node edge → rejected.
- `override: true` on an edge from a `wait.human` node with `backend: claude-code` → accepted (backend override does not affect handler-name resolution).
- `override + restart` (both on same edge) → accepted, no warning.
- `override + when` → accepted.
- `override + restart + when` (triple combination) → accepted.

### 11.4 Audit / classifyStatus

Test scenarios driven by inline fixture run dirs under `tracker_audit_test_data/` (matching the existing `testdata/runs/ok` and `testdata/runs/failed` pattern):

- override + complete → `validation_overridden`
- override + fail → `fail` (failure dominates)
- override + budget_exceeded → `budget_exceeded` (failure dominates)
- 2x override + complete → `validation_overridden` (latest detail wins for §9.2 headline)
- override at terminal (no later complete, `cp.CurrentNode != ""`) → `fail` (per §6.4 step 2.b)
- override at terminal (no later complete, `cp.CurrentNode == ""`) → `validation_overridden` (per §6.4 step 2.a)
- resumed run with override on attempt 1, complete on attempt 2 → `validation_overridden`
- **legacy run mapping** (no `ValidationOverrides` in checkpoint, no override event in activity log):
  - `pipeline_completed` last → `"success"` (unchanged)
  - `pipeline_failed` last → `"fail"` (unchanged)
  - `budget_exceeded` last → `"budget_exceeded"` (**v0.34 behavior was `"fail"`** — this is the D12 fix; see §14 CHANGELOG migration note)
  - No terminal event + `cp.CurrentNode != ""` → `"fail"` (unchanged)
  - No terminal event + `cp.CurrentNode == ""` → `"success"` (unchanged)
- secure log lost / file gone but `ValidationOverrides` populated on checkpoint AND `cp.CurrentNode == ""` → `validation_overridden` (fallback path)
- secure log lost AND `ValidationOverrides` empty AND `cp.CurrentNode == ""` → `success` (no signal anywhere; default to clean completion)
- secure log lost AND `ValidationOverrides` empty AND `cp.CurrentNode != ""` → `fail` (halted mid-graph)
- **Pre-existing budget collapse confirmation**: grep for any existing test in `tracker_audit_test.go` that asserts the v0.34 collapse — confirmed in v2 review there is **no such test**, so the D12 fix has no existing assertion to update. Document this absence in the test commit message.

### 11.5 JSONLEventHandler

- Round-trip: emit `EventValidationOverridden`, close handler, reload via `LoadActivityLog`, parse `override_*` fields correctly from both secure and snapshot paths.
- Sentinel applies to override events like every other event.
- Sentinel-stripped forgery attempt: write an override-shaped line WITHOUT the sentinel prefix; `Audit` counts it as `InjectedLines` and does NOT use the override fields.

### 11.6 CLI

- `tracker list` row renders the new statuses without column overflow (Status column widens to 10).
- `tracker audit <runID>` renders the `Override:` chain line.
- `tracker run` default exits 0 on override.
- `tracker run --fail-on-override` exits 2 on override; stderr line matches the §8.2 format.
- `TRACKER_FAIL_ON_OVERRIDE=1` has the same effect as the flag.
- `TRACKER_FAIL_ON_OVERRIDE=true` / `TRACKER_FAIL_ON_OVERRIDE=yes` / any non-`1` value → **falsy** (matches existing strict-`=1` convention from `TRACKER_PASS_API_KEYS` per v0.24.2). Override exits 0.
- **Precedence**: `--fail-on-override` + genuine fail → exit 1 (failure dominates over the override-strict flag).
- **Precedence**: `--fail-on-override` + budget_exceeded → exit 1 (failure dominates).
- TUI mode (`runPipelineAsync`) honors `--fail-on-override` identically to non-TUI mode.

### 11.7 Library API

- `EngineResult.Status` returns the new value where appropriate (typed `TerminalStatus`).
- `tracker.Result.ValidationOverrides` mirrored correctly.
- `OutcomeSuccess.IsSuccess() == true`
- `OutcomeValidationOverridden.IsSuccess() == true`
- `OutcomeFail.IsSuccess() == false`
- `OutcomeBudgetExceeded.IsSuccess() == false`
- `TerminalStatus("future_status").IsSuccess() == false` (fail-closed default per §5.1).
- Re-typing compatibility: `result.Status == "success"` still compiles and returns the expected boolean.
- `Checkpoint.ValidationOverrides` `omitempty` round-trips: v0.34 checkpoint (no field) → v0.35 deserializes as nil slice → re-serializes without the field.

### 11.8 Conformance

- `cmd/tracker-conformance` emits both `status` and `status_class` on all paths.
- Override-run conformance fixture (`cmd/tracker-conformance/fixtures/override.dip` — new file): a minimal workflow with one `wait.human` gate and one `override: true` edge, using `AutoApproveInterviewer` for deterministic non-LLM CI runs. Test asserts `status == "validation_overridden"` and `status_class == "succeeded"`.

### 11.9 dippin doctor TRK102

Lint rule tests use programmatically-constructed graphs (matching `lint_tracker_test.go:43 buildTRK101DangerousGraph` pattern):

- **Positive (fires)**: build a graph with a `wait.human` node, an outgoing labeled `"accept"` edge to a forward-progress node, AND an incoming edge predicated on `outcome = fail`. Confirm TRK102 fires.
- **Positive (label variants)**: same graph with labels `"mark done"`, `"approve"`, `"Accept"` (case-insensitive). All fire.
- **Negative — no failed-validation upstream**: `ApprovePlan` shape — `wait.human` with label `"approve"` but no incoming `outcome = fail` edge. Predicate 4 misses; **no warning**. This is the disambiguator that suppresses plan-approval false positives.
- **Negative — abandon/reject labels**: `EscalateReview → Done label: "abandon"` — source is `wait.human`, label is `"abandon"` (not in heuristic list); no warning.
- **Negative — target not exit-reachable**: gate label `"accept"` routes to a node that itself routes back through another gate before exit. No warning.
- **Negative — already marked**: edge has `override: true`. No warning.
- **Migrated-corpus negative**: after the §10 migration is applied, `dippin doctor` on all four example workflows produces **zero TRK102 warnings**.

### 11.10 Audit recommendation copy

`buildAuditRecommendations` output is tested with substring assertions on the templates from §9.3:

- For a single-override run: recommendations contain "This run terminated via a validation override" and "Validation override at gate" with the gate ID substituted.
- For a multi-override run: one summary entry + N per-override entries, in chronological order.
- Override notes appear **first** in the recommendations slice (no alphabetical sort).
- Retry/budget notes are absent unless they are also genuinely relevant.

## 12. Verification gates (per CLAUDE.md)

- `go build ./...` clean
- `go test ./... -short` — all 17 packages pass
- `TestPinnedDippinVersionMatchesGoMod` passes (lockstep between `go.mod` dippin-lang version and `tracker_doctor.go:26 PinnedDippinVersion`)
- `dippin doctor examples/{ask_and_execute,build_product,build_product_with_superspec,deep_review}.dip` — A grade on all four after the edge migrations in §10
- `dippin doctor` produces **zero TRK102 warnings** on the migrated workflows
- `dippin simulate -all-paths` on the three core pipelines — all paths terminate; override paths recognized
- `tracker audit` on a real run that exercises `EscalateReview` "accept" — confirm `Status: validation_overridden` with the gate / label / actor inline, `Override:` chain line present
- `tracker run --fail-on-override` on the same fixture run — exit code 2 confirmed; stderr line emitted

## 13. Open questions

(All v1 open questions resolved in v2.)

- ~~Color choice in `cmd/tracker/summary.go`~~ → **D18**: amber `#D97706` (Tailwind amber-600) or nearest existing lipgloss palette entry. Applies to TUI completion row, CLI summary header, and `tracker list` row.
- ~~`dippin doctor` lint code number~~ → **D14**: TRK102 (confirmed next-available in `pipeline/lint_tracker.go`).

## 14. CHANGELOG entry shape (Keep a Changelog format)

For `CHANGELOG.md` under `[Unreleased]` → `[0.35.0]`:

```markdown
## [0.35.0] — YYYY-MM-DD

### Added

- **New terminal `EngineResult.Status` value `validation_overridden`.** Runs that traverse a `wait.human` gate edge marked `override: true` in their `.dip` workflow now terminate with `Status == "validation_overridden"` instead of `"success"`, recording in the audit trail that a non-workflow decision (a human at the TUI, an autopilot persona, or a webhook callback) accepted a result the automated checks flagged as failed. The `Override:` line in `tracker audit` traces the routing; the `--json` output carries a stable `status_class: succeeded|failed` companion field for downstream consumers. Closes Gap 5.2 of #233 (#271).
- **New edge attribute `override: true`** in `.dip` syntax, valid only on edges from `wait.human` nodes. Adapter rejects misuse at compile-time. See the `validation_overridden` design doc for the per-edge guidance.
- **New CLI flag `--fail-on-override`** (and env var `TRACKER_FAIL_ON_OVERRIDE=1`, strict-`=1` parsing matching the existing `TRACKER_PASS_*` convention). Causes `tracker run` to exit code 2 (distinguishable from generic-fail exit 1) when a run terminates as `validation_overridden`. Default unchanged: exit 0.
- **New `dippin doctor` lint rule TRK102** (warn-level): fires on `wait.human` edges with label `accept` / `mark done` / `approve` (case-insensitive) that route to a forward-progress target, are upstream-reachable from an `outcome = fail` edge, and lack `override: true`. Surfaces the migration opportunity without false-positive on plan-approval gates.
- **TUI live override rendering**: `MsgPipelineCompleted` gains a typed `Status` field; the TUI completion row renders the amber override badge in real-time. No more identical green badges for `success` and `validation_overridden`.

### Changed

- **Library-API delta**: `EngineResult.Status`, `tracker.Result.Status`, `tracker.AuditReport.Status`, `tracker.RunSummary.Status` are re-typed from `string` to `pipeline.TerminalStatus` (a named string type). Existing literal-string comparisons (`result.Status == "success"`) continue to compile and produce the same answer. Assignments from a `string` variable into a `Status` field require an explicit cast — this is the one breaking change in the re-typing. Embedded library callers should migrate to the new `TerminalStatus.IsSuccess()` helper for forward-compat across future status additions.
- **New exported symbols** (additive): `pipeline.TerminalStatus` (type), `pipeline.OutcomeValidationOverridden`, `pipeline.OverrideDetail`, `pipeline.Actor` (type), `pipeline.ActorHuman` / `ActorAutopilot` / `ActorWebhook` / `ActorUnknown`, `pipeline.Edge.Override`, `pipeline.Outcome.ChildOverride`, `pipeline.Outcome.OverrideActor`, `pipeline.EngineResult.ValidationOverrides`, `pipeline.Checkpoint.ValidationOverrides`, `pipeline.EventValidationOverridden`, `pipeline.PipelineEvent.Override`, `pipeline.ErrValidationOverridden`. Plus mirrored fields on `tracker.Result`, `tracker.AuditReport`, `tracker.RunSummary`, `tracker.DiagnoseReport`.
- `tracker_audit.AuditReport.Recommendations` is no longer alphabetically sorted — entries appear in priority order (override notes first, then per-override entries chronologically, then retry/budget notes). Downstream tools that sort on receipt are unaffected; tools that displayed in receive-order will see a different order.

### Fixed

- **`tracker_audit.classifyStatus` previously collapsed `budget_exceeded` events to `"fail"`**, so `tracker audit` and `tracker list` reported budget-halted runs as failures. Audit surfaces now surface `budget_exceeded` correctly. **User-visible behavior change**: scripts filtering on `status == "fail"` will see budget-halted runs move out of the `fail` bucket into a new `budget_exceeded` bucket. Update filters to `status_class == "failed"` (introduced in this release) for a stable bucket that survives future enum extensions. `EngineResult.Status` on budget-halted runs was already `budget_exceeded` since `OutcomeBudgetExceeded` shipped — this fix aligns the audit surface with the engine surface, closing a long-standing reporting gap.

### Requires

- `dippin-lang vX.Y.Z` (a real tag, not a pseudo-version pin — see release sequence in §16). `PinnedDippinVersion` bumped in lockstep with `go.mod` in the same commit.

### Migration for embedded library callers

```go
// Before:
if result.Status == pipeline.OutcomeSuccess {
    deploy()
}

// After (treats validation_overridden as success — opt into the new audit-positive semantic):
if result.Status.IsSuccess() {
    deploy()
}

// To gate deploy on overrides explicitly:
if result.Status.IsSuccess() && len(result.ValidationOverrides) == 0 {
    deploy()
}
```

If your code only ever cared about "completed cleanly without further action," no change is needed — `IsSuccess()` returns the same value for `OutcomeSuccess` as the old string comparison did. Override runs now classify as `IsSuccess() == true` (audit-positive) rather than as failures.

### Operator notes (monitoring & CI integrations)

Two surfaces silently change behavior on upgrade:

1. **Monitoring dashboards filtering JSON output on `status == "success"` will silently undercount completed runs.** Override runs surface as `status == "validation_overridden"` — update filters to `status_class == "succeeded"` (the new stable open-enum-tolerant companion field) or to the union `status IN ("success", "validation_overridden")`.
2. **Scripts counting failed runs via `status == "fail"` will see budget-halted runs disappear from the failure bucket** (D12 fix). Update to `status_class == "failed"` for a stable bucket.

For CI integrations that should NOT auto-deploy on overrides:

```bash
tracker run --fail-on-override workflow.dip && deploy.sh
```

Without `--fail-on-override`, override runs exit 0 and `deploy.sh` will fire — this is the intended default (an override is a deliberate operator decision), but CI integrators should opt in to strict mode.

## 15. Out of scope (deferred)

(De-duplicated from §3 non-goals — this section enumerates items NOT addressed; the rationale lives in §3.)

- `OutcomeCancelled` for ctx-cancellation runs (currently `fail`). Sibling lossy-collapse worth a separate issue and a follow-up release.
- Per-line HMAC or stronger forgery defense for the activity log. (See #213 threat model; sentinel is detection, not authentication.)
- General `tracker.Result.ChildRuns []ChildRunSummary` programmatic cross-run inspection API. Subgraph propagation here is scoped to `ValidationOverrides` only.
- `--fail-on-override` extended to fire on `budget_exceeded` or other future statuses. Today the flag is override-specific; future statuses get their own knobs.

## 16. Release sequence

Per CLAUDE.md, merging the `release: vX.Y.Z` PR is NOT the release — the tag push is. Two prior releases (v0.19.0, v0.20.0) missed this. Explicit sequence for v0.35.0:

1. **dippin-lang PR**: add `Override bool` to `ir.Edge`, parser grammar, JSON field. Lands on `2389-research/dippin-lang` main.
2. **dippin-lang tag and release**: tag `vX.Y.Z` (a real tag, not a pseudo-version pin). Push tag. Confirm GitHub release page exists for that tag before opening the tracker PR.
3. **tracker implementation PR** (single PR, ordered commits to keep CI green):
   - Commit 1: bump `go.mod` dippin-lang to the new tag + bump `tracker_doctor.go PinnedDippinVersion` constant. (Lockstep enforced by `TestPinnedDippinVersionMatchesGoMod`.)
   - Commit 2: adapter changes (`Edge.Override` field, validation, conversion).
   - Commit 3: engine changes (`TerminalStatus` type, `OutcomeValidationOverridden`, sticky machinery, classifyStatus reorder).
   - Commit 4: event taxonomy, JSONL wire format.
   - Commit 5: subgraph + manager_loop + parallel propagation.
   - Commit 6: CLI changes (`run.go` exit codes, summary, audit, list).
   - Commit 7: TUI live rendering (`tui/adapter.go`, `tui/messages.go`).
   - Commit 8: library API surface updates (`tracker.go`, `tracker_audit.go`, `tracker_diagnose.go`).
   - Commit 9: dippin doctor TRK102 lint rule.
   - Commit 10: workflow migration (`examples/*.dip` edits per §10).
   - Commit 11: tests.
   - Commit 12: documentation surface updates (per §17).
   - (Squash on merge — single squashed commit lands on `main` with the full set.)
4. **Implementation PR merges to `main`.**
5. **`release: v0.35.0` PR**: updates `CHANGELOG.md` (Unreleased → 0.35.0 header), `README.md` "Previously in" callout, `site/content/changelog.html` v0.35.0 entry.
6. **Release PR merges to `main`.**
7. **Tag and push**: `git tag -a v0.35.0 <release-merge-sha> -m "release: v0.35.0"` then `git push origin v0.35.0`. The tag push triggers `.github/workflows/release.yml` → GoReleaser, which builds binaries and publishes the GitHub release.
8. **Verify**: `gh release view v0.35.0` returns the published entry with assets. Without this step, the release is not complete (this is the step prior releases missed).
9. **Optional follow-up**: `docs(site)` PR for site/content/changelog.html if not bundled in step 5, matching the cadence of PRs #266 / #269.

## 17. Documentation surfaces

The implementation PR must touch the following doc surfaces. This list is exhaustive for in-repo docs; external integrators of `github.com/2389-research/tracker` are notified via the CHANGELOG operator notes in §14.

| File | Change |
|------|--------|
| `README.md:573` | Replace `result.Status == pipeline.OutcomeBudgetExceeded` example with `if !result.Status.IsSuccess() { ... }` or paired check; teach `IsSuccess()` helper alongside. |
| `README.md:432` (Status Icons section, currently only TUI node lamps) | Add "Run terminal status" subsection enumerating the four `TerminalStatus` values + the open-enum note. |
| `site/content/glossary.html:92` | Split into two glossary entries: "Node outcome" (`success`/`fail`/`retry` — what handlers return) and "Run terminal status" (`success`/`fail`/`budget_exceeded`/`validation_overridden` — what `EngineResult.Status` carries). Mark the latter as open enum. |
| `site/content/glossary.html:96` (Escalation entry) | Note that override marks the audit signal, not the routing — escalation continues to be a routing convention built on `OutcomeFail` edges. |
| `site/content/cli.html` (env vars table) | Add `TRACKER_FAIL_ON_OVERRIDE` row; document strict-`=1` parsing. |
| `site/content/cli.html` (flags table) | Add `--fail-on-override` row. |
| `site/content/cli.html` | Add a new exit-codes section: `0 = success or validation_overridden (default)`, `1 = generic fail or budget_exceeded`, `2 = validation_overridden with --fail-on-override`. |
| `site/content/architecture.html:299` (Error mapping table area) | Add a "Run terminal statuses" subsection enumerating the four values + their meaning. |
| `site/content/changelog.html` | v0.35.0 entry (matches CHANGELOG.md per §14). |
| `tracker run --help` | Add `--fail-on-override` flag with one-line description per D3 and §8.2. |
| `tracker audit --help` | Mention override surfaces in `tracker audit` output. |
| `tracker list --help` | Mention the new `override` and `budget` Status column values. |
