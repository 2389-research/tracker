# validation_overridden Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a new terminal `EngineResult.Status` value `validation_overridden`, triggered by per-edge `override: true` in `.dip` workflows, that distinguishes runs completed via human/autopilot/webhook acceptance of a failed validation from runs the workflow proved on its own. Audit-only signal; routing unchanged; default exit code 0 with `--fail-on-override` CI-strict opt-in. Also fixes the pre-existing `budget_exceeded → "fail"` collapse in `classifyStatus`.

**Architecture:** New `pipeline.TerminalStatus` named type re-types `EngineResult.Status` and gains `IsSuccess()`. New `OverrideDetail` struct + `Actor` enum captures gate, label, actor, and subgraph_path. Detection plumbing: an opportunistic `Interviewer.Actor() pipeline.Actor` method (queried via interface-assertion to preserve third-party compat) reaches the engine via a new `Outcome.OverrideActor` field. Sticky write happens at `advanceToNextNode` when an `Edge.Override`-marked edge is traversed, with synchronous checkpoint save for crash-resume durability and an idempotency check for restart re-traversal. Subgraph/parallel/manager_loop propagate `Outcome.ChildOverride` with `SubgraphPath` prepended. Audit-side `classifyStatus` reordered so the checkpoint fallback is reachable; same change fixes the `budget_exceeded → "fail"` collapse. TUI live rendering moved in-scope.

**Tech Stack:** Go 1.24+, dippin-lang (Go module), lipgloss (TUI), cobra (CLI).

**Spec:** `docs/superpowers/specs/2026-05-29-validation-overridden-design.md`. Tracking issue: [#271](https://github.com/2389-research/tracker/issues/271). Closes Gap 5.2 of #233. Targets v0.35.0.

---

## File Structure

### New files

| File | Purpose |
|------|---------|
| `pipeline/terminal_status.go` | `TerminalStatus` named type, terminal-status constants (re-exported), `IsSuccess()` method. |
| `pipeline/override.go` | `OverrideDetail` struct, `Actor` named type + constants, `ErrValidationOverridden` sentinel error. |
| `pipeline/testdata/override/single_gate.dip` | Engine-test fixture: minimal `wait.human` graph with one `override: true` edge. |
| `pipeline/testdata/override/restart_loop.dip` | Engine-test fixture: override + restart on same edge. |
| `cmd/tracker-conformance/fixtures/override.dip` | Conformance-test fixture: `AutoApproveInterviewer`-driven override. |

### Modified files (by chunk)

| File | Chunks touching it |
|------|---|
| `pipeline/engine.go` | 1, 5 (`OutcomeValidationOverridden`, `EngineResult.Status` retype, `EngineResult.ValidationOverrides`) |
| `pipeline/handler.go` | 1, 3 (`Outcome.ChildOverride`, `Outcome.OverrideActor`) |
| `pipeline/graph.go` | 1 (`Edge.Override`) |
| `pipeline/checkpoint.go` | 1 (`Checkpoint.ValidationOverrides`) |
| `pipeline/events.go` | 2 (`EventValidationOverridden`, `PipelineEvent.Override`, new `EdgePriority` value) |
| `pipeline/events_jsonl.go` | 2 (JSONL wire format) |
| `pipeline/handlers/interviewer.go` | 3 (`actorOf` helper) |
| `pipeline/handlers/human.go` | 3 (populate `Outcome.OverrideActor`) |
| `pipeline/handlers/autopilot.go` | 3 (`Actor()` method) |
| `pipeline/handlers/autopilot_claudecode.go` | 3 (`Actor()` method) |
| `pipeline/handlers/webhook_interviewer.go` | 3 (`Actor()` method) |
| `pipeline/handlers/auto_approve.go` (or wherever AutoApproveInterviewer lives) | 3 (`Actor()` method) |
| `tui/interviewer.go` | 3 (`Actor()` on `BubbleteaInterviewer`) |
| `tui/autopilot_interviewer.go` | 3 (`Actor()` on `AutopilotTUIInterviewer`) |
| `go.mod` | 4 (dippin-lang version bump) |
| `tracker_doctor.go` | 4 (`PinnedDippinVersion` bump, lockstep) |
| `pipeline/dippin_adapter.go` | 4 (map `IREdge.Override`, validation) |
| `pipeline/engine_run.go` | 5, 6 (flip-point in `advanceToNextNode`, terminal-status rule in `handleExitNode`, `applyOutcome` propagation) |
| `pipeline/engine_edges.go` | 5 (emit `EventDecisionEdge` with priority `"override"`) |
| `pipeline/subgraph.go` | 6 (status mapping, `ChildOverride` prepend) |
| `pipeline/handlers/manager_loop.go` | 6 (status mapping, `ChildOverride` prepend) |
| `pipeline/handlers/parallel.go` | 6 (aggregate `ChildOverride` across branches) |
| `tracker_audit.go` | 7 (`classifyStatus` reorder, `AuditReport`/`RunSummary` fields, doc-comments, recommendations) |
| `tracker.go` | 8 (`Result.Status` doc, `Result.ValidationOverrides`) |
| `tracker_diagnose.go` | 8 (`DiagnoseReport.ValidationOverrides`, render section) |
| `cmd/tracker/flags.go` | 9 (`--fail-on-override`) |
| `cmd/tracker/run.go` | 9 (`interpretRunResult` rewrite, `runPipelineAsync`) |
| `cmd/tracker/main.go` (or cobra entry) | 9 (exit code 2 wiring) |
| `cmd/tracker/audit.go` | 9 (list icons, audit header `Override:` chain) |
| `cmd/tracker/summary.go` | 9 (status header cases + amber color) |
| `tui/messages.go` | 10 (`MsgPipelineCompleted.Status`, `Override`) |
| `tui/adapter.go` | 10 (populate fields) |
| `cmd/tracker-conformance/main.go` | 11 (`status_class` field) |
| `pipeline/lint_tracker.go` | 12 (TRK102 rule) |
| `examples/build_product.dip` | 13 (mark 3 edges) |
| `examples/build_product_with_superspec.dip` | 13 (mark 1 edge) |
| `examples/ask_and_execute.dip` | 13 (mark 1 edge) |
| `CHANGELOG.md` | 14 |
| `README.md` | 14 |
| `site/content/glossary.html`, `cli.html`, `architecture.html`, `changelog.html` | 14 |

### Tests (added alongside the code under test)

- `pipeline/terminal_status_test.go` — `IsSuccess()` table.
- `pipeline/override_test.go` — `OverrideDetail` JSON round-trip; `Actor` constants.
- `pipeline/handlers/interviewer_test.go` — `actorOf` over each interviewer type.
- `pipeline/dippin_adapter_test.go` — adapter override-validation cases.
- `pipeline/engine_test.go` — flip-point, terminal-status, restart, max-restart exhaustion, idempotency, multi-override, crash-resume, multi-resume, actor enumeration, D9 invariant.
- `pipeline/subgraph_test.go` — propagation, 2-level + 3-level nesting, child override + parent fail.
- `pipeline/handlers/parallel_test.go` — branch aggregation.
- `pipeline/handlers/manager_loop_test.go` — manager_loop propagation.
- `pipeline/events_jsonl_test.go` — override-event round-trip, sentinel-stripped forgery.
- `pipeline/lint_tracker_test.go` — TRK102 positive + negative.
- `tracker_audit_test.go` — `classifyStatus` scenarios (incl. budget collapse fix), recommendation copy.
- `cmd/tracker/run_test.go` (or new file) — exit-code + `--fail-on-override` + env var precedence.
- `cmd/tracker-conformance/main_test.go` — `status_class` emission, override fixture.

---

## Chunk 1: Foundation types

These tasks add new types and fields without changing engine behavior. After this chunk, the codebase compiles and existing tests pass, but nothing has terminal-status-flipping behavior yet.

### Task 1: `TerminalStatus` named type + `IsSuccess()`

**Files:**
- Create: `pipeline/terminal_status.go`
- Create: `pipeline/terminal_status_test.go`
- Modify: `pipeline/engine.go` (re-type `OutcomeBudgetExceeded` constant, retype `EngineResult.Status`)
- Modify: `pipeline/handler.go` (re-type `OutcomeSuccess/Fail/Retry` constants — see note)

**Note on re-typing existing constants**: `OutcomeSuccess`/`Fail`/`Retry` in `pipeline/handler.go` are used both as handler-outcome strings (where they're compared as plain strings) AND as `EngineResult.Status` values (where `TerminalStatus` is the new type). Re-typing all of them to `TerminalStatus` works in both contexts because `TerminalStatus` is `type TerminalStatus string` — comparisons against untyped string literals still work.

- [ ] **Step 1: Write failing tests in `pipeline/terminal_status_test.go`**

```go
// ABOUTME: Tests for TerminalStatus and IsSuccess() classification.
// ABOUTME: Pins the {success, validation_overridden} = success / others = fail rule.
package pipeline

import "testing"

func TestTerminalStatus_IsSuccess(t *testing.T) {
    cases := []struct {
        name string
        in   TerminalStatus
        want bool
    }{
        {"success", OutcomeSuccess, true},
        {"validation_overridden", OutcomeValidationOverridden, true},
        {"fail", OutcomeFail, false},
        {"budget_exceeded", OutcomeBudgetExceeded, false},
        {"retry", OutcomeRetry, false},
        {"unknown_future_value", TerminalStatus("future_status"), false},
        {"empty", TerminalStatus(""), false},
    }
    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            if got := tc.in.IsSuccess(); got != tc.want {
                t.Errorf("%s.IsSuccess() = %v, want %v", tc.in, got, tc.want)
            }
        })
    }
}

func TestTerminalStatus_StringCompat(t *testing.T) {
    // Existing literal-string comparisons must continue to compile and produce
    // the same answer (the v1->v2 spec compat promise).
    var s TerminalStatus = OutcomeSuccess
    if s != "success" {
        t.Errorf("TerminalStatus(success) != literal \"success\"")
    }
    if string(s) != "success" {
        t.Errorf("string(TerminalStatus(success)) = %q, want \"success\"", string(s))
    }
}
```

- [ ] **Step 2: Run the test, confirm it fails**

```
go test ./pipeline/ -run TestTerminalStatus -v
```

Expected: FAIL — `TerminalStatus` and `OutcomeValidationOverridden` are undefined.

- [ ] **Step 3: Create `pipeline/terminal_status.go`**

```go
// ABOUTME: TerminalStatus named string type for EngineResult.Status taxonomy.
// ABOUTME: Carries IsSuccess() helper used by CLI exit-code, audit, and JSON consumers.
package pipeline

// TerminalStatus is the run-level terminal status carried on EngineResult.Status,
// tracker.Result.Status, tracker.AuditReport.Status, and tracker.RunSummary.Status.
//
// The known values are:
//
//   - OutcomeSuccess              "success"
//   - OutcomeFail                 "fail"
//   - OutcomeBudgetExceeded       "budget_exceeded"
//   - OutcomeValidationOverridden "validation_overridden"
//
// The enum is open — future minor releases may add new values. Consumers should
// use IsSuccess() to classify rather than switching on the raw string.
type TerminalStatus string

// IsSuccess reports whether the terminal status represents a run that completed
// without failure. Currently true for {success, validation_overridden}. Any
// unrecognized value returns false (fail-closed).
func (s TerminalStatus) IsSuccess() bool {
    switch s {
    case OutcomeSuccess, OutcomeValidationOverridden:
        return true
    default:
        return false
    }
}
```

- [ ] **Step 4: Modify `pipeline/handler.go` to re-type the handler outcome constants**

Find the existing block:

```go
const (
    OutcomeSuccess = "success"
    OutcomeRetry   = "retry"
    OutcomeFail    = "fail"
)
```

Replace with:

```go
const (
    OutcomeSuccess TerminalStatus = "success"
    OutcomeRetry   TerminalStatus = "retry"
    OutcomeFail    TerminalStatus = "fail"
)
```

(`OutcomeRetry` doesn't really belong on a `TerminalStatus`-typed constant — it's a per-handler outcome, not a run-terminal status. But typing it as `TerminalStatus` is harmless: a handler-side comparison `outcome.Status == OutcomeRetry` still works because both sides are `TerminalStatus`. Keeping a single named type for the whole constant family simplifies the codebase.)

- [ ] **Step 5: Modify `pipeline/engine.go` to add `OutcomeValidationOverridden` and re-type `OutcomeBudgetExceeded` + `EngineResult.Status`**

Find:

```go
// OutcomeBudgetExceeded signals that a BudgetGuard halted the run.
const OutcomeBudgetExceeded = "budget_exceeded"
```

Replace with:

```go
// OutcomeBudgetExceeded signals that a BudgetGuard halted the run.
const OutcomeBudgetExceeded TerminalStatus = "budget_exceeded"

// OutcomeValidationOverridden signals that the run reached the success exit
// after traversing at least one Edge.Override == true edge. Engine-terminal-only:
// handlers never return this value; the engine writes it post-loop based on the
// runState.validationOverrides slice. See docs/superpowers/specs/2026-05-29-validation-overridden-design.md.
const OutcomeValidationOverridden TerminalStatus = "validation_overridden"
```

In the same file, find the `EngineResult` struct:

```go
type EngineResult struct {
    RunID           string
    Status          string
    ...
}
```

Change `Status` to `TerminalStatus`:

```go
type EngineResult struct {
    RunID           string
    Status          TerminalStatus
    ...
}
```

- [ ] **Step 6: Run the tests, confirm pass**

```
go test ./pipeline/ -run TestTerminalStatus -v
```

Expected: PASS.

- [ ] **Step 7: Run the full build to confirm no ripple breakages**

```
go build ./...
```

Expected: clean. If callers compare `Status` against a `string` variable (not literal), they'll need a cast — fix call sites by adding `TerminalStatus(...)` or use the typed constants directly.

- [ ] **Step 8: Commit**

```
git add pipeline/terminal_status.go pipeline/terminal_status_test.go pipeline/handler.go pipeline/engine.go
git commit -m "$(cat <<'EOF'
feat(pipeline): introduce TerminalStatus named type with IsSuccess() helper

Re-types existing outcome constants (OutcomeSuccess/Fail/Retry/BudgetExceeded)
to a new named string type pipeline.TerminalStatus. Adds OutcomeValidationOverridden.

Adds IsSuccess() method returning true for {success, validation_overridden} and
false for everything else (fail-closed for unknown future values).

Existing literal-string comparisons (result.Status == "success") continue to
compile and produce the same answer.

Part of Gap 5.2 / #271.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 2: `Actor` enum + `OverrideDetail` struct + `ErrValidationOverridden`

**Files:**
- Create: `pipeline/override.go`
- Create: `pipeline/override_test.go`

- [ ] **Step 1: Write failing tests in `pipeline/override_test.go`**

```go
// ABOUTME: Tests for OverrideDetail JSON round-trip and Actor constants.
package pipeline

import (
    "encoding/json"
    "errors"
    "testing"
    "time"
)

func TestActor_StringCompat(t *testing.T) {
    cases := []struct {
        actor Actor
        want  string
    }{
        {ActorHuman, "human"},
        {ActorAutopilot, "autopilot"},
        {ActorWebhook, "webhook"},
        {ActorUnknown, "unknown"},
    }
    for _, tc := range cases {
        if string(tc.actor) != tc.want {
            t.Errorf("Actor %q != %q", tc.actor, tc.want)
        }
    }
}

func TestOverrideDetail_JSON(t *testing.T) {
    in := OverrideDetail{
        GateNodeID:   "EscalateReview",
        Label:        "accept",
        Actor:        ActorHuman,
        SubgraphPath: []string{"Outer", "Inner"},
        Timestamp:    time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC),
    }
    data, err := json.Marshal(in)
    if err != nil {
        t.Fatalf("marshal: %v", err)
    }
    var out OverrideDetail
    if err := json.Unmarshal(data, &out); err != nil {
        t.Fatalf("unmarshal: %v", err)
    }
    if out.GateNodeID != in.GateNodeID {
        t.Errorf("GateNodeID: got %q want %q", out.GateNodeID, in.GateNodeID)
    }
    if out.Label != in.Label {
        t.Errorf("Label: got %q want %q", out.Label, in.Label)
    }
    if out.Actor != in.Actor {
        t.Errorf("Actor: got %q want %q", out.Actor, in.Actor)
    }
    if len(out.SubgraphPath) != 2 || out.SubgraphPath[0] != "Outer" || out.SubgraphPath[1] != "Inner" {
        t.Errorf("SubgraphPath: got %v want [Outer Inner]", out.SubgraphPath)
    }
    if !out.Timestamp.Equal(in.Timestamp) {
        t.Errorf("Timestamp: got %v want %v", out.Timestamp, in.Timestamp)
    }
}

func TestOverrideDetail_OmitEmpty(t *testing.T) {
    in := OverrideDetail{
        GateNodeID: "Gate",
        Actor:      ActorAutopilot,
        Timestamp:  time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC),
    }
    data, err := json.Marshal(in)
    if err != nil {
        t.Fatalf("marshal: %v", err)
    }
    // Label and SubgraphPath should be omitted from the JSON when empty/nil.
    s := string(data)
    if contains(s, `"label"`) {
        t.Errorf("expected label omitted, got %s", s)
    }
    if contains(s, `"subgraph_path"`) {
        t.Errorf("expected subgraph_path omitted, got %s", s)
    }
}

func TestErrValidationOverridden_Is(t *testing.T) {
    wrapped := errors.New("outer: " + ErrValidationOverridden.Error())
    if errors.Is(wrapped, ErrValidationOverridden) {
        // wrapping via errors.Is requires a custom Wrap or fmt.Errorf %w —
        // for the sentinel-error-is-identity test, just verify the sentinel exists:
    }
    if ErrValidationOverridden == nil {
        t.Fatal("ErrValidationOverridden is nil")
    }
    if ErrValidationOverridden.Error() == "" {
        t.Fatal("ErrValidationOverridden has empty message")
    }
}

func contains(s, sub string) bool {
    for i := 0; i+len(sub) <= len(s); i++ {
        if s[i:i+len(sub)] == sub {
            return true
        }
    }
    return false
}
```

- [ ] **Step 2: Run, confirm failure**

```
go test ./pipeline/ -run "TestActor|TestOverrideDetail|TestErrValidationOverridden" -v
```

Expected: FAIL — undefined types.

- [ ] **Step 3: Create `pipeline/override.go`**

```go
// ABOUTME: OverrideDetail describes a single validation-override event captured at edge selection.
// ABOUTME: Actor enum identifies who took the override edge; ErrValidationOverridden is the CLI exit sentinel.
package pipeline

import (
    "errors"
    "time"
)

// Actor identifies who took a validation-override edge. Stored on OverrideDetail.Actor.
// Defined as a named string type so JSON marshals as the bare string and the constant
// set is grep-able.
type Actor string

const (
    ActorHuman     Actor = "human"     // human-driven interviewer (TUI or non-TUI console)
    ActorAutopilot Actor = "autopilot" // any autopilot variant (LLM-backed or deterministic auto-approve)
    ActorWebhook   Actor = "webhook"   // external callback via WebhookInterviewer
    ActorUnknown   Actor = "unknown"   // third-party or future Interviewer with no recognized Actor() value
)

// OverrideDetail describes a single validation-override event: the gate that
// produced it, the label that selected the override edge, who acted, and the
// subgraph path (if propagated from a child run). Persisted on
// Checkpoint.ValidationOverrides and EngineResult.ValidationOverrides; emitted
// on PipelineEvent.Override when an override edge is traversed.
type OverrideDetail struct {
    // GateNodeID is the source node of the override edge (the wait.human gate).
    GateNodeID string `json:"gate_node_id"`

    // Label is the edge label of the override edge ("accept", "mark done", etc.).
    // Empty when the override edge has no label.
    Label string `json:"label,omitempty"`

    // Actor identifies who took the override edge.
    Actor Actor `json:"actor"`

    // SubgraphPath is populated when this override was propagated from a child
    // run via Outcome.ChildOverride. Outermost-to-innermost subgraph node IDs;
    // the leaf gate node ID lives in GateNodeID, not in SubgraphPath. Empty for
    // overrides taken in the run's own graph.
    SubgraphPath []string `json:"subgraph_path,omitempty"`

    // Timestamp is the moment the override edge was traversed. In the JSONL wire
    // format, the enclosing event line carries its own timestamp; this field is
    // primarily for Checkpoint persistence where there is no enclosing timestamp.
    Timestamp time.Time `json:"timestamp"`
}

// ErrValidationOverridden is the sentinel error returned by interpretRunResult
// when --fail-on-override is set and the run terminated as validation_overridden.
// The cobra entry checks errors.Is(err, ErrValidationOverridden) and exits with
// code 2 (distinct from generic-fail exit 1).
var ErrValidationOverridden = errors.New("run completed via validation_overridden")
```

- [ ] **Step 4: Run, confirm pass**

```
go test ./pipeline/ -run "TestActor|TestOverrideDetail|TestErrValidationOverridden" -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```
git add pipeline/override.go pipeline/override_test.go
git commit -m "$(cat <<'EOF'
feat(pipeline): add OverrideDetail, Actor enum, and ErrValidationOverridden sentinel

New types for the validation_overridden audit signal:
- Actor: human | autopilot | webhook | unknown (named string type)
- OverrideDetail: gate, label, actor, subgraph_path, timestamp
- ErrValidationOverridden: sentinel for --fail-on-override exit code 2 wiring

Part of Gap 5.2 / #271.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 3: Add `Edge.Override`, `Outcome.OverrideActor`, `Outcome.ChildOverride`, `EngineResult.ValidationOverrides`, `Checkpoint.ValidationOverrides`

**Files:**
- Modify: `pipeline/graph.go` (add `Edge.Override`)
- Modify: `pipeline/handler.go` (add `Outcome.OverrideActor`, `Outcome.ChildOverride`)
- Modify: `pipeline/engine.go` (add `EngineResult.ValidationOverrides`)
- Modify: `pipeline/checkpoint.go` (add `Checkpoint.ValidationOverrides`)

These are additive field additions with no behavior changes — covered by existing tests passing (nothing reads them yet).

- [ ] **Step 1: Add `Edge.Override` to `pipeline/graph.go`**

Find the `Edge` struct definition (around line 238). Add the field after `Label`:

```go
type Edge struct {
    From      string
    To        string
    Label     string
    Condition string
    // Override marks the edge as a validation-override path. When the engine
    // traverses an override edge from a wait.human gate, the run's terminal
    // EngineResult.Status becomes OutcomeValidationOverridden (audit-only;
    // routing is unaffected). Valid only on edges from wait.human-handler
    // nodes; enforced by the adapter at graph construction time.
    Override bool
    Attrs    map[string]string
    // (other existing fields stay as-is)
}
```

- [ ] **Step 2: Add `Outcome.OverrideActor` and `Outcome.ChildOverride` to `pipeline/handler.go`**

Find the `Outcome` struct. Add at the end:

```go
type Outcome struct {
    Status             TerminalStatus
    ContextUpdates     map[string]string
    PreferredLabel     string
    SuggestedNextNodes []string
    Stats              *SessionStats
    ChildUsage         *UsageSummary
    Truncations        []TruncationDetail
    MissingMarker      *MarkerDetail
    MissingRoute       *RouteDetail

    // OverrideActor is the Actor classification of the interviewer that produced
    // this outcome. Populated by HumanHandler from its bound interviewer's
    // Actor() method (via actorOf helper). The engine reads this at edge-selection
    // time when an override edge is traversed, to populate OverrideDetail.Actor.
    // Empty for non-wait.human handlers (they cannot originate override edges).
    OverrideActor Actor

    // ChildOverride carries OverrideDetail entries propagated up from a child
    // run (subgraph, manager_loop, or parallel branch with a subgraph child).
    // The engine's applyOutcome path appends these to the parent's
    // runState.validationOverrides after the handler returns.
    ChildOverride []OverrideDetail
}
```

(Note: `Status` is already typed as `TerminalStatus` after Task 1 retyped the constants. If the spec's existing `Outcome.Status` was bare `string`, change it here.)

- [ ] **Step 3: Add `EngineResult.ValidationOverrides` to `pipeline/engine.go`**

Find the `EngineResult` struct. Add the field after `BudgetLimitsHit`:

```go
type EngineResult struct {
    RunID           string
    Status          TerminalStatus
    CompletedNodes  []string
    Context         map[string]string
    Trace           *Trace
    Usage           *UsageSummary
    BudgetLimitsHit []string

    // ValidationOverrides is the list of override edges traversed during this
    // run, in chronological order. Populated for every terminal path (success,
    // fail, budget, validation_overridden) so failure-after-override forensics
    // still see the override. Empty for runs with no override edges.
    //
    // The terminal-status rule writes Status=OutcomeValidationOverridden when
    // len(ValidationOverrides) > 0 AND the run reached the success exit;
    // failure paths return fail/budget regardless of override presence.
    ValidationOverrides []OverrideDetail
}
```

- [ ] **Step 4: Add `Checkpoint.ValidationOverrides` to `pipeline/checkpoint.go`**

Find the `Checkpoint` struct. Add:

```go
type Checkpoint struct {
    // ... existing fields ...

    // ValidationOverrides persists the override sticky list across resume and
    // bundle export. Appended at the flip-point in advanceToNextNode whenever
    // an override edge is traversed; never cleared by clearDownstream or
    // handleLoopRestart. omitempty for backwards compat with pre-v0.35
    // checkpoints (absent = "no overrides happened").
    ValidationOverrides []OverrideDetail `json:"validation_overrides,omitempty"`
}
```

- [ ] **Step 5: Run build, confirm clean**

```
go build ./...
```

Expected: clean.

- [ ] **Step 6: Run existing tests, confirm pass**

```
go test ./... -short
```

Expected: PASS. Nothing reads the new fields yet; existing tests are unaffected.

- [ ] **Step 7: Commit**

```
git add pipeline/graph.go pipeline/handler.go pipeline/engine.go pipeline/checkpoint.go
git commit -m "$(cat <<'EOF'
feat(pipeline): add override-related fields to Edge, Outcome, EngineResult, Checkpoint

Additive field additions, no behavior change:
- Edge.Override bool — adapter-mapped from dippin IREdge.Override
- Outcome.OverrideActor Actor — populated by HumanHandler
- Outcome.ChildOverride []OverrideDetail — propagated from subgraph/manager_loop/parallel
- EngineResult.ValidationOverrides []OverrideDetail — terminal sticky list
- Checkpoint.ValidationOverrides []OverrideDetail (omitempty) — durable across resume

Part of Gap 5.2 / #271.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Chunk 2: Event taxonomy

### Task 4: `EventValidationOverridden` event type + `PipelineEvent.Override` field + new `EdgePriority` value

**Files:**
- Modify: `pipeline/events.go`
- Modify: `pipeline/events_jsonl.go`
- Modify: `pipeline/events_jsonl_test.go`

- [ ] **Step 1: Write a failing JSONL round-trip test in `pipeline/events_jsonl_test.go`**

Add this test (or create the file if it doesn't exist):

```go
func TestJSONL_OverrideEventRoundTrip(t *testing.T) {
    detail := &OverrideDetail{
        GateNodeID:   "EscalateReview",
        Label:        "accept",
        Actor:        ActorHuman,
        SubgraphPath: []string{"Outer", "Inner"},
        Timestamp:    time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC),
    }
    ev := PipelineEvent{
        Type:      EventValidationOverridden,
        Timestamp: time.Date(2026, 5, 29, 12, 0, 1, 0, time.UTC),
        RunID:     "test-run",
        NodeID:    "EscalateReview",
        Message:   "validation override",
        Override:  detail,
    }
    entry := buildLogEntry(ev)

    // Verify the override fields land on the entry.
    if entry.OverrideGate != "EscalateReview" {
        t.Errorf("OverrideGate = %q, want EscalateReview", entry.OverrideGate)
    }
    if entry.OverrideLabel != "accept" {
        t.Errorf("OverrideLabel = %q, want accept", entry.OverrideLabel)
    }
    if entry.OverrideActor != ActorHuman {
        t.Errorf("OverrideActor = %q, want %q", entry.OverrideActor, ActorHuman)
    }
    if len(entry.OverrideSubgraphPath) != 2 ||
        entry.OverrideSubgraphPath[0] != "Outer" ||
        entry.OverrideSubgraphPath[1] != "Inner" {
        t.Errorf("OverrideSubgraphPath = %v, want [Outer Inner]", entry.OverrideSubgraphPath)
    }

    // Round-trip through JSON.
    data, err := json.Marshal(entry)
    if err != nil {
        t.Fatalf("marshal: %v", err)
    }
    var decoded jsonlLogEntry
    if err := json.Unmarshal(data, &decoded); err != nil {
        t.Fatalf("unmarshal: %v", err)
    }
    if decoded.OverrideGate != entry.OverrideGate {
        t.Errorf("round-trip OverrideGate: got %q want %q", decoded.OverrideGate, entry.OverrideGate)
    }
}
```

- [ ] **Step 2: Run, confirm failure**

```
go test ./pipeline/ -run TestJSONL_OverrideEventRoundTrip -v
```

Expected: FAIL — `EventValidationOverridden`, `PipelineEvent.Override`, `entry.Override*` fields undefined.

- [ ] **Step 3: Modify `pipeline/events.go` — add the event type and PipelineEvent field**

Find the existing event type constants. Add:

```go
// EventValidationOverridden fires when the engine traverses an Edge.Override-marked
// edge at advanceToNextNode. Carries an OverrideDetail payload via
// PipelineEvent.Override. Stage-level event (NodeID = gate node), same shape as
// EventConditionalFallthrough.
const EventValidationOverridden PipelineEventType = "validation_overridden"
```

Find the existing `EdgePriority` constants (or wherever the `DecisionDetail.EdgePriority` values live). Add:

```go
// EdgePriorityOverride identifies an edge selected at advanceToNextNode that
// carries Edge.Override == true. Emitted as the EdgePriority on the
// EventDecisionEdge event for the override-edge selection. EventValidationOverridden
// rides alongside (not instead of) the DecisionEdge event for these traversals.
const EdgePriorityOverride = "override"
```

Find the `PipelineEvent` struct. Add the field after the existing detail pointers:

```go
type PipelineEvent struct {
    Type      PipelineEventType
    Timestamp time.Time
    RunID     string
    NodeID    string
    Message   string
    // ... existing typed-detail pointers (Decision, Truncation, Marker, Route, etc.) ...

    // Override is non-nil on EventValidationOverridden events. Carries the gate,
    // label, actor, and subgraph_path of the traversed override edge.
    Override *OverrideDetail
}
```

- [ ] **Step 4: Modify `pipeline/events_jsonl.go` — add override fields to `jsonlLogEntry` and `buildLogEntry`**

Find the `jsonlLogEntry` struct. Add:

```go
type jsonlLogEntry struct {
    // ... existing fields (Timestamp, Type, RunID, NodeID, Message, etc.) ...

    OverrideGate         string   `json:"override_gate,omitempty"`
    OverrideLabel        string   `json:"override_label,omitempty"`
    OverrideActor        Actor    `json:"override_actor,omitempty"`
    OverrideSubgraphPath []string `json:"override_subgraph_path,omitempty"`
}
```

Find `buildLogEntry`. After the existing `Truncation` / `Marker` / `Route` blocks, add:

```go
func buildLogEntry(e PipelineEvent) jsonlLogEntry {
    entry := jsonlLogEntry{
        // ... existing assignments ...
    }

    // ... existing Truncation / Marker / Route blocks ...

    if e.Override != nil {
        entry.OverrideGate = e.Override.GateNodeID
        entry.OverrideLabel = e.Override.Label
        entry.OverrideActor = e.Override.Actor
        if len(e.Override.SubgraphPath) > 0 {
            // Copy to defend against later mutation of the source slice.
            entry.OverrideSubgraphPath = append([]string(nil), e.Override.SubgraphPath...)
        }
    }

    return entry
}
```

- [ ] **Step 5: Run, confirm pass**

```
go test ./pipeline/ -run TestJSONL_OverrideEventRoundTrip -v
```

Expected: PASS.

- [ ] **Step 6: Run the rest of the JSONL test suite to confirm no regression**

```
go test ./pipeline/ -run TestJSONL -v
```

Expected: All pass.

- [ ] **Step 7: Commit**

```
git add pipeline/events.go pipeline/events_jsonl.go pipeline/events_jsonl_test.go
git commit -m "$(cat <<'EOF'
feat(pipeline): add EventValidationOverridden + JSONL wire format

- New PipelineEventType const EventValidationOverridden.
- New EdgePriorityOverride const for the existing DecisionDetail.EdgePriority enum.
- New typed payload PipelineEvent.Override *OverrideDetail.
- jsonlLogEntry gains override_gate, override_label, override_actor,
  override_subgraph_path (omitempty); buildLogEntry populates them when the
  event carries an Override payload.

Part of Gap 5.2 / #271.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Chunk 3: Interviewer Actor() plumbing

The engine needs to know which Actor produced each gate outcome so it can populate `OverrideDetail.Actor` at edge-selection time. The mechanism per §5.3.1: add an `Actor() pipeline.Actor` method to every `Interviewer` implementation; `HumanHandler.Execute` queries it via opportunistic interface assertion (so third-party implementations stay compatible) and writes the result to `Outcome.OverrideActor`.

### Task 5: `actorOf` helper in `pipeline/handlers/interviewer.go`

**Files:**
- Modify: `pipeline/handlers/interviewer.go` (or wherever the `Interviewer` interface is declared; if it lives in `human.go`, edit there)
- Create: `pipeline/handlers/interviewer_test.go` (or append to an existing test file)

- [ ] **Step 1: Locate the Interviewer interface declaration**

Run:

```
grep -rn "type Interviewer interface\|type LabeledFreeformInterviewer interface" pipeline/handlers/
```

Note the file. (Expected: `pipeline/handlers/human.go` or `pipeline/handlers/interviewer.go`.)

- [ ] **Step 2: Write a failing test in `pipeline/handlers/interviewer_test.go`**

Replace `<package>` with the actual package name from the located file (likely `handlers`):

```go
// ABOUTME: Tests for actorOf — opportunistic Actor() interface assertion.
// ABOUTME: Verifies the fallback to ActorUnknown when an interviewer doesn't implement Actor().
package handlers

import (
    "testing"

    "github.com/2389-research/tracker/pipeline"
)

type stubInterviewerWithActor struct {
    actor pipeline.Actor
}

func (s *stubInterviewerWithActor) Actor() pipeline.Actor { return s.actor }

// (the other Interviewer methods would be no-ops; for compile, stub minimally if
// the interface is asserted at construction — adjust as needed for the actual
// Interviewer surface.)

type stubInterviewerWithoutActor struct{}

func TestActorOf_KnownInterviewer(t *testing.T) {
    cases := []struct {
        name  string
        actor pipeline.Actor
    }{
        {"human", pipeline.ActorHuman},
        {"autopilot", pipeline.ActorAutopilot},
        {"webhook", pipeline.ActorWebhook},
        {"unknown", pipeline.ActorUnknown},
    }
    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            got := actorOf(&stubInterviewerWithActor{actor: tc.actor})
            if got != tc.actor {
                t.Errorf("actorOf returned %q, want %q", got, tc.actor)
            }
        })
    }
}

func TestActorOf_UnknownInterviewer(t *testing.T) {
    // An interviewer that doesn't implement Actor() falls back to ActorUnknown.
    got := actorOf(&stubInterviewerWithoutActor{})
    if got != pipeline.ActorUnknown {
        t.Errorf("actorOf for no-Actor() interviewer = %q, want %q",
            got, pipeline.ActorUnknown)
    }
}

func TestActorOf_NilInterviewer(t *testing.T) {
    // Nil interviewer → ActorUnknown (defensive; should not happen in practice).
    got := actorOf(nil)
    if got != pipeline.ActorUnknown {
        t.Errorf("actorOf(nil) = %q, want %q", got, pipeline.ActorUnknown)
    }
}
```

(If the existing Interviewer interface requires more methods to satisfy at compile time, the stub types need to implement them as no-ops. Inspect `human.go` and add stubs accordingly.)

- [ ] **Step 3: Run, confirm failure**

```
go test ./pipeline/handlers/ -run TestActorOf -v
```

Expected: FAIL — `actorOf` undefined.

- [ ] **Step 4: Implement `actorOf` in the interviewer source file**

Add to `pipeline/handlers/interviewer.go` (or wherever Interviewer is defined):

```go
// actorOf returns the Actor classification for an Interviewer by querying its
// optional Actor() method via interface assertion. This pattern avoids adding
// a method to the exported Interviewer interface (which would break third-party
// implementations); interviewers in the tracker codebase implement the method,
// third-party implementations default to ActorUnknown.
//
// Used by HumanHandler.Execute to populate Outcome.OverrideActor.
func actorOf(i Interviewer) pipeline.Actor {
    if i == nil {
        return pipeline.ActorUnknown
    }
    if a, ok := i.(interface{ Actor() pipeline.Actor }); ok {
        return a.Actor()
    }
    return pipeline.ActorUnknown
}
```

(If the package doesn't already import `pipeline`, add it.)

- [ ] **Step 5: Run, confirm pass**

```
go test ./pipeline/handlers/ -run TestActorOf -v
```

Expected: PASS.

- [ ] **Step 6: Commit**

```
git add pipeline/handlers/interviewer.go pipeline/handlers/interviewer_test.go
git commit -m "$(cat <<'EOF'
feat(handlers): add actorOf helper for Interviewer Actor() detection

Opportunistic interface assertion — interviewers that implement Actor() pipeline.Actor
return their classification; everything else (including nil and third-party
implementations) falls back to ActorUnknown.

This preserves backward compatibility for external Interviewer implementations
that satisfy the existing interface but don't know about the new method.

Part of Gap 5.2 / #271.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 6: Add `Actor()` to each in-tree Interviewer implementation

**Files (modify, one method each):**
- `pipeline/handlers/human.go` (`ConsoleInterviewer`)
- `pipeline/handlers/autopilot.go` (`AutopilotInterviewer`)
- `pipeline/handlers/autopilot_claudecode.go` (`ClaudeCodeAutopilotInterviewer`)
- `pipeline/handlers/webhook_interviewer.go` (`WebhookInterviewer`)
- `pipeline/handlers/auto_approve.go` (or wherever `AutoApproveInterviewer` and `AutoApproveFreeformInterviewer` live — grep to confirm)
- `tui/interviewer.go` (`BubbleteaInterviewer`)
- `tui/autopilot_interviewer.go` (`AutopilotTUIInterviewer`)

- [ ] **Step 1: Locate each Interviewer concrete type**

```
grep -rn "func.*Interview\|func.*FreeformInterview" pipeline/handlers/ tui/ | grep -v _test.go | head -30
```

Confirm the file locations and exact type names.

- [ ] **Step 2: Add `Actor()` to `ConsoleInterviewer` in `pipeline/handlers/human.go`**

After the existing `ConsoleInterviewer` methods, add:

```go
// Actor returns ActorHuman — the console interviewer prompts a real human at stdin.
func (c *ConsoleInterviewer) Actor() pipeline.Actor { return pipeline.ActorHuman }
```

- [ ] **Step 3: Add `Actor()` to `AutopilotInterviewer` in `pipeline/handlers/autopilot.go`**

```go
// Actor returns ActorAutopilot — LLM persona standing in for a human at a gate.
func (a *AutopilotInterviewer) Actor() pipeline.Actor { return pipeline.ActorAutopilot }
```

- [ ] **Step 4: Add `Actor()` to `ClaudeCodeAutopilotInterviewer` in `pipeline/handlers/autopilot_claudecode.go`**

```go
// Actor returns ActorAutopilot — claude CLI subprocess persona standing in for a human.
func (c *ClaudeCodeAutopilotInterviewer) Actor() pipeline.Actor { return pipeline.ActorAutopilot }
```

- [ ] **Step 5: Add `Actor()` to `WebhookInterviewer` in `pipeline/handlers/webhook_interviewer.go`**

```go
// Actor returns ActorWebhook — gate response came from an external callback service.
func (w *WebhookInterviewer) Actor() pipeline.Actor { return pipeline.ActorWebhook }
```

- [ ] **Step 6: Add `Actor()` to `AutoApproveInterviewer` and `AutoApproveFreeformInterviewer`**

`AutoApprove*` interviewers are deterministic auto-acceptors — used in tests and headless CI. Per the spec §5.3.1 mapping table, both classify as `ActorAutopilot` (a flavor of autopilot for attribution purposes; no real human is in the loop).

```go
// Actor returns ActorAutopilot — deterministic auto-accept, no human in the loop.
func (a *AutoApproveInterviewer) Actor() pipeline.Actor { return pipeline.ActorAutopilot }

// Actor returns ActorAutopilot — deterministic auto-accept, no human in the loop.
func (a *AutoApproveFreeformInterviewer) Actor() pipeline.Actor { return pipeline.ActorAutopilot }
```

- [ ] **Step 7: Add `Actor()` to `BubbleteaInterviewer` in `tui/interviewer.go`**

```go
// Actor returns ActorHuman — gate response came from a real human at the TUI.
func (b *BubbleteaInterviewer) Actor() pipeline.Actor { return pipeline.ActorHuman }
```

(The TUI package will need to import `github.com/2389-research/tracker/pipeline` for the constant if it doesn't already. Confirm there's no import cycle.)

- [ ] **Step 8: Add `Actor()` to `AutopilotTUIInterviewer` in `tui/autopilot_interviewer.go`**

```go
// Actor returns ActorAutopilot — autopilot persona acting through the TUI surface.
func (a *AutopilotTUIInterviewer) Actor() pipeline.Actor { return pipeline.ActorAutopilot }
```

- [ ] **Step 9: Build to confirm no compile errors / import cycles**

```
go build ./...
```

Expected: clean.

- [ ] **Step 10: Write a test that exercises each in-tree `Actor()` method**

Append to `pipeline/handlers/interviewer_test.go`:

```go
func TestInterviewer_ActorMappings(t *testing.T) {
    cases := []struct {
        name  string
        intr  Interviewer
        want  pipeline.Actor
    }{
        {"ConsoleInterviewer", &ConsoleInterviewer{}, pipeline.ActorHuman},
        // AutopilotInterviewer requires construction args — adapt to its real signature.
        // Same for the other non-default-constructable types; use minimal zero-args
        // where possible, or table-test via subtests that construct each in-place.
    }
    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            got := actorOf(tc.intr)
            if got != tc.want {
                t.Errorf("%s actor = %q, want %q", tc.name, got, tc.want)
            }
        })
    }
}
```

(Some interviewers — `AutopilotInterviewer`, `WebhookInterviewer` — need an LLM client / URL etc. to construct. Either provide minimal stub args, or write per-type tests in each interviewer's own `_test.go` file where the construction is straightforward. The principle is: each `Actor()` implementation has exactly one assertion verifying its mapping.)

- [ ] **Step 11: Run all interviewer tests**

```
go test ./pipeline/handlers/ ./tui/ -run "Actor|Interviewer" -v
```

Expected: PASS.

- [ ] **Step 12: Commit**

```
git add pipeline/handlers/human.go pipeline/handlers/autopilot.go pipeline/handlers/autopilot_claudecode.go pipeline/handlers/webhook_interviewer.go pipeline/handlers/auto_approve*.go tui/interviewer.go tui/autopilot_interviewer.go pipeline/handlers/interviewer_test.go
git commit -m "$(cat <<'EOF'
feat(handlers,tui): implement Actor() on all in-tree Interviewer types

Per §5.3.1 mapping table:
- ConsoleInterviewer, BubbleteaInterviewer → ActorHuman
- AutopilotInterviewer, ClaudeCodeAutopilotInterviewer, AutopilotTUIInterviewer,
  AutoApproveInterviewer, AutoApproveFreeformInterviewer → ActorAutopilot
- WebhookInterviewer → ActorWebhook

actorOf queries via interface assertion, so third-party implementations that
don't add the method classify as ActorUnknown — no breaking change to the
exported Interviewer interface.

Part of Gap 5.2 / #271.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 7: `HumanHandler.Execute` populates `Outcome.OverrideActor`

**Files:**
- Modify: `pipeline/handlers/human.go`

- [ ] **Step 1: Locate `HumanHandler.Execute`**

```
grep -n "func.*HumanHandler.*Execute" pipeline/handlers/human.go
```

- [ ] **Step 2: Add a test that asserts the outcome carries the actor**

Append to `pipeline/handlers/human_test.go` (or create if missing):

```go
func TestHumanHandler_PopulatesOverrideActor(t *testing.T) {
    // Use AutoApproveInterviewer (deterministic, no LLM required).
    h := NewHumanHandler(&AutoApproveInterviewer{})
    node := &pipeline.Node{
        ID:      "Gate",
        Handler: "wait.human",
        Attrs:   map[string]string{"label": "test"},
    }
    pctx := pipeline.NewPipelineContext()
    out, err := h.Execute(context.Background(), node, pctx)
    if err != nil {
        t.Fatalf("execute: %v", err)
    }
    if out.OverrideActor != pipeline.ActorAutopilot {
        t.Errorf("OverrideActor = %q, want %q",
            out.OverrideActor, pipeline.ActorAutopilot)
    }
}
```

(Adapt the constructor signatures to match the actual `HumanHandler` / `AutoApproveInterviewer` APIs in the codebase. The point is: construct a handler with a known interviewer type, execute, assert the outcome carries the expected actor.)

- [ ] **Step 3: Run, confirm failure**

```
go test ./pipeline/handlers/ -run TestHumanHandler_PopulatesOverrideActor -v
```

Expected: FAIL — `OverrideActor` is empty.

- [ ] **Step 4: In `HumanHandler.Execute`, set `OverrideActor` before returning**

At the end of the function, where the `Outcome` is constructed (or just before returning), add:

```go
outcome.OverrideActor = actorOf(h.interviewer)
return outcome, nil
```

(Replace `h.interviewer` with the actual field name on `HumanHandler`. If the handler resolves the interviewer dynamically per-execute via a registry, query that registry result with `actorOf`.)

- [ ] **Step 5: Run, confirm pass**

```
go test ./pipeline/handlers/ -run TestHumanHandler_PopulatesOverrideActor -v
```

Expected: PASS.

- [ ] **Step 6: Run the rest of the human handler tests to confirm no regression**

```
go test ./pipeline/handlers/ -run HumanHandler -v
```

Expected: All pass.

- [ ] **Step 7: Commit**

```
git add pipeline/handlers/human.go pipeline/handlers/human_test.go
git commit -m "$(cat <<'EOF'
feat(handlers): HumanHandler populates Outcome.OverrideActor

Calls actorOf(h.interviewer) before returning, threading the Actor classification
through to the engine for use at edge-selection time when an override edge is
traversed.

Part of Gap 5.2 / #271.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Chunk 4: dippin-lang dependency + adapter

### Task 8: Bump `go.mod` dippin-lang version + `PinnedDippinVersion`

**Prerequisite:** A tagged release of `2389-research/dippin-lang` exists with the `Override bool` IR field. Per §16, dippin-lang lands first; this task assumes that release is published. If not yet, the dippin-lang implementation must precede this task (out of scope of this plan — see spec §7.1).

**Files:**
- Modify: `go.mod`
- Modify: `tracker_doctor.go` (around line 26)

- [ ] **Step 1: Confirm the target dippin-lang tag**

Ask the user / check the dippin-lang releases page for the tag that includes `ir.Edge.Override`. Let's call it `vX.Y.Z`.

- [ ] **Step 2: Bump go.mod**

```
go get github.com/2389-research/dippin-lang@vX.Y.Z
go mod tidy
```

- [ ] **Step 3: Update `PinnedDippinVersion` in `tracker_doctor.go`**

Find the constant (around line 26):

```go
const PinnedDippinVersion = "v0.32.0"
```

Replace with the new tag:

```go
const PinnedDippinVersion = "vX.Y.Z"
```

- [ ] **Step 4: Run the lockstep test**

```
go test ./... -run TestPinnedDippinVersionMatchesGoMod -v
```

Expected: PASS.

- [ ] **Step 5: Confirm full build clean**

```
go build ./...
```

Expected: clean. (The new `IREdge.Override` field is present but not yet read by the adapter.)

- [ ] **Step 6: Commit**

```
git add go.mod go.sum tracker_doctor.go
git commit -m "$(cat <<'EOF'
build(deps): bump dippin-lang to vX.Y.Z (IREdge.Override)

PinnedDippinVersion bumped in lockstep with go.mod per
TestPinnedDippinVersionMatchesGoMod.

Required for Gap 5.2 / #271 — the override: true edge attribute is parsed and
emitted into the IR by this dippin-lang version.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 9: Adapter maps `IREdge.Override` and validates placement

**Files:**
- Modify: `pipeline/dippin_adapter.go`
- Modify: `pipeline/dippin_adapter_test.go`

- [ ] **Step 1: Locate `convertEdge` in the adapter**

```
grep -n "func convertEdge\|func.*convertEdge" pipeline/dippin_adapter.go
```

- [ ] **Step 2: Write failing adapter-validation tests in `pipeline/dippin_adapter_test.go`**

```go
func TestAdapter_OverrideOnWaitHumanAccepted(t *testing.T) {
    ir := minimalIRWithEdge(t, "Gate", "Target", true /* override */, "wait.human")
    g, err := FromDippinIR(ir)
    if err != nil {
        t.Fatalf("FromDippinIR: %v", err)
    }
    // Find the edge in g.
    var found *Edge
    for i := range g.Edges {
        if g.Edges[i].From == "Gate" && g.Edges[i].To == "Target" {
            found = &g.Edges[i]
        }
    }
    if found == nil {
        t.Fatal("edge Gate->Target not found")
    }
    if !found.Override {
        t.Errorf("Edge.Override = false, want true")
    }
}

func TestAdapter_OverrideOnToolNodeRejected(t *testing.T) {
    ir := minimalIRWithEdge(t, "ToolNode", "Target", true /* override */, "tool")
    _, err := FromDippinIR(ir)
    if err == nil {
        t.Fatal("expected error, got nil")
    }
    want := "override: true but ToolNode is not a wait.human gate"
    if !strings.Contains(err.Error(), want) {
        t.Errorf("error = %q, want substring %q", err.Error(), want)
    }
}

func TestAdapter_OverrideRejectedOnHandlerTypes(t *testing.T) {
    bad := []string{
        "tool",
        "codergen", // agent
        "parallel",
        "parallel.fan_in",
        "conditional",
        "subgraph",
        "stack.manager_loop",
    }
    for _, h := range bad {
        t.Run(h, func(t *testing.T) {
            ir := minimalIRWithEdge(t, "Src", "Dst", true, h)
            _, err := FromDippinIR(ir)
            if err == nil {
                t.Errorf("handler %q: expected rejection, got nil", h)
            }
        })
    }
}

func TestAdapter_OverridePlusRestartAccepted(t *testing.T) {
    ir := minimalIRWithEdgeRestart(t, "Gate", "Target", true /* override */, true /* restart */, "wait.human")
    _, err := FromDippinIR(ir)
    if err != nil {
        t.Errorf("override+restart should be accepted: %v", err)
    }
}

// minimalIRWithEdge constructs a minimal dippin IR with one node and one edge
// for adapter tests. Fill in the actual ir package construction.
func minimalIRWithEdge(t *testing.T, srcHandler, dst string, override bool, handlerName string) *ir.Workflow {
    t.Helper()
    // ... construct minimal IR — adapt to actual dippin-lang ir package shape ...
    return nil // placeholder; implement based on the ir package
}

func minimalIRWithEdgeRestart(t *testing.T, src, dst string, override, restart bool, handlerName string) *ir.Workflow {
    t.Helper()
    return nil
}
```

(`minimalIRWithEdge` is a test helper to build a minimal `*ir.Workflow` programmatically. The exact shape depends on the dippin-lang IR — inspect `vendor/github.com/2389-research/dippin-lang/ir/` or use existing adapter tests as a template.)

- [ ] **Step 3: Run, confirm failure**

```
go test ./pipeline/ -run TestAdapter_Override -v
```

Expected: FAIL — Override field not mapped; validation not implemented.

- [ ] **Step 4: Modify `convertEdge` to map `IREdge.Override`**

In `pipeline/dippin_adapter.go`, find `convertEdge`. After the existing `Condition`/`Label`/`Restart` mapping, add:

```go
gEdge.Override = irEdge.Override
```

- [ ] **Step 5: Add the validation pass to `FromDippinIR` (or wherever final adapter assembly happens)**

After all edges are added to the graph, validate override placement:

```go
// validateOverridePlacement enforces D11: override: true is valid only on
// edges from wait.human nodes. Tool/agent/parallel/conditional/subgraph/
// manager_loop edges are rejected at adapter time.
func validateOverridePlacement(g *Graph) error {
    for _, e := range g.Edges {
        if !e.Override {
            continue
        }
        srcNode, ok := g.Nodes[e.From]
        if !ok {
            return fmt.Errorf("edge %s->%s: override: true but source node %q not found", e.From, e.To, e.From)
        }
        if srcNode.Handler != "wait.human" {
            return fmt.Errorf("edge %s->%s: override: true but %s is not a wait.human gate (handler=%s)",
                e.From, e.To, e.From, srcNode.Handler)
        }
    }
    return nil
}
```

Call it from `FromDippinIR` after graph assembly:

```go
if err := validateOverridePlacement(g); err != nil {
    return nil, err
}
```

- [ ] **Step 6: Run, confirm pass**

```
go test ./pipeline/ -run TestAdapter_Override -v
```

Expected: PASS.

- [ ] **Step 7: Run the full adapter test suite**

```
go test ./pipeline/ -run TestAdapter -v
```

Expected: All pass.

- [ ] **Step 8: Commit**

```
git add pipeline/dippin_adapter.go pipeline/dippin_adapter_test.go
git commit -m "$(cat <<'EOF'
feat(adapter): map IREdge.Override and validate override-edge placement

convertEdge copies IREdge.Override into pipeline.Edge.Override.
validateOverridePlacement (called from FromDippinIR after graph assembly)
rejects any edge with Override=true whose source node is not a wait.human
gate, closing the tool-node forgery vector documented in spec §7.3.

Rejected handlers: tool, codergen (agent), parallel, parallel.fan_in,
conditional, subgraph, stack.manager_loop.

Part of Gap 5.2 / #271.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Chunk 5: Engine flip-point + terminal-status rule

### Task 10: Sticky list on `runState` + flip-point in `advanceToNextNode`

**Files:**
- Modify: `pipeline/engine.go` (add `validationOverrides` field to `runState`)
- Modify: `pipeline/engine_run.go` (flip-point inside `advanceToNextNode` — find the post-`selectEdge`, pre-`SetEdgeSelection` window)
- Modify: `pipeline/engine_test.go` (failing test for flip-point)

- [ ] **Step 1: Add `validationOverrides` field to `runState`**

In `pipeline/engine.go`, find the `runState` struct (likely around line 186). Add:

```go
type runState struct {
    // ... existing fields ...

    // validationOverrides is the per-run sticky list of override events
    // appended at the flip-point in advanceToNextNode. Mirrors
    // cp.ValidationOverrides; the runState copy is the in-memory hot-path read,
    // the cp copy is the durable record.
    validationOverrides []OverrideDetail

    // lastOutcome carries the most recent handler outcome through edge selection
    // so advanceToNextNode can read Outcome.OverrideActor when an override edge
    // is traversed.
    lastOutcome Outcome
}
```

(If `runState` doesn't already track the last outcome — verify with grep — add `lastOutcome` too. Either way, the flip-point needs to read `outcome.OverrideActor`.)

- [ ] **Step 2: Write a failing engine test in `pipeline/engine_test.go`**

```go
func TestEngine_OverrideEdge_SetsSticky(t *testing.T) {
    // Build a minimal graph: Start -> Gate(wait.human) -> End
    // with two outgoing edges from Gate: one with Override:true to End (preferred),
    // one without to Start.
    g := &Graph{
        Nodes: map[string]*Node{
            "Start": {ID: "Start", Handler: "start"},
            "Gate":  {ID: "Gate", Handler: "wait.human", Attrs: map[string]string{"label": "Accept?"}},
            "End":   {ID: "End", Handler: "exit"},
        },
        Edges: []Edge{
            {From: "Start", To: "Gate"},
            {From: "Gate", To: "End", Label: "accept", Override: true},
            {From: "Gate", To: "Start", Label: "retry"},
        },
    }

    registry := NewHandlerRegistry()
    // Register a stub wait.human handler that picks "accept" and returns
    // OverrideActor: ActorHuman.
    registry.Register(&stubHumanHandler{
        name:          "wait.human",
        preferLabel:   "accept",
        overrideActor: ActorHuman,
    })
    // Register start/exit stubs that return OutcomeSuccess.

    engine := NewEngine(g, registry)
    result, err := engine.Run(context.Background())
    if err != nil {
        t.Fatalf("run: %v", err)
    }
    if result.Status != OutcomeValidationOverridden {
        t.Errorf("Status = %q, want %q", result.Status, OutcomeValidationOverridden)
    }
    if len(result.ValidationOverrides) != 1 {
        t.Fatalf("ValidationOverrides length = %d, want 1", len(result.ValidationOverrides))
    }
    got := result.ValidationOverrides[0]
    if got.GateNodeID != "Gate" {
        t.Errorf("GateNodeID = %q, want Gate", got.GateNodeID)
    }
    if got.Label != "accept" {
        t.Errorf("Label = %q, want accept", got.Label)
    }
    if got.Actor != ActorHuman {
        t.Errorf("Actor = %q, want %q", got.Actor, ActorHuman)
    }
}

// stubHumanHandler is a test helper: a wait.human handler that picks a fixed
// preferred label and reports a fixed OverrideActor on its outcome.
type stubHumanHandler struct {
    name          string
    preferLabel   string
    overrideActor Actor
}

func (s *stubHumanHandler) Name() string { return s.name }

func (s *stubHumanHandler) Execute(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
    return Outcome{
        Status:         OutcomeSuccess,
        PreferredLabel: s.preferLabel,
        OverrideActor:  s.overrideActor,
    }, nil
}
```

- [ ] **Step 3: Run, confirm failure**

```
go test ./pipeline/ -run TestEngine_OverrideEdge_SetsSticky -v
```

Expected: FAIL — `Status` is `success`, not `validation_overridden`.

- [ ] **Step 4: Implement the flip-point in `advanceToNextNode`**

In `pipeline/engine_run.go`, find `advanceToNextNode`. After the line that assigns the selected edge (usually `traceEntry.EdgeTo = next.To`) and BEFORE `s.cp.SetEdgeSelection(...)`, add the override-handling block:

```go
// Override edge handling: if the selected edge has Override:true, append a
// new OverrideDetail to the sticky list and persist synchronously.
// Idempotency: a re-traversal of the same override edge during restart or
// goal-gate retry is a no-op.
if next.Override {
    if !overrideAlreadyRecorded(s.validationOverrides, currentNodeID, next.Label) {
        detail := OverrideDetail{
            GateNodeID: currentNodeID,
            Label:      next.Label,
            Actor:      s.lastOutcome.OverrideActor, // may be empty/ActorUnknown
            Timestamp:  time.Now(),
        }
        if detail.Actor == "" {
            detail.Actor = ActorUnknown
        }
        s.validationOverrides = append(s.validationOverrides, detail)
        s.cp.ValidationOverrides = append(s.cp.ValidationOverrides, detail)
        e.emit(PipelineEvent{
            Type:      EventValidationOverridden,
            Timestamp: detail.Timestamp,
            RunID:     s.runID,
            NodeID:    currentNodeID,
            Message:   fmt.Sprintf("validation override at %q via label %q (actor=%s)", currentNodeID, next.Label, detail.Actor),
            Override:  &detail,
        })
        // Synchronously persist so a kill -9 between this point and the next
        // selectEdge does not lose the override-fired state. (Resume from the
        // prior checkpoint would replay this same edge, which is correct.)
        e.saveCheckpointWithTag(s.cp, s.pctx, s.runID, s, currentNodeID)
    }
}
```

Add the helper somewhere accessible (e.g., bottom of `engine_run.go`):

```go
// overrideAlreadyRecorded returns true if the sticky list already contains an
// own-graph override entry with the same gate node and label. Used by the
// flip-point for the restart re-traversal idempotency check (D5a).
// Note: only checks entries with empty SubgraphPath; child-propagated entries
// can never collide with own-graph entries.
func overrideAlreadyRecorded(list []OverrideDetail, gateNodeID, label string) bool {
    for _, d := range list {
        if len(d.SubgraphPath) == 0 && d.GateNodeID == gateNodeID && d.Label == label {
            return true
        }
    }
    return false
}
```

- [ ] **Step 5: Wire `runState.lastOutcome` to receive the outcome**

In `applyOutcome` (or wherever the handler outcome is processed before edge selection), add:

```go
s.lastOutcome = outcome
```

before `advanceToNextNode` runs.

- [ ] **Step 6: Implement the terminal-status rule in `handleExitNode` success branch**

In `pipeline/engine_run.go`, find `handleExitNode` (around line 742). At the success exit (around line 805+), where `EngineResult` is constructed via `return true, "", nil` or similar, replace with a result-builder that consults the sticky list:

```go
// Success exit. Apply the terminal-status rule: if any override fired during
// the run, terminal status is validation_overridden; otherwise success.
status := OutcomeSuccess
if len(s.validationOverrides) > 0 {
    status = OutcomeValidationOverridden
}
result := &EngineResult{
    RunID:               s.runID,
    Status:              status,
    CompletedNodes:      s.cp.CompletedNodes,
    Context:             s.pctx.Snapshot(),
    Trace:               s.trace,
    Usage:               s.trace.AggregateUsage(),
    ValidationOverrides: append([]OverrideDetail(nil), s.validationOverrides...),
}
return false, "", result
```

(Adapt to the exact return-signature of `handleExitNode`. Trace through whether `Run()` ends via `handleExitNode` or via a separate success exit at the end of the main loop — apply the same rule there.)

- [ ] **Step 7: Propagate `ValidationOverrides` on failure-path EngineResults**

In `pipeline/engine.go` (or wherever `failResult`, `cancelledResult`, and `handleLoopRestart` build their `EngineResult`), add `ValidationOverrides: append([]OverrideDetail(nil), s.validationOverrides...)` to each. Failure paths still expose the list so post-hoc readers can see "this run had an override AND it failed."

- [ ] **Step 8: Run, confirm pass**

```
go test ./pipeline/ -run TestEngine_OverrideEdge_SetsSticky -v
```

Expected: PASS.

- [ ] **Step 9: Add a test for the idempotency rule (restart re-traversal)**

```go
func TestEngine_OverrideEdge_RestartIdempotent(t *testing.T) {
    // Build a graph where the override edge has restart:true and the run
    // re-traverses it N times before completing. The sticky list must contain
    // exactly one entry (not N).
    g := &Graph{
        Nodes: map[string]*Node{
            "Start": {ID: "Start", Handler: "start"},
            "Gate":  {ID: "Gate", Handler: "wait.human"},
            "Loop":  {ID: "Loop", Handler: "stub"},
            "End":   {ID: "End", Handler: "exit"},
        },
        Edges: []Edge{
            {From: "Start", To: "Gate"},
            // Override edge with restart:true — re-traversed on each loop.
            // (Restart routing is handled elsewhere; this test fixture is
            // structured to ensure the same edge is taken twice.)
            {From: "Gate", To: "Loop", Label: "accept", Override: true},
            {From: "Loop", To: "Gate", Attrs: map[string]string{"restart": "true"}},
            {From: "Gate", To: "End", Label: "done"},
        },
    }
    // ... setup handlers; arrange for two traversals of the override edge ...
    // After run: assert len(result.ValidationOverrides) == 1.
}
```

(The exact graph wiring depends on how `restart` interacts with edge selection in the codebase. The point: configure handlers + graph so the override edge is traversed twice, then assert `len(ValidationOverrides) == 1`.)

- [ ] **Step 10: Run, confirm pass**

```
go test ./pipeline/ -run TestEngine_OverrideEdge -v
```

Expected: All pass.

- [ ] **Step 11: Commit**

```
git add pipeline/engine.go pipeline/engine_run.go pipeline/engine_test.go
git commit -m "$(cat <<'EOF'
feat(engine): flip-point in advanceToNextNode + terminal-status rule

When the engine selects an Edge.Override-marked edge:
- Build an OverrideDetail from currentNodeID, edge.Label, lastOutcome.OverrideActor
- Append to runState.validationOverrides AND runState.cp.ValidationOverrides
- Emit EventValidationOverridden (PipelineEvent.Override payload)
- Synchronously saveCheckpointWithTag for crash-resume durability
- Idempotency: re-traversal of the same gate+label is a no-op (D5a)

handleExitNode success branch consults the sticky list:
- len(validationOverrides) > 0 → Status=OutcomeValidationOverridden
- Otherwise → Status=OutcomeSuccess

failResult / cancelledResult / handleLoopRestart still populate
ValidationOverrides so forensics see the override even when failure dominates.

Part of Gap 5.2 / #271.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 11: `EventDecisionEdge` with `EdgePriorityOverride`

**Files:**
- Modify: `pipeline/engine_edges.go` (or wherever `EventDecisionEdge` is emitted in `selectEdge`)
- Modify: `pipeline/engine_test.go`

- [ ] **Step 1: Locate the emission point**

```
grep -n "EventDecisionEdge\|EdgePriority" pipeline/engine_edges.go pipeline/engine_run.go
```

- [ ] **Step 2: Add a test that asserts the priority value on the emitted event**

```go
func TestEngine_OverrideEdge_DecisionPriorityIsOverride(t *testing.T) {
    // ... same fixture as TestEngine_OverrideEdge_SetsSticky ...
    var observedPriority string
    handler := PipelineEventHandlerFunc(func(ev PipelineEvent) {
        if ev.Type == EventDecisionEdge && ev.Decision != nil &&
            ev.Decision.EdgeFrom == "Gate" {
            observedPriority = ev.Decision.EdgePriority
        }
    })
    engine := NewEngine(g, registry, WithPipelineEventHandler(handler))
    if _, err := engine.Run(context.Background()); err != nil {
        t.Fatalf("run: %v", err)
    }
    if observedPriority != EdgePriorityOverride {
        t.Errorf("EdgePriority = %q, want %q", observedPriority, EdgePriorityOverride)
    }
}
```

(`PipelineEventHandlerFunc` adapts a closure to the `PipelineEventHandler` interface — add if not present.)

- [ ] **Step 3: Run, confirm failure**

```
go test ./pipeline/ -run TestEngine_OverrideEdge_DecisionPriorityIsOverride -v
```

Expected: FAIL — current EdgePriority is whatever the existing selection logic emits (likely empty or "label").

- [ ] **Step 4: Update edge-selection emission**

In `engine_edges.go` (or wherever `EventDecisionEdge` is built), in the block that constructs the `DecisionDetail`, set the priority to `"override"` when the selected edge has `Override:true`:

```go
priority := decisionPriorityFor(next) // existing helper, or inline logic
if next.Override {
    priority = EdgePriorityOverride
}
detail := DecisionDetail{
    EdgeFrom:     currentNodeID,
    EdgeTo:       next.To,
    EdgePriority: priority,
    // ...
}
```

- [ ] **Step 5: Run, confirm pass**

```
go test ./pipeline/ -run TestEngine_OverrideEdge_DecisionPriorityIsOverride -v
```

Expected: PASS.

- [ ] **Step 6: Commit**

```
git add pipeline/engine_edges.go pipeline/engine_test.go
git commit -m "$(cat <<'EOF'
feat(engine): emit EdgePriorityOverride on DecisionDetail for override-edge selections

When advanceToNextNode picks an Edge.Override-marked edge, the accompanying
EventDecisionEdge now carries EdgePriority="override" (sixth value alongside
the existing five). EventValidationOverridden rides alongside, the same way
EventConditionalFallthrough rides alongside EventDecisionEdge.

NDJSON consumers that key off edge_priority see the new value; consumers that
filter on event Type get the dedicated EventValidationOverridden.

Part of Gap 5.2 / #271.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

**Remaining chunks** (6-14) follow the same pattern: failing test → minimal implementation → run → commit. Their detailed task breakdowns appear in `2026-05-29-validation-overridden.part2.md` (this file's continuation — see Index below). The structure:

- **Chunk 6:** Subgraph + manager_loop + parallel propagation
- **Chunk 7:** classifyStatus reorder + audit/list surface (incl. budget_exceeded fix)
- **Chunk 8:** tracker.go library surface (Result, AuditReport, RunSummary, DiagnoseReport)
- **Chunk 9:** CLI — `--fail-on-override` flag, `interpretRunResult`, `run.go`/`main.go` exit-code wiring, `tracker list` icons, `tracker audit` header chain, summary printer
- **Chunk 10:** TUI live rendering (MsgPipelineCompleted, adapter, completion row amber)
- **Chunk 11:** tracker-conformance status_class + override fixture
- **Chunk 12:** dippin doctor TRK102 lint rule + tests
- **Chunk 13:** Workflow migration (5 edges across 4 .dip files)
- **Chunk 14:** Documentation (CHANGELOG, README, site/, --help)

Each chunk's tasks are itemized in part 2. To keep this single-file plan manageable on first read, chunks 6-14 are written in the same TDD shape but with implementation steps that build directly on the foundations established here.

---

## Final verification

After all chunks complete, run the full verification gate per spec §12:

- [ ] `go build ./...` clean
- [ ] `go test ./... -short` — all 17 packages pass
- [ ] `TestPinnedDippinVersionMatchesGoMod` passes
- [ ] `dippin doctor examples/{ask_and_execute,build_product,build_product_with_superspec,deep_review}.dip` — A grade on all four
- [ ] `dippin doctor` produces zero TRK102 warnings on the migrated workflows
- [ ] `dippin simulate -all-paths` on the three core pipelines — all paths terminate; override paths recognized
- [ ] `tracker audit` on a real run exercising `EscalateReview` "accept" — confirm `Status: validation_overridden` inline with gate/label/actor
- [ ] `tracker run --fail-on-override` on the same fixture — exit code 2; stderr line emitted
- [ ] Visual: TUI completion row renders amber for an override run; CLI summary header matches; `tracker list` row shows `override` Status
