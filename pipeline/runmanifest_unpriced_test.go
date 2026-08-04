// ABOUTME: Tests that a run whose usage was attributed to an uncatalogued model
// ABOUTME: is flagged Unpriced in run.json, while a catalogued model is not.
package pipeline

import (
	"path/filepath"
	"testing"
)

func TestAssembleRunManifestFlagsUnpricedModel(t *testing.T) {
	runDir := filepath.Join(t.TempDir(), "up")
	// A finish line carrying usage attributed to a model with no catalog entry:
	// EstimateCost priced it $0, which is indistinguishable from a genuinely-free
	// model unless the manifest records that the rate was unknown.
	writeLog(t, runDir,
		`{"ts":"2026-01-01T00:00:01.000Z","source":"llm","type":"finish","call_id":"c1","model":"totally-made-up-model","token_input":1000,"token_output":200}`,
	)

	m, err := AssembleRunManifest(runDir, "up")
	if err != nil {
		t.Fatalf("AssembleRunManifest: %v", err)
	}
	if !m.Totals.Unpriced {
		t.Error("Totals.Unpriced = false, want true (usage was attributed to an uncatalogued model)")
	}
	found := false
	for _, name := range m.Totals.UnpricedModels {
		if name == "totally-made-up-model" {
			found = true
		}
	}
	if !found {
		t.Errorf("UnpricedModels = %v, want it to contain the uncatalogued model", m.Totals.UnpricedModels)
	}
}

func TestAssembleRunManifestCataloguedModelNotUnpriced(t *testing.T) {
	runDir := filepath.Join(t.TempDir(), "priced")
	writeLog(t, runDir,
		`{"ts":"2026-01-01T00:00:01.000Z","source":"llm","type":"finish","call_id":"c1","model":"claude-sonnet-4-5","token_input":1000,"token_output":200,"estimated_cost":0.006}`,
	)

	m, err := AssembleRunManifest(runDir, "priced")
	if err != nil {
		t.Fatalf("AssembleRunManifest: %v", err)
	}
	if m.Totals.Unpriced {
		t.Error("Totals.Unpriced = true, want false (the model is in the catalog)")
	}
	if len(m.Totals.UnpricedModels) != 0 {
		t.Errorf("UnpricedModels = %v, want empty", m.Totals.UnpricedModels)
	}
}

// A usage-free line naming an unknown model (e.g. an llm_start probe) must not
// trip the flag — only actual billable usage under an unknown rate matters.
func TestAssembleRunManifestUnknownModelNoUsageNotUnpriced(t *testing.T) {
	runDir := filepath.Join(t.TempDir(), "probe")
	writeLog(t, runDir,
		`{"ts":"2026-01-01T00:00:01.000Z","source":"llm","type":"llm_start","model":"totally-made-up-model"}`,
	)

	m, err := AssembleRunManifest(runDir, "probe")
	if err != nil {
		t.Fatalf("AssembleRunManifest: %v", err)
	}
	if m.Totals.Unpriced {
		t.Error("Totals.Unpriced = true, want false (no billable usage on the line)")
	}
}
