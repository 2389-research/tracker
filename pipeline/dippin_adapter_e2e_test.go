// ABOUTME: Integration tests for Dippin adapter with real .dip files.
// ABOUTME: Verifies end-to-end parsing and conversion from .dip to Graph.
package pipeline

import (
	"os"
	"testing"

	"github.com/2389-research/dippin-lang/parser"
)

// TestDippinAdapter_E2E_Simple verifies round-trip for a simple workflow.
func TestDippinAdapter_E2E_Simple(t *testing.T) {
	// Read the .dip file
	source, err := os.ReadFile("testdata/simple.dip")
	if err != nil {
		t.Fatalf("failed to read testdata/simple.dip: %v", err)
	}

	// Parse with dippin-lang parser
	p := parser.NewParser(string(source), "testdata/simple.dip")
	workflow, err := p.Parse()
	if err != nil {
		t.Fatalf("parser.Parse() failed: %v", err)
	}

	// Convert to Graph
	graph, err := FromDippinIR(workflow)
	if err != nil {
		t.Fatalf("FromDippinIR() failed: %v", err)
	}

	// Verify basic properties
	if graph.Name != "simple_pipeline" {
		t.Errorf("graph.Name = %q, want %q", graph.Name, "simple_pipeline")
	}

	if graph.StartNode != "start" {
		t.Errorf("graph.StartNode = %q, want %q", graph.StartNode, "start")
	}

	if graph.ExitNode != "done" {
		t.Errorf("graph.ExitNode = %q, want %q", graph.ExitNode, "done")
	}

	// Verify nodes exist and have correct shapes
	startNode := graph.Nodes["start"]
	if startNode == nil {
		t.Fatal("start node not found")
	}
	if startNode.Shape != "Mdiamond" {
		t.Errorf("start node shape = %q, want %q", startNode.Shape, "Mdiamond")
	}

	generateNode := graph.Nodes["generate"]
	if generateNode == nil {
		t.Fatal("generate node not found")
	}
	if generateNode.Shape != "box" {
		t.Errorf("generate node shape = %q, want %q", generateNode.Shape, "box")
	}
	if generateNode.Attrs["prompt"] != "Write hello world" {
		t.Errorf("generate node prompt = %q, want %q", generateNode.Attrs["prompt"], "Write hello world")
	}
	if generateNode.Attrs["llm_model"] != "claude-sonnet-4-5" {
		t.Errorf("generate node llm_model = %q, want %q", generateNode.Attrs["llm_model"], "claude-sonnet-4-5")
	}

	doneNode := graph.Nodes["done"]
	if doneNode == nil {
		t.Fatal("done node not found")
	}
	if doneNode.Shape != "Msquare" {
		t.Errorf("done node shape = %q, want %q", doneNode.Shape, "Msquare")
	}

	// Verify edges
	if len(graph.Edges) != 2 {
		t.Fatalf("len(graph.Edges) = %d, want 2", len(graph.Edges))
	}

	// Verify the graph validates
	if err := Validate(graph); err != nil {
		t.Errorf("Validate() failed: %v", err)
	}
}

// TestDippinAdapter_E2E_CompareWithDOT verifies that a .dip and .dot file
// representing the same workflow produce equivalent graphs.
func TestDippinAdapter_E2E_CompareWithDOT(t *testing.T) {
	// Parse the .dot file
	dotSource, err := os.ReadFile("testdata/simple.dot")
	if err != nil {
		t.Fatalf("failed to read testdata/simple.dot: %v", err)
	}
	dotGraph, err := ParseDOT(string(dotSource))
	if err != nil {
		t.Fatalf("ParseDOT() failed: %v", err)
	}

	// Parse the .dip file
	dipSource, err := os.ReadFile("testdata/simple.dip")
	if err != nil {
		t.Fatalf("failed to read testdata/simple.dip: %v", err)
	}
	p := parser.NewParser(string(dipSource), "testdata/simple.dip")
	workflow, err := p.Parse()
	if err != nil {
		t.Fatalf("parser.Parse() failed: %v", err)
	}
	dipGraph, err := FromDippinIR(workflow)
	if err != nil {
		t.Fatalf("FromDippinIR() failed: %v", err)
	}

	// Compare basic properties
	if dotGraph.Name != dipGraph.Name {
		t.Errorf("names differ: DOT=%q, DIP=%q", dotGraph.Name, dipGraph.Name)
	}

	if dotGraph.StartNode != dipGraph.StartNode {
		t.Errorf("start nodes differ: DOT=%q, DIP=%q", dotGraph.StartNode, dipGraph.StartNode)
	}

	if dotGraph.ExitNode != dipGraph.ExitNode {
		t.Errorf("exit nodes differ: DOT=%q, DIP=%q", dotGraph.ExitNode, dipGraph.ExitNode)
	}

	// Compare node count
	if len(dotGraph.Nodes) != len(dipGraph.Nodes) {
		t.Errorf("node count differs: DOT=%d, DIP=%d", len(dotGraph.Nodes), len(dipGraph.Nodes))
	}

	// Compare specific node attributes
	for id := range dotGraph.Nodes {
		dotNode := dotGraph.Nodes[id]
		dipNode := dipGraph.Nodes[id]
		if dipNode == nil {
			t.Errorf("node %q exists in DOT but not in DIP", id)
			continue
		}

		if dotNode.Shape != dipNode.Shape {
			t.Errorf("node %q shape differs: DOT=%q, DIP=%q", id, dotNode.Shape, dipNode.Shape)
		}

		// Note: handlers may differ for start/exit nodes since DOT uses special
		// "start"/"exit" handlers while DIP uses the node kind's handler
		// (e.g. "codergen" for agent nodes). This is expected and both are valid.
		// We only compare handlers for non-start/exit nodes.
		if id != dotGraph.StartNode && id != dotGraph.ExitNode {
			if dotNode.Handler != dipNode.Handler {
				t.Errorf("node %q handler differs: DOT=%q, DIP=%q", id, dotNode.Handler, dipNode.Handler)
			}
		}
	}

	// Compare edge count
	if len(dotGraph.Edges) != len(dipGraph.Edges) {
		t.Errorf("edge count differs: DOT=%d, DIP=%d", len(dotGraph.Edges), len(dipGraph.Edges))
	}
}
