// ABOUTME: Tests for turn-budget checkpoint evaluation.
// ABOUTME: Validates that checkpoints fire at the right turn thresholds.
package agent

import "testing"

func TestEvalCheckpoints_NoCheckpoints(t *testing.T) {
	msg := evalCheckpoint(nil, 10, 80)
	if msg != "" {
		t.Errorf("expected empty, got %q", msg)
	}
}

func TestEvalCheckpoints_BudgetWarning(t *testing.T) {
	cps := []Checkpoint{
		{Fraction: 0.6, Message: "60% budget used"},
		{Fraction: 0.85, Message: "85% budget used"},
	}
	// Turn 48 of 80 = 0.6 exactly — should fire first checkpoint
	msg := evalCheckpoint(cps, 48, 80)
	if msg != "60% budget used" {
		t.Errorf("expected 60%% warning, got %q", msg)
	}
}

func TestEvalCheckpoints_CriticalWarning(t *testing.T) {
	cps := []Checkpoint{
		{Fraction: 0.6, Message: "60% budget used"},
		{Fraction: 0.85, Message: "85% budget used"},
	}
	// Turn 68 of 80 = 0.85 — should fire second checkpoint
	msg := evalCheckpoint(cps, 68, 80)
	if msg != "85% budget used" {
		t.Errorf("expected 85%% warning, got %q", msg)
	}
}

func TestEvalCheckpoints_BeforeFirstThreshold(t *testing.T) {
	cps := []Checkpoint{
		{Fraction: 0.6, Message: "60% budget used"},
	}
	msg := evalCheckpoint(cps, 10, 80)
	if msg != "" {
		t.Errorf("expected empty before threshold, got %q", msg)
	}
}

func TestEvalCheckpoints_ExactlyOnTurn(t *testing.T) {
	cps := []Checkpoint{
		{Fraction: 0.5, Message: "halfway"},
	}
	// Turn 40 of 80 = 0.5 — fires exactly at threshold
	msg := evalCheckpoint(cps, 40, 80)
	if msg != "halfway" {
		t.Errorf("expected halfway, got %q", msg)
	}
	// Turn 41 — past the threshold turn, should not fire again
	msg = evalCheckpoint(cps, 41, 80)
	if msg != "" {
		t.Errorf("expected empty on turn after threshold, got %q", msg)
	}
}
