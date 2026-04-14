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
