// ABOUTME: Tests for edge routing helpers in engine_edges.go — specifically the
// ABOUTME: edgeRoutingKey helper and Choice-aware selectByLabel (DIP150).
package pipeline

import (
	"testing"
)

func TestEdgeRoutingKey_ChoiceWinsOverLabel(t *testing.T) {
	e := &Edge{Label: "Approve and Continue", Choice: "approve"}
	if got := edgeRoutingKey(e); got != "approve" {
		t.Errorf("edgeRoutingKey = %q, want %q", got, "approve")
	}
}

func TestEdgeRoutingKey_LabelFallback(t *testing.T) {
	e := &Edge{Label: "Reject"}
	if got := edgeRoutingKey(e); got != "Reject" {
		t.Errorf("edgeRoutingKey = %q, want %q", got, "Reject")
	}
}

func TestEdgeRoutingKey_EmptyBoth(t *testing.T) {
	e := &Edge{}
	if got := edgeRoutingKey(e); got != "" {
		t.Errorf("edgeRoutingKey = %q, want empty", got)
	}
}

func TestSelectByLabel_ChoiceKey(t *testing.T) {
	g := NewGraph("test")
	g.StartNode = "start"
	g.ExitNode = "end"
	g.AddNode(&Node{ID: "gate", Shape: "hexagon", Handler: "wait.human"})
	g.AddNode(&Node{ID: "next", Shape: "rectangle", Handler: "tool"})
	g.AddNode(&Node{ID: "reject", Shape: "rectangle", Handler: "tool"})
	// Edge with Choice key — preferred_label should match on "approve", not the display label.
	g.AddEdge(&Edge{From: "gate", To: "next", Label: "Approve and Continue", Choice: "approve"})
	// Edge without Choice — preferred_label matches on Label directly.
	g.AddEdge(&Edge{From: "gate", To: "reject", Label: "Reject"})

	reg := NewHandlerRegistry()
	engine := NewEngine(g, reg)

	pctx := NewPipelineContext()
	pctx.Set(ContextKeyPreferredLabel, "approve")

	edges := g.OutgoingEdges("gate")
	ctxSnap := engine.routingContextSnapshot(pctx)
	selected := engine.selectByLabel("test-run", edges, pctx, ctxSnap)
	if selected == nil {
		t.Fatal("selectByLabel returned nil, want edge to next")
	}
	if selected.To != "next" {
		t.Errorf("selected.To = %q, want %q", selected.To, "next")
	}
}

func TestSelectByLabel_LabelFallback(t *testing.T) {
	g := NewGraph("test")
	g.StartNode = "start"
	g.ExitNode = "end"
	g.AddNode(&Node{ID: "gate", Shape: "hexagon", Handler: "wait.human"})
	g.AddNode(&Node{ID: "reject", Shape: "rectangle", Handler: "tool"})
	// Edge without Choice — preferred_label matches on Label.
	g.AddEdge(&Edge{From: "gate", To: "reject", Label: "Reject"})

	reg := NewHandlerRegistry()
	engine := NewEngine(g, reg)

	pctx := NewPipelineContext()
	pctx.Set(ContextKeyPreferredLabel, "Reject")

	edges := g.OutgoingEdges("gate")
	ctxSnap := engine.routingContextSnapshot(pctx)
	selected := engine.selectByLabel("test-run", edges, pctx, ctxSnap)
	if selected == nil {
		t.Fatal("selectByLabel returned nil, want edge to reject")
	}
	if selected.To != "reject" {
		t.Errorf("selected.To = %q, want %q", selected.To, "reject")
	}
}
