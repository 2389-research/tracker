// ABOUTME: Regression guard for issue #657 — a subgraph child engine must inherit
// ABOUTME: the parent run's run_id + artifact dir so subgraph capture / TRACKER_RUN_DIR
// ABOUTME: correlate with the parent run instead of diverging to a fresh id / empty dir.
package pipeline

import (
	"context"
	"testing"
)

// TestSubgraph_InheritsParentRunIdentity pins that a node executing INSIDE a
// subgraph observes the same InternalKeyRunID and InternalKeyArtifactDir as the
// parent run. Before #657 the subgraph child engine minted a fresh run id and,
// having no artifact dir, left InternalKeyArtifactDir unset — so a subgraph's
// capture sidecars and its tool subprocesses' TRACKER_RUN_DIR pointed at a
// divergent/empty location, breaking `tracker diagnose` correlation for
// anything done inside a subgraph. The parallel handler already re-propagates
// the parent run id to its branches (parallel.go); this makes the subgraph
// boundary do the equivalent.
func TestSubgraph_InheritsParentRunIdentity(t *testing.T) {
	subGraph := NewGraph("sub")
	subGraph.AddNode(&Node{ID: "sub_s", Shape: "Mdiamond", Label: "SubStart"})
	subGraph.AddNode(&Node{ID: "sub_probe", Shape: "box", Label: "Probe"})
	subGraph.AddNode(&Node{ID: "sub_end", Shape: "Msquare", Label: "SubEnd"})
	subGraph.AddEdge(&Edge{From: "sub_s", To: "sub_probe"})
	subGraph.AddEdge(&Edge{From: "sub_probe", To: "sub_end"})

	var parentRunID, parentArtifactDir string
	var childRunID, childArtifactDir string

	reg := newTestRegistry()
	// Both probe nodes are shape "box" -> the codergen handler. parent_probe
	// records the parent run's identity; sub_probe (inside the subgraph, run by
	// the child engine, which shares this registry) records the child's. This
	// is the #657 assertion surface: the two must be equal.
	reg.Register(&testHandler{name: "codergen", executeFn: func(_ context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
		switch node.ID {
		case "parent_probe":
			parentRunID, _ = pctx.GetInternal(InternalKeyRunID)
			parentArtifactDir, _ = pctx.GetInternal(InternalKeyArtifactDir)
		case "sub_probe":
			childRunID, _ = pctx.GetInternal(InternalKeyRunID)
			childArtifactDir, _ = pctx.GetInternal(InternalKeyArtifactDir)
		}
		return Outcome{Status: OutcomeSuccess}, nil
	}})
	reg.Register(&testHandler{
		name:      "subgraph",
		executeFn: NewSubgraphHandler(map[string]*Graph{"child": subGraph}, reg, nil, nil).Execute,
	})

	g := NewGraph("parent")
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond", Label: "Start"})
	g.AddNode(&Node{ID: "parent_probe", Shape: "box", Label: "ParentProbe", Handler: "codergen"})
	g.AddNode(&Node{ID: "sg", Shape: "tab", Label: "SubgraphNode", Attrs: map[string]string{"subgraph_ref": "child"}})
	g.AddNode(&Node{ID: "end", Shape: "Msquare", Label: "End"})
	g.AddEdge(&Edge{From: "s", To: "parent_probe"})
	g.AddEdge(&Edge{From: "parent_probe", To: "sg"})
	g.AddEdge(&Edge{From: "sg", To: "end"})

	engine := NewEngine(g, reg, WithArtifactDir(t.TempDir()))
	if _, err := engine.Run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}

	if parentArtifactDir == "" || parentRunID == "" {
		t.Fatalf("parent identity not captured: runID=%q artifactDir=%q", parentRunID, parentArtifactDir)
	}
	if childArtifactDir != parentArtifactDir {
		t.Errorf("subgraph InternalKeyArtifactDir = %q, want the parent's %q — subgraph capture / TRACKER_RUN_DIR diverges from the parent run (issue #657)", childArtifactDir, parentArtifactDir)
	}
	if childRunID != parentRunID {
		t.Errorf("subgraph InternalKeyRunID = %q, want the parent's %q — subgraph events attribute to a divergent run id (issue #657)", childRunID, parentRunID)
	}
}
