// ABOUTME: Pins every catalog price and cache multiplier to a recorded published-price provenance entry.
// ABOUTME: A provider price change, or a catalog/multiplier that drifts from its source, fails the build naming the model and its source URL.
package llm

import "testing"

// TestCatalogPricesMatchPublishedProvenance is #518's decision-free methodology
// half: each catalog entry is asserted CONSISTENT with a recorded published
// price and its source URL, rather than a test baking in the same hand-entered
// constant the catalog uses. When a provider changes a price, or someone edits
// a catalog rate without updating its provenance, this fails with a pointer to
// the page to re-check — the drift signal that was missing when #522's
// cache-write bug shipped invisibly.
//
// For the near-future fictional models (gpt-5.x, gemini-3) whose absolute rates
// can't be checked against a live page, the provenance records the provider's
// pricing page as the source and the assertion is internal consistency
// (multipliers vs the recorded base), not an external absolute number.
func TestCatalogPricesMatchPublishedProvenance(t *testing.T) {
	for i := range defaultCatalog {
		m := &defaultCatalog[i]
		p, ok := publishedPrices[m.ID]
		if !ok {
			t.Errorf("%s: no published-price provenance entry — every catalogued model must record its source of truth", m.ID)
			continue
		}
		if p.Source == "" {
			t.Errorf("%s: published-price provenance has an empty Source URL", m.ID)
		}

		// Base input/output must equal the recorded published figures. For the
		// fictional models this is the recorded provider-page value; editing the
		// catalog rate without re-checking the page trips this.
		if !closeEnough(m.InputCostPerM, p.InputPerM) {
			t.Errorf("%s: InputCostPerM = %v, published %v (%s)", m.ID, m.InputCostPerM, p.InputPerM, p.Source)
		}
		if !closeEnough(m.OutputCostPerM, p.OutputPerM) {
			t.Errorf("%s: OutputCostPerM = %v, published %v (%s)", m.ID, m.OutputCostPerM, p.OutputPerM, p.Source)
		}

		// Cache-read multiplier: computed from the published cached-input price
		// where the provider publishes one per model (OpenAI), otherwise from
		// the documented multiplier convention (Anthropic/Gemini 0.1x).
		if wantRead, hasRead := p.expectedReadMultiplier(); hasRead {
			if got := effectiveReadMultiplier(m); !closeEnough(got, wantRead) {
				t.Errorf("%s: cache READ multiplier = %v, published %v (%s)",
					m.ID, got, wantRead, p.Source)
			}
		}

		// Cache-write multiplier: the published per-token write premium
		// convention. Anthropic 1.25x (5-minute); OpenAI/Gemini 0 (free / not
		// per-token). This is the per-model write assertion #522 flagged missing.
		if got := effectiveWriteMultiplier(m); !closeEnough(got, p.WriteMult) {
			t.Errorf("%s: cache WRITE multiplier = %v, published %v (%s)",
				m.ID, got, p.WriteMult, p.Source)
		}
	}
}

// TestEveryPricedModelHasASourceURL asserts the provenance table stays in
// lockstep with the catalog and that nothing is priced without a citation. A
// priced model with no source URL has no drift signal — the exact gap #518 is
// closing.
func TestEveryPricedModelHasASourceURL(t *testing.T) {
	for i := range defaultCatalog {
		m := &defaultCatalog[i]
		p, ok := publishedPrices[m.ID]
		if !ok {
			t.Errorf("%s: priced in the catalog but absent from publishedPrices", m.ID)
			continue
		}
		if p.Source == "" {
			t.Errorf("%s: priced with no source URL", m.ID)
		}
	}
	// And no orphan provenance rows for models the catalog dropped.
	for id := range publishedPrices {
		if GetModelInfo(id) == nil {
			t.Errorf("publishedPrices has an entry for %q with no catalog model", id)
		}
	}
}
