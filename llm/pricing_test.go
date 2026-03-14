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
	// 100K input: 100_000 / 1_000_000 * 15.00 = 1.50
	// 10K output: 10_000 / 1_000_000 * 75.00 = 0.75
	// Total: 2.25
	usage := Usage{InputTokens: 100_000, OutputTokens: 10_000}
	got := EstimateCost("claude-opus-4-6", usage)
	want := 2.25
	if math.Abs(got-want) > 0.001 {
		t.Errorf("EstimateCost(opus, 100K/10K) = %f, want %f", got, want)
	}
}

func TestEstimateCost_CacheTokensIgnored(t *testing.T) {
	cacheRead := 500_000
	cacheWrite := 200_000
	usage := Usage{
		InputTokens:      100_000,
		OutputTokens:     50_000,
		CacheReadTokens:  &cacheRead,
		CacheWriteTokens: &cacheWrite,
	}
	// Cost should only be based on input/output tokens
	// 100K input: 100_000 / 1_000_000 * 3.00 = 0.30
	// 50K output: 50_000 / 1_000_000 * 15.00 = 0.75
	// Total: 1.05
	got := EstimateCost("claude-sonnet-4-5", usage)
	want := 1.05
	if math.Abs(got-want) > 0.001 {
		t.Errorf("EstimateCost(with cache tokens) = %f, want %f", got, want)
	}
}
