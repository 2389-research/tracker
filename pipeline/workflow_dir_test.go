// ABOUTME: Tests for NodesReferencingWorkflowDir (the #430 packed-run guard input).
package pipeline

import "testing"

func TestNodesReferencingWorkflowDir(t *testing.T) {
	g := NewGraph("t")
	g.AddNode(&Node{ID: "boot", Attrs: map[string]string{
		"command": `. "${graph.workflow_dir}/scripts/lib/bootstrap.sh"`,
	}})
	g.AddNode(&Node{ID: "plain", Attrs: map[string]string{"command": "echo hi"}})
	g.AddNode(&Node{ID: "prompt_ref", Attrs: map[string]string{
		"prompt": "read graph.workflow_dir/notes.md",
	}})

	got := NodesReferencingWorkflowDir(g)
	if len(got) != 2 || got[0] != "boot" || got[1] != "prompt_ref" {
		t.Errorf("NodesReferencingWorkflowDir = %v, want [boot prompt_ref]", got)
	}

	if refs := NodesReferencingWorkflowDir(NewGraph("empty")); len(refs) != 0 {
		t.Errorf("empty graph should reference nothing, got %v", refs)
	}
	if refs := NodesReferencingWorkflowDir(nil); refs != nil {
		t.Errorf("nil graph should return nil, got %v", refs)
	}
}
