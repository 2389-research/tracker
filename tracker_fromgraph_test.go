// ABOUTME: Tests NewEngineFromGraph — assemble an engine from a pre-parsed graph (#478).
package tracker

import (
	"context"
	"testing"

	"github.com/2389-research/tracker/pipeline"
)

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
