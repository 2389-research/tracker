// ABOUTME: Regression test for #599 — a gate timeout must cancel only that gate,
// ABOUTME: not tear the whole run's interviewer down and kill later/sibling gates.
package chatops

import (
	"context"
	"testing"
	"time"

	"github.com/2389-research/tracker/pipeline"
	"github.com/2389-research/tracker/pipeline/handlers"
)

// gateNode builds a choice-mode human gate node routing to two targets.
func gateNode(id string, timeout string) *pipeline.Node {
	attrs := map[string]string{"default_choice": "alpha"}
	if timeout != "" {
		attrs["timeout"] = timeout
		attrs["timeout_action"] = "default"
	}
	return &pipeline.Node{ID: id, Shape: "hexagon", Label: id, Attrs: attrs}
}

// gateGraph wires two independent gate nodes, each with alpha/beta edges.
func gateGraph(a, b *pipeline.Node) *pipeline.Graph {
	g := pipeline.NewGraph("gate-timeout")
	for _, n := range []*pipeline.Node{a, b} {
		g.AddNode(n)
		g.AddNode(&pipeline.Node{ID: n.ID + "-alpha", Shape: "box"})
		g.AddNode(&pipeline.Node{ID: n.ID + "-beta", Shape: "box"})
		g.AddEdge(&pipeline.Edge{From: n.ID, To: n.ID + "-alpha", Label: "alpha"})
		g.AddEdge(&pipeline.Edge{From: n.ID, To: n.ID + "-beta", Label: "beta"})
	}
	return g
}

// TestGateTimeoutDoesNotTearDownInterviewer is the #599 regression at the
// transport level: gate A times out, and gate B — a LATER gate on the SAME
// ThreadInterviewer — must still resolve normally. Before the fix, the gate-A
// timeout invoked Cancel(), permanently closing the run-wide channel so gate B
// returned errGateCanceled instead of the human's answer.
func TestGateTimeoutDoesNotTearDownInterviewer(t *testing.T) {
	ui := newFakeUI()
	iv := NewThreadInterviewer(ui, seqIDs())
	nodeA := gateNode("GateA", "20ms")
	nodeB := gateNode("GateB", "")
	graph := gateGraph(nodeA, nodeB)
	h := handlers.NewHumanHandler(iv, graph)

	// Gate A: run it, let it time out (never answered).
	aDone := make(chan pipeline.Outcome, 1)
	go func() {
		out, err := h.Execute(context.Background(), nodeA, pipeline.NewPipelineContext())
		if err != nil {
			t.Errorf("gate A Execute returned error: %v", err)
		}
		aDone <- out
	}()
	// Drain gate A's posted gate so it does not shadow gate B on the fake UI.
	awaitGate(t, ui)
	select {
	case out := <-aDone:
		// timeout_action=default → success routed to the default choice.
		if out.Status != pipeline.OutcomeSuccess {
			t.Fatalf("gate A timeout outcome = %v, want success (default)", out.Status)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("gate A did not time out")
	}

	// Gate B: a LATER gate on the SAME interviewer must still work.
	bDone := make(chan pipeline.Outcome, 1)
	go func() {
		out, err := h.Execute(context.Background(), nodeB, pipeline.NewPipelineContext())
		if err != nil {
			t.Errorf("gate B Execute returned error: %v", err)
		}
		bDone <- out
	}()
	g := awaitGate(t, ui)
	if !iv.Resolve(g.ID, GateAnswer{Choice: "beta"}) {
		t.Fatal("Resolve returned false for gate B — the interviewer was torn down by gate A's timeout (#599)")
	}
	select {
	case out := <-bDone:
		if out.Status != pipeline.OutcomeSuccess || out.PreferredLabel != "beta" {
			t.Fatalf("gate B outcome = {%v %q}, want {success beta}", out.Status, out.PreferredLabel)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("gate B never resolved — the interviewer was torn down by gate A's timeout (#599)")
	}
}
