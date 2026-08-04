// ABOUTME: Tests that `tracker diagnose` surfaces unpriced usage (#518) from
// ABOUTME: run.json — including on a clean run with no node failures.
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tracker "github.com/2389-research/tracker"
	"github.com/2389-research/tracker/pipeline"
)

// writeRunManifest writes a minimal run.json into runDir carrying the given
// unpriced totals, so readUnpricedModels has a real file to parse.
func writeRunManifest(t *testing.T, runDir string, totals pipeline.RunTotals) {
	t.Helper()
	m := pipeline.RunManifest{
		SchemaVersion: pipeline.RunManifestSchemaVersion,
		RunID:         "unpriced-test",
		Totals:        totals,
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(runDir, pipeline.RunManifestFile), data, 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
}

// readUnpricedModels returns the uncatalogued model names when run.json flags
// unpriced usage, and nil otherwise.
func TestReadUnpricedModels(t *testing.T) {
	t.Run("unpriced run yields the models", func(t *testing.T) {
		dir := t.TempDir()
		writeRunManifest(t, dir, pipeline.RunTotals{
			Unpriced:       true,
			UnpricedModels: []string{"typo-sonnet-4"},
		})
		got := readUnpricedModels(dir)
		if len(got) != 1 || got[0] != "typo-sonnet-4" {
			t.Errorf("readUnpricedModels = %v, want [typo-sonnet-4]", got)
		}
	})

	t.Run("priced run yields nil", func(t *testing.T) {
		dir := t.TempDir()
		writeRunManifest(t, dir, pipeline.RunTotals{Unpriced: false})
		if got := readUnpricedModels(dir); got != nil {
			t.Errorf("readUnpricedModels = %v, want nil", got)
		}
	})

	t.Run("missing manifest yields nil", func(t *testing.T) {
		if got := readUnpricedModels(t.TempDir()); got != nil {
			t.Errorf("readUnpricedModels = %v, want nil", got)
		}
	})
}

// A clean run (no failures) that consumed unpriced usage must still surface the
// signal — the whole point of #518 is that the ceiling bypass is not silent in
// the documented after-the-fact analysis tool.
func TestPrintDiagnoseReport_SurfacesUnpricedOnCleanRun(t *testing.T) {
	report := &tracker.DiagnoseReport{
		RunID:          "clean-but-unpriced",
		CompletedNodes: 4,
	}
	out := captureDiagnoseStdout(t, func() {
		printDiagnoseReport(report, []string{"typo-sonnet-4"})
	})
	if !strings.Contains(out, "Unpriced Usage") {
		t.Errorf("expected an 'Unpriced Usage' section, got:\n%s", out)
	}
	if !strings.Contains(out, "typo-sonnet-4") {
		t.Errorf("expected the uncatalogued model name, got:\n%s", out)
	}
	if strings.Contains(out, "No failures found") {
		t.Errorf("clean-run early-return must not fire when unpriced usage is present, got:\n%s", out)
	}
}
