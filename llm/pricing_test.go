// ABOUTME: Tests for cost estimation — verifies delegation to dippin-lang/pricing,
// ABOUTME: the cache-multiplier overlay, and the catalog-vs-pricing drift guard (#558).
package llm

import (
	"math"
	"testing"

	"github.com/2389-research/dippin-lang/pricing"
)

// knownUnpricedCatalogModels are catalog entries dippin-lang/pricing does not
// price — a tracker/dippin catalog divergence that would silently cost $0 and
// escape --max-cost. This allowlist is the explicit record of that gap so
// TestCatalogModelsArePricedByDippin fails when a NEW model drops out, without
// failing on ones already dispositioned. Empty as of dippin v0.57.0: it now
// prices gpt-5.2-codex (dippin#209) and the phantom gpt-5.2-mini was dropped
// from the catalog. Keep empty by reconciling with dippin whenever it grows.
var knownUnpricedCatalogModels = map[string]bool{}

func TestEstimateCost_UnknownModel(t *testing.T) {
	if got := EstimateCost("unknown-model-xyz", Usage{InputTokens: 1_000_000, OutputTokens: 1_000_000}); got != 0 {
		t.Errorf("EstimateCost(unknown) = %f, want 0", got)
	}
	if IsPriced("unknown-model-xyz") {
		t.Error("IsPriced(unknown) = true, want false")
	}
}

func TestEstimateCost_ZeroTokens(t *testing.T) {
	if got := EstimateCost("claude-sonnet-4-5", Usage{}); got != 0 {
		t.Errorf("EstimateCost(zero tokens) = %f, want 0", got)
	}
}

// TestEstimateCost_DelegatesToDippin verifies the base price comes from
// dippin-lang/pricing without pinning a specific dollar figure in tracker
// (dippin owns and tests the values, #558): an input-only estimate must equal
// dippin's published per-million input rate for the model.
func TestEstimateCost_DelegatesToDippin(t *testing.T) {
	const model = "claude-sonnet-4-5"
	p, ok := pricing.Lookup(model)
	if !ok {
		t.Fatalf("dippin does not price %s", model)
	}
	got := EstimateCost(model, Usage{InputTokens: 1_000_000})
	if math.Abs(got-p.InputPerM) > 1e-9 {
		t.Errorf("input-only cost = %v, want dippin InputPerM %v", got, p.InputPerM)
	}
}

// TestEstimateCost_VersionFold checks the dotted/dashed model-ID fold resolves
// (claude-haiku-4.5 == claude-haiku-4-5) and both price identically and non-zero.
func TestEstimateCost_VersionFold(t *testing.T) {
	u := Usage{InputTokens: 1_000_000, OutputTokens: 500_000}
	dashed := EstimateCost("claude-haiku-4-5", u)
	dotted := EstimateCost("claude-haiku-4.5", u)
	if dashed == 0 || math.Abs(dashed-dotted) > 1e-9 {
		t.Errorf("version fold mismatch: dashed=%v dotted=%v", dashed, dotted)
	}
}

// TestEstimateCost_CacheOverlay verifies tracker's retained cache multipliers
// are applied on top of dippin's base input rate (the #558 overlay), computing
// the expectation from dippin's looked-up base rather than a pinned constant.
func TestEstimateCost_CacheOverlay(t *testing.T) {
	const model = "claude-sonnet-4-5" // reads 0.1x, writes 1.25x (Anthropic)
	p, ok := pricing.Lookup(model)
	if !ok || p.CacheReadMult > 0 || p.CachedInputPerM > 0 {
		t.Skip("dippin now prices cache for this model; overlay no longer applies")
	}
	read, write := 500_000, 200_000
	got := EstimateCost(model, Usage{CacheReadTokens: &read, CacheWriteTokens: &write})
	want := float64(read)/1e6*p.InputPerM*0.1 + float64(write)/1e6*p.InputPerM*1.25
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("cache-only cost = %v, want %v (base %v × tracker mults)", got, want, p.InputPerM)
	}
}

// TestCatalogModelsArePricedByDippin guards the tracker/dippin catalog
// divergence that the #558 cutover exposed: a model in tracker's capability
// catalog that dippin does not price silently costs $0, so --max-cost cannot
// bound it. Fails when a model drops out of dippin's pricing that is not already
// in the known-unpriced allowlist.
func TestCatalogModelsArePricedByDippin(t *testing.T) {
	for _, m := range ListModels("") {
		if _, ok := pricing.Lookup(m.ID); !ok && !knownUnpricedCatalogModels[m.ID] {
			t.Errorf("catalog model %q is not priced by dippin-lang/pricing and is not in the known-unpriced allowlist — it will cost $0 and escape --max-cost", m.ID)
		}
	}
}
