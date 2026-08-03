// ABOUTME: Tests for TokenTracker per-provider cost rollup.
// ABOUTME: Verifies CostByProvider resolves models via the caller callback and prices via EstimateCost.
package llm

import (
	"testing"
)

func TestTokenTracker_CostByProvider(t *testing.T) {
	tr := NewTokenTracker()
	tr.AddUsage("anthropic", Usage{InputTokens: 1_000_000, OutputTokens: 500_000})
	tr.AddUsage("openai", Usage{InputTokens: 2_000_000, OutputTokens: 1_000_000})

	resolver := func(provider string) string {
		switch provider {
		case "anthropic":
			return "claude-sonnet-4-6"
		case "openai":
			return "gpt-4o"
		}
		return ""
	}

	breakdown := tr.CostByProvider(resolver)

	// 1M anthropic input @ $3 + 0.5M output @ $15 = 3 + 7.5 = 10.50
	if got := breakdown["anthropic"].USD; got < 10.49 || got > 10.51 {
		t.Errorf("anthropic cost: got %.4f, want 10.50", got)
	}
	// 2M openai input @ $2.50 + 1M output @ $10 = 5 + 10 = 15.00
	if got := breakdown["openai"].USD; got < 14.99 || got > 15.01 {
		t.Errorf("openai cost: got %.4f, want 15.00", got)
	}
	if breakdown["anthropic"].Model != "claude-sonnet-4-6" {
		t.Errorf("anthropic model = %q", breakdown["anthropic"].Model)
	}
	if breakdown["openai"].Usage.InputTokens != 2_000_000 {
		t.Errorf("openai usage input = %d", breakdown["openai"].Usage.InputTokens)
	}
}

func TestTokenTracker_CostByProvider_UnknownModel(t *testing.T) {
	tr := NewTokenTracker()
	tr.AddUsage("mystery", Usage{InputTokens: 10_000, OutputTokens: 5_000})

	breakdown := tr.CostByProvider(func(string) string { return "" })
	if breakdown["mystery"].USD != 0 {
		t.Errorf("unknown model should yield $0, got %.4f", breakdown["mystery"].USD)
	}
	if _, ok := breakdown["mystery"]; !ok {
		t.Errorf("unknown-model provider should still appear in map")
	}
}

func TestTokenTracker_TotalCostUSD(t *testing.T) {
	tr := NewTokenTracker()
	tr.AddUsage("anthropic", Usage{InputTokens: 1_000_000, OutputTokens: 500_000})
	tr.AddUsage("openai", Usage{InputTokens: 2_000_000, OutputTokens: 1_000_000})

	resolver := func(provider string) string {
		switch provider {
		case "anthropic":
			return "claude-sonnet-4-6"
		case "openai":
			return "gpt-4o"
		}
		return ""
	}

	total := tr.TotalCostUSD(resolver)
	// 10.50 + 15.00 = 25.50
	if total < 25.49 || total > 25.51 {
		t.Errorf("total = %.4f, want 25.50", total)
	}
}

func TestTokenTracker_CostByProvider_NilResolver(t *testing.T) {
	tr := NewTokenTracker()
	tr.AddUsage("anthropic", Usage{InputTokens: 1000, OutputTokens: 500})

	breakdown := tr.CostByProvider(nil)
	if breakdown["anthropic"].USD != 0 {
		t.Errorf("nil resolver should yield $0, got %.4f", breakdown["anthropic"].USD)
	}
}

func TestTokenTracker_AddUsage_NormalizesModel(t *testing.T) {
	// AddUsage must canonicalize the model through the catalog so callers
	// passing aliases or versioned IDs get the same resolution as WrapComplete.
	tr := NewTokenTracker()
	tr.AddUsage("anthropic", Usage{InputTokens: 1000}, "sonnet-4-6")

	if got := tr.ModelForProvider("anthropic"); got != "claude-sonnet-4-6" {
		t.Errorf("alias not canonicalized: got %q, want %q", got, "claude-sonnet-4-6")
	}
}

func TestTokenTracker_AddUsage_UnknownModelPreserved(t *testing.T) {
	// Unknown models pass through so downstream fallback resolvers get
	// a chance to use them.
	tr := NewTokenTracker()
	tr.AddUsage("anthropic", Usage{InputTokens: 1000}, "claude-custom-2099")

	if got := tr.ModelForProvider("anthropic"); got != "claude-custom-2099" {
		t.Errorf("unknown model not preserved: got %q", got)
	}
}

// TestTokenTracker_CostByProvider_MultiModel is the #527 repro: two models of
// the same provider must each be priced with ITS OWN rate and summed. The total
// must be order-independent. opus-4-7 input $5/M, haiku-4-5 input $1/M; 1M each
// = $6.00 true. The pre-fix code keyed usage by provider only and priced the
// whole bucket with the last-observed model, yielding $2 (haiku last) or $10
// (opus last).
func TestTokenTracker_CostByProvider_MultiModel(t *testing.T) {
	const wantUSD = 6.00

	// Order A: opus then haiku.
	trA := NewTokenTracker()
	trA.AddUsage("anthropic", Usage{InputTokens: 1_000_000}, "claude-opus-4-7")
	trA.AddUsage("anthropic", Usage{InputTokens: 1_000_000}, "claude-haiku-4-5")

	// Order B: haiku then opus.
	trB := NewTokenTracker()
	trB.AddUsage("anthropic", Usage{InputTokens: 1_000_000}, "claude-haiku-4-5")
	trB.AddUsage("anthropic", Usage{InputTokens: 1_000_000}, "claude-opus-4-7")

	// nil resolver: per-model observed pricing must drive the total on its own.
	for _, tc := range []struct {
		name string
		tr   *TokenTracker
	}{{"opus-then-haiku", trA}, {"haiku-then-opus", trB}} {
		got := tc.tr.CostByProvider(nil)["anthropic"]
		if got.USD < wantUSD-0.01 || got.USD > wantUSD+0.01 {
			t.Errorf("%s: anthropic total = $%.4f, want $%.2f", tc.name, got.USD, wantUSD)
		}
		// Full 2M input must still roll up under the single provider bucket.
		if got.Usage.InputTokens != 2_000_000 {
			t.Errorf("%s: anthropic usage input = %d, want 2000000", tc.name, got.Usage.InputTokens)
		}
	}

	// TotalCostUSD (drives tracker.Result.Cost / the CLI summary) must agree.
	if total := trA.TotalCostUSD(nil); total < wantUSD-0.01 || total > wantUSD+0.01 {
		t.Errorf("TotalCostUSD = $%.4f, want $%.2f", total, wantUSD)
	}
}
