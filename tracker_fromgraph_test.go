// ABOUTME: Tests NewEngineFromGraph — assemble an engine from a pre-parsed graph (#478).
package tracker

import (
	"context"
	"testing"

	"github.com/2389-research/tracker/pipeline"
)

// TestNewEngineFromGraph_RejectsPostDippinMutation pins SIFT-SUB-03-02 (#606) at
// the engine-construction seam: a graph that passed dippin validation carries
// DippinValidated=true, which suppresses tracker's structural checks. If the
// caller then mutates it (here, an edge to an undeclared node),
// NewEngineFromGraph's finalization boundary must still reject it — provenance is
// diagnostic input, not permission to skip execution-critical invariants.
func TestNewEngineFromGraph_RejectsPostDippinMutation(t *testing.T) {
	g := pipeline.NewGraph("post-dippin-mutation")
	g.DippinValidated = true
	g.AddNode(&pipeline.Node{ID: "s", Shape: "Mdiamond"})
	g.AddNode(&pipeline.Node{ID: "e", Shape: "Msquare"})
	g.AddEdge(&pipeline.Edge{From: "s", To: "e"})

	// Post-validation mutation: an edge whose target node does not exist.
	g.AddEdge(&pipeline.Edge{From: "s", To: "ghost"})

	eng, err := NewEngineFromGraph(context.Background(), g, Config{
		WorkingDir: t.TempDir(),
		LLMClient:  successStub(),
	})
	if err == nil {
		eng.Close()
		t.Fatal("NewEngineFromGraph accepted a graph with a dangling edge despite DippinValidated=true")
	}
}

// TestNewEngineFromGraph_RunsPreParsedGraph proves the parse/assemble split:
// a caller that already holds a *pipeline.Graph (e.g. the CLI, which resolves
// subgraph files itself) can assemble and run an engine without re-parsing.
func TestNewEngineFromGraph_RunsPreParsedGraph(t *testing.T) {
	graph, err := parsePipelineSource(quickDip, "dip")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	eng, err := NewEngineFromGraph(context.Background(), graph, Config{
		WorkingDir: t.TempDir(),
		LLMClient:  successStub(),
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
	if eng.TokenTracker() == nil {
		t.Fatal("expected a non-nil TokenTracker accessor")
	}
}
