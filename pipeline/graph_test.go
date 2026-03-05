// ABOUTME: Tests for the pipeline graph data model.
// ABOUTME: Validates Graph, Node, Edge construction and shape-to-handler mapping.
package pipeline

import (
	"testing"
)

func TestNewGraph(t *testing.T) {
	g := NewGraph("test-pipeline")
	if g.Name != "test-pipeline" {
		t.Errorf("expected name 'test-pipeline', got %q", g.Name)
	}
	if g.Nodes == nil {
		t.Error("expected Nodes map to be initialized")
	}
	if g.Edges == nil {
		t.Error("expected Edges slice to be initialized")
	}
	if g.Attrs == nil {
		t.Error("expected Attrs map to be initialized")
	}
}

func TestGraphAddNode(t *testing.T) {
	g := NewGraph("test")
	node := &Node{
		ID:    "n1",
		Shape: "box",
		Label: "Generate Code",
		Attrs: map[string]string{"prompt": "write hello world"},
	}
	g.AddNode(node)

	got := g.Nodes["n1"]
	if got == nil {
		t.Fatal("expected node n1 to exist")
	}
	if got.Label != "Generate Code" {
		t.Errorf("expected label 'Generate Code', got %q", got.Label)
	}
	if got.Handler != "codergen" {
		t.Errorf("expected handler 'codergen' for box shape, got %q", got.Handler)
	}
}

func TestGraphAddEdge(t *testing.T) {
	g := NewGraph("test")
	g.AddNode(&Node{ID: "a", Shape: "Mdiamond"})
	g.AddNode(&Node{ID: "b", Shape: "box"})
	edge := &Edge{From: "a", To: "b", Label: "go"}
	g.AddEdge(edge)

	if len(g.Edges) != 1 {
		t.Fatalf("expected 1 edge, got %d", len(g.Edges))
	}
	if g.Edges[0].Label != "go" {
		t.Errorf("expected label 'go', got %q", g.Edges[0].Label)
	}
}

func TestShapeToHandler(t *testing.T) {
	tests := []struct {
		shape   string
		handler string
		ok      bool
	}{
		{"Mdiamond", "start", true},
		{"Msquare", "exit", true},
		{"box", "codergen", true},
		{"hexagon", "wait.human", true},
		{"diamond", "conditional", true},
		{"component", "parallel", true},
		{"tripleoctagon", "parallel.fan_in", true},
		{"parallelogram", "tool", true},
		{"unknown_shape", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.shape, func(t *testing.T) {
			handler, ok := ShapeToHandler(tt.shape)
			if ok != tt.ok {
				t.Errorf("ShapeToHandler(%q): ok = %v, want %v", tt.shape, ok, tt.ok)
			}
			if handler != tt.handler {
				t.Errorf("ShapeToHandler(%q) = %q, want %q", tt.shape, handler, tt.handler)
			}
		})
	}
}

func TestGraphStartAndExitNodes(t *testing.T) {
	g := NewGraph("test")
	g.AddNode(&Node{ID: "begin", Shape: "Mdiamond"})
	g.AddNode(&Node{ID: "work", Shape: "box"})
	g.AddNode(&Node{ID: "end", Shape: "Msquare"})

	if g.StartNode != "begin" {
		t.Errorf("expected StartNode 'begin', got %q", g.StartNode)
	}
	if g.ExitNode != "end" {
		t.Errorf("expected ExitNode 'end', got %q", g.ExitNode)
	}
}

func TestGraphOutgoingEdges(t *testing.T) {
	g := NewGraph("test")
	g.AddNode(&Node{ID: "a", Shape: "Mdiamond"})
	g.AddNode(&Node{ID: "b", Shape: "box"})
	g.AddNode(&Node{ID: "c", Shape: "box"})
	g.AddEdge(&Edge{From: "a", To: "b"})
	g.AddEdge(&Edge{From: "a", To: "c"})
	g.AddEdge(&Edge{From: "b", To: "c"})

	edges := g.OutgoingEdges("a")
	if len(edges) != 2 {
		t.Errorf("expected 2 outgoing edges from 'a', got %d", len(edges))
	}

	edges = g.OutgoingEdges("c")
	if len(edges) != 0 {
		t.Errorf("expected 0 outgoing edges from 'c', got %d", len(edges))
	}
}

func TestGraphIncomingEdges(t *testing.T) {
	g := NewGraph("test")
	g.AddNode(&Node{ID: "a", Shape: "Mdiamond"})
	g.AddNode(&Node{ID: "b", Shape: "box"})
	g.AddNode(&Node{ID: "c", Shape: "Msquare"})
	g.AddEdge(&Edge{From: "a", To: "c"})
	g.AddEdge(&Edge{From: "b", To: "c"})

	edges := g.IncomingEdges("c")
	if len(edges) != 2 {
		t.Errorf("expected 2 incoming edges to 'c', got %d", len(edges))
	}
}
