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
