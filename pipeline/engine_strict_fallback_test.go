// ABOUTME: Tests for strict-failure fallback catch-all (issue #295).
// ABOUTME: An unhandled agent OutcomeFail (only unconditional edges) routes to a
// node/graph-level fallback_target instead of dead-stopping the pipeline.
package pipeline

import (
	"context"
	"testing"
	"time"
)

// traceEdgeTo returns the EdgeTo of the first trace entry for nodeID.
func traceEdgeTo(tr *Trace, nodeID string) (string, bool) {
	for _, e := range tr.Entries {
		if e.NodeID == nodeID {
			return e.EdgeTo, true
		}
	}
	return "", false
}

// TestStrictFailureRoutesToGraphFallbackTarget verifies that a bare agent
// OutcomeFail with only unconditional edges routes to the graph-level
// fallback_target and the run actually reaches that node, rather than halting.
func TestStrictFailureRoutesToGraphFallbackTarget(t *testing.T) {
	g := NewGraph("strict-fallback-graph")
	g.Attrs["fallback_target"] = "escalate"

	g.AddNode(&Node{ID: "start", Shape: "Mdiamond"})
	g.AddNode(&Node{ID: "agentFail", Shape: "box"})
	g.AddNode(&Node{ID: "escalate", Shape: "box"})
	g.AddNode(&Node{ID: "done", Shape: "Msquare"})

	g.AddEdge(&Edge{From: "start", To: "agentFail"})
	g.AddEdge(&Edge{From: "agentFail", To: "done"}) // only edge: unconditional
	g.AddEdge(&Edge{From: "escalate", To: "done"})

	reg := newTestRegistry()
	escalateVisited := false
	reg.Register(&testHandler{
		name: "codergen",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			switch node.ID {
			case "agentFail":
				return Outcome{Status: OutcomeFail}, nil
			case "escalate":
				escalateVisited = true
				return Outcome{Status: OutcomeSuccess}, nil
			default:
				return Outcome{Status: OutcomeSuccess}, nil
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
	if !escalateVisited {
		t.Fatal("expected escalate node to be reached via fallback_target")
	}
	if edgeTo, ok := traceEdgeTo(result.Trace, "agentFail"); !ok || edgeTo != "escalate" {
		t.Fatalf("agentFail trace EdgeTo = %q (found=%v), want %q", edgeTo, ok, "escalate")
	}
}

// TestStrictFailureNodeFallbackPrecedesGraph verifies node-level fallback_target
// wins over the graph-level one on the strict-failure path.
func TestStrictFailureNodeFallbackPrecedesGraph(t *testing.T) {
	g := NewGraph("strict-fallback-precedence")
	g.Attrs["fallback_target"] = "graphEscalate"

	g.AddNode(&Node{ID: "start", Shape: "Mdiamond"})
	g.AddNode(&Node{ID: "agentFail", Shape: "box", Attrs: map[string]string{
		"fallback_target": "nodeEscalate",
	}})
	g.AddNode(&Node{ID: "nodeEscalate", Shape: "box"})
	g.AddNode(&Node{ID: "graphEscalate", Shape: "box"})
	g.AddNode(&Node{ID: "done", Shape: "Msquare"})

	g.AddEdge(&Edge{From: "start", To: "agentFail"})
	g.AddEdge(&Edge{From: "agentFail", To: "done"})
	g.AddEdge(&Edge{From: "nodeEscalate", To: "done"})
	g.AddEdge(&Edge{From: "graphEscalate", To: "done"})

	reg := newTestRegistry()
	var visited string
	reg.Register(&testHandler{
		name: "codergen",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			switch node.ID {
			case "agentFail":
				return Outcome{Status: OutcomeFail}, nil
			case "nodeEscalate", "graphEscalate":
				visited = node.ID
				return Outcome{Status: OutcomeSuccess}, nil
			default:
				return Outcome{Status: OutcomeSuccess}, nil
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
	if visited != "nodeEscalate" {
		t.Fatalf("visited = %q, want node-level fallback %q", visited, "nodeEscalate")
	}
	if edgeTo, _ := traceEdgeTo(result.Trace, "agentFail"); edgeTo != "nodeEscalate" {
		t.Fatalf("agentFail trace EdgeTo = %q, want %q", edgeTo, "nodeEscalate")
	}
}

// TestStrictFailureFallbackTakenAtMostOnce verifies the FallbackTaken guard: a
// node that re-enters after the fallback target loops back to it does NOT take
// the fallback a second time — it halts as today.
func TestStrictFailureFallbackTakenAtMostOnce(t *testing.T) {
	g := NewGraph("strict-fallback-once")
	g.Attrs["fallback_target"] = "rescue"

	g.AddNode(&Node{ID: "start", Shape: "Mdiamond"})
	g.AddNode(&Node{ID: "agentFail", Shape: "box"})
	g.AddNode(&Node{ID: "rescue", Shape: "box"})
	g.AddNode(&Node{ID: "done", Shape: "Msquare"})

	g.AddEdge(&Edge{From: "start", To: "agentFail"})
	g.AddEdge(&Edge{From: "agentFail", To: "done"})
	g.AddEdge(&Edge{From: "rescue", To: "agentFail"}) // loop back into the failing node

	reg := newTestRegistry()
	agentFailCalls := 0
	rescueCalls := 0
	reg.Register(&testHandler{
		name: "codergen",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			switch node.ID {
			case "agentFail":
				agentFailCalls++
				return Outcome{Status: OutcomeFail}, nil
			case "rescue":
				rescueCalls++
				return Outcome{Status: OutcomeSuccess}, nil
			default:
				return Outcome{Status: OutcomeSuccess}, nil
			}
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	engine := NewEngine(g, reg)
	result, err := engine.Run(ctx)

	if rescueCalls != 1 {
		t.Fatalf("rescue ran %d times, want exactly 1 (fallback taken at most once)", rescueCalls)
	}
	if agentFailCalls != 2 {
		t.Fatalf("agentFail ran %d times, want 2 (initial + one loop-back)", agentFailCalls)
	}
	if result.Status != OutcomeFail {
		t.Fatalf("status = %q, want %q (halt on second unhandled failure)", result.Status, OutcomeFail)
	}
	if err == nil {
		t.Fatal("expected terminal error on the second unhandled failure")
	}
}

// TestStrictFailureFallbackRespectsBudget verifies that when the failed node's
// own usage already breaches a hard budget ceiling, the run halts on budget
// instead of spending more by running the fallback node. The strict-failure
// fallback advance must apply the same post-node budget check as the normal
// advanceToNextNode path (#311 review, Codex).
func TestStrictFailureFallbackRespectsBudget(t *testing.T) {
	g := NewGraph("strict-fallback-budget")
	g.Attrs["fallback_target"] = "escalate"

	g.AddNode(&Node{ID: "start", Shape: "Mdiamond"})
	g.AddNode(&Node{ID: "agentFail", Shape: "box"})
	g.AddNode(&Node{ID: "escalate", Shape: "box"})
	g.AddNode(&Node{ID: "done", Shape: "Msquare"})

	g.AddEdge(&Edge{From: "start", To: "agentFail"})
	g.AddEdge(&Edge{From: "agentFail", To: "done"})
	g.AddEdge(&Edge{From: "escalate", To: "done"})

	reg := newTestRegistry()
	escalateVisited := false
	reg.Register(&testHandler{
		name: "codergen",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			switch node.ID {
			case "agentFail":
				// Spend past the 1000-token ceiling before failing.
				return Outcome{Status: OutcomeFail, Stats: &SessionStats{TotalTokens: 5000}}, nil
			case "escalate":
				escalateVisited = true
				return Outcome{Status: OutcomeSuccess}, nil
			default:
				return Outcome{Status: OutcomeSuccess}, nil
			}
		},
	})

	guard := NewBudgetGuard(BudgetLimits{MaxTotalTokens: 1000})
	engine := NewEngine(g, reg, WithBudgetGuard(guard))
	result, _ := engine.Run(context.Background())

	if escalateVisited {
		t.Fatal("escalate must NOT run: the failed node already breached the token budget")
	}
	if result.Status != OutcomeBudgetExceeded {
		t.Fatalf("status = %q, want %q", result.Status, OutcomeBudgetExceeded)
	}
}

// TestStrictFailureNoFallbackPreservesHalt pins the unchanged behavior when no
// node/graph fallback_target is declared: same status and same error string.
func TestStrictFailureNoFallbackPreservesHalt(t *testing.T) {
	g := NewGraph("strict-fallback-none")

	g.AddNode(&Node{ID: "start", Shape: "Mdiamond"})
	g.AddNode(&Node{ID: "agentFail", Shape: "box"})
	g.AddNode(&Node{ID: "done", Shape: "Msquare"})

	g.AddEdge(&Edge{From: "start", To: "agentFail"})
	g.AddEdge(&Edge{From: "agentFail", To: "done"})

	reg := newTestRegistry()
	reg.Register(&testHandler{
		name: "codergen",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			if node.ID == "agentFail" {
				return Outcome{Status: OutcomeFail}, nil
			}
			return Outcome{Status: OutcomeSuccess}, nil
		},
	})

	engine := NewEngine(g, reg)
	result, err := engine.Run(context.Background())

	if result.Status != OutcomeFail {
		t.Fatalf("status = %q, want %q", result.Status, OutcomeFail)
	}
	const wantErr = `node "agentFail" failed with no conditional edges to handle failure`
	if err == nil || err.Error() != wantErr {
		t.Fatalf("err = %v, want %q", err, wantErr)
	}
}
