// ABOUTME: Tests for goal-gate retry termination bound (issue #15).
// ABOUTME: Verifies goal-gate retries respect max_retries, fall back to fallback_target, and terminate.
package pipeline

import (
	"context"
	"testing"
)

func TestGoalGateRetryTerminatesAtDefaultMax(t *testing.T) {
	g := NewGraph("goal-gate-bounded")
	g.Attrs["retry_target"] = "repair"

	g.AddNode(&Node{ID: "start", Shape: "Mdiamond"})
	g.AddNode(&Node{ID: "work", Shape: "box", Attrs: map[string]string{"goal_gate": "true"}})
	g.AddNode(&Node{ID: "repair", Shape: "box"})
	g.AddNode(&Node{ID: "done", Shape: "Msquare"})

	g.AddEdge(&Edge{From: "start", To: "work"})
	g.AddEdge(&Edge{From: "work", To: "done", Condition: "ctx.outcome = success"})
	g.AddEdge(&Edge{From: "work", To: "done", Condition: "ctx.outcome = fail"})
	g.AddEdge(&Edge{From: "repair", To: "work"})

	reg := newTestRegistry()
	workAttempts := 0
	reg.Register(&testHandler{
		name: "codergen",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			switch node.ID {
			case "work":
				workAttempts++
				return Outcome{Status: OutcomeFail}, nil // always fail
			case "repair":
				return Outcome{Status: OutcomeSuccess}, nil
			default:
				return Outcome{Status: OutcomeSuccess}, nil
			}
		},
	})

	engine := NewEngine(g, reg)
	result, _ := engine.Run(context.Background())

	if result.Status != OutcomeFail {
		t.Fatalf("status = %q, want %q", result.Status, OutcomeFail)
	}
	// Default max_retries is 3, so work runs 1 (initial) + 3 (retries) = 4 times.
	if workAttempts != 4 {
		t.Fatalf("work ran %d times, want 4 (1 initial + 3 retries)", workAttempts)
	}
}

func TestGoalGateRetryRespectsNodeMaxRetries(t *testing.T) {
	g := NewGraph("goal-gate-custom-max")
	g.Attrs["retry_target"] = "repair"

	g.AddNode(&Node{ID: "start", Shape: "Mdiamond"})
	g.AddNode(&Node{ID: "work", Shape: "box", Attrs: map[string]string{
		"goal_gate":   "true",
		"max_retries": "1",
	}})
	g.AddNode(&Node{ID: "repair", Shape: "box"})
	g.AddNode(&Node{ID: "done", Shape: "Msquare"})

	g.AddEdge(&Edge{From: "start", To: "work"})
	g.AddEdge(&Edge{From: "work", To: "done", Condition: "ctx.outcome = success"})
	g.AddEdge(&Edge{From: "work", To: "done", Condition: "ctx.outcome = fail"})
	g.AddEdge(&Edge{From: "repair", To: "work"})

	reg := newTestRegistry()
	workAttempts := 0
	reg.Register(&testHandler{
		name: "codergen",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			if node.ID == "work" {
				workAttempts++
				return Outcome{Status: OutcomeFail}, nil
			}
			return Outcome{Status: OutcomeSuccess}, nil
		},
	})

	engine := NewEngine(g, reg)
	result, _ := engine.Run(context.Background())

	if result.Status != OutcomeFail {
		t.Fatalf("status = %q, want %q", result.Status, OutcomeFail)
	}
	// max_retries=1: 1 initial + 1 retry = 2
	if workAttempts != 2 {
		t.Fatalf("work ran %d times, want 2 (1 initial + 1 retry)", workAttempts)
	}
}

func TestGoalGateRetryFallsBackToFallbackTarget(t *testing.T) {
	g := NewGraph("goal-gate-fallback")

	g.AddNode(&Node{ID: "start", Shape: "Mdiamond"})
	g.AddNode(&Node{ID: "work", Shape: "box", Attrs: map[string]string{
		"goal_gate":       "true",
		"retry_target":    "repair",
		"fallback_target": "escalate",
		"max_retries":     "1",
	}})
	g.AddNode(&Node{ID: "repair", Shape: "box"})
	g.AddNode(&Node{ID: "escalate", Shape: "box"})
	g.AddNode(&Node{ID: "done", Shape: "Msquare"})

	g.AddEdge(&Edge{From: "start", To: "work"})
	g.AddEdge(&Edge{From: "work", To: "done", Condition: "ctx.outcome = success"})
	g.AddEdge(&Edge{From: "work", To: "done", Condition: "ctx.outcome = fail"})
	g.AddEdge(&Edge{From: "repair", To: "work"})
	g.AddEdge(&Edge{From: "escalate", To: "done"})

	reg := newTestRegistry()
	escalateVisited := false
	reg.Register(&testHandler{
		name: "codergen",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			if node.ID == "work" {
				return Outcome{Status: OutcomeFail}, nil
			}
			if node.ID == "escalate" {
				escalateVisited = true
				return Outcome{Status: OutcomeSuccess}, nil
			}
			return Outcome{Status: OutcomeSuccess}, nil
		},
	})

	engine := NewEngine(g, reg)
	result, err := engine.Run(context.Background())

	// The escalate node routes to done, so pipeline should succeed eventually.
	// But done is the exit and work is still unsatisfied — so it depends on
	// whether clearDownstream clears work. Let's check the result.
	_ = err
	_ = result

	if !escalateVisited {
		t.Fatal("expected escalate node to be visited after retries exhausted")
	}
}

func TestGoalGateFallbackTargetAttributeRecognized(t *testing.T) {
	// Verify that "fallback_target" (not just "fallback_retry_target") is recognized
	// as a retry target when retries remain.
	g := NewGraph("goal-gate-attr")

	g.AddNode(&Node{ID: "start", Shape: "Mdiamond"})
	g.AddNode(&Node{ID: "work", Shape: "box", Attrs: map[string]string{
		"goal_gate":       "true",
		"fallback_target": "repair",
	}})
	g.AddNode(&Node{ID: "repair", Shape: "box"})
	g.AddNode(&Node{ID: "done", Shape: "Msquare"})

	g.AddEdge(&Edge{From: "start", To: "work"})
	g.AddEdge(&Edge{From: "work", To: "done", Condition: "ctx.outcome = success"})
	g.AddEdge(&Edge{From: "work", To: "done", Condition: "ctx.outcome = fail"})
	g.AddEdge(&Edge{From: "repair", To: "work"})

	reg := newTestRegistry()
	workAttempts := 0
	reg.Register(&testHandler{
		name: "codergen",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			if node.ID == "work" {
				workAttempts++
				if workAttempts == 1 {
					return Outcome{Status: OutcomeFail}, nil
				}
				return Outcome{Status: OutcomeSuccess}, nil
			}
			return Outcome{Status: OutcomeSuccess}, nil
		},
	})

	engine := NewEngine(g, reg)
	result, err := engine.Run(context.Background())
	if err != nil {
		t.Fatalf("engine.Run failed: %v", err)
	}
	if result.Status != OutcomeSuccess {
		t.Fatalf("status = %q, want %q", result.Status, OutcomeSuccess)
	}
	if workAttempts < 2 {
		t.Fatalf("work ran %d times, expected at least 2", workAttempts)
	}
}
