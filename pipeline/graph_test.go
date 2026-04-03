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

func TestDiamondWithPromptUsesCodergen(t *testing.T) {
	g := NewGraph("test")
	// Diamond with prompt should upgrade to codergen with auto_status.
	g.AddNode(&Node{ID: "check", Shape: "diamond", Attrs: map[string]string{
		"prompt": "Run tests. If pass: outcome=success. If fail: outcome=fail",
	}})
	node := g.Nodes["check"]
	if node.Handler != "codergen" {
		t.Errorf("expected diamond+prompt to use codergen, got %q", node.Handler)
	}
	if node.Attrs["auto_status"] != "true" {
		t.Errorf("expected auto_status=true, got %q", node.Attrs["auto_status"])
	}
}

func TestDiamondWithToolCommandUsesTool(t *testing.T) {
	g := NewGraph("test")
	// Diamond with tool_command should use tool handler, even with a prompt.
	g.AddNode(&Node{ID: "check", Shape: "diamond", Attrs: map[string]string{
		"tool_command": "echo pass",
		"prompt":       "Verify something",
	}})
	node := g.Nodes["check"]
	if node.Handler != "tool" {
		t.Errorf("expected diamond+tool_command to use tool, got %q", node.Handler)
	}
}

func TestDiamondWithoutPromptStaysConditional(t *testing.T) {
	g := NewGraph("test")
	// Diamond without prompt stays as conditional.
	g.AddNode(&Node{ID: "check", Shape: "diamond"})
	node := g.Nodes["check"]
	if node.Handler != "conditional" {
		t.Errorf("expected diamond without prompt to stay conditional, got %q", node.Handler)
	}
}

func TestDiamondWithExplicitTypeNotOverridden(t *testing.T) {
	g := NewGraph("test")
	// Diamond with explicit type should keep it, even with a prompt.
	g.AddNode(&Node{ID: "check", Shape: "diamond", Attrs: map[string]string{
		"type":   "custom_handler",
		"prompt": "some prompt",
	}})
	node := g.Nodes["check"]
	if node.Handler != "custom_handler" {
		t.Errorf("expected explicit type to be preserved, got %q", node.Handler)
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

func TestOutgoingEdgesIndexed(t *testing.T) {
	g := NewGraph("test")
	g.AddNode(&Node{ID: "a", Shape: "box", Attrs: map[string]string{}})
	g.AddNode(&Node{ID: "b", Shape: "box", Attrs: map[string]string{}})
	g.AddNode(&Node{ID: "c", Shape: "box", Attrs: map[string]string{}})
	g.AddEdge(&Edge{From: "a", To: "b"})
	g.AddEdge(&Edge{From: "a", To: "c"})
	g.AddEdge(&Edge{From: "b", To: "c"})

	if len(g.OutgoingEdges("a")) != 2 {
		t.Errorf("OutgoingEdges(a) = %d, want 2", len(g.OutgoingEdges("a")))
	}
	if len(g.OutgoingEdges("b")) != 1 {
		t.Errorf("OutgoingEdges(b) = %d, want 1", len(g.OutgoingEdges("b")))
	}
	if len(g.OutgoingEdges("c")) != 0 {
		t.Errorf("OutgoingEdges(c) = %d, want 0", len(g.OutgoingEdges("c")))
	}
	if len(g.IncomingEdges("c")) != 2 {
		t.Errorf("IncomingEdges(c) = %d, want 2", len(g.IncomingEdges("c")))
	}
	if len(g.IncomingEdges("a")) != 0 {
		t.Errorf("IncomingEdges(a) = %d, want 0", len(g.IncomingEdges("a")))
	}
}
