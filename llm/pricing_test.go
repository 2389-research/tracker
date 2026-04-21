// ABOUTME: Tests for model pricing table and cost estimation.
// ABOUTME: Validates cost calculation for known/unknown models and edge cases.
package llm

import (
	"math"
	"testing"
)

func TestEstimateCost_KnownModel(t *testing.T) {
	usage := Usage{InputTokens: 1_000_000, OutputTokens: 1_000_000}
	got := EstimateCost("claude-sonnet-4-5", usage)
	want := 18.00
	if math.Abs(got-want) > 0.001 {
		t.Errorf("EstimateCost(claude-sonnet-4-5, 1M/1M) = %f, want %f", got, want)
	}
}

func TestEstimateCost_UnknownModel(t *testing.T) {
	usage := Usage{InputTokens: 1_000_000, OutputTokens: 1_000_000}
	got := EstimateCost("unknown-model-xyz", usage)
	if got != 0 {
		t.Errorf("EstimateCost(unknown) = %f, want 0", got)
	}
}

func TestEstimateCost_ZeroTokens(t *testing.T) {
	usage := Usage{InputTokens: 0, OutputTokens: 0}
	got := EstimateCost("claude-sonnet-4-5", usage)
	if got != 0 {
		t.Errorf("EstimateCost(zero tokens) = %f, want 0", got)
	}
}

func TestEstimateCost_OpusModel(t *testing.T) {
	// 100K input: 100_000 / 1_000_000 * 5.00 = 0.50
	// 10K output: 10_000 / 1_000_000 * 25.00 = 0.25
	// Total: 0.75
	usage := Usage{InputTokens: 100_000, OutputTokens: 10_000}
	got := EstimateCost("claude-opus-4-6", usage)
	want := 0.75
	if math.Abs(got-want) > 0.001 {
		t.Errorf("EstimateCost(opus, 100K/10K) = %f, want %f", got, want)
	}
}

func TestEstimateCost_CacheTokensPriced(t *testing.T) {
	cacheRead := 500_000
	cacheWrite := 200_000
	usage := Usage{
		InputTokens:      100_000,
		OutputTokens:     50_000,
		CacheReadTokens:  &cacheRead,
		CacheWriteTokens: &cacheWrite,
	}
	// claude-sonnet-4-5: input $3/M, output $15/M
	// 100K input: 100_000 / 1_000_000 * 3.00 = 0.30
	// 50K output: 50_000 / 1_000_000 * 15.00 = 0.75
	// 500K cache read: 500_000 / 1_000_000 * 3.00 * 0.1 = 0.15
	// 200K cache write: 200_000 / 1_000_000 * 3.00 * 0.25 = 0.15
	// Total: 0.30 + 0.75 + 0.15 + 0.15 = 1.35
	got := EstimateCost("claude-sonnet-4-5", usage)
	want := 1.35
	if math.Abs(got-want) > 0.001 {
		t.Errorf("EstimateCost(with cache tokens) = %f, want %f", got, want)
	}
}

func TestEstimateCost_GPT52(t *testing.T) {
	usage := Usage{InputTokens: 1_000_000, OutputTokens: 1_000_000}
	got := EstimateCost("gpt-5.2", usage)
	// 1M input @ $5 + 1M output @ $15 = 20.00
	want := 20.00
	if math.Abs(got-want) > 0.001 {
		t.Errorf("EstimateCost(gpt-5.2, 1M/1M) = %f, want %f", got, want)
	}
}

func TestEstimateCost_GeminiFlash(t *testing.T) {
	usage := Usage{InputTokens: 1_000_000, OutputTokens: 1_000_000}
	got := EstimateCost("gemini-3-flash-preview", usage)
	// 1M input @ $0.50 + 1M output @ $3.00 = 3.50
	want := 3.50
	if math.Abs(got-want) > 0.001 {
		t.Errorf("EstimateCost(gemini-3-flash, 1M/1M) = %f, want %f", got, want)
	}
}

func TestEstimateCost_Alias(t *testing.T) {
	usage := Usage{InputTokens: 1_000_000, OutputTokens: 1_000_000}
	got := EstimateCost("codex", usage)
	// gpt-5.2-codex via alias: 1M input @ $2.50 + 1M output @ $10 = 12.50
	want := 12.50
	if math.Abs(got-want) > 0.001 {
		t.Errorf("EstimateCost(codex alias, 1M/1M) = %f, want %f", got, want)
	}
}
