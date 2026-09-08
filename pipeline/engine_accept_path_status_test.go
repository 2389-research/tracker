// ABOUTME: #633 terminal-status guards: how the run reached the exit node
// ABOUTME: decides the terminal status — an operator rejection at a human gate
// ABOUTME: must not be reported success, an accepted (override) run must not
// ABOUTME: be reported fail.
package pipeline

import (
	"context"
	"testing"
)

// acceptPathGraph builds the #633 repro shape: a NON-goal-gate node that
// fails, routes to a human escalation gate; the operator's "accept" override
// edge sends the run on to cleanup + a final node and then to the exit node.
// goalGated=true marks the failing node as a goal gate (the FinalSpecCheck
// variant). directAccept routes the gate's accept override edge straight to
// the exit node instead of through cleanup/final.
func acceptPathGraph(goalGated, directAccept bool) *Graph {
	g := NewGraph("accept-path")
	workAttrs := map[string]string{"max_retries": "0"}
	if goalGated {
		workAttrs["goal_gate"] = "true"
	}
	g.AddNode(&Node{ID: "start", Shape: "Mdiamond"})
	g.AddNode(&Node{ID: "work", Shape: "box", Attrs: workAttrs})
	g.AddNode(&Node{ID: "escalate", Shape: "hexagon"})
	g.AddNode(&Node{ID: "cleanup", Shape: "box"})
	g.AddNode(&Node{ID: "final", Shape: "box"})
	g.AddNode(&Node{ID: "done", Shape: "Msquare"})

	g.AddEdge(&Edge{From: "start", To: "work"})
	g.AddEdge(&Edge{From: "work", To: "escalate", Condition: "ctx.outcome = fail"})
	if directAccept {
		g.AddEdge(&Edge{From: "escalate", To: "done", Label: "accept", Override: true})
	} else {
		g.AddEdge(&Edge{From: "escalate", To: "cleanup", Label: "accept", Override: true})
		g.AddEdge(&Edge{From: "cleanup", To: "final"})
		g.AddEdge(&Edge{From: "final", To: "done"})
	}
	g.AddEdge(&Edge{From: "escalate", To: "done", Label: "abandon"})
	return g
}

// acceptPathRegistry drives the #633 graph: "work" fails once then succeeds
// (so a re-execution, if any, does not loop), every other node succeeds, and
// the human gate accepts (accept=true) or abandons as a human.
func acceptPathRegistry(t *testing.T, accept bool) *HandlerRegistry {
	t.Helper()
	reg := newTestRegistry()
	workFailed := false
	reg.Register(&testHandler{
		name: "codergen",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			if node.ID == "work" && !workFailed {
				workFailed = true
				return Outcome{Status: OutcomeFail}, nil
			}
			return Outcome{Status: OutcomeSuccess}, nil
		},
	})
	reg.Register(&testHandler{
		name: "wait.human",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			label := "accept"
			if !accept {
				label = "abandon"
			}
			return Outcome{Status: OutcomeSuccess, PreferredLabel: label, OverrideActor: ActorHuman}, nil
		},
	})
	return reg
}

func runAcceptPath(t *testing.T, goalGated, directAccept, accept bool) TerminalStatus {
	t.Helper()
	engine := NewEngine(acceptPathGraph(goalGated, directAccept), acceptPathRegistry(t, accept),
		WithCheckpointPath(t.TempDir()+"/cp.json"))
	result, err := engine.Run(context.Background())
	if err != nil {
		t.Fatalf("engine run failed: %v", err)
	}
	return result.Status
}

// TestAcceptPath_ReachesExitSucceeds pins #633 AC1: a run that a human
// accepted at an escalation gate and that then runs cleanly to the exit node
// must NOT be reported as fail — regardless of whether the accepted node was
// a goal gate and whether the accept edge goes straight to the exit.
func TestAcceptPath_ReachesExitSucceeds(t *testing.T) {
	for _, goalGated := range []bool{false, true} {
		for _, directAccept := range []bool{false, true} {
			gg, da := goalGated, directAccept
			name := "non-goal-gate"
			if gg {
				name = "goal-gate"
			}
			if da {
				name += "/direct-exit"
			}
			t.Run(name, func(t *testing.T) {
				status := runAcceptPath(t, gg, da, true)
				if !status.IsSuccess() {
					t.Errorf("Status = %q, want a success terminal (success or validation_overridden) — #633: an accepted run that reached the exit node is reported failed", status)
				}
				// A human accept traversed an override edge: the run must be
				// auditable as validation-overridden, never plain success.
				if status != OutcomeValidationOverridden {
					t.Errorf("Status = %q, want %q — the accept edge is override-marked", status, OutcomeValidationOverridden)
				}
			})
		}
	}
}

// TestAcceptPath_AbandonStaysFail pins #633's inverse: abandon routes to the
// exit node too, but must remain a non-success terminal — the exit node's own
// passthrough success must not mask the operator's rejection.
func TestAcceptPath_AbandonStaysFail(t *testing.T) {
	for _, goalGated := range []bool{false, true} {
		gg := goalGated
		name := "non-goal-gate"
		if gg {
			name = "goal-gate"
		}
		t.Run(name, func(t *testing.T) {
			status := runAcceptPath(t, gg, false, false)
			if status.IsSuccess() {
				t.Errorf("Status = %q, want a non-success terminal — #633: an abandoned run that reached the exit node is reported success", status)
			}
		})
	}
}

// TestAcceptPath_NormalCompletionSucceeds guards the common path: a run whose
// last node before exit is a plain work node (no human gate, no failure) must
// still terminate success.
func TestAcceptPath_NormalCompletionSucceeds(t *testing.T) {
	g := NewGraph("normal-completion")
	g.AddNode(&Node{ID: "start", Shape: "Mdiamond"})
	g.AddNode(&Node{ID: "work", Shape: "box"})
	g.AddNode(&Node{ID: "done", Shape: "Msquare"})
	g.AddEdge(&Edge{From: "start", To: "work"})
	g.AddEdge(&Edge{From: "work", To: "done"})

	engine := NewEngine(g, newTestRegistry(), WithCheckpointPath(t.TempDir()+"/cp.json"))
	result, err := engine.Run(context.Background())
	if err != nil {
		t.Fatalf("engine run failed: %v", err)
	}
	if result.Status != OutcomeSuccess {
		t.Errorf("Status = %q, want %q — an uncontested completion must not be demoted", result.Status, OutcomeSuccess)
	}
}
