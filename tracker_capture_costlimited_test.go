// ABOUTME: Regression test for #518 — the unpriced-budget warning must fire when
// ABOUTME: the --max-cost ceiling comes only from a .dip `defaults:` block.
package tracker

import (
	"testing"

	"github.com/2389-research/tracker/pipeline"
)

// A cost ceiling set only via a .dip `defaults: { max_cost: ... }` block is
// folded in by ResolveBudgetLimits, not carried on cfg.Budget. captureState
// must still record costLimited=true for that configuration, or
// WarnUnpricedBudget returns early and the exact silent bypass #518 targets
// stays silent for a documented, supported way to configure the ceiling.
func TestSetupCaptureCostLimitedFromDefaultsBlock(t *testing.T) {
	graph := &pipeline.Graph{
		Name:  "t",
		Nodes: map[string]*pipeline.Node{},
		// Mirrors the adapter populating max_cost_cents from a .dip
		// `defaults:` block; cfg.Budget is left zero (no CLI/library ceiling).
		Attrs: map[string]string{"max_cost_cents": "500"},
	}
	cfg := &Config{Capture: &CaptureConfig{}}

	cap := setupCapture(cfg, t.TempDir(), graph)
	if cap == nil {
		t.Fatal("setupCapture returned nil with Capture set")
	}
	if !cap.costLimited {
		t.Errorf("costLimited = false; want true — a defaults-block ceiling must count")
	}
}

// The explicit-ceiling path must keep working after the resolved-budget change.
func TestSetupCaptureCostLimitedFromConfigBudget(t *testing.T) {
	graph := &pipeline.Graph{Name: "t", Nodes: map[string]*pipeline.Node{}}
	cfg := &Config{
		Capture: &CaptureConfig{},
		Budget:  pipeline.BudgetLimits{MaxCostCents: 250},
	}
	cap := setupCapture(cfg, t.TempDir(), graph)
	if cap == nil {
		t.Fatal("setupCapture returned nil with Capture set")
	}
	if !cap.costLimited {
		t.Errorf("costLimited = false; want true — an explicit Config.Budget ceiling must count")
	}
}

// No ceiling anywhere leaves costLimited false — the warning is silent.
func TestSetupCaptureCostLimitedUnsetWhenNoCeiling(t *testing.T) {
	graph := &pipeline.Graph{Name: "t", Nodes: map[string]*pipeline.Node{}}
	cfg := &Config{Capture: &CaptureConfig{}}
	cap := setupCapture(cfg, t.TempDir(), graph)
	if cap == nil {
		t.Fatal("setupCapture returned nil with Capture set")
	}
	if cap.costLimited {
		t.Errorf("costLimited = true; want false — no ceiling was configured")
	}
}
