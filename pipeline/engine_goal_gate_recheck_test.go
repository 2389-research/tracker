// ABOUTME: Tests for goal-gate recheck on retry (issue #348 defect 1).
// ABOUTME: A goal-gate retry must cause the gate to re-execute before the run can end in plain success.
package pipeline

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// recheckTestGraph builds the case-study shape (code-goblin b68b532619c3):
//
//	start → gate(goal_gate, fallback_target: escalate)
//	gate -success→ done; gate -fail→ escalate
//	escalate -route=accept→ cleanup → done   (the "accept" tail)
//	escalate -route=fix→ fix → gate          (makes the gate REACHABLE from
//	                                          escalate, so clearDownstream
//	                                          on retry clears the gate)
//
// The escalate handler always picks "accept", so the executed path routes
// AROUND the gate to done while the gate was cleared from the completed set
// — the exact mechanism that let the case-study run complete "success"
// with FinalSpecCheck still at outcome=fail.
func recheckTestGraph(maxRetries string) *Graph {
	g := NewGraph("goal-gate-recheck")
	gateAttrs := map[string]string{
		"goal_gate":       "true",
		"fallback_target": "escalate",
	}
	if maxRetries != "" {
		gateAttrs["max_retries"] = maxRetries
	}
	g.AddNode(&Node{ID: "start", Shape: "Mdiamond"})
	g.AddNode(&Node{ID: "gate", Shape: "box", Attrs: gateAttrs})
	g.AddNode(&Node{ID: "escalate", Shape: "box"})
	g.AddNode(&Node{ID: "fix", Shape: "box"})
	g.AddNode(&Node{ID: "cleanup", Shape: "box"})
	g.AddNode(&Node{ID: "done", Shape: "Msquare"})

	g.AddEdge(&Edge{From: "start", To: "gate"})
	g.AddEdge(&Edge{From: "gate", To: "done", Condition: "ctx.outcome = success"})
	g.AddEdge(&Edge{From: "gate", To: "escalate", Condition: "ctx.outcome = fail"})
	g.AddEdge(&Edge{From: "escalate", To: "cleanup", Condition: "ctx.route = accept"})
	g.AddEdge(&Edge{From: "escalate", To: "fix", Condition: "ctx.route = fix"})
	g.AddEdge(&Edge{From: "fix", To: "gate"})
	g.AddEdge(&Edge{From: "cleanup", To: "done"})
	return g
}

// TestGoalGateRetryReRunsGateAfterEscalationTail reproduces #348 defect 1:
// the gate fails once, the goal-gate retry redirects to the escalation tail
// (escalate → cleanup → done) which performs remediation but never flows
// back through the gate. The retry must cause the GATE to re-execute so it
// re-evaluates the remediated tree — here it passes on re-run and the run
// completes success with the gate genuinely satisfied.
//
// Pre-fix behavior: clearDownstream(escalate) removed the gate from the
// completed set, the accept tail routed around it, and goalGateRetryTarget
// (which only scanned CompletedNodes) lost track of the gate entirely — the
// run completed plain success with the gate at outcome=fail and exactly one
// gate execution.
func TestGoalGateRetryReRunsGateAfterEscalationTail(t *testing.T) {
	g := recheckTestGraph("")

	reg := newTestRegistry()
	gateAttempts := 0
	reg.Register(&testHandler{
		name: "codergen",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			switch node.ID {
			case "gate":
				gateAttempts++
				if gateAttempts == 1 {
					return Outcome{Status: string(OutcomeFail)}, nil
				}
				// Remediation happened in the tail (cleanup); re-run passes.
				return Outcome{Status: string(OutcomeSuccess)}, nil
			case "escalate":
				return Outcome{
					Status:         string(OutcomeSuccess),
					ContextUpdates: map[string]string{"route": "accept"},
				}, nil
			default:
				return Outcome{Status: string(OutcomeSuccess)}, nil
			}
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	engine := NewEngine(g, reg)
	result, err := engine.Run(ctx)
	if err != nil {
		t.Fatalf("engine.Run error: %v", err)
	}

	if gateAttempts < 2 {
		t.Errorf("gate ran %d times, want >= 2 — a goal-gate retry must re-execute the gate", gateAttempts)
	}
	if result.Status != OutcomeSuccess {
		t.Errorf("status = %q, want %q (gate satisfied on re-run)", result.Status, OutcomeSuccess)
	}
}

// TestGoalGateNeverSatisfiedCannotEndInPlainSuccess pins the terminal
// invariant: when the gate fails on every evaluation and the escalation
// tail keeps routing around it, the run must NOT report plain success —
// the retry budget drains, the one-shot fallback fires, and the run fails.
//
// Pre-fix behavior: the first retry's clearDownstream dropped the gate from
// the completed set and the run completed success on the next pass.
func TestGoalGateNeverSatisfiedCannotEndInPlainSuccess(t *testing.T) {
	g := recheckTestGraph("2")

	reg := newTestRegistry()
	gateAttempts := 0
	reg.Register(&testHandler{
		name: "codergen",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			switch node.ID {
			case "gate":
				gateAttempts++
				return Outcome{Status: string(OutcomeFail)}, nil // never satisfied
			case "escalate":
				return Outcome{
					Status:         string(OutcomeSuccess),
					ContextUpdates: map[string]string{"route": "accept"},
				}, nil
			default:
				return Outcome{Status: string(OutcomeSuccess)}, nil
			}
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	engine := NewEngine(g, reg)
	result, err := engine.Run(ctx)
	if err != nil {
		t.Fatalf("engine.Run error: %v", err)
	}

	if result.Status == OutcomeSuccess {
		t.Fatalf("run completed plain success with the goal gate never satisfied (gate ran %d times)", gateAttempts)
	}
}

// TestGoalGateRetryViaFixPathStillWorks pins the existing behavior that must
// not regress: when the retry target's path flows BACK through the gate
// (escalate routes to fix → gate), the gate re-executes on the normal pass
// and no extra re-entry fires. The gate fails once, the fix runs, and the
// gate passes on the second evaluation.
func TestGoalGateRetryViaFixPathStillWorks(t *testing.T) {
	g := recheckTestGraph("")

	reg := newTestRegistry()
	gateAttempts := 0
	fixRuns := 0
	reg.Register(&testHandler{
		name: "codergen",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			switch node.ID {
			case "gate":
				gateAttempts++
				if gateAttempts == 1 {
					return Outcome{Status: string(OutcomeFail)}, nil
				}
				return Outcome{Status: string(OutcomeSuccess)}, nil
			case "escalate":
				return Outcome{
					Status:         string(OutcomeSuccess),
					ContextUpdates: map[string]string{"route": "fix"},
				}, nil
			case "fix":
				fixRuns++
				return Outcome{Status: string(OutcomeSuccess)}, nil
			default:
				return Outcome{Status: string(OutcomeSuccess)}, nil
			}
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	engine := NewEngine(g, reg)
	result, err := engine.Run(ctx)
	if err != nil {
		t.Fatalf("engine.Run error: %v", err)
	}

	if result.Status != OutcomeSuccess {
		t.Fatalf("status = %q, want %q", result.Status, OutcomeSuccess)
	}
	if gateAttempts != 2 {
		t.Errorf("gate ran %d times, want exactly 2 (initial fail + fix-path re-run)", gateAttempts)
	}
	if fixRuns != 1 {
		t.Errorf("fix ran %d times, want 1", fixRuns)
	}
}

// TestGoalGateRecheckPendingSurvivesResume pins resume determinism: a run
// killed mid-escalation-tail after a goal-gate retry redirect (checkpoint
// has gate_recheck_pending set, the gate cleared from completed_nodes, and
// CurrentNode on the tail) must still re-execute the gate after resume —
// the pending flag is the durable record that the redirect fired.
func TestGoalGateRecheckPendingSurvivesResume(t *testing.T) {
	g := recheckTestGraph("")

	dir := t.TempDir()
	cpPath := filepath.Join(dir, "checkpoint.json")
	cp := &Checkpoint{
		RunID:          "resume-348",
		CurrentNode:    "escalate",
		CompletedNodes: []string{"start"},
		RetryCounts:    map[string]int{"gate": 1},
		Context:        map[string]string{},
	}
	cp.SetGateRecheckPending("gate")
	if err := SaveCheckpoint(cp, cpPath); err != nil {
		t.Fatalf("SaveCheckpoint: %v", err)
	}

	reg := newTestRegistry()
	gateAttempts := 0
	reg.Register(&testHandler{
		name: "codergen",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			switch node.ID {
			case "gate":
				gateAttempts++
				return Outcome{Status: string(OutcomeSuccess)}, nil // remediated pre-kill
			case "escalate":
				return Outcome{
					Status:         string(OutcomeSuccess),
					ContextUpdates: map[string]string{"route": "accept"},
				}, nil
			default:
				return Outcome{Status: string(OutcomeSuccess)}, nil
			}
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	engine := NewEngine(g, reg, WithCheckpointPath(cpPath))
	result, err := engine.Run(ctx)
	if err != nil {
		t.Fatalf("engine.Run error: %v", err)
	}

	if gateAttempts < 1 {
		t.Error("gate never re-executed after resume despite a pending recheck")
	}
	if result.Status != OutcomeSuccess {
		t.Errorf("status = %q, want %q", result.Status, OutcomeSuccess)
	}
}

// TestGoalGateRecheckWithSingleRetryBudget covers the codex P2 review
// finding on #360: with max_retries=1, the tail redirect consumes the whole
// retry budget, and an exhausted-branch-first check would route to fallback
// before the pending re-entry could fire — the gate would still never
// re-judge the remediated tree. The pending re-entry is the COMPLETION of
// the redirect's retry cycle (budget was charged when the redirect fired),
// so it must run before the exhausted branch and without a fresh charge.
func TestGoalGateRecheckWithSingleRetryBudget(t *testing.T) {
	g := recheckTestGraph("1")

	reg := newTestRegistry()
	gateAttempts := 0
	reg.Register(&testHandler{
		name: "codergen",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			switch node.ID {
			case "gate":
				gateAttempts++
				if gateAttempts == 1 {
					return Outcome{Status: string(OutcomeFail)}, nil
				}
				return Outcome{Status: string(OutcomeSuccess)}, nil // remediated in the tail
			case "escalate":
				return Outcome{
					Status:         string(OutcomeSuccess),
					ContextUpdates: map[string]string{"route": "accept"},
				}, nil
			default:
				return Outcome{Status: string(OutcomeSuccess)}, nil
			}
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	engine := NewEngine(g, reg)
	result, err := engine.Run(ctx)
	if err != nil {
		t.Fatalf("engine.Run error: %v", err)
	}

	if gateAttempts < 2 {
		t.Errorf("gate ran %d times, want >= 2 — the re-entry must fire even with max_retries=1", gateAttempts)
	}
	if result.Status != OutcomeSuccess {
		t.Errorf("status = %q, want %q (gate satisfied on re-run)", result.Status, OutcomeSuccess)
	}
}
