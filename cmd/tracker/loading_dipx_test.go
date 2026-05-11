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
