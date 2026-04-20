// ABOUTME: Tests for TUI node-list construction from pipeline graphs.
// ABOUTME: Verifies subgraph child pre-population, nesting, and flag propagation.
package main

import (
	"reflect"
	"testing"

	"github.com/2389-research/tracker/pipeline"
	"github.com/2389-research/tracker/tui"
)

func TestBuildNodeListPrepopulatesSubgraphChildren(t *testing.T) {
	parent := pipeline.NewGraph("parent")
	parent.AddNode(&pipeline.Node{ID: "start", Shape: "Mdiamond"})
	parent.AddNode(&pipeline.Node{ID: "sg", Shape: "tab", Attrs: map[string]string{"subgraph_ref": "child"}})
	parent.AddNode(&pipeline.Node{ID: "done", Shape: "Msquare"})
	parent.AddEdge(&pipeline.Edge{From: "start", To: "sg"})
	parent.AddEdge(&pipeline.Edge{From: "sg", To: "done"})

	child := pipeline.NewGraph("child")
	child.AddNode(&pipeline.Node{ID: "cstart", Shape: "Mdiamond"})
	child.AddNode(&pipeline.Node{ID: "build", Shape: "box"})
	child.AddNode(&pipeline.Node{ID: "parse", Shape: "box"})
	child.AddNode(&pipeline.Node{ID: "cend", Shape: "Msquare"})
	child.AddEdge(&pipeline.Edge{From: "cstart", To: "build"})
	child.AddEdge(&pipeline.Edge{From: "build", To: "parse"})
	child.AddEdge(&pipeline.Edge{From: "parse", To: "cend"})

	got := nodeIDs(buildNodeList(parent, map[string]*pipeline.Graph{"child": child}))
	want := []string{
		"start",
		"sg",
		"sg/cstart",
		"sg/build",
		"sg/parse",
		"sg/cend",
		"done",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("node IDs mismatch\n got: %v\nwant: %v", got, want)
	}
}

func TestBuildNodeListPrepopulatesNestedSubgraphs(t *testing.T) {
	parent := pipeline.NewGraph("parent")
	parent.AddNode(&pipeline.Node{ID: "start", Shape: "Mdiamond"})
	parent.AddNode(&pipeline.Node{ID: "sg", Shape: "tab", Attrs: map[string]string{"subgraph_ref": "childA"}})
	parent.AddNode(&pipeline.Node{ID: "done", Shape: "Msquare"})
	parent.AddEdge(&pipeline.Edge{From: "start", To: "sg"})
	parent.AddEdge(&pipeline.Edge{From: "sg", To: "done"})

	childA := pipeline.NewGraph("childA")
	childA.AddNode(&pipeline.Node{ID: "aStart", Shape: "Mdiamond"})
	childA.AddNode(&pipeline.Node{ID: "aSub", Shape: "tab", Attrs: map[string]string{"subgraph_ref": "childB"}})
	childA.AddNode(&pipeline.Node{ID: "aEnd", Shape: "Msquare"})
	childA.AddEdge(&pipeline.Edge{From: "aStart", To: "aSub"})
	childA.AddEdge(&pipeline.Edge{From: "aSub", To: "aEnd"})

	childB := pipeline.NewGraph("childB")
	childB.AddNode(&pipeline.Node{ID: "bStart", Shape: "Mdiamond"})
	childB.AddNode(&pipeline.Node{ID: "bStep", Shape: "box"})
	childB.AddNode(&pipeline.Node{ID: "bEnd", Shape: "Msquare"})
	childB.AddEdge(&pipeline.Edge{From: "bStart", To: "bStep"})
	childB.AddEdge(&pipeline.Edge{From: "bStep", To: "bEnd"})

	got := nodeIDs(buildNodeList(parent, map[string]*pipeline.Graph{
		"childA": childA,
		"childB": childB,
	}))
	want := []string{
		"start",
		"sg",
		"sg/aStart",
		"sg/aSub",
		"sg/aSub/bStart",
		"sg/aSub/bStep",
		"sg/aSub/bEnd",
		"sg/aEnd",
		"done",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("node IDs mismatch\n got: %v\nwant: %v", got, want)
	}
}

func TestBuildNodeListSubgraphParallelFlagsArePreserved(t *testing.T) {
	parent := pipeline.NewGraph("parent")
	parent.AddNode(&pipeline.Node{ID: "start", Shape: "Mdiamond"})
	parent.AddNode(&pipeline.Node{ID: "sg", Shape: "tab", Attrs: map[string]string{"subgraph_ref": "child"}})
	parent.AddNode(&pipeline.Node{ID: "done", Shape: "Msquare"})
	parent.AddEdge(&pipeline.Edge{From: "start", To: "sg"})
	parent.AddEdge(&pipeline.Edge{From: "sg", To: "done"})

	child := pipeline.NewGraph("child")
	child.AddNode(&pipeline.Node{ID: "cStart", Shape: "Mdiamond"})
	child.AddNode(&pipeline.Node{ID: "dispatch", Shape: "component", Attrs: map[string]string{"parallel_targets": "left,right"}})
	child.AddNode(&pipeline.Node{ID: "left", Shape: "box"})
	child.AddNode(&pipeline.Node{ID: "right", Shape: "box"})
	child.AddNode(&pipeline.Node{ID: "join", Shape: "tripleoctagon", Attrs: map[string]string{"fan_in_sources": "left,right"}})
	child.AddNode(&pipeline.Node{ID: "cEnd", Shape: "Msquare"})
	child.AddEdge(&pipeline.Edge{From: "cStart", To: "dispatch"})
	child.AddEdge(&pipeline.Edge{From: "dispatch", To: "left"})
	child.AddEdge(&pipeline.Edge{From: "dispatch", To: "right"})
	child.AddEdge(&pipeline.Edge{From: "left", To: "join"})
	child.AddEdge(&pipeline.Edge{From: "right", To: "join"})
	child.AddEdge(&pipeline.Edge{From: "join", To: "cEnd"})

	entries := buildNodeList(parent, map[string]*pipeline.Graph{"child": child})

	dispatch := findNodeEntry(t, entries, "sg/dispatch")
	if !dispatch.Flags.IsParallelDispatcher {
		t.Fatal("expected sg/dispatch to be marked as parallel dispatcher")
	}

	left := findNodeEntry(t, entries, "sg/left")
	if !left.Flags.IsParallelBranch {
		t.Fatal("expected sg/left to be marked as parallel branch")
	}
	right := findNodeEntry(t, entries, "sg/right")
	if !right.Flags.IsParallelBranch {
		t.Fatal("expected sg/right to be marked as parallel branch")
	}

	join := findNodeEntry(t, entries, "sg/join")
	if !join.Flags.IsFanIn {
		t.Fatal("expected sg/join to be marked as fan-in")
	}
}

// TestBuildNodeListCyclicSubgraphsTerminate verifies that a cyclic subgraphs
// map (A references B, B references A) does not stack-overflow in
// buildNodeList. Cycles are normally rejected upstream by
// loadSubgraphsRecursive, but a library caller passing a map directly
// bypasses that check. The cycle-guard visited set must catch it.
func TestBuildNodeListCyclicSubgraphsTerminate(t *testing.T) {
	parent := pipeline.NewGraph("parent")
	parent.AddNode(&pipeline.Node{ID: "start", Shape: "Mdiamond"})
	parent.AddNode(&pipeline.Node{ID: "sg", Shape: "tab", Attrs: map[string]string{"subgraph_ref": "A"}})
	parent.AddNode(&pipeline.Node{ID: "done", Shape: "Msquare"})
	parent.AddEdge(&pipeline.Edge{From: "start", To: "sg"})
	parent.AddEdge(&pipeline.Edge{From: "sg", To: "done"})

	a := pipeline.NewGraph("A")
	a.AddNode(&pipeline.Node{ID: "aStart", Shape: "Mdiamond"})
	a.AddNode(&pipeline.Node{ID: "aRef", Shape: "tab", Attrs: map[string]string{"subgraph_ref": "B"}})
	a.AddNode(&pipeline.Node{ID: "aEnd", Shape: "Msquare"})
	a.AddEdge(&pipeline.Edge{From: "aStart", To: "aRef"})
	a.AddEdge(&pipeline.Edge{From: "aRef", To: "aEnd"})

	b := pipeline.NewGraph("B")
	b.AddNode(&pipeline.Node{ID: "bStart", Shape: "Mdiamond"})
	b.AddNode(&pipeline.Node{ID: "bRef", Shape: "tab", Attrs: map[string]string{"subgraph_ref": "A"}}) // cycle
	b.AddNode(&pipeline.Node{ID: "bEnd", Shape: "Msquare"})
	b.AddEdge(&pipeline.Edge{From: "bStart", To: "bRef"})
	b.AddEdge(&pipeline.Edge{From: "bRef", To: "bEnd"})

	// Must terminate; must not recurse infinitely. Any non-panic return is a pass.
	entries := buildNodeList(parent, map[string]*pipeline.Graph{"A": a, "B": b})
	if len(entries) == 0 {
		t.Fatal("expected non-empty result even with cycle; got zero entries")
	}
	// bRef references A again — expansion must stop there. Verify the deepest
	// ID does not include a second "A" expansion (i.e. "sg/aRef/bRef/aStart"
	// would indicate the cycle guard failed).
	ids := nodeIDs(entries)
	for _, id := range ids {
		// "sg/aRef/bRef" is OK (the cycle-pointing parent itself appears).
		// What must NOT appear is anything under "sg/aRef/bRef/a..." — that's re-expansion.
		if len(id) > len("sg/aRef/bRef/") && id[:len("sg/aRef/bRef/")] == "sg/aRef/bRef/" {
			t.Errorf("cycle guard failed: %q shows re-expansion past the cycle boundary", id)
		}
	}
}

// TestBuildNodeListSelfReferencingSubgraphTerminates verifies a single-ref
// cycle (X → X) is caught by the visited set.
// TestBuildNodeListSubgraphPreservesChildLabels verifies that a child
// workflow's user-set Labels survive prefixing. Before the fix, the
// expansion overwrote Label with SubgraphChildLabel(newPrefixedID),
// silently discarding any explicit label the user set inside the child
// workflow. Regression guard for PR #119 Copilot feedback.
func TestBuildNodeListSubgraphPreservesChildLabels(t *testing.T) {
	parent := pipeline.NewGraph("parent")
	parent.AddNode(&pipeline.Node{ID: "start", Shape: "Mdiamond"})
	parent.AddNode(&pipeline.Node{ID: "sg", Shape: "tab", Attrs: map[string]string{"subgraph_ref": "child"}})
	parent.AddNode(&pipeline.Node{ID: "done", Shape: "Msquare"})
	parent.AddEdge(&pipeline.Edge{From: "start", To: "sg"})
	parent.AddEdge(&pipeline.Edge{From: "sg", To: "done"})

	child := pipeline.NewGraph("child")
	child.AddNode(&pipeline.Node{ID: "cstart", Shape: "Mdiamond"})
	// Explicit user-set label with spaces — must not be clobbered.
	child.AddNode(&pipeline.Node{ID: "build", Shape: "box", Label: "Build Artifact"})
	// No label — falls back to ID in nodeEntryFor.
	child.AddNode(&pipeline.Node{ID: "parse", Shape: "box"})
	child.AddNode(&pipeline.Node{ID: "cend", Shape: "Msquare"})
	child.AddEdge(&pipeline.Edge{From: "cstart", To: "build"})
	child.AddEdge(&pipeline.Edge{From: "build", To: "parse"})
	child.AddEdge(&pipeline.Edge{From: "parse", To: "cend"})

	entries := buildNodeList(parent, map[string]*pipeline.Graph{"child": child})
	buildEntry := findNodeEntry(t, entries, "sg/build")
	if buildEntry.Label != "Build Artifact" {
		t.Errorf("user-set Label lost: got %q, want %q", buildEntry.Label, "Build Artifact")
	}
	// No-label node: Label should be the original unqualified ID, not the
	// prefixed one (so render's short-form display still works).
	parseEntry := findNodeEntry(t, entries, "sg/parse")
	if parseEntry.Label != "parse" {
		t.Errorf("ID-fallback Label wrong: got %q, want %q", parseEntry.Label, "parse")
	}
}

func TestBuildNodeListSelfReferencingSubgraphTerminates(t *testing.T) {
	parent := pipeline.NewGraph("parent")
	parent.AddNode(&pipeline.Node{ID: "start", Shape: "Mdiamond"})
	parent.AddNode(&pipeline.Node{ID: "sg", Shape: "tab", Attrs: map[string]string{"subgraph_ref": "X"}})
	parent.AddNode(&pipeline.Node{ID: "done", Shape: "Msquare"})
	parent.AddEdge(&pipeline.Edge{From: "start", To: "sg"})
	parent.AddEdge(&pipeline.Edge{From: "sg", To: "done"})

	x := pipeline.NewGraph("X")
	x.AddNode(&pipeline.Node{ID: "xStart", Shape: "Mdiamond"})
	x.AddNode(&pipeline.Node{ID: "xRef", Shape: "tab", Attrs: map[string]string{"subgraph_ref": "X"}}) // self-ref
	x.AddNode(&pipeline.Node{ID: "xEnd", Shape: "Msquare"})
	x.AddEdge(&pipeline.Edge{From: "xStart", To: "xRef"})
	x.AddEdge(&pipeline.Edge{From: "xRef", To: "xEnd"})

	entries := buildNodeList(parent, map[string]*pipeline.Graph{"X": x})
	ids := nodeIDs(entries)
	for _, id := range ids {
		if len(id) > len("sg/xRef/") && id[:len("sg/xRef/")] == "sg/xRef/" {
			t.Errorf("self-ref cycle guard failed: %q shows re-expansion", id)
		}
	}
}

func nodeIDs(entries []tui.NodeEntry) []string {
	ids := make([]string, 0, len(entries))
	for _, e := range entries {
		ids = append(ids, e.ID)
	}
	return ids
}

func findNodeEntry(t *testing.T, entries []tui.NodeEntry, id string) tui.NodeEntry {
	t.Helper()
	for _, e := range entries {
		if e.ID == id {
			return e
		}
	}
	t.Fatalf("node entry %q not found", id)
	return tui.NodeEntry{}
}
