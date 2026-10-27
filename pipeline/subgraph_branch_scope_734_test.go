// ABOUTME: Regression guard for issue #734 — a subgraph child engine running as (or
// ABOUTME: downstream of) a parallel branch target must inherit the branch scope, so a
// ABOUTME: gate inside the subgraph locates itself within the branch (ScopePath + index).
package pipeline

import (
	"context"
	"testing"
)

// TestEngine_InheritedBranchScope pins the engine-level propagation: an engine
// built with WithInheritedBranchScope seeds the branch internal keys so its
// first node observes the parallel branch it runs inside. PipelineContext
// snapshots drop the internal namespace, so a child engine needs this explicit
// hand-off — the branch-scope analog of WithInheritedRunIdentity (#657).
func TestEngine_InheritedBranchScope(t *testing.T) {
	g := NewGraph("bs")
	g.AddNode(&Node{ID: "First", Shape: "box", Attrs: map[string]string{"type": "probe"}})
	g.AddNode(&Node{ID: "End", Shape: "Msquare"})
	g.AddEdge(&Edge{From: "First", To: "End"})
	g.StartNode = "First"

	var branchID string
	var idx int
	var ok bool
	reg := newTestRegistry()
	reg.Register(&testHandler{name: "probe", executeFn: func(_ context.Context, _ *Node, pctx *PipelineContext) (Outcome, error) {
		branchID, idx, ok = BranchScope(pctx)
		return Outcome{Status: OutcomeSuccess}, nil
	}})

	if _, err := NewEngine(g, reg, WithInheritedBranchScope("BR", 3)).Run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !ok || branchID != "BR" || idx != 3 {
		t.Fatalf("first node BranchScope = (%q,%d,%v), want (BR,3,true)", branchID, idx, ok)
	}
}

// TestEngine_InheritedBranchScope_EmptyIsNoop pins that the option is inert
// outside a branch: a top-level run must not look like it runs in a branch.
func TestEngine_InheritedBranchScope_EmptyIsNoop(t *testing.T) {
	g := NewGraph("bs")
	g.AddNode(&Node{ID: "First", Shape: "box", Attrs: map[string]string{"type": "probe"}})
	g.AddNode(&Node{ID: "End", Shape: "Msquare"})
	g.AddEdge(&Edge{From: "First", To: "End"})
	g.StartNode = "First"

	var ok bool
	reg := newTestRegistry()
	reg.Register(&testHandler{name: "probe", executeFn: func(_ context.Context, _ *Node, pctx *PipelineContext) (Outcome, error) {
		_, _, ok = BranchScope(pctx)
		return Outcome{Status: OutcomeSuccess}, nil
	}})

	if _, err := NewEngine(g, reg, WithInheritedBranchScope("", 0)).Run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if ok {
		t.Fatal("BranchScope reported in-branch for a top-level run with an empty inherited branch id")
	}
}

// TestSubgraph_InheritsParentBranchScope pins the subgraph boundary: when the
// subgraph node carries a parallel branch scope on its context (the parallel
// handler stamps InternalKeyBranchScope/Index on the branch context), the child
// engine must observe the same branch. Before #734 buildSubgraphChildEngine
// built the child from pctx.Snapshot() alone — which copies only user values —
// so a gate inside the subgraph lost the branch from its ScopePath and reported
// BranchIndex 0. The wrapper stamps the branch keys the parallel handler would
// have set, mirroring the #657 run-identity guard.
func TestSubgraph_InheritsParentBranchScope(t *testing.T) {
	subGraph := NewGraph("sub")
	subGraph.AddNode(&Node{ID: "sub_probe", Shape: "box", Label: "Probe"})
	subGraph.AddNode(&Node{ID: "sub_end", Shape: "Msquare"})
	subGraph.AddEdge(&Edge{From: "sub_probe", To: "sub_end"})
	subGraph.StartNode = "sub_probe"

	var childBranch string
	var childIdx int
	var childOK bool
	reg := newTestRegistry()
	reg.Register(&testHandler{name: "codergen", executeFn: func(_ context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
		if node.ID == "sub_probe" {
			childBranch, childIdx, childOK = BranchScope(pctx)
		}
		return Outcome{Status: OutcomeSuccess}, nil
	}})
	sub := NewSubgraphHandler(map[string]*Graph{"child": subGraph}, reg, nil, nil)
	// Stamp the branch scope the parallel handler sets on a branch context
	// before the subgraph node executes, then delegate to the real handler.
	reg.Register(&testHandler{
		name: "subgraph",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			pctx.SetInternal(InternalKeyBranchScope, "SG")
			pctx.SetInternal(InternalKeyBranchIndex, "2")
			return sub.Execute(ctx, node, pctx)
		},
	})

	g := NewGraph("parent")
	g.AddNode(&Node{ID: "sg", Shape: "tab", Attrs: map[string]string{"subgraph_ref": "child"}})
	g.AddNode(&Node{ID: "end", Shape: "Msquare"})
	g.AddEdge(&Edge{From: "sg", To: "end"})
	g.StartNode = "sg"

	if _, err := NewEngine(g, reg, WithArtifactDir(t.TempDir())).Run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !childOK || childBranch != "SG" || childIdx != 2 {
		t.Fatalf("subgraph child BranchScope = (%q,%d,%v), want (SG,2,true) — branch scope not propagated across the subgraph boundary (#734)", childBranch, childIdx, childOK)
	}
}
