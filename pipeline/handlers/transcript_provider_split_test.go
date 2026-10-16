// ABOUTME: Tests that buildSessionStats carries a session's per-provider usage split into
// ABOUTME: SessionStats.ProviderUsage (#702), and leaves it nil for a session one provider served.
package handlers

import (
	"reflect"
	"testing"

	"github.com/2389-research/tracker/agent"
	"github.com/2389-research/tracker/llm"
	"github.com/2389-research/tracker/pipeline"
)

func TestBuildSessionStatsCarriesProviderSplit(t *testing.T) {
	reasoning, cacheRead := 5, 7
	stats := buildSessionStats(agent.SessionResult{
		Provider: "anthropic",
		Usage:    llm.Usage{InputTokens: 150, OutputTokens: 15, TotalTokens: 165, EstimatedCost: 1.5},
		UsageByProvider: map[string]llm.Usage{
			"anthropic":           {InputTokens: 100, OutputTokens: 10, TotalTokens: 110, EstimatedCost: 1.5, ReasoningTokens: &reasoning, CacheReadTokens: &cacheRead},
			"openai-subscription": {InputTokens: 50, OutputTokens: 5, TotalTokens: 55},
		},
	})

	want := map[string]pipeline.ProviderUsage{
		"anthropic":           {InputTokens: 100, OutputTokens: 10, TotalTokens: 110, CostUSD: 1.5, ReasoningTokens: 5, CacheReadTokens: 7, SessionCount: 1},
		"openai-subscription": {InputTokens: 50, OutputTokens: 5, TotalTokens: 55, SessionCount: 1},
	}
	if !reflect.DeepEqual(stats.ProviderUsage, want) {
		t.Errorf("ProviderUsage = %+v\nwant %+v", stats.ProviderUsage, want)
	}
	if stats.Provider != "anthropic" {
		t.Errorf("Provider = %q, want the session's anthropic", stats.Provider)
	}
}

func TestBuildSessionStatsWithoutSplitHasNoProviderUsage(t *testing.T) {
	stats := buildSessionStats(agent.SessionResult{
		Provider: "anthropic",
		Usage:    llm.Usage{InputTokens: 100, OutputTokens: 10, TotalTokens: 110},
	})

	if stats.ProviderUsage != nil {
		t.Errorf("ProviderUsage = %+v, want nil for a session one provider served", stats.ProviderUsage)
	}
}
