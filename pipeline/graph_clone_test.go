// ABOUTME: Guards the execution-graph clone against silently dropping a newly-added
// ABOUTME: Node/Edge field, and pins map isolation + adjacency rebuild (#621).
package pipeline

import (
	"reflect"
	"testing"
)

// TestCloneForExecution_FieldGuard fails when a field is added to Node or Edge
// without also copying it in cloneForExecution. The execution clone is the
// snapshot the engine runs against; a dropped field would silently vanish from
// execution with no other test catching it (post-#606 review, #621).
func TestCloneForExecution_FieldGuard(t *testing.T) {
	if got := reflect.TypeOf(Node{}).NumField(); got != 5 {
		t.Errorf("Node has %d fields but cloneForExecution copies 5 (ID/Shape/Label/Handler/Attrs). "+
			"Add the new field to cloneForExecution AND update this guard.", got)
	}
	if got := reflect.TypeOf(Edge{}).NumField(); got != 7 {
		t.Errorf("Edge has %d fields but cloneForExecution copies 7 (From/To/Label/Choice/Condition/Override/Attrs). "+
			"Add the new field to cloneForExecution AND update this guard.", got)
	}
}

// TestPrepareForExecution_PreservesFieldsAndIsolatesMaps builds a fully-populated
// graph, finalizes it, and checks every field round-trips and the clone's maps
// are independent of the source (mutating the clone must not touch the original).
func TestPrepareForExecution_PreservesFieldsAndIsolatesMaps(t *testing.T) {
	src := &Graph{
		Name:            "g",
		Nodes:           map[string]*Node{},
		Attrs:           map[string]string{"k": "v"},
		StartNode:       "a",
		ExitNode:        "b",
		NodeOrder:       []string{"a", "b"},
		DippinValidated: true,
		LintWarnings:    []string{"w"},
	}
	src.AddNode(&Node{ID: "a", Shape: "box", Label: "A", Handler: "tool", Attrs: map[string]string{"na": "1"}})
	src.AddNode(&Node{ID: "b", Shape: "box", Label: "B", Handler: "tool", Attrs: map[string]string{}})
	src.AddEdge(&Edge{From: "a", To: "b", Label: "go", Choice: "c", Condition: "ctx.outcome = success", Override: true, Attrs: map[string]string{"ea": "1"}})

	clone, err := PrepareForExecution(src)
	if err != nil {
		t.Fatalf("PrepareForExecution: %v", err)
	}

	if clone.Name != "g" || clone.StartNode != "a" || clone.ExitNode != "b" || !clone.DippinValidated {
		t.Errorf("scalar graph fields not preserved: %+v", clone)
	}
	if len(clone.NodeOrder) != 2 || len(clone.LintWarnings) != 1 || clone.Attrs["k"] != "v" {
		t.Errorf("graph slices/maps not preserved: %+v", clone)
	}
	n := clone.Nodes["a"]
	if n == nil || n.Shape != "box" || n.Label != "A" || n.Handler != src.Nodes["a"].Handler || n.Attrs["na"] != "1" {
		t.Errorf("node fields not preserved: clone=%+v src=%+v", n, src.Nodes["a"])
	}
	e := clone.OutgoingEdges("a")
	if len(e) != 1 || e[0].Choice != "c" || e[0].Condition != "ctx.outcome = success" || !e[0].Override || e[0].Attrs["ea"] != "1" {
		t.Errorf("edge fields / adjacency not preserved: %+v", e)
	}

	// Map isolation: mutating the clone must not reach the source.
	clone.Nodes["a"].Attrs["na"] = "MUTATED"
	clone.Attrs["k"] = "MUTATED"
	if src.Nodes["a"].Attrs["na"] != "1" || src.Attrs["k"] != "v" {
		t.Error("clone shares map state with the source graph")
	}
}
