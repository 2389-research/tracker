// ABOUTME: Tests for graph.workflow_dir seeding when loading raw .dip files.
// ABOUTME: Embedded built-ins and pre-set values must not be touched.
package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/2389-research/tracker/pipeline"
)

const workflowDirFixture = `workflow WorkflowDirFixture
  start: Start
  exit: Done

  agent Start
    label: Start

  tool Echo
    label: "Echo"
    command: printf 'dir=${graph.workflow_dir}'

  agent Done
    label: Done

  edges
    Start -> Echo
    Echo -> Done
`

// writeWorkflowDirFixture writes the fixture .dip into a temp dir and returns
// its absolute path.
func writeWorkflowDirFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "fixture.dip")
	if err := os.WriteFile(path, []byte(workflowDirFixture), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return path
}

func TestLoadPipeline_SeedsWorkflowDirFromForeignCwd(t *testing.T) {
	path := writeWorkflowDirFixture(t)

	// Load from an unrelated cwd: the attr must be the .dip's parent dir.
	t.Chdir(t.TempDir())

	graph, err := loadPipeline(path, "")
	if err != nil {
		t.Fatalf("loadPipeline: %v", err)
	}
	want := filepath.Dir(path)
	if got := graph.Attrs["workflow_dir"]; got != want {
		t.Errorf("workflow_dir = %q, want %q", got, want)
	}
}

func TestLoadPipeline_SeedsAbsoluteWorkflowDirFromRelativePath(t *testing.T) {
	path := writeWorkflowDirFixture(t)

	// Load via a relative path: the attr must still be absolute.
	t.Chdir(filepath.Dir(path))

	graph, err := loadPipeline("fixture.dip", "")
	if err != nil {
		t.Fatalf("loadPipeline: %v", err)
	}
	got := graph.Attrs["workflow_dir"]
	if !filepath.IsAbs(got) {
		t.Errorf("workflow_dir = %q, want an absolute path", got)
	}
	if want := filepath.Dir(path); got != want {
		t.Errorf("workflow_dir = %q, want %q", got, want)
	}
}

func TestSeedWorkflowDir_PreservesExistingValue(t *testing.T) {
	graph := pipeline.NewGraph("test")
	graph.Attrs["workflow_dir"] = "/author/declared"

	seedWorkflowDir(graph, "/some/other/place/fixture.dip")

	if got := graph.Attrs["workflow_dir"]; got != "/author/declared" {
		t.Errorf("workflow_dir clobbered: got %q, want %q", got, "/author/declared")
	}
}

func TestSeedWorkflowDir_PreservesExplicitEmptyValue(t *testing.T) {
	// ParseDOT copies arbitrary graph attrs, so a .dot author can declare
	// workflow_dir="" to opt out — presence wins, not non-emptiness.
	graph := pipeline.NewGraph("test")
	graph.Attrs["workflow_dir"] = ""

	seedWorkflowDir(graph, "/some/other/place/fixture.dot")

	if got := graph.Attrs["workflow_dir"]; got != "" {
		t.Errorf("explicit empty workflow_dir clobbered: got %q, want \"\"", got)
	}
}

func TestLoadEmbeddedPipeline_DoesNotSeedWorkflowDir(t *testing.T) {
	_, _, info, err := resolvePipelineSource("build_product")
	if err != nil {
		t.Fatalf("resolvePipelineSource: %v", err)
	}
	graph, err := loadEmbeddedPipeline(info)
	if err != nil {
		t.Fatalf("loadEmbeddedPipeline: %v", err)
	}
	if got, ok := graph.Attrs["workflow_dir"]; ok {
		t.Errorf("embedded workflow should not carry workflow_dir, got %q", got)
	}
}
