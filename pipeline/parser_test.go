// ABOUTME: Tests for DOT file parsing into the pipeline Graph model.
// ABOUTME: Validates node extraction, edge extraction, attribute mapping, and error handling.
package pipeline

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseSimpleDOT(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "simple.dot"))
	if err != nil {
		t.Fatalf("failed to read test DOT file: %v", err)
	}

	g, err := ParseDOT(string(data))
	if err != nil {
		t.Fatalf("ParseDOT failed: %v", err)
	}

	if g.Name != "simple_pipeline" {
		t.Errorf("expected graph name 'simple_pipeline', got %q", g.Name)
	}

	if len(g.Nodes) != 3 {
		t.Errorf("expected 3 nodes, got %d", len(g.Nodes))
	}

	if len(g.Edges) != 2 {
		t.Errorf("expected 2 edges, got %d", len(g.Edges))
	}

	if g.StartNode != "start" {
		t.Errorf("expected StartNode 'start', got %q", g.StartNode)
	}

	if g.ExitNode != "done" {
		t.Errorf("expected ExitNode 'done', got %q", g.ExitNode)
	}

	gen := g.Nodes["generate"]
	if gen == nil {
		t.Fatal("expected node 'generate' to exist")
	}
	if gen.Handler != "codergen" {
		t.Errorf("expected handler 'codergen', got %q", gen.Handler)
	}
	if gen.Label != "Generate Code" {
		t.Errorf("expected label 'Generate Code', got %q", gen.Label)
	}
	if gen.Attrs["prompt"] != "Write hello world" {
		t.Errorf("expected prompt attr 'Write hello world', got %q", gen.Attrs["prompt"])
	}
	if gen.Attrs["llm_model"] != "claude-sonnet-4-5" {
		t.Errorf("expected llm_model 'claude-sonnet-4-5', got %q", gen.Attrs["llm_model"])
	}
}

func TestParseDiamondDOT(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "diamond.dot"))
	if err != nil {
		t.Fatalf("failed to read test DOT file: %v", err)
	}

	g, err := ParseDOT(string(data))
	if err != nil {
		t.Fatalf("ParseDOT failed: %v", err)
	}

	if len(g.Nodes) != 5 {
		t.Errorf("expected 5 nodes, got %d", len(g.Nodes))
	}

	if len(g.Edges) != 5 {
		t.Errorf("expected 5 edges, got %d", len(g.Edges))
	}

	check := g.Nodes["check"]
	if check == nil {
		t.Fatal("expected node 'check' to exist")
	}
	if check.Handler != "conditional" {
		t.Errorf("expected handler 'conditional', got %q", check.Handler)
	}

	// Verify edge conditions were parsed.
	outEdges := g.OutgoingEdges("check")
	if len(outEdges) != 2 {
		t.Fatalf("expected 2 outgoing edges from 'check', got %d", len(outEdges))
	}

	conditionFound := false
	for _, e := range outEdges {
		if e.Condition == "outcome=success" {
			conditionFound = true
		}
	}
	if !conditionFound {
		t.Error("expected to find edge with condition 'outcome=success'")
	}
}

func TestParseGraphAttrs(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "simple.dot"))
	if err != nil {
		t.Fatalf("failed to read test DOT file: %v", err)
	}

	g, err := ParseDOT(string(data))
	if err != nil {
		t.Fatalf("ParseDOT failed: %v", err)
	}

	if g.Attrs["default_max_retry"] != "2" {
		t.Errorf("expected graph attr default_max_retry='2', got %q", g.Attrs["default_max_retry"])
	}
}

func TestParseEdgeLabels(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "simple.dot"))
	if err != nil {
		t.Fatalf("failed to read test DOT file: %v", err)
	}

	g, err := ParseDOT(string(data))
	if err != nil {
		t.Fatalf("ParseDOT failed: %v", err)
	}

	edges := g.OutgoingEdges("generate")
	if len(edges) != 1 {
		t.Fatalf("expected 1 outgoing edge from 'generate', got %d", len(edges))
	}
	if edges[0].Label != "success" {
		t.Errorf("expected edge label 'success', got %q", edges[0].Label)
	}
}

func TestParseInvalidDOT(t *testing.T) {
	_, err := ParseDOT("this is not valid DOT syntax {{{")
	if err == nil {
		t.Error("expected error for invalid DOT input")
	}
}

func TestParseEmptyDOT(t *testing.T) {
	_, err := ParseDOT("")
	if err == nil {
		t.Error("expected error for empty DOT input")
	}
}
