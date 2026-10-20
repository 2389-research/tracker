// ABOUTME: Tests that a pause inside a subgraph child pauses the parent run
// ABOUTME: (resumable at the subgraph node) instead of becoming a fatal error.
package pipeline

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

// subgraphPauseGraphs builds parent start -> sg(subgraph) -> end around a
// child start -> work -> end, with a codergen handler driven by pause.
func subgraphPauseGraphs(pause *bool) (*Graph, *HandlerRegistry) {
	child := NewGraph("child")
	child.AddNode(&Node{ID: "cs", Shape: "Mdiamond", Label: "Start"})
	child.AddNode(&Node{ID: "work", Shape: "box", Label: "Work"})
	child.AddNode(&Node{ID: "ce", Shape: "Msquare", Label: "End"})
	child.AddEdge(&Edge{From: "cs", To: "work"})
	child.AddEdge(&Edge{From: "work", To: "ce"})

	reg := newTestRegistry()
	reg.Register(&testHandler{
		name: "codergen",
		executeFn: func(_ context.Context, node *Node, _ *PipelineContext) (Outcome, error) {
			if node.ID == "work" && *pause {
				return Outcome{}, NewPauseError(OutcomePausedBilling, errors.New("credit balance is too low"))
			}
			return Outcome{Status: OutcomeSuccess}, nil
		},
	})
	reg.Register(&testHandler{
		name:      "subgraph",
		executeFn: NewSubgraphHandler(map[string]*Graph{"child": child}, reg, nil, nil).Execute,
	})

	g := NewGraph("parent")
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond", Label: "Start"})
	g.AddNode(&Node{ID: "sg", Shape: "tab", Label: "Sub", Attrs: map[string]string{"subgraph_ref": "child"}})
	g.AddNode(&Node{ID: "end", Shape: "Msquare", Label: "End"})
	g.AddEdge(&Edge{From: "s", To: "sg"})
	g.AddEdge(&Edge{From: "sg", To: "end"})
	return g, reg
}

func TestSubgraph_ChildBillingPausePausesParent(t *testing.T) {
	pause := true
	g, reg := subgraphPauseGraphs(&pause)
	cpPath := filepath.Join(t.TempDir(), "checkpoint.json")

	var parentPaused int
	handler := PipelineEventHandlerFunc(func(evt PipelineEvent) {
		if evt.Type == EventBillingPaused && evt.NodeID == "sg" {
			parentPaused++
		}
	})
	result, err := NewEngine(g, reg, WithPipelineEventHandler(handler), WithCheckpointPath(cpPath)).Run(context.Background())
	if result == nil || result.Status != OutcomePausedBilling {
		t.Fatalf("parent status = %v (err=%v), want paused_billing", result, err)
	}
	if parentPaused != 1 {
		t.Errorf("parent billing_paused events at sg = %d, want 1", parentPaused)
	}
	cp, lerr := LoadCheckpoint(cpPath)
	if lerr != nil {
		t.Fatalf("load checkpoint: %v", lerr)
	}
	if cp.CurrentNode != "sg" || cp.IsCompleted("sg") {
		t.Fatalf("checkpoint CurrentNode=%q completed(sg)=%v; want sg, false", cp.CurrentNode, cp.IsCompleted("sg"))
	}

	pause = false
	resumed, rerr := NewEngine(g, reg, WithCheckpointPath(cpPath)).Run(context.Background())
	if rerr != nil || resumed.Status != OutcomeSuccess {
		t.Fatalf("resume status = %v err = %v, want success", resumed, rerr)
	}
}

func TestHaltForPause_ReturnsPauseError(t *testing.T) {
	pause := true
	g, reg := subgraphPauseGraphs(&pause)
	_, err := NewEngine(g, reg).Run(context.Background())
	var pe *PauseError
	if !errors.As(err, &pe) {
		t.Fatalf("run error %v does not carry a *PauseError", err)
	}
	if pe.Status != OutcomePausedBilling {
		t.Errorf("pause status = %q", pe.Status)
	}
	if err.Error() != "credit balance is too low" {
		t.Errorf("run error message = %q, want the provider error text", err.Error())
	}
}
