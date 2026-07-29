// ABOUTME: Pins cached-token pricing multipliers against each provider's published rates.
// ABOUTME: A wrong multiplier produces correct token counts at the wrong price, which no behavioral test can catch.
package llm

import (
	"math"
	"testing"
)

// TestCacheMultipliersMatchPublishedRates is the guard for a class of bug that
// is invisible to every other check we have: the token counts are right and the
// arithmetic is self-consistent, so the manifest agrees with the CLI and the
// ladder's token-fidelity assertion passes — only the rate is wrong.
//
// The regression it exists for priced cache writes at 0.25x the input rate,
// reading Anthropic's "+25% premium" as "0.25x rate". Real multiplier is 1.25x,
// so every cached Anthropic run understated its cache-write cost by 5x — about
// a third of the bill on a caching-heavy run.
//
// Each row is a published price, not a derived one. When a provider changes its
// pricing this test should fail; re-check the pricing page rather than adjusting
// the expectation to match the code.
func TestCacheMultipliersMatchPublishedRates(t *testing.T) {
	cases := []struct {
		model string
		// Published per-million prices. cachedIn of 0 means the provider does
		// not publish a separate cached-input price for this model.
		baseIn, cachedIn float64
		source           string
	}{
		// Anthropic publishes multipliers rather than per-model cached prices:
		// reads 0.1x, 5-minute writes 1.25x, 1-hour writes 2x.
		{"claude-sonnet-4-6", 3.0, 0.30, "0.1x published multiplier"},
		{"claude-haiku-4-5", 1.0, 0.10, "0.1x published multiplier"},
		// OpenAI publishes explicit cached-input prices, and they are not one
		// ratio across the lineup — this is why the multiplier is per model.
		{"gpt-5.4", 2.50, 0.25, "developers.openai.com/api/docs/pricing"},
		{"gpt-5.4-mini", 0.75, 0.075, "developers.openai.com/api/docs/pricing"},
		{"gpt-5.4-nano", 0.20, 0.02, "developers.openai.com/api/docs/pricing"},
		{"gpt-4.1", 2.00, 0.50, "developers.openai.com/api/docs/pricing"},
		{"gpt-4.1-mini", 0.40, 0.10, "developers.openai.com/api/docs/pricing"},
		{"gpt-4.1-nano", 0.10, 0.025, "developers.openai.com/api/docs/pricing"},
		{"gpt-4o", 2.50, 1.25, "developers.openai.com/api/docs/pricing"},
		{"gpt-4o-mini", 0.15, 0.075, "developers.openai.com/api/docs/pricing"},
		// Gemini context caching is 10% of standard input (ai.google.dev
		// pricing). The separate hourly storage fee is not token-derived and is
		// therefore not modeled here — see the note in the pricing docs.
		{"gemini-3-flash-preview", 0, 0, "0.1x published; base rate not asserted"},
	}

	for _, c := range cases {
		info := GetModelInfo(c.model)
		if info == nil {
			t.Errorf("%s: no catalog entry", c.model)
			continue
		}
		if c.baseIn > 0 && !closeEnough(info.InputCostPerM, c.baseIn) {
			t.Errorf("%s: InputCostPerM = %v, published %v (%s)",
				c.model, info.InputCostPerM, c.baseIn, c.source)
		}
		if c.cachedIn == 0 {
			continue
		}
		want := c.cachedIn / c.baseIn
		got := info.CacheReadMultiplier
		if got == 0 {
			got = defaultCacheReadMultiplier
		}
		if !closeEnough(got, want) {
			t.Errorf("%s: cache read multiplier = %v, published cached/base = %v/%v = %v (%s)",
				c.model, got, c.cachedIn, c.baseIn, want, c.source)
		}
	}
}

// TestCacheWriteIsAPremiumNotADiscount states the invariant the original bug
// violated. A write multiplier below 1 means cached writes are cheaper than
// uncached input, which is true of no provider we price — writes cost *more*
// because the cache has to be populated.
func TestCacheWriteIsAPremiumNotADiscount(t *testing.T) {
	if defaultCacheWriteMultiplier < 1 {
		t.Errorf("default cache write multiplier = %v; a write is a premium over base input, not a discount",
			defaultCacheWriteMultiplier)
	}
	// Anthropic's published 5-minute rate.
	if !closeEnough(defaultCacheWriteMultiplier, 1.25) {
		t.Errorf("default cache write multiplier = %v, want 1.25 (Anthropic 5-minute published rate)",
			defaultCacheWriteMultiplier)
	}
}

// TestEstimateCostPricesCacheBucketsAdditively checks the arithmetic end to end
// against a hand-computed figure, on the llm.Usage invariant that InputTokens
// excludes the cache buckets.
func TestEstimateCostPricesCacheBucketsAdditively(t *testing.T) {
	read, write := 100_000, 10_000
	got := EstimateCost("claude-sonnet-4-6", Usage{
		InputTokens:      1_000,
		OutputTokens:     2_000,
		CacheReadTokens:  &read,
		CacheWriteTokens: &write,
	})

	// sonnet-4-6: $3/M in, $15/M out; reads 0.1x, writes 1.25x.
	want := 1_000/1e6*3.0 + 2_000/1e6*15.0 + 100_000/1e6*3.0*0.1 + 10_000/1e6*3.0*1.25
	if !closeEnough(got, want) {
		t.Errorf("EstimateCost = %v, want %v", got, want)
	}

	// The old 0.25x write multiplier would land here — assert we moved off it,
	// since both figures are plausible-looking totals.
	old := 1_000/1e6*3.0 + 2_000/1e6*15.0 + 100_000/1e6*3.0*0.1 + 10_000/1e6*3.0*0.25
	if closeEnough(got, old) {
		t.Errorf("EstimateCost still matches the 0.25x cache-write price (%v)", old)
	}
}

// TestPerModelMultiplierBeatsTheDefault proves the catalog override is actually
// consulted — a field that is read nowhere would leave every model on 0.1x and
// silently underprice the GPT-4.1 and GPT-4o families.
func TestPerModelMultiplierBeatsTheDefault(t *testing.T) {
	read := 1_000_000
	// gpt-4o-mini: $0.15/M in, cached at 0.5x — not the 0.1x default.
	got := EstimateCost("gpt-4o-mini", Usage{CacheReadTokens: &read})
	if want := 0.15 * 0.5; !closeEnough(got, want) {
		t.Errorf("gpt-4o-mini cached-read cost = %v, want %v (0.5x, not the %v default)",
			got, want, defaultCacheReadMultiplier)
	}
}

func closeEnough(a, b float64) bool { return math.Abs(a-b) < 1e-9 }
