// ABOUTME: Tests for #734 gate ScopePath assembly across a parallel-branch + subgraph
// ABOUTME: nesting — the branch id belongs in ScopePath exactly once, index preserved.
package handlers

import (
	"context"
	"testing"

	"github.com/2389-research/tracker/pipeline"
)

// TestHumanHandler_GateScope_BranchTargetIsSubgraph: when the parallel branch
// target is itself the enclosing subgraph node, that id already enters
// ScopePath via SubgraphScope (carried on the Go context). gateInfo must not
// append the same id a second time from BranchScope — ScopePath stays [SG], and
// the branch index still rides along.
func TestHumanHandler_GateScope_BranchTargetIsSubgraph(t *testing.T) {
	graph := newChoiceGateGraph()
	rec := &recordingGateAware{}
	h := NewHumanHandler(rec, graph)

	pctx := pipeline.NewPipelineContext()
	pctx.SetInternal(pipeline.InternalKeyBranchScope, "SG")
	pctx.SetInternal(pipeline.InternalKeyBranchIndex, "2")

	ctx := pipeline.WithSubgraphScope(context.Background(), "SG")
	if _, err := h.Execute(ctx, graph.Nodes["gate"], pctx); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(rec.infos) != 1 {
		t.Fatalf("BeginGate called %d times, want 1", len(rec.infos))
	}
	info := rec.infos[0]
	if got := info.ScopePath; len(got) != 1 || got[0] != "SG" {
		t.Errorf("ScopePath = %v, want [SG] (branch target == enclosing subgraph — no double-count)", got)
	}
	if info.BranchIndex != 2 {
		t.Errorf("BranchIndex = %d, want 2", info.BranchIndex)
	}
}

// TestHumanHandler_GateScope_BranchDistinctFromSubgraph: a gate inside a
// subgraph reached from a DISTINCT branch target lists both the subgraph node
// and the branch id in ScopePath. This already held before #734 (regression
// guard that the dedup did not over-drop a genuinely distinct branch).
func TestHumanHandler_GateScope_BranchDistinctFromSubgraph(t *testing.T) {
	graph := newChoiceGateGraph()
	rec := &recordingGateAware{}
	h := NewHumanHandler(rec, graph)

	pctx := pipeline.NewPipelineContext()
	pctx.SetInternal(pipeline.InternalKeyBranchScope, "BR")
	pctx.SetInternal(pipeline.InternalKeyBranchIndex, "1")

	ctx := pipeline.WithSubgraphScope(context.Background(), "SG")
	if _, err := h.Execute(ctx, graph.Nodes["gate"], pctx); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	info := rec.infos[0]
	if got := info.ScopePath; len(got) != 2 || got[0] != "SG" || got[1] != "BR" {
		t.Errorf("ScopePath = %v, want [SG BR]", got)
	}
	if info.BranchIndex != 1 {
		t.Errorf("BranchIndex = %d, want 1", info.BranchIndex)
	}
}
