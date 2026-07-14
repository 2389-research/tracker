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
	// max_retries=0: the first failure immediately takes the uncharged
	// exhausted-budget fallback path (goalGateExhaustedPath), matching the
	// "RetryCount untouched" assertion below — a charged retry redirect
	// would bump RetryCounts[gate] to 1 even though the human override (not
	// a remediation retry) is what actually resolves the gate.
	gateAttrs := map[string]string{"goal_gate": "true", "max_retries": "0"}
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
	if fallbackOnly {
		// No direct gate->escalate edge: the fail outcome must route to the
		// exit node first (matching the established goal-gate pattern in
		// engine_goal_gate_test.go), where goalGateRetryTarget's exhausted/
		// remaining-path logic redirects to fallback_target ("escalate").
		g.AddEdge(&Edge{From: "gate", To: "done", Condition: "ctx.outcome = fail"})
	} else {
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
