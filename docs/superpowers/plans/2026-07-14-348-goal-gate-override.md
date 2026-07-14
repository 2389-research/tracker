# #348 Defect 2 — Goal-Gate Override Wiring — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let a human "accept" on a failed `goal_gate`'s escalation resolve that gate as overridden, so the run completes `validation_overridden` instead of draining retry budget and failing (the #348 symptom).

**Architecture:** When an `override: true` edge is traversed **by a human** from escalation node `N`, mark every goal gate that actually executed-and-failed and whose fail path routes to `N` as override-satisfied (a new persisted `Checkpoint.OverriddenGates` set). `checkGoalGateNode` short-circuits an overridden gate as satisfied; the existing `buildSuccessResult` rule then flips the terminal status to `validation_overridden`. The override is cleared if the gate re-executes, so a looping workflow re-prompts on fresh work.

**Tech Stack:** Go; `pipeline/` engine package. No dippin-lang changes (uses the existing `override: true` edge attribute).

**Design spec:** `docs/superpowers/specs/2026-07-14-348-goal-gate-override-design.md` (revised after 5-expert squad review).

## Global Constraints

- **No `--no-verify`, no `--amend`.** Every commit goes through the pre-commit hook (build, tests, race, coverage ≥ 80%, complexity ≤ 8/8 cyclo+cognitive, dippin lint). If a hook fails, fix the root cause. Commits run the race detector (~50s) — allow up to 7 minutes per commit; if a background commit is killed, re-run in the foreground with an extended timeout.
- **Complexity gate is at zero headroom.** `checkGoalGateNode` is currently 7/7 and reaches 8/8 with the one-line short-circuit — add no other logic to it. New helpers (`markCoveredGoalGates`, `coveredGoalGates`, `gateRoutesTo`) must each stay ≤ 8/8.
- **Covering a failed goal gate is human-only.** The covering step runs only when `s.lastOutcome.OverrideActor == ActorHuman`. Non-human actors (`autopilot`/`webhook`/`unknown`) still record a plain `OverrideDetail` but never resolve a failed goal gate.
- **Cover only gates that executed AND failed:** `s.nodeOutcomes[G] == string(OutcomeFail)` — never a `""` (never-run) or non-`fail` outcome.
- **Mark covered gates BEFORE the `overrideAlreadyRecorded` dedup early-return** (marking is idempotent; only the `OverrideDetail` append is deduped).
- **The `checkGoalGateNode` short-circuit must sit AFTER the success/partial early-return and BEFORE the `IsGateRecheckPending` branch.** This ordering is the only guard on resume (where `nodeOutcomes` is empty).
- Match existing patterns: mirror `GateRecheckPending`'s field+methods shape; reuse `findFallbackTarget` (no fresh raw `node.Attrs["fallback_target"]` read).

---

## Task 1: Checkpoint `OverriddenGates` set + methods

**Files:**
- Modify: `pipeline/checkpoint.go` (add field near `GateRecheckPending` ~line 42; add methods near ~line 151)
- Test: `pipeline/checkpoint_test.go`

**Interfaces:**
- Produces: `Checkpoint.OverriddenGates map[string]bool`; `func (cp *Checkpoint) MarkGateOverridden(nodeID string)`; `func (cp *Checkpoint) ClearGateOverridden(nodeID string)`; `func (cp *Checkpoint) IsGateOverridden(nodeID string) bool`.

- [ ] **Step 1: Write the failing test**

Add to `pipeline/checkpoint_test.go` (mirrors the existing `TestCheckpoint_GateRecheckPending_Roundtrip`):

```go
func TestCheckpoint_OverriddenGates_Roundtrip(t *testing.T) {
	cp := &Checkpoint{}
	// nil-map safety: read/clear before any write must not panic.
	if cp.IsGateOverridden("gate") {
		t.Fatal("IsGateOverridden on nil map = true, want false")
	}
	cp.ClearGateOverridden("gate") // must not panic on nil map

	cp.MarkGateOverridden("gate")
	if !cp.IsGateOverridden("gate") {
		t.Fatal("IsGateOverridden after Mark = false, want true")
	}

	data, err := json.Marshal(cp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got Checkpoint
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !got.IsGateOverridden("gate") {
		t.Fatal("overridden gate did not survive JSON round-trip")
	}

	got.ClearGateOverridden("gate")
	if got.IsGateOverridden("gate") {
		t.Fatal("IsGateOverridden after Clear = true, want false")
	}

	// omitempty: an empty set must not appear in JSON (backward-compat).
	empty, _ := json.Marshal(&Checkpoint{})
	if strings.Contains(string(empty), "overridden_gates") {
		t.Fatalf("empty checkpoint JSON contains overridden_gates: %s", empty)
	}
}
```

If `json` or `strings` are not already imported in `checkpoint_test.go`, add them.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pipeline/ -run TestCheckpoint_OverriddenGates_Roundtrip -count=1`
Expected: FAIL — `cp.IsGateOverridden` / `cp.MarkGateOverridden` / `cp.ClearGateOverridden` undefined.

- [ ] **Step 3: Add the field**

In `pipeline/checkpoint.go`, immediately after the `GateRecheckPending map[string]bool ...` field (~line 42):

```go
	// OverriddenGates records goal-gate node IDs whose last (failed) outcome
	// a human resolved by traversing an override edge from the gate's
	// escalation (#348 defect 2). An overridden gate is treated as satisfied
	// by the exit-time goal-gate check and is not re-entered; the run
	// completes validation_overridden. Cleared when the gate re-executes so a
	// fresh failure on new work re-prompts the human. Persisted so a resumed
	// run stays resolved.
	OverriddenGates map[string]bool `json:"overridden_gates,omitempty"`
```

- [ ] **Step 4: Add the methods**

In `pipeline/checkpoint.go`, after `IsGateRecheckPending` (~line 152):

```go
// MarkGateOverridden records that a human resolved a failed goal gate via an
// override edge from its escalation (#348 defect 2).
func (cp *Checkpoint) MarkGateOverridden(nodeID string) {
	if cp.OverriddenGates == nil {
		cp.OverriddenGates = make(map[string]bool)
	}
	cp.OverriddenGates[nodeID] = true
}

// ClearGateOverridden drops a gate's override when the gate re-executes, so a
// fresh failure on new work re-prompts the human (#348 defect 2).
func (cp *Checkpoint) ClearGateOverridden(nodeID string) {
	delete(cp.OverriddenGates, nodeID)
}

// IsGateOverridden reports whether a goal gate was human-overridden (#348
// defect 2). A nil map returns false.
func (cp *Checkpoint) IsGateOverridden(nodeID string) bool {
	return cp.OverriddenGates[nodeID]
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./pipeline/ -run TestCheckpoint_OverriddenGates_Roundtrip -count=1`
Expected: PASS (`ok  github.com/2389-research/tracker/pipeline`).

- [ ] **Step 6: Commit**

```bash
git add pipeline/checkpoint.go pipeline/checkpoint_test.go
git commit -m "feat(#348): add Checkpoint.OverriddenGates set + Mark/Clear/Is methods"
```

---

## Task 2: Covering + honoring mechanism (the #348 fix)

**Files:**
- Modify: `pipeline/override.go` (add `CoveredGates` field to `OverrideDetail`)
- Modify: `pipeline/engine_checkpoint.go` (add `gateRoutesTo`, `coveredGoalGates`, `markCoveredGoalGates` helpers; add the short-circuit to `checkGoalGateNode` ~line 208)
- Modify: `pipeline/engine.go` (`recordOverrideIfPresent` ~line 691 — call the covering helper, set `CoveredGates`)
- Test: `pipeline/engine_goal_gate_override_test.go` (create)

**Interfaces:**
- Consumes: `Checkpoint.MarkGateOverridden`, `IsGateOverridden`, `ClearGateRecheckPending` (Task 1 + existing); `e.findFallbackTarget(*Node) string`; `e.graph.OutgoingEdges(string) []*Edge`; `isGoalGate(*Node) bool`; `ActorHuman`; `OutcomeFail`.
- Produces: `OverrideDetail.CoveredGates []string`; `func (e *Engine) markCoveredGoalGates(s *runState, escalationID string, actor Actor) []string`; `func (e *Engine) coveredGoalGates(s *runState, escalationID string) []string`; `func (e *Engine) gateRoutesTo(gate *Node, escalationID string) bool`.

- [ ] **Step 1: Write the failing reproduction test**

Create `pipeline/engine_goal_gate_override_test.go`:

```go
package pipeline

import (
	"context"
	"sync"
	"testing"
)

// overrideGateGraph builds a goal_gate that always fails, routing to a
// wait.human escalation whose "accept" edge carries override: true.
// fallbackOnly=true removes the direct gate->escalate edge so the gate reaches
// the escalation solely via fallback_target (exercises rule 3's fallback arm);
// fallbackOnly=false keeps a direct conditional edge and no fallback_target
// (exercises rule 3's direct-edge arm).
func overrideGateGraph(fallbackOnly bool) *Graph {
	g := NewGraph("goal-gate-override")
	gateAttrs := map[string]string{"goal_gate": "true", "max_retries": "1"}
	if fallbackOnly {
		gateAttrs["fallback_target"] = "escalate"
	}
	g.AddNode(&Node{ID: "start", Shape: "Mdiamond"})
	g.AddNode(&Node{ID: "gate", Shape: "box", Attrs: gateAttrs})
	g.AddNode(&Node{ID: "escalate", Shape: "hexagon", Attrs: map[string]string{"label": "Accept?"}})
	g.AddNode(&Node{ID: "cleanup", Shape: "box"})
	g.AddNode(&Node{ID: "done", Shape: "Msquare"})

	g.AddEdge(&Edge{From: "start", To: "gate"})
	g.AddEdge(&Edge{From: "gate", To: "done", Condition: "ctx.outcome = success"})
	if !fallbackOnly {
		g.AddEdge(&Edge{From: "gate", To: "escalate", Condition: "ctx.outcome = fail"})
	}
	g.AddEdge(&Edge{From: "escalate", To: "cleanup", Label: "accept", Override: true})
	g.AddEdge(&Edge{From: "cleanup", To: "done"})
	return g
}

// failingGoalGateRegistry returns a registry whose gate handler fails
// unconditionally (counting attempts) and whose wait.human handler accepts as a
// human. actor lets tests substitute a non-human actor.
func failingGoalGateRegistry(t *testing.T, attempts *int, mu *sync.Mutex, actor Actor) *HandlerRegistry {
	t.Helper()
	reg := newTestRegistry()
	reg.Register(&testHandler{
		name: "codergen",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			if node.ID == "gate" {
				mu.Lock()
				*attempts++
				mu.Unlock()
				return Outcome{Status: OutcomeFail}, nil
			}
			return Outcome{Status: OutcomeSuccess}, nil
		},
	})
	reg.Register(&testHandler{
		name: "wait.human",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			return Outcome{Status: OutcomeSuccess, PreferredLabel: "accept", OverrideActor: actor}, nil
		},
	})
	return reg
}

func TestGoalGateOverride_HumanAcceptCompletesValidationOverridden(t *testing.T) {
	var attempts int
	var mu sync.Mutex

	var sawEvent bool
	var covered []string
	var evtActor Actor
	handler := PipelineEventHandlerFunc(func(evt PipelineEvent) {
		if evt.Type == EventValidationOverridden && evt.Override != nil {
			sawEvent = true
			covered = append([]string(nil), evt.Override.CoveredGates...)
			evtActor = evt.Override.Actor
		}
	})

	cpPath := t.TempDir() + "/cp.json"
	g := overrideGateGraph(true)
	reg := failingGoalGateRegistry(t, &attempts, &mu, ActorHuman)
	engine := NewEngine(g, reg, WithPipelineEventHandler(handler), WithCheckpointPath(cpPath))
	result, err := engine.Run(context.Background())
	if err != nil {
		t.Fatalf("engine run failed: %v", err)
	}

	if result.Status != OutcomeValidationOverridden {
		t.Errorf("Status = %q, want %q", result.Status, OutcomeValidationOverridden)
	}
	if attempts != 1 {
		t.Errorf("gate executed %d times, want 1 (override must resolve it, not a retry)", attempts)
	}
	if !sawEvent {
		t.Error("expected EventValidationOverridden")
	}
	if len(covered) != 1 || covered[0] != "gate" {
		t.Errorf("event CoveredGates = %v, want [gate]", covered)
	}
	if evtActor != ActorHuman {
		t.Errorf("event Actor = %q, want %q", evtActor, ActorHuman)
	}

	cp, err := LoadCheckpoint(cpPath)
	if err != nil {
		t.Fatalf("load checkpoint: %v", err)
	}
	if !cp.IsGateOverridden("gate") {
		t.Error("checkpoint OverriddenGates[gate] = false, want true")
	}
	if cp.RetryCount("gate") != 0 {
		t.Errorf("RetryCount(gate) = %d, want 0 (budget untouched)", cp.RetryCount("gate"))
	}
}
```

Note: `newTestRegistry`, `testHandler`, `PipelineEventHandlerFunc`, `WithPipelineEventHandler`, `WithCheckpointPath`, `LoadCheckpoint` all already exist (see `pipeline/engine_test.go` and `pipeline/engine_goal_gate_recheck_test.go`). The gate node's `Shape: "box"` maps to the `codergen` handler; `Shape: "hexagon"` maps to `wait.human`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pipeline/ -run TestGoalGateOverride_HumanAcceptCompletesValidationOverridden -count=1`
Expected: FAIL — the gate re-runs (attempts > 1) and the run ends in `fail`, not `validation_overridden` (the override is not yet connected to the gate). Compile errors on `evt.Override.CoveredGates` / `cp.IsGateOverridden` are expected until Steps 3–6 land.

- [ ] **Step 3: Add the `CoveredGates` field**

In `pipeline/override.go`, inside `type OverrideDetail struct`, after the `Actor` field:

```go
	// CoveredGates lists the goal-gate node IDs this override resolved as
	// satisfied (#348 defect 2). Empty for a plain override edge that covers
	// no failed goal gate.
	CoveredGates []string `json:"covered_gates,omitempty"`
```

- [ ] **Step 4: Add the covering helpers**

In `pipeline/engine_checkpoint.go` (co-located with `findFallbackTarget` / `goalGateCandidates`, which already import `sort` and use `isGoalGate`), add:

```go
// gateRoutesTo reports whether goal-gate node gate's fail path leads to
// escalationID — either a direct outgoing edge gate->escalationID, or gate's
// resolved fallback target (node/graph fallback_target or fallback_retry_target).
func (e *Engine) gateRoutesTo(gate *Node, escalationID string) bool {
	if e.findFallbackTarget(gate) == escalationID {
		return true
	}
	for _, edge := range e.graph.OutgoingEdges(gate.ID) {
		if edge.To == escalationID {
			return true
		}
	}
	return false
}

// coveredGoalGates returns the goal-gate node IDs that an override edge taken
// from escalationID resolves: gates that actually executed and failed and whose
// fail path routes to escalationID. Sorted; empty when none apply (#348 defect 2).
func (e *Engine) coveredGoalGates(s *runState, escalationID string) []string {
	var covered []string
	for id, node := range e.graph.Nodes {
		if !isGoalGate(node) {
			continue
		}
		if s.nodeOutcomes[id] != string(OutcomeFail) {
			continue
		}
		if e.gateRoutesTo(node, escalationID) {
			covered = append(covered, id)
		}
	}
	sort.Strings(covered)
	return covered
}

// markCoveredGoalGates marks (and returns) the goal gates a human override at
// escalationID resolves. Non-human actors resolve nothing — a failed quality
// gate must not be auto-satisfied headless (#348 defect 2, squad I1).
func (e *Engine) markCoveredGoalGates(s *runState, escalationID string, actor Actor) []string {
	if actor != ActorHuman {
		return nil
	}
	covered := e.coveredGoalGates(s, escalationID)
	for _, g := range covered {
		s.cp.MarkGateOverridden(g)
		s.cp.ClearGateRecheckPending(g) // defensive: pending is moot once overridden
	}
	return covered
}
```

- [ ] **Step 5: Wire the covering into `recordOverrideIfPresent`**

In `pipeline/engine.go`, replace the body of `recordOverrideIfPresent` (lines 691-720) so covering happens **before** the dedup early-return and `CoveredGates` is set on the detail:

```go
func (e *Engine) recordOverrideIfPresent(s *runState, currentNodeID string, next *Edge) {
	if next == nil || !next.Override {
		return
	}
	actor := s.lastOutcome.OverrideActor
	if actor == "" {
		actor = ActorUnknown
	}
	// #348 defect 2: mark covered goal gates BEFORE the dedup early-return
	// (marking is idempotent), so a legitimate re-accept still resolves the
	// gate; only the OverrideDetail append below is deduped.
	covered := e.markCoveredGoalGates(s, currentNodeID, actor)
	if overrideAlreadyRecorded(s.validationOverrides, currentNodeID, next.Label) {
		return
	}
	detail := OverrideDetail{
		GateNodeID:   currentNodeID,
		Label:        next.Label,
		Actor:        actor,
		CoveredGates: covered,
		Timestamp:    time.Now(),
	}
	s.appendOverride(detail)
	e.emit(PipelineEvent{
		Type:      EventValidationOverridden,
		Timestamp: detail.Timestamp,
		RunID:     s.runID,
		NodeID:    currentNodeID,
		Message:   fmt.Sprintf("validation override at %q via label %q (actor=%s)", currentNodeID, next.Label, detail.Actor),
		Override:  &detail,
	})
	// Synchronously persist so a kill -9 between this point and the next
	// selectEdge does not lose the override-fired state.
	e.saveCheckpointWithTag(s.cp, s.pctx, s.runID, s, currentNodeID)
}
```

- [ ] **Step 6: Add the `checkGoalGateNode` short-circuit**

In `pipeline/engine_checkpoint.go`, inside `checkGoalGateNode`, immediately after the `success`/`partial_success` early-return (after line 208) and **before** the `if cp.IsGateRecheckPending(nodeID)` block:

```go
	// #348 defect 2: a human accept at this gate's escalation resolved it.
	// Must precede the recheck-pending branch (a gate can be both) and is the
	// only guard on resume, where nodeOutcomes is empty and the success
	// early-return above does not fire.
	if cp.IsGateOverridden(nodeID) {
		return goalGateCheckResult{}, false
	}
```

- [ ] **Step 7: Run the reproduction test to verify it passes**

Run: `go test ./pipeline/ -run TestGoalGateOverride_HumanAcceptCompletesValidationOverridden -count=1`
Expected: PASS — `attempts == 1`, `Status == validation_overridden`, `CoveredGates == [gate]`, `RetryCount == 0`.

- [ ] **Step 8: Run the full pipeline suite + complexity to confirm no regressions**

Run: `go test ./pipeline/ -short -count=1 && bash scripts/complexity/gate.sh gate`
Expected: `ok  github.com/2389-research/tracker/pipeline`; `complexity gate OK`. The existing `TestEngine_OverrideEdge_*` tests must still pass (plain overrides cover no goal gate → `CoveredGates` nil, behavior unchanged).

- [ ] **Step 9: Commit**

```bash
git add pipeline/override.go pipeline/engine_checkpoint.go pipeline/engine.go pipeline/engine_goal_gate_override_test.go
git commit -m "feat(#348): resolve human-overridden failed goal gates as validation_overridden"
```

---

## Task 3: Clear override when the gate re-executes

**Files:**
- Modify: `pipeline/engine_run.go` (`applyOutcome` goal-gate clear block ~lines 765-767)
- Test: `pipeline/engine_goal_gate_override_test.go` (add)

**Interfaces:**
- Consumes: `Checkpoint.ClearGateOverridden` (Task 1); `isGoalGate`; the `overrideGateGraph` / `failingGoalGateRegistry` helpers (Task 2).

- [ ] **Step 1: Write the failing test**

Add to `pipeline/engine_goal_gate_override_test.go`. This graph loops back through the gate after the accept, so the gate re-executes on "fresh" work; without clearing the override, the re-run gate would still auto-complete `validation_overridden`.

```go
func TestGoalGateOverride_ClearedWhenGateReExecutes(t *testing.T) {
	// gate fails; escalate --accept(override)--> loop --> gate (re-run).
	// A gate that runs a 2nd time must NOT stay overridden: it should be
	// re-judged, so the override is cleared on re-execution.
	g := NewGraph("goal-gate-override-loop")
	g.AddNode(&Node{ID: "start", Shape: "Mdiamond"})
	g.AddNode(&Node{ID: "gate", Shape: "box", Attrs: map[string]string{"goal_gate": "true", "fallback_target": "escalate", "max_retries": "1"}})
	g.AddNode(&Node{ID: "escalate", Shape: "hexagon", Attrs: map[string]string{"label": "Accept?"}})
	g.AddNode(&Node{ID: "loop", Shape: "box"})
	g.AddNode(&Node{ID: "done", Shape: "Msquare"})
	g.AddEdge(&Edge{From: "start", To: "gate"})
	g.AddEdge(&Edge{From: "gate", To: "done", Condition: "ctx.outcome = success"})
	g.AddEdge(&Edge{From: "gate", To: "escalate", Condition: "ctx.outcome = fail"})
	g.AddEdge(&Edge{From: "escalate", To: "loop", Label: "accept", Override: true})
	g.AddEdge(&Edge{From: "loop", To: "gate"})

	// Directly exercise applyOutcome's clear: after a human override marks the
	// gate, a subsequent gate execution must clear the override flag.
	cp := &Checkpoint{}
	cp.MarkGateOverridden("gate")
	if !cp.IsGateOverridden("gate") {
		t.Fatal("precondition: gate not marked overridden")
	}
	// Simulate the gate re-executing.
	e := NewEngine(g, newTestRegistry())
	s := &runState{cp: cp}
	e.clearGoalGateFlagsOnExecute(s, "gate")
	if cp.IsGateOverridden("gate") {
		t.Fatal("override not cleared when the gate re-executed")
	}
}
```

Note: this test calls a small named helper `clearGoalGateFlagsOnExecute` that Step 3 extracts from `applyOutcome`, so the clear is unit-testable without driving a full loop run. If you prefer not to add a helper, assert via a full `engine.Run` on `g` instead and check the final checkpoint's `OverriddenGates["gate"]` is absent — but the helper keeps `applyOutcome` under the complexity gate and the test focused.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./pipeline/ -run TestGoalGateOverride_ClearedWhenGateReExecutes -count=1`
Expected: FAIL — `clearGoalGateFlagsOnExecute` undefined (and, if you inline instead, the override is never cleared).

- [ ] **Step 3: Implement the clear**

In `pipeline/engine_run.go`, replace the goal-gate clear block in `applyOutcome` (lines 765-767):

```go
	if isGoalGate(e.nodeOrDefault(currentNodeID)) {
		e.clearGoalGateFlagsOnExecute(s, currentNodeID)
	}
```

And add the helper (near `applyOutcome`):

```go
// clearGoalGateFlagsOnExecute clears both the recheck-pending flag (#348
// defect 1) and the human-override flag (#348 defect 2) when a goal gate
// re-executes: the gate re-judges the current tree, so a stale override must
// not auto-pass fresh, unreviewed work.
func (e *Engine) clearGoalGateFlagsOnExecute(s *runState, nodeID string) {
	s.cp.ClearGateRecheckPending(nodeID)
	s.cp.ClearGateOverridden(nodeID)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./pipeline/ -run TestGoalGateOverride_ClearedWhenGateReExecutes -count=1`
Expected: PASS.

- [ ] **Step 5: Run the recheck suite to confirm defect-1 behavior is intact**

Run: `go test ./pipeline/ -run 'GoalGate' -count=1`
Expected: PASS — the existing `engine_goal_gate_recheck_test.go` tests (which depend on `ClearGateRecheckPending` firing on re-execution) still pass.

- [ ] **Step 6: Commit**

```bash
git add pipeline/engine_run.go pipeline/engine_goal_gate_override_test.go
git commit -m "feat(#348): clear a gate's override when it re-executes so fresh failures re-prompt"
```

---

## Task 4: Invariant + regression hardening tests

**Files:**
- Test: `pipeline/engine_goal_gate_override_test.go` (add). No production code changes — these lock the design's invariants.

**Interfaces:**
- Consumes: `overrideGateGraph`, `failingGoalGateRegistry` (Task 2); `ActorAutopilot`; `SaveCheckpoint`, `LoadCheckpoint`, `WithCheckpointPath`.

- [ ] **Step 1: Non-human actor does NOT cover the gate**

```go
func TestGoalGateOverride_NonHumanActorDoesNotCover(t *testing.T) {
	var attempts int
	var mu sync.Mutex
	g := overrideGateGraph(true)
	reg := failingGoalGateRegistry(t, &attempts, &mu, ActorAutopilot)
	result, _ := NewEngine(g, reg).Run(context.Background())
	if result.Status == OutcomeValidationOverridden {
		t.Error("autopilot override wrongly resolved a failed goal gate")
	}
	// The gate stays unsatisfied → run does not succeed via override.
	if result.Status == OutcomeSuccess {
		t.Error("failed goal gate reported plain success under autopilot")
	}
}
```

- [ ] **Step 2: Direct-edge arm of rule 3 (no fallback_target)**

```go
func TestGoalGateOverride_DirectEdgeArm(t *testing.T) {
	var attempts int
	var mu sync.Mutex
	g := overrideGateGraph(false) // direct gate->escalate edge, no fallback_target
	reg := failingGoalGateRegistry(t, &attempts, &mu, ActorHuman)
	result, err := NewEngine(g, reg).Run(context.Background())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if result.Status != OutcomeValidationOverridden || attempts != 1 {
		t.Errorf("direct-edge arm: Status=%q attempts=%d, want validation_overridden / 1", result.Status, attempts)
	}
}
```

- [ ] **Step 3: Never-run adjacent gate is NOT covered (squad C1 lock)**

```go
func TestGoalGateOverride_NeverRunGateNotCovered(t *testing.T) {
	// escalate is reachable from an early node; a later goal gate that has not
	// executed shares the escalation but must NOT be marked overridden.
	g := NewGraph("goal-gate-override-neverrun")
	g.AddNode(&Node{ID: "start", Shape: "Mdiamond"})
	g.AddNode(&Node{ID: "early", Shape: "box"})
	g.AddNode(&Node{ID: "lategate", Shape: "box", Attrs: map[string]string{"goal_gate": "true", "fallback_target": "escalate"}})
	g.AddNode(&Node{ID: "escalate", Shape: "hexagon", Attrs: map[string]string{"label": "Accept?"}})
	g.AddNode(&Node{ID: "done", Shape: "Msquare"})
	g.AddEdge(&Edge{From: "start", To: "early"})
	g.AddEdge(&Edge{From: "early", To: "escalate", Condition: "ctx.outcome = fail"})
	g.AddEdge(&Edge{From: "early", To: "lategate", Condition: "ctx.outcome = success"})
	g.AddEdge(&Edge{From: "lategate", To: "done"})
	g.AddEdge(&Edge{From: "escalate", To: "done", Label: "accept", Override: true})

	reg := newTestRegistry()
	reg.Register(&testHandler{name: "codergen", executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
		if node.ID == "early" {
			return Outcome{Status: OutcomeFail}, nil // routes to escalate; lategate never runs
		}
		return Outcome{Status: OutcomeSuccess}, nil
	}})
	reg.Register(&testHandler{name: "wait.human", executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
		return Outcome{Status: OutcomeSuccess, PreferredLabel: "accept", OverrideActor: ActorHuman}, nil
	}})

	cpPath := t.TempDir() + "/cp.json"
	NewEngine(g, reg, WithCheckpointPath(cpPath)).Run(context.Background())
	cp, err := LoadCheckpoint(cpPath)
	if err != nil {
		t.Fatalf("load checkpoint: %v", err)
	}
	if cp.IsGateOverridden("lategate") {
		t.Error("a never-run goal gate was wrongly marked overridden")
	}
}
```

- [ ] **Step 4: Negative — non-override edge leaves no override state**

```go
func TestGoalGateOverride_NonOverrideEdgeMarksNothing(t *testing.T) {
	// Human takes a plain (non-override) edge; the gate must not be resolved.
	g := NewGraph("goal-gate-override-negative")
	g.AddNode(&Node{ID: "start", Shape: "Mdiamond"})
	g.AddNode(&Node{ID: "gate", Shape: "box", Attrs: map[string]string{"goal_gate": "true", "fallback_target": "escalate", "max_retries": "1"}})
	g.AddNode(&Node{ID: "escalate", Shape: "hexagon", Attrs: map[string]string{"label": "Accept?"}})
	g.AddNode(&Node{ID: "done", Shape: "Msquare"})
	g.AddEdge(&Edge{From: "start", To: "gate"})
	g.AddEdge(&Edge{From: "gate", To: "done", Condition: "ctx.outcome = success"})
	g.AddEdge(&Edge{From: "gate", To: "escalate", Condition: "ctx.outcome = fail"})
	g.AddEdge(&Edge{From: "escalate", To: "done", Label: "reject"}) // no Override

	reg := newTestRegistry()
	reg.Register(&testHandler{name: "codergen", executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
		return Outcome{Status: OutcomeFail}, nil
	}})
	reg.Register(&testHandler{name: "wait.human", executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
		return Outcome{Status: OutcomeSuccess, PreferredLabel: "reject", OverrideActor: ActorHuman}, nil
	}})

	cpPath := t.TempDir() + "/cp.json"
	result, _ := NewEngine(g, reg, WithCheckpointPath(cpPath)).Run(context.Background())
	if result.Status == OutcomeValidationOverridden {
		t.Error("non-override edge produced validation_overridden")
	}
	if len(result.ValidationOverrides) != 0 {
		t.Errorf("ValidationOverrides = %d, want 0", len(result.ValidationOverrides))
	}
	cp, _ := LoadCheckpoint(cpPath)
	if cp.IsGateOverridden("gate") {
		t.Error("gate marked overridden on a non-override edge")
	}
}
```

- [ ] **Step 5: Resume — an overridden gate stays resolved and does NOT re-run**

```go
func TestGoalGateOverride_SurvivesResume(t *testing.T) {
	// Seed a checkpoint as if a human already overrode the gate, then resume:
	// the gate must not execute (attempts == 0) and the run completes overridden.
	var attempts int
	var mu sync.Mutex
	g := overrideGateGraph(true)
	reg := failingGoalGateRegistry(t, &attempts, &mu, ActorHuman)

	cpPath := t.TempDir() + "/cp.json"
	seed := &Checkpoint{CurrentNode: "done", CompletedNodes: []string{"start", "gate", "escalate", "cleanup"}}
	seed.MarkGateOverridden("gate")
	seed.ValidationOverrides = []OverrideDetail{{GateNodeID: "escalate", Label: "accept", Actor: ActorHuman, CoveredGates: []string{"gate"}}}
	if err := SaveCheckpoint(seed, cpPath); err != nil {
		t.Fatalf("save seed checkpoint: %v", err)
	}

	result, err := NewEngine(g, reg, WithCheckpointPath(cpPath)).Run(context.Background())
	if err != nil {
		t.Fatalf("resume run: %v", err)
	}
	if attempts != 0 {
		t.Errorf("gate executed %d times on resume, want 0 (already resolved)", attempts)
	}
	if result.Status != OutcomeValidationOverridden {
		t.Errorf("resume Status = %q, want validation_overridden", result.Status)
	}
}
```

If the exact resume-seed fields (`CurrentNode`, `CompletedNodes`) don't drive the loop straight to the exit in this engine, mirror the seeding used by `TestGoalGateRecheckPendingSurvivesResume` in `engine_goal_gate_recheck_test.go` (same file family) and adjust the completed-node set so the resumed run advances to `done`.

- [ ] **Step 6: Multi-hop escalation is out of scope (pin the limitation)**

```go
func TestGoalGateOverride_MultiHopNotCovered(t *testing.T) {
	// gate -> mid -> escalate(human, override). No direct gate->escalate edge
	// and no fallback_target: the gate is NOT covered (documented limitation).
	g := NewGraph("goal-gate-override-multihop")
	g.AddNode(&Node{ID: "start", Shape: "Mdiamond"})
	g.AddNode(&Node{ID: "gate", Shape: "box", Attrs: map[string]string{"goal_gate": "true"}})
	g.AddNode(&Node{ID: "mid", Shape: "box"})
	g.AddNode(&Node{ID: "escalate", Shape: "hexagon", Attrs: map[string]string{"label": "Accept?"}})
	g.AddNode(&Node{ID: "done", Shape: "Msquare"})
	g.AddEdge(&Edge{From: "start", To: "gate"})
	g.AddEdge(&Edge{From: "gate", To: "mid", Condition: "ctx.outcome = fail"})
	g.AddEdge(&Edge{From: "mid", To: "escalate"})
	g.AddEdge(&Edge{From: "escalate", To: "done", Label: "accept", Override: true})

	reg := newTestRegistry()
	reg.Register(&testHandler{name: "codergen", executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
		if node.ID == "gate" {
			return Outcome{Status: OutcomeFail}, nil
		}
		return Outcome{Status: OutcomeSuccess}, nil
	}})
	reg.Register(&testHandler{name: "wait.human", executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
		return Outcome{Status: OutcomeSuccess, PreferredLabel: "accept", OverrideActor: ActorHuman}, nil
	}})

	cpPath := t.TempDir() + "/cp.json"
	NewEngine(g, reg, WithCheckpointPath(cpPath)).Run(context.Background())
	cp, _ := LoadCheckpoint(cpPath)
	if cp.IsGateOverridden("gate") {
		t.Error("multi-hop escalation wrongly covered the gate (should be out of scope)")
	}
}
```

- [ ] **Step 7: Override wins over a pending recheck (short-circuit ordering lock)**

```go
func TestGoalGateOverride_OverrideWinsOverPending(t *testing.T) {
	// A gate that is BOTH recheck-pending AND overridden must NOT re-enter:
	// the IsGateOverridden short-circuit must precede the IsGateRecheckPending
	// branch in checkGoalGateNode. Guards against a future reordering.
	g := overrideGateGraph(true)
	e := NewEngine(g, newTestRegistry())
	cp := &Checkpoint{CompletedNodes: []string{"gate"}}
	cp.SetGateRecheckPending("gate")
	cp.MarkGateOverridden("gate")
	target, gateID, retry, unsatisfied := e.goalGateRetryTarget(cp, map[string]string{"gate": string(OutcomeFail)})
	if retry || unsatisfied || target != "" || gateID != "" {
		t.Errorf("overridden+pending gate re-entered: target=%q gate=%q retry=%v unsatisfied=%v (override must win)", target, gateID, retry, unsatisfied)
	}
}
```

`goalGateRetryTarget` returns `(target, goalGateNodeID, shouldRetry, unsatisfied)`; an overridden gate must yield all-zero.

- [ ] **Step 8: Run all new + regression tests**

Run: `go test ./pipeline/ -run 'GoalGateOverride|OverrideEdge' -count=1 -v`
Expected: all PASS. `TestEngine_OverrideEdge_*` (plain overrides) unchanged.

- [ ] **Step 9: Full verification gate**

Run: `go build ./... && go test ./... -short -count=1 && bash scripts/complexity/gate.sh gate`
Expected: build OK; all packages `ok`; `complexity gate OK (… 0 new)`.

- [ ] **Step 10: Commit**

```bash
git add pipeline/engine_goal_gate_override_test.go
git commit -m "test(#348): lock override invariants — actor gate, C1 never-run, pending-order, resume, multi-hop"
```

---

## Task 5: Documentation

**Files:**
- Modify: `CHANGELOG.md` (add under `## [Unreleased]`)
- Modify: `docs/architecture/handlers/conditional.md` or `docs/architecture/engine.md` (whichever documents goal gates / escalation — grep for `goal_gate` and `validation_overridden`)

- [ ] **Step 1: Update the changelog**

Under `## [Unreleased]` in `CHANGELOG.md`, add a `### Fixed` entry:

```markdown
### Fixed

- **Goal-gate escalations honor a human override (#348 defect 2).** A human who
  chooses "accept" at a failed `goal_gate`'s escalation now resolves that gate
  as overridden, so the run completes `validation_overridden` instead of
  draining retry budget and failing with the gate still at `outcome=fail`. Only
  a human actor resolves a failed gate (autopilot/`--auto-approve`/webhook still
  fail an unsatisfied goal gate); the override is recorded against the specific
  gate (`OverrideDetail.CoveredGates`) and cleared if the gate re-executes.
```

- [ ] **Step 2: Update the architecture doc**

Run: `grep -rln "validation_overridden\|goal_gate" docs/architecture/`. In the file that documents goal-gate escalation, add a short paragraph: a human `override: true` edge from a failed goal gate's escalation marks the gate overridden (`Checkpoint.OverriddenGates`), the exit check treats it as satisfied, and the terminal status becomes `validation_overridden`; non-human actors do not resolve failed gates; the override clears on gate re-execution. Keep it to 3–4 sentences and match the surrounding style.

- [ ] **Step 3: Verify docs build/links**

Run: `go build ./... && go test ./... -short -count=1`
Expected: PASS (docs are markdown; this just confirms nothing else broke).

- [ ] **Step 4: Commit**

```bash
git add CHANGELOG.md docs/architecture/
git commit -m "docs(#348): document goal-gate human-override resolution"
```

---

## Final verification (before finishing the branch)

- [ ] `go build ./...` — passes
- [ ] `go test ./... -short` — all packages pass
- [ ] `bash scripts/complexity/gate.sh gate` — `0 new` (helpers ≤ 8/8; `checkGoalGateNode` at 8/8 with only the one short-circuit added)
- [ ] `dippin doctor examples/ask_and_execute.dip examples/build_product.dip examples/build_product_with_superspec.dip` — A grade (no example `.dip` changed, so this should be unaffected — confirm)
- [ ] The reproduction test `TestGoalGateOverride_HumanAcceptCompletesValidationOverridden` fails on `main` and passes on this branch (the red→green proof for #348 defect 2)

## Out of scope (do NOT do in this plan)

- Wiring `build_product_with_superspec`'s `EscalateReview` accept edge with `override: true` (a behavior change to the shipped approval flow — separate PR with `dippin doctor`/`simulate` sign-off).
- Surfacing covered gate IDs in the human-gate prompt (informed-consent enhancement — separate).
- Any dippin-lang lint rule or `--fail-on-override` default change.
