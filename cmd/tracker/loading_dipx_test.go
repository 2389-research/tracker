// ABOUTME: Tests for the loadPipelineAndBundle entry point — verifies .dipx
// ABOUTME: bundles dispatch to pipeline.LoadDipxBundle while .dip falls through.
package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/2389-research/tracker/internal/dipxtest"
)

func TestLoadPipelineAndBundle_DipxDispatch(t *testing.T) {
	dir := t.TempDir()
	entry := filepath.Join(dir, "entry.dip")
	if err := os.WriteFile(entry, []byte(dipxtest.MinimalDip("cli_dispatch", "start", "exit")), 0o644); err != nil {
		t.Fatal(err)
	}
	bundlePath := dipxtest.PackTestBundle(t, entry)

	graph, subgraphs, info, err := loadPipelineAndBundle(bundlePath, "")
	if err != nil {
		t.Fatalf("loadPipelineAndBundle on .dipx: %v", err)
	}
	if graph == nil {
		t.Fatal("graph nil")
	}
	if info.Identity == "" {
		t.Error("BundleInfo.Identity empty on .dipx path")
	}
	if subgraphs == nil {
		t.Error("subgraphs map nil on .dipx path")
	}
}

// TestLoadPipelineAndBundle_DipxWithSubgraph_PassesValidation is a
// regression guard for the bug where LoadDipxBundle keyed its subgraphs map
// by canonical bundle path (e.g., "workflows/sub.dip") while the entry
// graph's nodes carried the author's source ref (e.g., "sub.dip"), causing
// validateSubgraphRefs to fail on every bundle with subgraphs.
func TestLoadPipelineAndBundle_DipxWithSubgraph_PassesValidation(t *testing.T) {
	dir := t.TempDir()

	// Write subgraph file alongside entry — dipx.Pack will walk refs from disk.
	subPath := filepath.Join(dir, "sub.dip")
	if err := os.WriteFile(subPath, []byte(dipxtest.MinimalDip("sub_workflow", "s_start", "s_exit")), 0o644); err != nil {
		t.Fatal(err)
	}

	entrySource := `workflow entry_with_sub
  start: a
  exit: c

  agent a
    label: "Start"

  subgraph b
    ref: sub.dip

  agent c
    label: "Exit"

  edges
    a -> b
    b -> c
`
	entryPath := filepath.Join(dir, "entry.dip")
	if err := os.WriteFile(entryPath, []byte(entrySource), 0o644); err != nil {
		t.Fatal(err)
	}

	bundlePath := dipxtest.PackTestBundle(t, entryPath)

	graph, subgraphs, _, err := loadPipelineAndBundle(bundlePath, "")
	if err != nil {
		t.Fatalf("loadPipelineAndBundle: %v", err)
	}

	// The critical assertion: subgraph_ref validation passes for a real bundle.
	if err := validateSubgraphRefs(graph, subgraphs); err != nil {
		t.Errorf("validateSubgraphRefs failed: %v", err)
	}
}

func TestLoadPipelineAndBundle_DipPath(t *testing.T) {
	dir := t.TempDir()
	entry := filepath.Join(dir, "entry.dip")
	if err := os.WriteFile(entry, []byte(dipxtest.MinimalDip("cli_dip_path", "start", "exit")), 0o644); err != nil {
		t.Fatal(err)
	}

	graph, subgraphs, info, err := loadPipelineAndBundle(entry, "")
	if err != nil {
		t.Fatalf("loadPipelineAndBundle on .dip: %v", err)
	}
	if graph == nil {
		t.Fatal("graph nil")
	}
	if info.Identity != "" {
		t.Errorf("BundleInfo.Identity should be empty on .dip, got %q", info.Identity)
	}
	if subgraphs == nil {
		t.Error("subgraphs map should be non-nil even for entry-only .dip")
	}
}
