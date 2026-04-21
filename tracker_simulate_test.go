// ABOUTME: Tests for the Simulate library API — validates SimulateReport fields.
package tracker

import (
	"context"
	"strings"
	"testing"
)

const simpleSource = `workflow X
  goal: "x"
  start: S
  exit: E

  agent S
    label: "Start"
    prompt: hi

  agent E
    label: "End"
    prompt: bye

  edges
    S -> E
`

func TestSimulate_BasicGraph(t *testing.T) {
	r, err := Simulate(context.Background(), simpleSource)
	if err != nil {
		t.Fatalf("Simulate: %v", err)
	}
	if r.Format != "dip" {
		t.Errorf("format = %q, want dip", r.Format)
	}
	if len(r.Nodes) != 2 {
		t.Errorf("got %d nodes, want 2", len(r.Nodes))
	}
	if len(r.Edges) != 1 {
		t.Errorf("got %d edges, want 1", len(r.Edges))
	}
	if len(r.ExecutionPlan) != 2 {
		t.Errorf("plan length = %d, want 2", len(r.ExecutionPlan))
	}
	if r.ExecutionPlan[0].NodeID != "S" || r.ExecutionPlan[1].NodeID != "E" {
		t.Errorf("plan order wrong: %+v", r.ExecutionPlan)
	}
}

func TestSimulate_UnreachableDetection(t *testing.T) {
	// Use DOT format because dippin-lang's DIP004 validation rejects unreachable
	// nodes as a hard error. DOT format allows us to test the library's own
	// unreachable-node detection logic.
	src := `digraph pipeline {
		Start [shape=Mdiamond label="Start"];
		S [shape=box label="Middle"];
		E [shape=Msquare label="End"];
		Orphan [shape=box label="Orphan"];
		Start -> S;
		S -> E;
	}`
	r, err := Simulate(context.Background(), src)
	if err != nil {
		t.Fatalf("Simulate: %v", err)
	}
	if len(r.Unreachable) != 1 || r.Unreachable[0] != "Orphan" {
		t.Errorf("unreachable = %v, want [Orphan]", r.Unreachable)
	}
}

func TestSimulate_EdgeConditionPropagated(t *testing.T) {
	src := `workflow X
  goal: "x"
  start: S
  exit: E

  agent S
    prompt: hi

  agent E
    prompt: bye

  edges
    S -> E when ctx.outcome = success
`
	r, err := Simulate(context.Background(), src)
	if err != nil {
		t.Fatalf("Simulate: %v", err)
	}
	if len(r.Edges) != 1 {
		t.Fatalf("got %d edges, want 1", len(r.Edges))
	}
	if !strings.Contains(r.Edges[0].Condition, "outcome") {
		t.Errorf("edge condition lost: %q", r.Edges[0].Condition)
	}
}

func TestSimulate_InvalidSource(t *testing.T) {
	_, err := Simulate(context.Background(), "this is not a pipeline")
	if err == nil {
		t.Fatal("expected error for invalid source")
	}
}

// TestSimulate_CtxCancelledAtEntry verifies Simulate returns the caller's
// cancellation error immediately rather than silently parsing.
func TestSimulate_CtxCancelledAtEntry(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Simulate(ctx, simpleSource)
	if err != context.Canceled {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

func TestSimulateGraph_AcceptsPrebuiltGraph(t *testing.T) {
	// Parse once via ValidateSource, then pass the graph to SimulateGraph.
	// The resulting report's nodes/edges/plan must match what Simulate
	// produces for the same source — only Format differs (SimulateGraph
	// leaves it empty because it has no source string to detect from).
	result, err := ValidateSource(simpleSource)
	if err != nil {
		t.Fatalf("ValidateSource: %v", err)
	}
	if result.Graph == nil {
		t.Fatal("ValidateSource returned nil graph for valid source")
	}

	viaGraph, err := SimulateGraph(context.Background(), result.Graph)
	if err != nil {
		t.Fatalf("SimulateGraph: %v", err)
	}
	viaSource, err := Simulate(context.Background(), simpleSource)
	if err != nil {
		t.Fatalf("Simulate: %v", err)
	}

	if viaGraph.Format != "" {
		t.Errorf("SimulateGraph Format should be empty, got %q", viaGraph.Format)
	}
	if viaGraph.StartNode != viaSource.StartNode || viaGraph.ExitNode != viaSource.ExitNode {
		t.Errorf("start/exit mismatch: graph=(%s,%s) source=(%s,%s)",
			viaGraph.StartNode, viaGraph.ExitNode, viaSource.StartNode, viaSource.ExitNode)
	}
	if len(viaGraph.Nodes) != len(viaSource.Nodes) {
		t.Errorf("node count: graph=%d source=%d", len(viaGraph.Nodes), len(viaSource.Nodes))
	}
	if len(viaGraph.Edges) != len(viaSource.Edges) {
		t.Errorf("edge count: graph=%d source=%d", len(viaGraph.Edges), len(viaSource.Edges))
	}
	if len(viaGraph.ExecutionPlan) != len(viaSource.ExecutionPlan) {
		t.Errorf("plan length: graph=%d source=%d", len(viaGraph.ExecutionPlan), len(viaSource.ExecutionPlan))
	}
}

func TestSimulateGraph_NilGraph(t *testing.T) {
	_, err := SimulateGraph(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error for nil graph")
	}
	if !strings.Contains(err.Error(), "nil") {
		t.Errorf("err = %v, expected to mention nil", err)
	}
}

func TestSimulateGraph_CtxCancelledAtEntry(t *testing.T) {
	result, err := ValidateSource(simpleSource)
	if err != nil {
		t.Fatalf("ValidateSource: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = SimulateGraph(ctx, result.Graph)
	if err != context.Canceled {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

func TestSimulate_GraphAttrsPopulated(t *testing.T) {
	// Use DOT format to set graph-level attributes reliably without
	// depending on dippin-lang's specific syntax for workflow-level attrs.
	src := `digraph pipeline {
		graph [llm_model="claude-sonnet-4-6"];
		Start [shape=Mdiamond label="Start"];
		End [shape=Msquare label="End"];
		Start -> End;
	}`
	r, err := Simulate(context.Background(), src)
	if err != nil {
		t.Fatalf("Simulate: %v", err)
	}
	if r.GraphAttrs == nil || r.GraphAttrs["llm_model"] != "claude-sonnet-4-6" {
		t.Errorf("graph attrs missing/wrong: %+v", r.GraphAttrs)
	}
}
