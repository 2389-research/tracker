// ABOUTME: Proves Config.Subgraphs is wired through NewEngineFromGraph (#478).
package tracker

import (
	"context"
	"testing"

	"github.com/2389-research/tracker/pipeline"
)

// TestNewEngineFromGraph_ResolvesSubgraphs proves the pre-loaded subgraph map is
// wired through: a parent whose subgraph node references "child" — resolvable
// ONLY via Config.Subgraphs — runs to success. If the map weren't wired, the
// subgraph handler could not resolve "child" and the node would fail.
func TestNewEngineFromGraph_ResolvesSubgraphs(t *testing.T) {
	child := pipeline.NewGraph("child")
	child.AddNode(&pipeline.Node{ID: "cs", Shape: "Mdiamond", Label: "ChildStart"})
	child.AddNode(&pipeline.Node{ID: "ce", Shape: "Msquare", Label: "ChildEnd"})
	child.AddEdge(&pipeline.Edge{From: "cs", To: "ce"})

	parent := pipeline.NewGraph("parent")
	parent.AddNode(&pipeline.Node{ID: "s", Shape: "Mdiamond", Label: "Start"})
	parent.AddNode(&pipeline.Node{ID: "sg", Shape: "tab", Label: "SG", Attrs: map[string]string{"subgraph_ref": "child"}})
	parent.AddNode(&pipeline.Node{ID: "e", Shape: "Msquare", Label: "End"})
	parent.AddEdge(&pipeline.Edge{From: "s", To: "sg"})
	parent.AddEdge(&pipeline.Edge{From: "sg", To: "e"})

	eng, err := NewEngineFromGraph(context.Background(), parent, Config{
		WorkingDir: t.TempDir(),
		LLMClient:  successStub(),
		Subgraphs:  map[string]*pipeline.Graph{"child": child},
	})
	if err != nil {
		t.Fatalf("NewEngineFromGraph: %v", err)
	}
	defer eng.Close()

	res, err := eng.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !pipeline.TerminalStatus(res.Status).IsSuccess() {
		t.Fatalf("status = %q, want success", res.Status)
	}

	var ranSubgraph bool
	for _, n := range res.CompletedNodes {
		if n == "sg" {
			ranSubgraph = true
		}
	}
	if !ranSubgraph {
		t.Fatalf("subgraph node did not complete (child unresolved?); completed = %v", res.CompletedNodes)
	}
}
