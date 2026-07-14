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
