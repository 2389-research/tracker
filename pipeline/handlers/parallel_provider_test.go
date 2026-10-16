// ABOUTME: Tests that a parallel node's aggregate stats keep its codergen branches' provider attribution
// ABOUTME: and session count (#711), so the usage rollup files each branch under its own provider.
package handlers

import (
	"testing"

	"github.com/2389-research/tracker/pipeline"
)

// rollup aggregates the parallel node's stats the way the engine's trace does.
func rollup(stats *pipeline.SessionStats) *pipeline.UsageSummary {
	tr := &pipeline.Trace{Entries: []pipeline.TraceEntry{{NodeID: "Fork", Stats: stats}}}
	return tr.AggregateUsage()
}

func branch(provider string, tokens int, cost float64) ParallelResult {
	return ParallelResult{Stats: &pipeline.SessionStats{
		Provider: provider, InputTokens: tokens, TotalTokens: tokens, CostUSD: cost, ToolCalls: map[string]int{},
	}}
}

func TestAggregateBranchStatsKeepsASharedProvider(t *testing.T) {
	agg := aggregateBranchStats([]ParallelResult{branch("anthropic", 10, 1), branch("anthropic", 20, 2)})

	if agg.Provider != "anthropic" || agg.SessionCount != 2 || agg.ProviderUsage != nil {
		t.Fatalf("aggregate Provider=%q SessionCount=%d ProviderUsage=%+v; want anthropic, 2, no split", agg.Provider, agg.SessionCount, agg.ProviderUsage)
	}
	s := rollup(agg)
	if _, ok := s.ProviderTotals["unknown"]; ok {
		t.Errorf("ProviderTotals has an unknown row: %+v", s.ProviderTotals)
	}
	if got := s.ProviderTotals["anthropic"]; got.TotalTokens != 30 || got.CostUSD != 3 || got.SessionCount != 2 {
		t.Errorf("anthropic = %+v, want 30 tokens, $3, 2 sessions", got)
	}
	if s.SessionCount != 2 {
		t.Errorf("SessionCount = %d, want 2", s.SessionCount)
	}
}

func TestAggregateBranchStatsSplitsMixedProviders(t *testing.T) {
	agg := aggregateBranchStats([]ParallelResult{branch("anthropic", 10, 1), branch("openai", 20, 2)})

	s := rollup(agg)
	if got := s.ProviderTotals["anthropic"]; got.TotalTokens != 10 || got.CostUSD != 1 || got.SessionCount != 1 {
		t.Errorf("anthropic = %+v, want 10 tokens, $1, 1 session", got)
	}
	if got := s.ProviderTotals["openai"]; got.TotalTokens != 20 || got.CostUSD != 2 || got.SessionCount != 1 {
		t.Errorf("openai = %+v, want 20 tokens, $2, 1 session", got)
	}
	if s.SessionCount != 2 || s.TotalTokens != 30 {
		t.Errorf("totals = %d sessions, %d tokens; want 2, 30", s.SessionCount, s.TotalTokens)
	}
}

func TestAggregateBranchStatsKeepsAFailoverBranchSplit(t *testing.T) {
	failedOver := branch("anthropic", 10, 1)
	failedOver.Stats.ProviderUsage = map[string]pipeline.ProviderUsage{
		"anthropic": {InputTokens: 5, TotalTokens: 5, CostUSD: 1, SessionCount: 1},
		"openai":    {InputTokens: 5, TotalTokens: 5, SessionCount: 1},
	}
	agg := aggregateBranchStats([]ParallelResult{failedOver, branch("openai", 20, 2)})

	s := rollup(agg)
	if got := s.ProviderTotals["anthropic"]; got.TotalTokens != 5 || got.SessionCount != 1 {
		t.Errorf("anthropic = %+v, want 5 tokens, 1 session", got)
	}
	if got := s.ProviderTotals["openai"]; got.TotalTokens != 25 || got.SessionCount != 2 {
		t.Errorf("openai = %+v, want 25 tokens, 2 sessions", got)
	}
	if s.SessionCount != 2 {
		t.Errorf("SessionCount = %d, want 2: two branch sessions, one of which failed over", s.SessionCount)
	}
}

func TestAggregateBranchStatsWithoutProvidersKeepsItsShape(t *testing.T) {
	agg := aggregateBranchStats([]ParallelResult{branch("", 10, 0), branch("", 20, 0)})

	if agg.Provider != "" || agg.ProviderUsage != nil {
		t.Errorf("aggregate Provider=%q ProviderUsage=%+v; want neither when no branch names a provider", agg.Provider, agg.ProviderUsage)
	}
	if got := rollup(agg).ProviderTotals["unknown"]; got.TotalTokens != 30 || got.SessionCount != 2 {
		t.Errorf("unknown = %+v, want 30 tokens, 2 sessions", got)
	}
}

func TestAggregateBranchStatsSumsReasoningAndCacheTokens(t *testing.T) {
	a, b := branch("anthropic", 10, 1), branch("anthropic", 20, 2)
	a.Stats.ReasoningTokens, a.Stats.CacheReadTokens, a.Stats.CacheWriteTokens = 3, 10, 1
	b.Stats.ReasoningTokens, b.Stats.CacheReadTokens, b.Stats.CacheWriteTokens = 4, 20, 2

	agg := aggregateBranchStats([]ParallelResult{a, b})

	if agg.ReasoningTokens != 7 || agg.CacheReadTokens != 30 || agg.CacheWriteTokens != 3 {
		t.Errorf("aggregate reasoning/cache-read/cache-write = %d/%d/%d, want 7/30/3", agg.ReasoningTokens, agg.CacheReadTokens, agg.CacheWriteTokens)
	}
	s := rollup(agg)
	if s.TotalReasoningTokens != 7 || s.TotalCacheReadTokens != 30 || s.TotalCacheWriteTokens != 3 {
		t.Errorf("rollup totals = %d/%d/%d, want 7/30/3", s.TotalReasoningTokens, s.TotalCacheReadTokens, s.TotalCacheWriteTokens)
	}
	if got := s.ProviderTotals["anthropic"]; got.ReasoningTokens != 7 || got.CacheReadTokens != 30 || got.CacheWriteTokens != 3 {
		t.Errorf("anthropic = %+v, want 7 reasoning, 30 cache-read, 3 cache-write tokens", got)
	}
}
