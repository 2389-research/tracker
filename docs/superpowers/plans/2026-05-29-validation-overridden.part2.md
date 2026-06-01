# validation_overridden Implementation Plan — Part 2

> Continuation of `2026-05-29-validation-overridden.md`. Chunks 6-14. Same TDD discipline: failing test → minimal implementation → run → commit.

---

## Chunk 6: Subgraph + parallel + manager_loop propagation

Goal: when a child run terminates with `ValidationOverrides`, the parent picks them up, prepends the child's subgraph node ID to each entry's `SubgraphPath`, and appends to the parent's own sticky list. Parallel branches running subgraph children union across branches.

### Task 12: Subgraph propagation + status mapping

**Files:**
- Modify: `pipeline/subgraph.go` (around line 147)
- Modify: `pipeline/subgraph_test.go`

- [ ] **Step 1: Failing test — child override propagates to parent**

```go
func TestSubgraph_ChildOverridePropagates(t *testing.T) {
    // Outer graph: Start -> SubgraphNode("ChildRun") -> End
    // Inner graph: Start -> Gate(wait.human, accepts) -override-> End
    // After parent run: parent.EngineResult.ValidationOverrides has one entry
    // with GateNodeID="Gate", SubgraphPath=["ChildRun"], Actor=ActorAutopilot.
    // Parent terminal status: validation_overridden.
    parent, child := buildSubgraphFixture(t)
    res, err := runParentEngine(t, parent, child)
    if err != nil { t.Fatalf("run: %v", err) }
    if res.Status != OutcomeValidationOverridden {
        t.Errorf("Status = %q, want %q", res.Status, OutcomeValidationOverridden)
    }
    if len(res.ValidationOverrides) != 1 {
        t.Fatalf("len = %d, want 1", len(res.ValidationOverrides))
    }
    got := res.ValidationOverrides[0]
    if got.GateNodeID != "Gate" {
        t.Errorf("GateNodeID = %q, want Gate", got.GateNodeID)
    }
    if len(got.SubgraphPath) != 1 || got.SubgraphPath[0] != "ChildRun" {
        t.Errorf("SubgraphPath = %v, want [ChildRun]", got.SubgraphPath)
    }
}
```

- [ ] **Step 2: Run, confirm failure**

```
go test ./pipeline/ -run TestSubgraph_ChildOverridePropagates -v
```

Expected: FAIL — parent sees `OutcomeSuccess` (child overrides discarded).

- [ ] **Step 3: Modify `pipeline/subgraph.go`**

Find the status-mapping switch (around line 147):

```go
switch result.Status {
case OutcomeSuccess, OutcomeBudgetExceeded:
    status = OutcomeSuccess
default:
    status = OutcomeFail
}
```

Replace with the override-aware version:

```go
switch result.Status {
case OutcomeSuccess, OutcomeBudgetExceeded, OutcomeValidationOverridden:
    status = OutcomeSuccess
default:
    status = OutcomeFail
}

// Propagate child overrides up with this subgraph node's ID prepended.
var childOverride []OverrideDetail
if len(result.ValidationOverrides) > 0 {
    childOverride = make([]OverrideDetail, len(result.ValidationOverrides))
    for i, det := range result.ValidationOverrides {
        // Prepend the current subgraph node ID to SubgraphPath.
        newPath := make([]string, 0, len(det.SubgraphPath)+1)
        newPath = append(newPath, currentNodeID)
        newPath = append(newPath, det.SubgraphPath...)
        det.SubgraphPath = newPath
        childOverride[i] = det
    }
}
```

(`currentNodeID` is the subgraph handler's own node ID — get it from `node.ID` or the function parameter.)

In the return statement:

```go
return Outcome{
    Status:         status,
    ContextUpdates: result.Context,
    ChildUsage:     result.Usage,
    ChildOverride:  childOverride,
}, nil
```

- [ ] **Step 4: Modify `pipeline/engine_run.go applyOutcome` to absorb `ChildOverride`**

After the handler returns, before the engine moves to edge selection, append child overrides into the sticky list:

```go
// applyOutcome (or wherever the handler outcome is integrated)
if len(outcome.ChildOverride) > 0 {
    for _, d := range outcome.ChildOverride {
        s.validationOverrides = append(s.validationOverrides, d)
        s.cp.ValidationOverrides = append(s.cp.ValidationOverrides, d)
        // Emit a stage-level EventValidationOverridden for the propagated entry
        // so the audit timeline records when the parent learned of the child's override.
        e.emit(PipelineEvent{
            Type:      EventValidationOverridden,
            Timestamp: time.Now(),
            RunID:     s.runID,
            NodeID:    currentNodeID,
            Message:   fmt.Sprintf("validation override propagated from subgraph child via %q", currentNodeID),
            Override:  &d,
        })
    }
    // Synchronously persist after child propagation.
    e.saveCheckpointWithTag(s.cp, s.pctx, s.runID, s, currentNodeID)
}
```

- [ ] **Step 5: Run, confirm pass**

```
go test ./pipeline/ -run TestSubgraph_ChildOverridePropagates -v
```

Expected: PASS.

- [ ] **Step 6: Add 3-level nesting test**

```go
func TestSubgraph_ThreeLevelNesting(t *testing.T) {
    // Outermost -> SubgraphNode("L1") -> SubgraphNode("L2") -> Gate(accept, override).
    // After outermost run: ValidationOverrides[0].SubgraphPath = ["L1", "L2"],
    // GateNodeID = "Gate".
    // ... fixture construction ...
    res, _ := runOutermost(t, fixture)
    if len(res.ValidationOverrides) != 1 {
        t.Fatalf("len = %d, want 1", len(res.ValidationOverrides))
    }
    got := res.ValidationOverrides[0]
    if got.GateNodeID != "Gate" {
        t.Errorf("GateNodeID = %q, want Gate", got.GateNodeID)
    }
    wantPath := []string{"L1", "L2"}
    if !reflect.DeepEqual(got.SubgraphPath, wantPath) {
        t.Errorf("SubgraphPath = %v, want %v", got.SubgraphPath, wantPath)
    }
}
```

Run and confirm pass.

- [ ] **Step 7: Commit**

```
git add pipeline/subgraph.go pipeline/engine_run.go pipeline/subgraph_test.go
git commit -m "$(cat <<'EOF'
feat(subgraph): propagate child ValidationOverrides with SubgraphPath prepend

SubgraphHandler.Execute now:
- Maps OutcomeValidationOverridden to OutcomeSuccess for parent routing (same
  treatment as OutcomeBudgetExceeded today).
- Builds Outcome.ChildOverride from child's ValidationOverrides, prepending
  the subgraph node ID to each entry's SubgraphPath.

Engine applyOutcome appends ChildOverride to runState.validationOverrides AND
the checkpoint, emits stage-level EventValidationOverridden events with the
propagated detail, and syncs the checkpoint.

Two-level and three-level nesting tested: SubgraphPath accumulates in
outermost-to-innermost order, GateNodeID remains the leaf gate.

Part of Gap 5.2 / #271.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 13: Manager_loop propagation

**Files:**
- Modify: `pipeline/handlers/manager_loop.go`
- Modify: `pipeline/handlers/manager_loop_test.go`

- [ ] **Step 1: Failing test**

```go
func TestManagerLoop_ChildOverridePropagates(t *testing.T) {
    // Parent has manager_loop with a child that overrides.
    // Assert: parent's outcome carries ChildOverride with the SubgraphPath
    // prepended; stack.child.exit_status context key carries
    // "validation_overridden".
    // ... fixture ...
}
```

- [ ] **Step 2: Modify the existing budget special-case at line 709**

Find:

```go
if result.Status == OutcomeBudgetExceeded {
    // ... existing budget propagation ...
}
```

Add the override case (parallel to budget):

```go
if result.Status == OutcomeValidationOverridden {
    // Propagate ChildOverride with this manager_loop's node ID prepended.
    childOverride := prependSubgraphPath(result.ValidationOverrides, currentNodeID)
    return Outcome{
        Status:        OutcomeSuccess, // parent routing continues
        ContextUpdates: map[string]string{
            "stack.child.exit_status": string(OutcomeValidationOverridden),
        },
        ChildUsage:    result.Usage,
        ChildOverride: childOverride,
    }, nil
}
```

Add the `prependSubgraphPath` helper (or extract it from subgraph.go's copy and share via `pipeline/override.go`):

```go
// prependSubgraphPath returns a copy of in with parentNodeID prepended to each
// entry's SubgraphPath. Used by subgraph and manager_loop to lift child overrides
// into parent-visible OverrideDetails.
func prependSubgraphPath(in []OverrideDetail, parentNodeID string) []OverrideDetail {
    if len(in) == 0 {
        return nil
    }
    out := make([]OverrideDetail, len(in))
    for i, d := range in {
        newPath := make([]string, 0, len(d.SubgraphPath)+1)
        newPath = append(newPath, parentNodeID)
        newPath = append(newPath, d.SubgraphPath...)
        d.SubgraphPath = newPath
        out[i] = d
    }
    return out
}
```

- [ ] **Step 3: Also handle the child-success branch (around line 667)**

If the child succeeded but had overrides earlier in the run, the manager_loop's success branch must still propagate them:

```go
// In the success branch:
if len(result.ValidationOverrides) > 0 {
    childOverride := prependSubgraphPath(result.ValidationOverrides, currentNodeID)
    outcome.ChildOverride = childOverride
}
```

- [ ] **Step 4: Run, confirm pass; commit**

```
go test ./pipeline/handlers/ -run TestManagerLoop_ChildOverride -v
git add pipeline/handlers/manager_loop.go pipeline/handlers/manager_loop_test.go pipeline/override.go
git commit -m "feat(manager_loop): propagate child ValidationOverrides like subgraph"
```

---

### Task 14: Parallel handler aggregation

**Files:**
- Modify: `pipeline/handlers/parallel.go`
- Modify: `pipeline/handlers/parallel_test.go`

- [ ] **Step 1: Failing test — two branches, one overrides, parent picks it up**

```go
func TestParallel_BranchOverrideAggregates(t *testing.T) {
    // Parallel with two branches:
    // - Branch A: runs subgraph that fires override.
    // - Branch B: runs a clean subgraph.
    // Assert: parent's outcome.ChildOverride contains exactly one entry (from
    // Branch A), with SubgraphPath including Branch A's subgraph node ID.
    // ... fixture ...
}
```

- [ ] **Step 2: In `ParallelHandler.Execute`, after branch results are assembled, union ChildOverride across branches**

```go
// Aggregate ChildOverride across all branches into a single slice on the
// parent's returned Outcome. Branches that didn't propagate any override
// contribute nothing; branches that did are concatenated in branch-result-order.
var aggregated []OverrideDetail
for _, br := range branchResults {
    if len(br.ChildOverride) > 0 {
        aggregated = append(aggregated, br.ChildOverride...)
    }
}
outcome.ChildOverride = aggregated
```

(`branchResults` is whatever the existing code calls the per-branch outcome list. Inspect `parallel.go` to find it.)

- [ ] **Step 3: Run, confirm pass**

```
go test ./pipeline/handlers/ -run TestParallel_BranchOverride -v
```

- [ ] **Step 4: Add test — both branches override**

```go
func TestParallel_BothBranchesOverride(t *testing.T) {
    // Both branches' subgraphs fire overrides.
    // Assert: parent's outcome.ChildOverride contains two entries, in
    // branch-result-order. Parent's terminal status: validation_overridden.
}
```

Run, confirm pass.

- [ ] **Step 5: Commit**

```
git add pipeline/handlers/parallel.go pipeline/handlers/parallel_test.go
git commit -m "feat(parallel): aggregate ChildOverride across branches for parent propagation"
```

---

## Chunk 7: classifyStatus + audit surface

### Task 15: `classifyStatus` reordered algorithm + budget_exceeded fix

**Files:**
- Modify: `tracker_audit.go` (around line 181)
- Modify: `tracker_audit_test.go`

- [ ] **Step 1: Failing tests covering all classification scenarios**

```go
func TestClassifyStatus_Scenarios(t *testing.T) {
    cases := []struct {
        name     string
        events   []ActivityEntry // synthetic activity log
        cp       *pipeline.Checkpoint
        want     string
    }{
        {
            name: "override then complete",
            events: []ActivityEntry{
                {Type: "validation_overridden"},
                {Type: "pipeline_completed"},
            },
            cp:   &pipeline.Checkpoint{CurrentNode: ""},
            want: "validation_overridden",
        },
        {
            name: "override then fail (failure dominates)",
            events: []ActivityEntry{
                {Type: "validation_overridden"},
                {Type: "pipeline_failed"},
            },
            cp:   &pipeline.Checkpoint{CurrentNode: ""},
            want: "fail",
        },
        {
            name: "override then budget (failure dominates)",
            events: []ActivityEntry{
                {Type: "validation_overridden"},
                {Type: "budget_exceeded"},
            },
            cp:   &pipeline.Checkpoint{CurrentNode: ""},
            want: "budget_exceeded",
        },
        {
            name: "budget_exceeded alone — D12 fix (was 'fail')",
            events: []ActivityEntry{
                {Type: "budget_exceeded"},
            },
            cp:   &pipeline.Checkpoint{CurrentNode: ""},
            want: "budget_exceeded",
        },
        {
            name: "no terminal, no overrides on cp, CurrentNode set → fail",
            events: nil,
            cp:   &pipeline.Checkpoint{CurrentNode: "Mid"},
            want: "fail",
        },
        {
            name: "no terminal, CurrentNode empty, sticky has overrides → validation_overridden",
            events: nil,
            cp: &pipeline.Checkpoint{
                CurrentNode: "",
                ValidationOverrides: []pipeline.OverrideDetail{{GateNodeID: "G"}},
            },
            want: "validation_overridden",
        },
        {
            name: "no terminal, CurrentNode empty, no overrides → success",
            events: nil,
            cp:   &pipeline.Checkpoint{CurrentNode: ""},
            want: "success",
        },
        {
            name: "resumed run: override on attempt 1, completed on attempt 2",
            events: []ActivityEntry{
                {Type: "pipeline_started"},
                {Type: "validation_overridden"},
                {Type: "pipeline_started"}, // resume
                {Type: "pipeline_completed"},
            },
            cp:   &pipeline.Checkpoint{CurrentNode: ""},
            want: "validation_overridden",
        },
    }
    for _, tc := range cases {
        t.Run(tc.name, func(t *testing.T) {
            got := classifyStatus(tc.cp, tc.events)
            if got != tc.want {
                t.Errorf("classifyStatus = %q, want %q", got, tc.want)
            }
        })
    }
}
```

- [ ] **Step 2: Run, confirm failures**

```
go test ./ -run TestClassifyStatus_Scenarios -v
```

Expected: several FAIL (budget→fail collapse, missing override case, ordering bug).

- [ ] **Step 3: Rewrite `classifyStatus` per spec §6.4**

```go
func classifyStatus(cp *pipeline.Checkpoint, activity []ActivityEntry) string {
    // Reverse-scan: failure dominates; override + completion = override; lone
    // halted override = override (only when cp.CurrentNode == "").
    sawCompletion := false
    sawOverride := false
    for i := len(activity) - 1; i >= 0; i-- {
        switch activity[i].Type {
        case "pipeline_failed":
            return "fail"
        case "budget_exceeded":
            return "budget_exceeded"
        case "pipeline_completed":
            sawCompletion = true
        case "validation_overridden":
            sawOverride = true
        }
    }
    if sawCompletion && sawOverride {
        return "validation_overridden"
    }
    if sawOverride && !sawCompletion {
        // Run halted at the override.
        return "validation_overridden"
    }
    if sawCompletion {
        return "success"
    }
    // No terminal event found — fall back to checkpoint signals.
    if len(cp.ValidationOverrides) > 0 && cp.CurrentNode == "" {
        return "validation_overridden"
    }
    if cp.CurrentNode != "" {
        return "fail"
    }
    return "success"
}
```

- [ ] **Step 4: Run, confirm pass**

```
go test ./ -run TestClassifyStatus_Scenarios -v
```

- [ ] **Step 5: Commit**

```
git add tracker_audit.go tracker_audit_test.go
git commit -m "$(cat <<'EOF'
fix(audit): rewrite classifyStatus — failure dominates + checkpoint fallback

Algorithm reordered per spec §6.4:
- Reverse-scan activity for terminal events.
- pipeline_failed / budget_exceeded short-circuit (failure dominates).
- pipeline_completed + validation_overridden anywhere in scan → "validation_overridden".
- Bare validation_overridden (no later completion) + cp.CurrentNode == "" → "validation_overridden".
- No terminal event found: fallback to cp.ValidationOverrides (durable when activity log is lost).

D12 fix: budget_exceeded events no longer collapse to "fail" in the audit surface.
This is a user-visible behavior change — scripts filtering on status == "fail"
will see budget-halted runs disappear from the failure bucket.

Part of Gap 5.2 / #271.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 16: `AuditReport` and `RunSummary` field additions + doc-comment fixes

**Files:**
- Modify: `tracker_audit.go`

- [ ] **Step 1: Update `AuditReport` and `RunSummary`**

Find `type AuditReport struct`. Add:

```go
type AuditReport struct {
    RunID  string `json:"run_id"`
    // Status is one of: "success", "fail", "budget_exceeded", "validation_overridden".
    // The set is open — future minor releases may add new values. Consumers should
    // use the StatusClass field for stable {succeeded|failed} bucketing.
    Status string `json:"status"`
    // StatusClass is one of "succeeded" or "failed" — stable companion to Status
    // for downstream consumers that need bucket categorization that survives
    // enum extensions.
    StatusClass string `json:"status_class"`
    // ... existing fields ...
    // ValidationOverrides is populated from the activity log (or checkpoint
    // fallback) when one or more override edges were traversed during the run.
    ValidationOverrides []pipeline.OverrideDetail `json:"validation_overrides,omitempty"`
    OverrideCount       int                       `json:"override_count,omitempty"`
}
```

Same shape for `RunSummary`:

```go
type RunSummary struct {
    RunID  string `json:"run_id"`
    // Status is one of: "success", "fail", "budget_exceeded", "validation_overridden".
    // Open enum; prefer StatusClass for stable bucketing.
    Status      string `json:"status"`
    StatusClass string `json:"status_class"`
    // ... existing fields ...
    // OverrideCount is the number of override edges traversed in this run.
    OverrideCount int `json:"override_count,omitempty"`
}
```

- [ ] **Step 2: Populate `StatusClass` and `ValidationOverrides` in the construction sites**

Find where `AuditReport` is built from the activity log + checkpoint. After `Status` is computed:

```go
r.StatusClass = "failed"
if pipeline.TerminalStatus(r.Status).IsSuccess() {
    r.StatusClass = "succeeded"
}
// Source ValidationOverrides from activity events first; fall back to checkpoint.
overrides := extractOverridesFromActivity(activity)
if len(overrides) == 0 {
    overrides = cp.ValidationOverrides
}
r.ValidationOverrides = overrides
r.OverrideCount = len(overrides)
```

Add `extractOverridesFromActivity`:

```go
// extractOverridesFromActivity returns the OverrideDetail entries from
// EventValidationOverridden activity entries, in chronological order.
func extractOverridesFromActivity(activity []ActivityEntry) []pipeline.OverrideDetail {
    var out []pipeline.OverrideDetail
    for _, e := range activity {
        if e.Type != "validation_overridden" {
            continue
        }
        det := pipeline.OverrideDetail{
            GateNodeID: e.OverrideGate,
            Label:      e.OverrideLabel,
            Actor:      e.OverrideActor,
            Timestamp:  e.Timestamp,
        }
        if len(e.OverrideSubgraphPath) > 0 {
            det.SubgraphPath = append([]string(nil), e.OverrideSubgraphPath...)
        }
        out = append(out, det)
    }
    return out
}
```

(`ActivityEntry` needs the `OverrideGate`/`OverrideLabel`/`OverrideActor`/`OverrideSubgraphPath` fields too — mirror the JSONL entry fields. Add to `tracker_activity.go` or wherever `ActivityEntry` is defined.)

- [ ] **Step 3: Drop the `sort.Strings(recs)` from `buildAuditRecommendations` (D16)**

Find line 269:

```go
sort.Strings(recs)
```

Remove it. Then prepend override notes ahead of any retry/budget notes:

```go
func buildAuditRecommendations(cp *pipeline.Checkpoint, status string, total time.Duration, overrides []pipeline.OverrideDetail) []string {
    var recs []string

    // Override notes go first.
    if len(overrides) > 0 {
        recs = append(recs,
            "This run terminated via a validation override. Workflow completion does not imply spec compliance — the override path bypassed at least one automated gate.")
        for _, d := range overrides {
            gate := d.GateNodeID
            if len(d.SubgraphPath) > 0 {
                gate = strings.Join(append(append([]string(nil), d.SubgraphPath...), d.GateNodeID), "/")
            }
            recs = append(recs,
                fmt.Sprintf("Validation override at gate %q (label: %q, actor: %s). Review the override decision to confirm it meets project policy.",
                    gate, d.Label, d.Actor))
        }
    }

    // Existing retry / budget / long-running recommendations follow.
    for nodeID, count := range cp.RetryCounts {
        if count >= 2 {
            recs = append(recs, fmt.Sprintf("Consider adjusting retry_policy for %s (used %d retries)", nodeID, count))
        }
    }
    // ... rest of existing logic, but no sort.Strings(recs) ...

    return recs
}
```

Update call sites to pass `overrides`.

- [ ] **Step 4: Run, confirm pass; commit**

```
go test ./ -run "TestAudit|TestRunSummary|TestRecommendations" -v
git add tracker_audit.go tracker_activity.go
git commit -m "$(cat <<'EOF'
feat(audit): add ValidationOverrides, StatusClass, OverrideCount to AuditReport/RunSummary

- Status doc-comments enumerate the four-value open enum.
- StatusClass is the stable succeeded|failed companion.
- ValidationOverrides sourced from activity events, fallback to checkpoint.
- buildAuditRecommendations: override notes go first, no alphabetical sort.

Part of Gap 5.2 / #271.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Chunk 8: tracker.go library surface

### Task 17: `Result.ValidationOverrides` + doc-comment

**Files:**
- Modify: `tracker.go`

- [ ] **Step 1: Add field + doc-comment to `Result` (around line 133)**

```go
type Result struct {
    RunID string
    // Status carries the run's terminal status. One of:
    //   - "success"
    //   - "fail"
    //   - "budget_exceeded"
    //   - "validation_overridden"
    // The enum is open — future minor releases may add new values. Use
    // pipeline.TerminalStatus(r.Status).IsSuccess() to classify rather than
    // switching on the raw string.
    Status pipeline.TerminalStatus
    // ... existing fields ...

    // ValidationOverrides is the list of override edges traversed during the run.
    // Empty for runs with no override edges. Populated for every terminal status
    // (including fail and budget_exceeded) so forensics see overrides even when
    // failure dominates.
    ValidationOverrides []pipeline.OverrideDetail
}
```

- [ ] **Step 2: Find the `EngineResult → Result` mapping site (around line 860)**

```go
return &Result{
    RunID:               er.RunID,
    Status:              er.Status,
    // ... existing ...
    ValidationOverrides: append([]pipeline.OverrideDetail(nil), er.ValidationOverrides...),
}
```

- [ ] **Step 3: Run tracker tests, confirm clean**

```
go test ./ -run "TestRun|TestResult" -v
```

- [ ] **Step 4: Commit**

```
git add tracker.go
git commit -m "feat(tracker): add Result.ValidationOverrides + Status doc-comment"
```

---

### Task 18: `DiagnoseReport.ValidationOverrides` + render section

**Files:**
- Modify: `tracker_diagnose.go`

- [ ] **Step 1: Add fields**

```go
type DiagnoseReport struct {
    // ... existing fields ...
    ValidationOverrides []pipeline.OverrideDetail `json:"validation_overrides,omitempty"`
    OverrideCount       int                       `json:"override_count,omitempty"`
}
```

- [ ] **Step 2: Populate from the activity-log scan**

In whatever function builds the `DiagnoseReport` from `activity.jsonl`:

```go
report.ValidationOverrides = extractOverridesFromActivity(activity)
if len(report.ValidationOverrides) == 0 && cp != nil {
    report.ValidationOverrides = cp.ValidationOverrides
}
report.OverrideCount = len(report.ValidationOverrides)
```

- [ ] **Step 3: Add the rendering section** (between BudgetHalt and Failures)

In `cmd/tracker/diagnose.go` (or wherever `DiagnoseReport` is rendered to stdout):

```go
if len(report.ValidationOverrides) > 0 {
    fmt.Println("─── Validation Override ─────")
    for _, d := range report.ValidationOverrides {
        gate := d.GateNodeID
        if len(d.SubgraphPath) > 0 {
            gate = strings.Join(append(d.SubgraphPath, d.GateNodeID), "/")
        }
        fmt.Printf("  Gate:     %s\n", gate)
        fmt.Printf("  Label:    %q\n", d.Label)
        fmt.Printf("  Actor:    %s\n", d.Actor)
        fmt.Println()
    }
}
```

- [ ] **Step 4: Test + commit**

```
go test ./ -run TestDiagnose -v
git add tracker_diagnose.go cmd/tracker/diagnose.go
git commit -m "feat(diagnose): surface ValidationOverrides as informational section"
```

---

## Chunk 9: CLI

### Task 19: `--fail-on-override` flag + env var

**Files:**
- Modify: `cmd/tracker/flags.go` (around line 236-238, grouped with `--max-*`)
- Modify: `cmd/tracker/run.go`

- [ ] **Step 1: Add the flag definition**

In `cmd/tracker/flags.go`, after the `--max-wall-time` flag:

```go
cmd.PersistentFlags().BoolVar(&cfg.FailOnOverride, "fail-on-override", false,
    "Exit code 2 if the run terminates via validation_overridden (default: exit 0)")
```

And the env var read (in whatever function reads env-var fallbacks):

```go
if !cfg.FailOnOverride && os.Getenv("TRACKER_FAIL_ON_OVERRIDE") == "1" {
    cfg.FailOnOverride = true
}
```

(Strict `=1` parsing — matches `TRACKER_PASS_*` convention.)

- [ ] **Step 2: Test the env var parsing**

```go
func TestFailOnOverride_EnvParsing(t *testing.T) {
    cases := []struct {
        envVal string
        want   bool
    }{
        {"1", true},
        {"true", false},  // strict =1
        {"yes", false},
        {"", false},
        {"TRUE", false},
    }
    for _, tc := range cases {
        t.Run(tc.envVal, func(t *testing.T) {
            t.Setenv("TRACKER_FAIL_ON_OVERRIDE", tc.envVal)
            // ... build runConfig with default FailOnOverride false ...
            cfg := buildRunConfig() // or whatever
            if cfg.FailOnOverride != tc.want {
                t.Errorf("env=%q FailOnOverride=%v, want %v",
                    tc.envVal, cfg.FailOnOverride, tc.want)
            }
        })
    }
}
```

Run, confirm pass.

- [ ] **Step 3: Commit**

```
git add cmd/tracker/flags.go cmd/tracker/run.go cmd/tracker/run_test.go
git commit -m "feat(cli): add --fail-on-override flag + TRACKER_FAIL_ON_OVERRIDE=1 env var"
```

---

### Task 20: `interpretRunResult` rewrite with sentinel error

**Files:**
- Modify: `cmd/tracker/run.go` (around line 313)
- Modify: `cmd/tracker/main.go` (cobra entry, near `os.Exit`)

- [ ] **Step 1: Failing test for exit code 2**

```go
func TestInterpretRunResult_FailOnOverride_ReturnsSentinel(t *testing.T) {
    res := &pipeline.EngineResult{
        Status: pipeline.OutcomeValidationOverridden,
        ValidationOverrides: []pipeline.OverrideDetail{
            {GateNodeID: "Gate", Label: "accept", Actor: pipeline.ActorHuman},
        },
    }
    cfg := &runConfig{FailOnOverride: true}
    err := interpretRunResult(res, cfg)
    if !errors.Is(err, pipeline.ErrValidationOverridden) {
        t.Errorf("err = %v, want ErrValidationOverridden", err)
    }
}

func TestInterpretRunResult_OverrideDefaultExitZero(t *testing.T) {
    res := &pipeline.EngineResult{Status: pipeline.OutcomeValidationOverridden}
    cfg := &runConfig{FailOnOverride: false}
    err := interpretRunResult(res, cfg)
    if err != nil {
        t.Errorf("err = %v, want nil (default exit 0 on override)", err)
    }
}

func TestInterpretRunResult_FailDominates(t *testing.T) {
    // --fail-on-override + Status=fail → exit 1 (not 2), generic fail error.
    res := &pipeline.EngineResult{Status: pipeline.OutcomeFail}
    cfg := &runConfig{FailOnOverride: true}
    err := interpretRunResult(res, cfg)
    if errors.Is(err, pipeline.ErrValidationOverridden) {
        t.Error("err is ErrValidationOverridden, want generic fail")
    }
    if err == nil {
        t.Error("err is nil, want generic fail error")
    }
}
```

- [ ] **Step 2: Rewrite `interpretRunResult`**

```go
func interpretRunResult(result *pipeline.EngineResult, cfg *runConfig) error {
    if result.Status == pipeline.OutcomeValidationOverridden && cfg.FailOnOverride {
        head := headlineOverride(result.ValidationOverrides)
        fmt.Fprintf(os.Stderr,
            "tracker: run completed via %s at %s (label %q); --fail-on-override caused non-zero exit\n",
            result.Status, head.GateNodeID, head.Label)
        return pipeline.ErrValidationOverridden
    }
    if !result.Status.IsSuccess() {
        return fmt.Errorf("pipeline finished with status: %s", result.Status)
    }
    return nil
}

// headlineOverride returns the latest entry from the slice (per D5a, the audit
// header picks the latest entry for the headline). Returns an empty detail
// if the slice is empty.
func headlineOverride(in []pipeline.OverrideDetail) pipeline.OverrideDetail {
    if len(in) == 0 {
        return pipeline.OverrideDetail{}
    }
    return in[len(in)-1]
}
```

Also apply the same `IsSuccess()` substitution at `cmd/tracker/run.go:696` (`runPipelineAsync`) and at `cmd/tracker/summary.go:447` (`printResumeHint`).

- [ ] **Step 3: Wire exit code 2 at the cobra entry**

In `cmd/tracker/main.go` (or wherever the cobra command's error is converted to `os.Exit`):

```go
if errors.Is(err, pipeline.ErrValidationOverridden) {
    os.Exit(2)
}
if err != nil {
    os.Exit(1)
}
```

- [ ] **Step 4: Run, confirm pass**

```
go test ./cmd/tracker/ -run "TestInterpretRunResult" -v
```

- [ ] **Step 5: Commit**

```
git add cmd/tracker/run.go cmd/tracker/main.go cmd/tracker/summary.go cmd/tracker/run_test.go
git commit -m "$(cat <<'EOF'
feat(cli): exit code 2 on --fail-on-override + validation_overridden

- interpretRunResult uses IsSuccess() helper, distinguishes the override case
  via ErrValidationOverridden sentinel.
- runPipelineAsync (TUI path) shares the same logic.
- cobra entry converts the sentinel to os.Exit(2); everything else stays exit 1.
- Failure dominates: --fail-on-override + genuine fail still exits 1.

Part of Gap 5.2 / #271.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

### Task 21: `tracker list` table — widen Status column + new icons

**Files:**
- Modify: `cmd/tracker/audit.go` (around line 32-48)

- [ ] **Step 1: Widen the column format string from `%-8s` to `%-10s`**

```go
fmt.Printf("  %-14s  %-10s  %-6s  %-8s  %-10s  %-26s  %s\n",
    "Run ID", "Status", "Nodes", "Retries", "Duration", "Bundle", "Failed At")
```

(Apply to both header and rows.)

- [ ] **Step 2: Extend the status icon switch**

```go
switch r.Status {
case "success":
    icon = "ok"
case "validation_overridden":
    icon = "override"
case "budget_exceeded":
    icon = "budget"
case "fail":
    icon = "FAIL"
}
```

- [ ] **Step 3: Confirm column layout fits 80-char wide terminal**

Print a mock table at terminal width 80 — confirm no wrapping. Adjust column widths if needed.

- [ ] **Step 4: Commit**

```
git add cmd/tracker/audit.go
git commit -m "feat(cli): tracker list table — widen Status column + override/budget icons"
```

---

### Task 22: `tracker audit` header — Override: chain line

**Files:**
- Modify: `cmd/tracker/audit.go` (`printAuditHeader`)

- [ ] **Step 1: Append override section to the header printer**

```go
func printAuditHeader(r *tracker.AuditReport) {
    // ... existing prints ...
    fmt.Printf("  Status:    %s", r.Status)
    if len(r.ValidationOverrides) > 0 {
        head := r.ValidationOverrides[len(r.ValidationOverrides)-1] // latest = headline per D5a
        gate := head.GateNodeID
        if len(head.SubgraphPath) > 0 {
            gate = strings.Join(append(head.SubgraphPath, head.GateNodeID), "/")
        }
        fmt.Printf(" (label %q at %s by %s)", head.Label, gate, head.Actor)
    }
    fmt.Println()
    if len(r.ValidationOverrides) > 0 {
        for _, d := range r.ValidationOverrides {
            gate := d.GateNodeID
            if len(d.SubgraphPath) > 0 {
                gate = strings.Join(append(d.SubgraphPath, d.GateNodeID), "/")
            }
            fmt.Printf("  Override:  %s → %s\n", gate, d.Label)
        }
    }
    // ... rest of header ...
}
```

- [ ] **Step 2: Commit**

```
git add cmd/tracker/audit.go
git commit -m "feat(cli): tracker audit header renders Override: chain when overrides present"
```

---

### Task 23: `tracker summary` printer — override status case + amber

**Files:**
- Modify: `cmd/tracker/summary.go` (around line 192)

- [ ] **Step 1: Add the amber color constant**

At the top of `summary.go`:

```go
var colorOverride = lipgloss.Color("#D97706") // amber-600
var overrideStyle = lipgloss.NewStyle().Foreground(colorOverride)
```

- [ ] **Step 2: Extend the status switch**

```go
switch result.Status {
case pipeline.OutcomeSuccess:
    statusText = selectedStyle.Render("✓ " + string(result.Status))
case pipeline.OutcomeValidationOverridden:
    label := ""
    if len(result.ValidationOverrides) > 0 {
        head := result.ValidationOverrides[len(result.ValidationOverrides)-1]
        label = fmt.Sprintf(" — at %s (label %q)", head.GateNodeID, head.Label)
    }
    statusText = overrideStyle.Render("● " + string(result.Status) + label)
case pipeline.OutcomeBudgetExceeded:
    statusText = mutedStyle.Render("● " + string(result.Status))
case pipeline.OutcomeFail:
    statusText = colorHot.Render("✗ " + string(result.Status))
default:
    statusText = mutedStyle.Render(statusIcon + " " + string(result.Status))
}
```

- [ ] **Step 3: Commit**

```
git add cmd/tracker/summary.go
git commit -m "feat(cli): tracker summary — amber override status with gate/label"
```

---

## Chunk 10: TUI live rendering

### Task 24: `MsgPipelineCompleted` gains `Status` + `Override`

**Files:**
- Modify: `tui/messages.go`
- Modify: `tui/adapter.go`

- [ ] **Step 1: Extend the message type**

```go
type MsgPipelineCompleted struct {
    Status   pipeline.TerminalStatus
    Override *pipeline.OverrideDetail // non-nil for override runs (headline entry)
}
```

- [ ] **Step 2: Populate from the engine result in `tui/adapter.go`**

In whatever maps `EngineResult` to `MsgPipelineCompleted`:

```go
msg := MsgPipelineCompleted{Status: result.Status}
if len(result.ValidationOverrides) > 0 {
    head := result.ValidationOverrides[len(result.ValidationOverrides)-1]
    msg.Override = &head
}
return msg
```

- [ ] **Step 3: Update the completion-row renderer**

In whatever TUI component renders the completion row, branch on Status:

```go
switch msg.Status {
case pipeline.OutcomeSuccess:
    // green check + "Completed"
case pipeline.OutcomeValidationOverridden:
    // amber bullet + "Completed — validation override at <gate> (<label> by <actor>)"
    if msg.Override != nil {
        text = fmt.Sprintf("Completed — validation override at %s (label %q by %s)",
            msg.Override.GateNodeID, msg.Override.Label, msg.Override.Actor)
    } else {
        text = "Completed — validation override"
    }
case pipeline.OutcomeBudgetExceeded:
    // red ✗ + "Budget exceeded"
case pipeline.OutcomeFail:
    // red ✗ + "Failed at <node>"
}
```

- [ ] **Step 4: Build, manual smoke-test the TUI on the conformance fixture (deferred to chunk 11)**

- [ ] **Step 5: Commit**

```
git add tui/messages.go tui/adapter.go tui/<component>.go
git commit -m "feat(tui): MsgPipelineCompleted carries Status + Override; completion row renders amber"
```

---

## Chunk 11: tracker-conformance status_class + override fixture

### Task 25: `status_class` on tracker-conformance JSON

**Files:**
- Modify: `cmd/tracker-conformance/main.go` (around line 1001)

- [ ] **Step 1: Add the paired emission**

```go
output := map[string]any{
    "status":       result.Status,
    "status_class": "failed",
}
if pipeline.TerminalStatus(result.Status).IsSuccess() {
    output["status_class"] = "succeeded"
}
```

- [ ] **Step 2: Commit**

```
git add cmd/tracker-conformance/main.go
git commit -m "feat(conformance): add status_class succeeded|failed alongside status"
```

---

### Task 26: Override conformance fixture

**Files:**
- Create: `cmd/tracker-conformance/fixtures/override.dip`

- [ ] **Step 1: Write a minimal override fixture**

```
workflow: override-conformance

nodes
  human Gate
    label: "Accept?"
    mode: yes_no

  tool End
    command: echo done

edges
  Gate -> End  label: "Yes"  override: true
```

(Adapt syntax to dippin-lang's actual grammar — confirm against examples/.)

- [ ] **Step 2: Add a conformance test that runs the fixture with `AutoApproveInterviewer` and asserts `status_class == "succeeded"`**

```go
func TestConformance_OverrideFixture(t *testing.T) {
    // ... run cmd/tracker-conformance against fixtures/override.dip ...
    var out map[string]any
    json.Unmarshal(stdout, &out)
    if out["status"] != "validation_overridden" {
        t.Errorf("status = %v, want validation_overridden", out["status"])
    }
    if out["status_class"] != "succeeded" {
        t.Errorf("status_class = %v, want succeeded", out["status_class"])
    }
}
```

- [ ] **Step 3: Commit**

```
git add cmd/tracker-conformance/fixtures/override.dip cmd/tracker-conformance/main_test.go
git commit -m "test(conformance): add override fixture validating status_class"
```

---

## Chunk 12: dippin doctor TRK102

### Task 27: TRK102 lint rule

**Files:**
- Modify: `pipeline/lint_tracker.go`
- Modify: `pipeline/lint_tracker_test.go`

- [ ] **Step 1: Failing tests**

```go
func TestTRK102_FiresOnUnmarkedOverrideShape(t *testing.T) {
    // wait.human node, label "accept", target reachable from exit without
    // another gate, upstream has when ctx.outcome = fail edge → fires.
    g := buildTRK102FiringGraph(t)
    warnings := runLint(g)
    if !containsCode(warnings, "TRK102") {
        t.Error("expected TRK102 warning")
    }
}

func TestTRK102_DoesNotFireOnApprovePlan(t *testing.T) {
    // wait.human node, label "approve", but no upstream when outcome=fail —
    // ApprovePlan shape. No warning.
    g := buildApprovePlanGraph(t)
    warnings := runLint(g)
    if containsCode(warnings, "TRK102") {
        t.Error("did not expect TRK102 on plan-approval shape")
    }
}

func TestTRK102_DoesNotFireOnAbandonLabel(t *testing.T) {
    // Label "abandon" doesn't match the heuristic list.
}

func TestTRK102_DoesNotFireOnAlreadyMarked(t *testing.T) {
    // Edge has override: true.
}

func TestTRK102_DoesNotFireOnMigratedExamples(t *testing.T) {
    // Run TRK102 over examples/build_product.dip after migration — zero warnings.
}
```

- [ ] **Step 2: Implement TRK102**

```go
// trk102_OverrideMissing warns when a wait.human edge with an accept-shape
// label routes to forward-progress reachable from exit, AND the source gate is
// reachable via a when ctx.outcome = fail edge upstream, AND the edge is NOT
// marked override: true. See spec §7.4.
func trk102_OverrideMissing(g *Graph) []LintWarning {
    var out []LintWarning
    overrideLabels := map[string]bool{
        "accept": true, "mark done": true, "approve": true,
    }
    for _, e := range g.Edges {
        if e.Override {
            continue
        }
        if !overrideLabels[strings.ToLower(e.Label)] {
            continue
        }
        srcNode := g.Nodes[e.From]
        if srcNode == nil || srcNode.Handler != "wait.human" {
            continue
        }
        if !targetReachableFromExitWithoutGate(g, e.To) {
            continue
        }
        if !gateReachableViaFailEdge(g, e.From) {
            continue
        }
        out = append(out, LintWarning{
            Code: "TRK102",
            Message: fmt.Sprintf(
                "edge from wait.human node %s to forward-progress node %s via label %q is not marked override: true. The gate is reachable from an upstream failure, which suggests this edge represents accepting a failed validation. Add override: true to record the audit signal.",
                e.From, e.To, e.Label),
        })
    }
    return out
}

// targetReachableFromExitWithoutGate returns true if there's a path from `target`
// to the run's exit node that doesn't pass through another wait.human node.
func targetReachableFromExitWithoutGate(g *Graph, target string) bool {
    visited := map[string]bool{}
    return dfsExitWithoutGate(g, target, visited)
}

func dfsExitWithoutGate(g *Graph, node string, visited map[string]bool) bool {
    if visited[node] { return false }
    visited[node] = true
    n := g.Nodes[node]
    if n == nil { return false }
    if n.Handler == "exit" { return true }
    if n.Handler == "wait.human" { return false }
    for _, e := range g.Edges {
        if e.From == node && dfsExitWithoutGate(g, e.To, visited) {
            return true
        }
    }
    return false
}

// gateReachableViaFailEdge returns true if any incoming edge to gateNodeID
// has a condition that matches outcome = fail (transitively from upstream).
func gateReachableViaFailEdge(g *Graph, gateNodeID string) bool {
    for _, e := range g.Edges {
        if e.To != gateNodeID { continue }
        if strings.Contains(strings.ToLower(e.Condition), "outcome = fail") ||
           strings.Contains(strings.ToLower(e.Condition), "outcome=fail") {
            return true
        }
    }
    return false
}
```

Wire it into the main lint runner.

- [ ] **Step 3: Run, confirm pass**

```
go test ./pipeline/ -run TestTRK102 -v
```

- [ ] **Step 4: Commit**

```
git add pipeline/lint_tracker.go pipeline/lint_tracker_test.go
git commit -m "feat(lint): TRK102 warn on unmarked override-shape edges"
```

---

## Chunk 13: Workflow migration

### Task 28: Mark override edges in example workflows

**Files:**
- Modify: `examples/build_product.dip` (3 edges)
- Modify: `examples/build_product_with_superspec.dip` (1 edge)
- Modify: `examples/ask_and_execute.dip` (1 edge)

- [ ] **Step 1: `examples/build_product.dip:1334`**

```
    EscalateMilestone -> MarkMilestoneDone label: "mark done" override: true
    # audit: human accepts unfinished milestone — workflow validation was bypassed
```

- [ ] **Step 2: `examples/build_product.dip:1336`**

```
    EscalateMilestone -> Cleanup label: "accept" override: true
    # audit: human accepts unfinished build — workflow validation was bypassed
```

- [ ] **Step 3: `examples/build_product.dip:1370`**

```
    EscalateReview -> Cleanup label: "accept" override: true
    # audit: human accepts review-flagged issues — workflow validation was bypassed
```

- [ ] **Step 4: `examples/build_product_with_superspec.dip:1016`**

```
    EscalateToHuman -> Cleanup label: "accept" override: true
    # audit: human accepts post-build issues — workflow validation was bypassed
```

- [ ] **Step 5: `examples/ask_and_execute.dip:416`**

```
    EscalateToHuman -> CommitFinal label: "accept" override: true
    # audit: human accepts verification failure — workflow validation was bypassed
```

- [ ] **Step 6: Run `dippin doctor` to confirm A grade**

```
dippin doctor examples/ask_and_execute.dip examples/build_product.dip examples/build_product_with_superspec.dip examples/deep_review.dip
```

Expected: A grade on all four; zero TRK102 warnings.

- [ ] **Step 7: Run `dippin simulate -all-paths` on the three core pipelines**

```
dippin simulate -all-paths examples/build_product.dip
dippin simulate -all-paths examples/build_product_with_superspec.dip
dippin simulate -all-paths examples/ask_and_execute.dip
```

Expected: all paths terminate.

- [ ] **Step 8: Commit**

```
git add examples/build_product.dip examples/build_product_with_superspec.dip examples/ask_and_execute.dip
git commit -m "$(cat <<'EOF'
feat(examples): mark validation-override edges with override: true

Five edges across three workflows opt into the new audit signal:
- build_product.dip: EscalateMilestone -> MarkMilestoneDone (mark done)
- build_product.dip: EscalateMilestone -> Cleanup (accept)
- build_product.dip: EscalateReview -> Cleanup (accept)
- build_product_with_superspec.dip: EscalateToHuman -> Cleanup (accept)
- ask_and_execute.dip: EscalateToHuman -> CommitFinal (accept)

Each edge gets a # audit: comment explaining the override intent. deep_review.dip
unchanged — its ReviewPlan -> ApplyFormat (approve) is plan-approval shape,
not validation-override.

Part of Gap 5.2 / #271.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Chunk 14: Documentation

### Task 29: CHANGELOG entry

**Files:**
- Modify: `CHANGELOG.md`

- [ ] **Step 1: Paste the §14 CHANGELOG block from the spec under `[Unreleased]`**

Use the full block from spec §14 (the Keep a Changelog format). Substitute `vX.Y.Z` for the actual dippin-lang version when known.

- [ ] **Step 2: Commit**

```
git add CHANGELOG.md
git commit -m "docs(changelog): v0.35.0 entry — validation_overridden + budget fix"
```

---

### Task 30: README.md edits

**Files:**
- Modify: `README.md` (lines 432 and 573)

- [ ] **Step 1: At line 573, replace the existing `OutcomeBudgetExceeded` example**

```go
// Before:
if result.Status == pipeline.OutcomeBudgetExceeded {
    fmt.Println("budget exceeded")
}

// After:
// IsSuccess() returns true for {success, validation_overridden}; classify by
// status_class for stable bucketing across future enum extensions.
if !result.Status.IsSuccess() {
    fmt.Printf("run did not complete cleanly: status=%s\n", result.Status)
}
// To branch on overrides specifically:
if len(result.ValidationOverrides) > 0 {
    fmt.Printf("run involved %d override(s)\n", len(result.ValidationOverrides))
}
```

- [ ] **Step 2: At line 432, add a "Run terminal status" subsection**

```markdown
### Run terminal status

`tracker.Result.Status` is one of:

| Value | Meaning | `IsSuccess()` |
|---|---|---|
| `success` | Run reached the success exit; all validations passed. | true |
| `validation_overridden` | Run reached the success exit, but a human, autopilot, or webhook accepted a failed validation along the way. See `Result.ValidationOverrides`. | true |
| `budget_exceeded` | A BudgetGuard halted the run. | false |
| `fail` | Run halted via failure. | false |

The enum is open — future minor releases may add new values. Use `IsSuccess()` (or `status_class` in JSON output) instead of switching on the raw string.
```

- [ ] **Step 3: Commit**

```
git add README.md
git commit -m "docs(readme): document Run terminal status + IsSuccess() helper"
```

---

### Task 31: Site content edits

**Files:**
- Modify: `site/content/glossary.html` (lines 92, 96)
- Modify: `site/content/cli.html`
- Modify: `site/content/architecture.html` (around line 299)
- Modify: `site/content/changelog.html`

- [ ] **Step 1: glossary.html — split node outcome vs run terminal status entries (per §17)**

Replace the v0.34 entry that conflates them. The two new entries:

```html
<dt>Node outcome</dt>
<dd>What a handler returns at the end of node execution. One of <code>success</code>, <code>fail</code>, <code>retry</code>. See <code>pipeline.Outcome.Status</code>.</dd>

<dt>Run terminal status</dt>
<dd>What <code>EngineResult.Status</code> carries when the run ends. One of <code>success</code>, <code>fail</code>, <code>budget_exceeded</code>, <code>validation_overridden</code>. Open enum — future minor releases may add new values. Use <code>TerminalStatus.IsSuccess()</code> to classify.</dd>
```

Update the Escalation entry (line 96) to note override marks the audit signal, not the routing.

- [ ] **Step 2: cli.html — add env var, flag, and exit-code section**

Add `TRACKER_FAIL_ON_OVERRIDE` (strict `=1`) to the env vars table. Add `--fail-on-override` to the flags table grouped with `--max-*`. Add a new "Exit codes" section:

```html
<h3>Exit codes</h3>
<ul>
  <li><code>0</code> — run completed (Status: <code>success</code> or <code>validation_overridden</code>)</li>
  <li><code>1</code> — run failed or budget exceeded</li>
  <li><code>2</code> — run completed via <code>validation_overridden</code> AND <code>--fail-on-override</code> was set</li>
</ul>
```

- [ ] **Step 3: architecture.html — add Run terminal statuses subsection (around line 299)**

Mirror the table from README.md.

- [ ] **Step 4: changelog.html — v0.35.0 entry** matching CHANGELOG.md.

- [ ] **Step 5: Commit**

```
git add site/content/glossary.html site/content/cli.html site/content/architecture.html site/content/changelog.html
git commit -m "docs(site): update glossary, CLI reference, architecture, and changelog for v0.35.0"
```

---

### Task 32: `--help` text updates

**Files:**
- (touched implicitly by the cobra flag definitions in Task 19; verify text reads correctly)

- [ ] **Step 1: Run `tracker run --help` and confirm `--fail-on-override` appears with sensible description**

```
go run ./cmd/tracker run --help | grep -A1 fail-on-override
```

- [ ] **Step 2: Run `tracker audit --help`, `tracker list --help` and confirm the new status values are mentioned**

(If the cobra long-description strings for `audit` and `list` mention status values, update them. Otherwise this task is verification-only.)

- [ ] **Step 3: Commit if any text edits made**

```
git add cmd/tracker/*.go
git commit -m "docs(cli): update --help text for new statuses and --fail-on-override"
```

---

## Final verification

Run the full verification gate per spec §12.

- [ ] `go build ./...` clean
- [ ] `go test ./... -short` — all 17 packages pass
- [ ] `TestPinnedDippinVersionMatchesGoMod` passes
- [ ] `dippin doctor examples/{ask_and_execute,build_product,build_product_with_superspec,deep_review}.dip` — A grade on all four
- [ ] `dippin doctor` zero TRK102 warnings on migrated workflows
- [ ] `dippin simulate -all-paths` on the three core pipelines — all paths terminate
- [ ] `tracker audit` on a real run exercising `EscalateReview` "accept" — confirm `Status: validation_overridden` with gate/label/actor inline; `Override:` chain line present
- [ ] `tracker run --fail-on-override` on the same fixture — exit code 2; stderr line emitted
- [ ] `tracker list` row shows `override` Status; column doesn't overflow
- [ ] Visual: TUI completion row renders amber for an override run; CLI summary header matches

Once all checks pass, hand off to `release: v0.35.0` PR (spec §16 sequence).
