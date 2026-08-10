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
		// wantWrite is the model's published effective cache-WRITE multiplier
		// (fraction of base input). Anthropic charges a 1.25x 5-minute premium;
		// OpenAI (prompt-cache writes free) and Gemini (time-based storage, not
		// per-token) charge no per-token write premium, so 0. Asserting this per
		// model is the coverage that was missing: the pre-#522 default silently
		// billed every OpenAI/Gemini model the Anthropic 1.25x premium.
		wantWrite float64
		source    string
	}{
		// Anthropic publishes multipliers rather than per-model cached prices:
		// reads 0.1x, 5-minute writes 1.25x, 1-hour writes 2x.
		{"claude-sonnet-4-6", 3.0, 0.30, 1.25, "0.1x read / 1.25x write published multiplier"},
		{"claude-haiku-4-5", 1.0, 0.10, 1.25, "0.1x read / 1.25x write published multiplier"},
		// OpenAI publishes explicit cached-input prices, and they are not one
		// ratio across the lineup — this is why the multiplier is per model.
		// Writes are free, so wantWrite is 0 for the whole lineup.
		{"gpt-5.4", 2.50, 0.25, 0, "developers.openai.com/api/docs/pricing"},
		{"gpt-5.4-mini", 0.75, 0.075, 0, "developers.openai.com/api/docs/pricing"},
		{"gpt-5.4-nano", 0.20, 0.02, 0, "developers.openai.com/api/docs/pricing"},
		// o-series and gpt-5.2 family ride the shared 0.1x read default (no
		// per-model override in the catalog) — pinned here so an accidental
		// override or a default change is caught. Writes are free.
		{"gpt-5.2", 5.0, 0.50, 0, "0.1x read default (GPT-5 family)"},
		{"o3", 2.00, 0.20, 0, "0.1x read default (o-series)"},
		{"o4-mini", 1.10, 0.11, 0, "0.1x read default (o-series)"},
		{"gpt-4.1", 2.00, 0.50, 0, "developers.openai.com/api/docs/pricing"},
		{"gpt-4.1-mini", 0.40, 0.10, 0, "developers.openai.com/api/docs/pricing"},
		{"gpt-4.1-nano", 0.10, 0.025, 0, "developers.openai.com/api/docs/pricing"},
		{"gpt-4o", 2.50, 1.25, 0, "developers.openai.com/api/docs/pricing"},
		{"gpt-4o-mini", 0.15, 0.075, 0, "developers.openai.com/api/docs/pricing"},
		// Gemini context caching is 10% of standard input (ai.google.dev
		// pricing). The separate hourly storage fee is not token-derived and is
		// therefore not modeled here — see the note in the pricing docs. No
		// per-token write premium.
		{"gemini-3-flash-preview", 0.50, 0.05, 0, "0.1x read (ai.google.dev); no per-token write"},
	}

	for _, c := range cases {
		info := GetModelInfo(c.model)
		if info == nil {
			t.Errorf("%s: no catalog entry", c.model)
			continue
		}
		// Base input/output prices are now dippin-lang/pricing's responsibility
		// (#558); the drift test for them lives there. c.baseIn is retained only
		// to derive the expected read multiplier below. This file guards the
		// cache multipliers, which tracker still owns until dippin ships them.
		if gotW := effectiveWriteMultiplier(info); !closeEnough(gotW, c.wantWrite) {
			t.Errorf("%s: cache WRITE multiplier = %v, published %v (%s)",
				c.model, gotW, c.wantWrite, c.source)
		}
		if c.cachedIn == 0 {
			continue
		}
		want := c.cachedIn / c.baseIn
		got := effectiveReadMultiplier(info)
		if !closeEnough(got, want) {
			t.Errorf("%s: cache read multiplier = %v, published cached/base = %v/%v = %v (%s)",
				c.model, got, c.cachedIn, c.baseIn, want, c.source)
		}
	}
}

// effectiveReadMultiplier and effectiveWriteMultiplier mirror the fallback
// resolution in cacheCost's perM helper: a zero on the model means "use the
// package default". The test asserts the *effective* rate a run is billed at,
// which is what the bug was about — never the raw catalog field in isolation.
func effectiveReadMultiplier(info *ModelInfo) float64 {
	if info.CacheReadMultiplier == 0 {
		return defaultCacheReadMultiplier
	}
	return info.CacheReadMultiplier
}

func effectiveWriteMultiplier(info *ModelInfo) float64 {
	if info.CacheWriteMultiplier == 0 {
		return defaultCacheWriteMultiplier
	}
	return info.CacheWriteMultiplier
}

// TestCacheWriteIsAPremiumNotADiscount states the invariant the #519 bug
// violated: for a provider that DOES charge a per-token write premium
// (Anthropic), a write costs *more* than uncached input because the cache has
// to be populated — a multiplier below 1 would be a nonsensical discount.
//
// This is now asserted per Anthropic model rather than against the package
// default, because #522 moved the default to 0 (no premium) so that OpenAI and
// Gemini — which charge nothing to write — are not billed a phantom 1.25x.
func TestCacheWriteIsAPremiumNotADiscount(t *testing.T) {
	for _, model := range []string{"claude-sonnet-4-6", "claude-haiku-4-5", "claude-opus-4-7"} {
		info := GetModelInfo(model)
		if info == nil {
			t.Errorf("%s: no catalog entry", model)
			continue
		}
		got := effectiveWriteMultiplier(info)
		if got < 1 {
			t.Errorf("%s: cache write multiplier = %v; an Anthropic write is a premium over base input, not a discount",
				model, got)
		}
		// Anthropic's published 5-minute rate.
		if !closeEnough(got, 1.25) {
			t.Errorf("%s: cache write multiplier = %v, want 1.25 (Anthropic 5-minute published rate)",
				model, got)
		}
	}
}

// TestNonAnthropicCacheWriteHasNoPremium is the direct #522 guard at the
// pricing layer: a model that charges no per-token write premium must resolve
// to a 0 effective write multiplier, so a populated CacheWriteTokens bucket
// adds nothing to the bill.
func TestNonAnthropicCacheWriteHasNoPremium(t *testing.T) {
	write := 10_000
	for _, model := range []string{"gpt-5.4", "gpt-4o", "gemini-3-flash-preview"} {
		info := GetModelInfo(model)
		if info == nil {
			t.Errorf("%s: no catalog entry", model)
			continue
		}
		if got := effectiveWriteMultiplier(info); got != 0 {
			t.Errorf("%s: cache write multiplier = %v, want 0 (no per-token write premium)", model, got)
		}
		if cost := EstimateCost(model, Usage{CacheWriteTokens: &write}); cost != 0 {
			t.Errorf("%s: EstimateCost with %d cache-write tokens = %v, want 0", model, write, cost)
		}
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
