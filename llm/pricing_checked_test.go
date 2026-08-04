// ABOUTME: Tests for the checked-pricing helpers that report whether a model was
// ABOUTME: in the catalog, so an uncatalogued model isn't silently priced as $0.
package llm

import (
	"math"
	"testing"
)

func TestIsPriced(t *testing.T) {
	if !IsPriced("claude-sonnet-4-5") {
		t.Error("IsPriced(claude-sonnet-4-5) = false, want true for a catalogued model")
	}
	if IsPriced("unknown-model-xyz") {
		t.Error("IsPriced(unknown-model-xyz) = true, want false for an uncatalogued model")
	}
	if IsPriced("") {
		t.Error("IsPriced(\"\") = true, want false — no model is not a priced model")
	}
}

func TestEstimateCostChecked_KnownModel(t *testing.T) {
	usage := Usage{InputTokens: 1_000_000, OutputTokens: 1_000_000}
	got, priced := EstimateCostChecked("claude-sonnet-4-5", usage)
	if !priced {
		t.Error("EstimateCostChecked(claude-sonnet-4-5) priced = false, want true")
	}
	if math.Abs(got-18.00) > 0.001 {
		t.Errorf("EstimateCostChecked(claude-sonnet-4-5) cost = %f, want 18.00", got)
	}
}

func TestEstimateCostChecked_UnknownModel(t *testing.T) {
	usage := Usage{InputTokens: 1_000_000, OutputTokens: 1_000_000}
	got, priced := EstimateCostChecked("unknown-model-xyz", usage)
	if priced {
		t.Error("EstimateCostChecked(unknown) priced = true, want false")
	}
	if got != 0 {
		t.Errorf("EstimateCostChecked(unknown) cost = %f, want 0", got)
	}
}
