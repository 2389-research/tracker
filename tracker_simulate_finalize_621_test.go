// ABOUTME: Regression guard for issue #621 — the simulate path must analyze the
// ABOUTME: same FINALIZED topology as execution, so a structurally-invalid graph
// ABOUTME: is rejected instead of producing a misleading report.
package tracker

import (
	"context"
	"strings"
	"testing"

	"github.com/2389-research/tracker/pipeline"
)

// TestSimulateGraph_FinalizesGraph pins the #621 gap: Simulate / SimulateGraph
// ran on the caller's un-finalized graph, so unlike execution
// (NewEngineFromGraph -> PrepareForExecution) they never applied the final
// endpoint-validation invariant. A graph with a dangling edge OFF the simulated
// path therefore produced a report; under the fix, finalizing surfaces the
// invalid edge and SimulateGraph returns an error — the same divergence #606
// set out to close.
func TestSimulateGraph_FinalizesGraph(t *testing.T) {
	g := pipeline.NewGraph("dangling")
	g.AddNode(&pipeline.Node{ID: "s", Shape: "Mdiamond", Label: "Start"})
	g.AddNode(&pipeline.Node{ID: "end", Shape: "Msquare", Label: "End"})
	g.AddEdge(&pipeline.Edge{From: "s", To: "end"})
	// Orphan real node with an edge to an UNDECLARED node, off the simulated
	// path — finalization's validateEdgeEndpoints rejects it.
	g.AddNode(&pipeline.Node{ID: "orphan", Shape: "box", Label: "Orphan"})
	g.AddEdge(&pipeline.Edge{From: "orphan", To: "ghost"})

	_, err := SimulateGraph(context.Background(), g)
	if err == nil {
		t.Fatalf("SimulateGraph produced a report for a graph with a dangling edge — the simulate path is not finalized (issue #621)")
	}
	if !strings.Contains(err.Error(), "undeclared") && !strings.Contains(err.Error(), "ghost") {
		t.Errorf("expected a finalization endpoint error, got %v", err)
	}
}
