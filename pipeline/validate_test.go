// ABOUTME: Tests for pipeline graph validation rules.
// ABOUTME: Validates start/exit node requirements, cycle detection, shape recognition, and reachability.
package pipeline

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateSimpleGraph(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "simple.dot"))
	if err != nil {
		t.Fatalf("failed to read DOT file: %v", err)
	}
	g, err := ParseDOT(string(data))
	if err != nil {
		t.Fatalf("ParseDOT failed: %v", err)
	}
	if err := Validate(g); err != nil {
		t.Errorf("expected simple graph to be valid, got: %v", err)
	}
}

func TestValidateDiamondGraph(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "diamond.dot"))
	if err != nil {
		t.Fatalf("failed to read DOT file: %v", err)
	}
	g, err := ParseDOT(string(data))
	if err != nil {
		t.Fatalf("ParseDOT failed: %v", err)
	}
	if err := Validate(g); err != nil {
		t.Errorf("expected diamond graph to be valid, got: %v", err)
	}
}

func TestValidateNoStartNode(t *testing.T) {
	g := NewGraph("no-start")
	g.AddNode(&Node{ID: "a", Shape: "box"})
	g.AddNode(&Node{ID: "b", Shape: "Msquare"})
	g.AddEdge(&Edge{From: "a", To: "b"})

	err := Validate(g)
	if err == nil {
		t.Fatal("expected error for missing start node")
	}
	if !strings.Contains(err.Error(), "start") {
		t.Errorf("error should mention 'start', got: %v", err)
	}
}

func TestValidateNoExitNode(t *testing.T) {
	g := NewGraph("no-exit")
	g.AddNode(&Node{ID: "a", Shape: "Mdiamond"})
	g.AddNode(&Node{ID: "b", Shape: "box"})
	g.AddEdge(&Edge{From: "a", To: "b"})

	err := Validate(g)
	if err == nil {
		t.Fatal("expected error for missing exit node")
	}
	if !strings.Contains(err.Error(), "exit") {
		t.Errorf("error should mention 'exit', got: %v", err)
	}
}

func TestValidateMultipleStartNodes(t *testing.T) {
	g := NewGraph("multi-start")
	g.AddNode(&Node{ID: "s1", Shape: "Mdiamond"})
	g.AddNode(&Node{ID: "s2", Shape: "Mdiamond"})
	g.AddNode(&Node{ID: "end", Shape: "Msquare"})
	g.AddEdge(&Edge{From: "s1", To: "end"})
	g.AddEdge(&Edge{From: "s2", To: "end"})

	err := Validate(g)
	if err == nil {
		t.Fatal("expected error for multiple start nodes")
	}
	if !strings.Contains(err.Error(), "start") {
		t.Errorf("error should mention 'start', got: %v", err)
	}
}

func TestValidateCycleDetection(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "cycle.dot"))
	if err != nil {
		t.Fatalf("failed to read DOT file: %v", err)
	}
	g, err := ParseDOT(string(data))
	if err != nil {
		t.Fatalf("ParseDOT failed: %v", err)
	}

	err = Validate(g)
	if err == nil {
		t.Fatal("expected error for graph with cycle")
	}
	if !strings.Contains(err.Error(), "cycle") {
		t.Errorf("error should mention 'cycle', got: %v", err)
	}
}

func TestValidateUnrecognizedShape(t *testing.T) {
	g := NewGraph("bad-shape")
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond"})
	g.AddNode(&Node{ID: "x", Shape: "trapezium"})
	g.AddNode(&Node{ID: "e", Shape: "Msquare"})
	g.AddEdge(&Edge{From: "s", To: "x"})
	g.AddEdge(&Edge{From: "x", To: "e"})

	err := Validate(g)
	if err == nil {
		t.Fatal("expected error for unrecognized shape")
	}
	if !strings.Contains(err.Error(), "trapezium") {
		t.Errorf("error should mention 'trapezium', got: %v", err)
	}
}

func TestValidateUnreachableNode(t *testing.T) {
	g := NewGraph("unreachable")
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond"})
	g.AddNode(&Node{ID: "a", Shape: "box"})
	g.AddNode(&Node{ID: "orphan", Shape: "box"})
	g.AddNode(&Node{ID: "e", Shape: "Msquare"})
	g.AddEdge(&Edge{From: "s", To: "a"})
	g.AddEdge(&Edge{From: "a", To: "e"})

	err := Validate(g)
	if err == nil {
		t.Fatal("expected error for unreachable node")
	}
	if !strings.Contains(err.Error(), "unreachable") {
		t.Errorf("error should mention 'unreachable', got: %v", err)
	}
}

func TestValidateEmptyGraph(t *testing.T) {
	g := NewGraph("empty")
	err := Validate(g)
	if err == nil {
		t.Fatal("expected error for empty graph")
	}
}
