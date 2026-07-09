// ABOUTME: Tests the packed-run guard that fails loud when a .dipx references
// ABOUTME: ${graph.workflow_dir} but has no seeded value (#430).
package main

import (
	"strings"
	"testing"

	"github.com/2389-research/tracker/pipeline"
)

func refWorkflowDirGraph() *pipeline.Graph {
	g := pipeline.NewGraph("t")
	g.AddNode(&pipeline.Node{ID: "boot", Attrs: map[string]string{
		"command": `. "${graph.workflow_dir}/scripts/lib/bootstrap.sh"`,
	}})
	return g
}

func TestGuardPackedWorkflowDir(t *testing.T) {
	// packed + references it + no seeded value => fail loud, name the node + issue
	err := guardPackedWorkflowDir(refWorkflowDirGraph(), true)
	if err == nil {
		t.Fatal("packed run referencing ${graph.workflow_dir} with no seeded value must fail loud (#430)")
	}
	if !strings.Contains(err.Error(), "boot") || !strings.Contains(err.Error(), "430") {
		t.Errorf("error should name the offending node and issue #430, got: %v", err)
	}

	// packed + references it + author/loader seeded a value => allowed
	g := refWorkflowDirGraph()
	g.Attrs["workflow_dir"] = "/somewhere"
	if err := guardPackedWorkflowDir(g, true); err != nil {
		t.Errorf("a seeded workflow_dir must pass in a packed run: %v", err)
	}

	// source-tree run (packed=false) => never guarded, even if empty+referenced
	if err := guardPackedWorkflowDir(refWorkflowDirGraph(), false); err != nil {
		t.Errorf("source-tree runs must not be guarded: %v", err)
	}

	// packed + no reference => allowed
	noRef := pipeline.NewGraph("t")
	noRef.AddNode(&pipeline.Node{ID: "x", Attrs: map[string]string{"command": "echo hi"}})
	if err := guardPackedWorkflowDir(noRef, true); err != nil {
		t.Errorf("packed run not referencing workflow_dir must pass: %v", err)
	}
}
