// ABOUTME: Tests for the built-in workflow catalog exposed by the tracker package.
// ABOUTME: Covers Workflows(), LookupWorkflow, and OpenWorkflow.
package tracker

import (
	"strings"
	"testing"
)

func TestWorkflows_ReturnsBuiltIns(t *testing.T) {
	wfs := Workflows()
	if len(wfs) == 0 {
		t.Fatal("expected at least one embedded workflow")
	}
	for _, wf := range wfs {
		if wf.Name == "" {
			t.Errorf("workflow %+v has empty Name", wf)
		}
		if wf.File == "" {
			t.Errorf("workflow %+v has empty File", wf)
		}
		if !strings.HasPrefix(wf.File, "workflows/") {
			t.Errorf("workflow %q File %q does not start with 'workflows/'", wf.Name, wf.File)
		}
		if !strings.HasSuffix(wf.File, ".dip") {
			t.Errorf("workflow %q File %q does not end with .dip", wf.Name, wf.File)
		}
	}
}

func TestWorkflows_SortedByName(t *testing.T) {
	wfs := Workflows()
	for i := 1; i < len(wfs); i++ {
		if wfs[i-1].Name > wfs[i].Name {
			t.Errorf("workflows not sorted: %q before %q", wfs[i-1].Name, wfs[i].Name)
		}
	}
}

func TestWorkflows_ReturnsCopy(t *testing.T) {
	wfs := Workflows()
	if len(wfs) == 0 {
		t.Skip("no workflows to mutate")
	}
	original := wfs[0].Name
	wfs[0].Name = "mutated"
	wfs2 := Workflows()
	if wfs2[0].Name != original {
		t.Errorf("Workflows() returned a shared slice: mutation leaked (%q → %q)", original, wfs2[0].Name)
	}
}

func TestLookupWorkflow_Known(t *testing.T) {
	info, ok := LookupWorkflow("build_product")
	if !ok {
		t.Fatal("build_product should be a known built-in")
	}
	if info.Name != "build_product" {
		t.Errorf("info.Name = %q, want 'build_product'", info.Name)
	}
}

func TestLookupWorkflow_Unknown(t *testing.T) {
	_, ok := LookupWorkflow("no_such_workflow_anywhere")
	if ok {
		t.Error("LookupWorkflow returned true for an unknown name")
	}
}

func TestOpenWorkflow_ReturnsSource(t *testing.T) {
	data, info, err := OpenWorkflow("build_product")
	if err != nil {
		t.Fatalf("OpenWorkflow failed: %v", err)
	}
	if info.Name != "build_product" {
		t.Errorf("info.Name = %q, want 'build_product'", info.Name)
	}
	if len(data) == 0 {
		t.Error("OpenWorkflow returned empty data")
	}
	if !strings.Contains(string(data), "workflow ") {
		t.Errorf("workflow source does not contain 'workflow ' declaration: %q", string(data)[:200])
	}
}

func TestOpenWorkflow_Unknown(t *testing.T) {
	_, _, err := OpenWorkflow("no_such_workflow_anywhere")
	if err == nil {
		t.Error("expected error for unknown workflow, got nil")
	}
}
