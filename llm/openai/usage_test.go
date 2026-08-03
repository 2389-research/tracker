// ABOUTME: Tests that OpenAI usage normalizes onto the llm.Usage invariants.
// ABOUTME: Reasoning is already inside output_tokens; cached tokens are already inside input_tokens.
package openai

import (
	"testing"

	"github.com/2389-research/tracker/llm"
)

// TestOpenAICacheWriteAddsNoPhantomPremium is the reviewer's repro for #522.
// OpenAI does not charge a per-token cache-write premium (prompt-cache writes
// are free), yet the shared pricing default applied Anthropic's 1.25x write
// premium to any model without an explicit override — every OpenAI model.
// A gpt-5.4 response reporting 10_000 cache-write tokens must add $0, not
// input_rate*1.25*10_000.
func TestOpenAICacheWriteAddsNoPhantomPremium(t *testing.T) {
	u := translateUsage(openaiUsage{
		InputTokens:  10_000, // the whole prompt is a cache write
		OutputTokens: 0,
		TotalTokens:  10_000,
		InputDetail:  &openaiInDetail{CacheWriteTokens: 10_000},
	})

	got := llm.EstimateCost("gpt-5.4", u)
	if got != 0 {
		t.Errorf("EstimateCost with 10_000 OpenAI cache-write tokens = %v, want 0 "+
			"(OpenAI charges no per-token write premium; the 1.25x default is Anthropic-only)", got)
	}
}

// TestTranslateUsageFloorsInputAtZero guards against a gateway reporting a
// non-nested write bucket: cached + write must not drive InputTokens negative.
func TestTranslateUsageFloorsInputAtZero(t *testing.T) {
	u := translateUsage(openaiUsage{
		InputTokens:  10_000,
		OutputTokens: 0,
		TotalTokens:  10_000,
		InputDetail:  &openaiInDetail{CachedTokens: 6_000, CacheWriteTokens: 6_000},
	})
	if u.InputTokens < 0 {
		t.Errorf("InputTokens = %d, want floored at 0 (6000 cached + 6000 write > 10000 input)", u.InputTokens)
	}
}

// TestTranslateUsageLiftsCachedTokensOut covers the nesting that makes this
// provider's fix asymmetric with Anthropic's. OpenAI reports cached tokens
// *inside* input_tokens, and pricing sums the buckets — so the cached count has
// to be subtracted on the way out. Copying Anthropic's mapping, where cache
// reads arrive as a separate bucket already, would count the prompt twice.
func TestTranslateUsageLiftsCachedTokensOut(t *testing.T) {
	u := translateUsage(openaiUsage{
		InputTokens:  1000, // includes the 800 cached
		OutputTokens: 50,
		TotalTokens:  1050,
		InputDetail:  &openaiInDetail{CachedTokens: 800},
	})

	if u.InputTokens != 200 {
		t.Errorf("InputTokens = %d, want 200 (1000 - 800 cached)", u.InputTokens)
	}
	if u.CacheReadTokens == nil || *u.CacheReadTokens != 800 {
		t.Errorf("CacheReadTokens = %v, want 800", u.CacheReadTokens)
	}
	if got := u.InputTokens + *u.CacheReadTokens; got != 1000 {
		t.Errorf("input+cacheRead = %d, want the 1000 tokens OpenAI reported", got)
	}
}

// TestTranslateUsageLeavesReasoningInsideOutput is the guard against the
// tempting symmetric change. Reasoning tokens are already counted in
// output_tokens, so adding them again — or pricing them separately — bills them
// twice. ReasoningTokens is reported for visibility only.
func TestTranslateUsageLeavesReasoningInsideOutput(t *testing.T) {
	u := translateUsage(openaiUsage{
		InputTokens:  10,
		OutputTokens: 500, // already contains the 450 reasoning tokens
		TotalTokens:  510,
		OutputDetail: &openaiOutDetail{ReasoningTokens: 450},
	})

	if u.OutputTokens != 500 {
		t.Errorf("OutputTokens = %d, want 500 unchanged — reasoning is already inside it", u.OutputTokens)
	}
	if u.ReasoningTokens == nil || *u.ReasoningTokens != 450 {
		t.Errorf("ReasoningTokens = %v, want 450 recorded as an informational subset", u.ReasoningTokens)
	}
}

// TestTranslateUsageWithoutDetails covers the common case: no caching, no
// reasoning. Both optional buckets stay nil rather than reporting a hollow zero.
func TestTranslateUsageWithoutDetails(t *testing.T) {
	u := translateUsage(openaiUsage{InputTokens: 100, OutputTokens: 20, TotalTokens: 120})
	if u.InputTokens != 100 || u.OutputTokens != 20 {
		t.Errorf("got %d/%d, want 100/20 passed through", u.InputTokens, u.OutputTokens)
	}
	if u.CacheReadTokens != nil || u.ReasoningTokens != nil {
		t.Errorf("optional buckets should stay nil, got cache=%v reasoning=%v",
			u.CacheReadTokens, u.ReasoningTokens)
	}
}
