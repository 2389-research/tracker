// ABOUTME: Tests that the usage rollup files a session served by more than one provider under each
// ABOUTME: provider by its SessionStats.ProviderUsage split (#702), and a one-provider session as before.
package pipeline

import "testing"

func TestAggregateUsage_SplitsASessionServedByTwoProviders(t *testing.T) {
	tr := &Trace{Entries: []TraceEntry{{NodeID: "Build", Stats: &SessionStats{
		InputTokens: 150, OutputTokens: 15, TotalTokens: 165, CostUSD: 1.5,
		Provider: "anthropic",
		ProviderUsage: map[string]ProviderUsage{
			"anthropic":           {InputTokens: 100, OutputTokens: 10, TotalTokens: 110, CostUSD: 1.5, SessionCount: 1},
			"openai-subscription": {InputTokens: 50, OutputTokens: 5, TotalTokens: 55, SessionCount: 1},
		},
	}}}}

	s := tr.AggregateUsage()

	if s.TotalTokens != 165 || s.TotalCostUSD != 1.5 || s.SessionCount != 1 {
		t.Errorf("totals = %d tokens, $%v, %d sessions; want 165, $1.5, 1", s.TotalTokens, s.TotalCostUSD, s.SessionCount)
	}
	if got := s.ProviderTotals["anthropic"]; got.TotalTokens != 110 || got.CostUSD != 1.5 || got.SessionCount != 1 {
		t.Errorf("anthropic = %+v, want 110 tokens, $1.5, 1 session", got)
	}
	if got := s.ProviderTotals["openai-subscription"]; got.TotalTokens != 55 || got.CostUSD != 0 || got.SessionCount != 1 {
		t.Errorf("openai-subscription = %+v, want 55 tokens, $0, 1 session", got)
	}
}

func TestAggregateUsage_SessionWithoutSplitBucketsUnderItsProvider(t *testing.T) {
	tr := &Trace{Entries: []TraceEntry{{NodeID: "Build", Stats: &SessionStats{
		InputTokens: 100, OutputTokens: 10, TotalTokens: 110, CostUSD: 1, Provider: "anthropic", Estimated: true,
	}}}}

	got := tr.AggregateUsage().ProviderTotals["anthropic"]

	if got.TotalTokens != 110 || got.CostUSD != 1 || got.SessionCount != 1 || !got.Estimated {
		t.Errorf("anthropic = %+v, want 110 tokens, $1, 1 session, estimated", got)
	}
}

func TestAggregateUsage_CountsEverySessionAParallelNodeSums(t *testing.T) {
	tr := &Trace{Entries: []TraceEntry{{NodeID: "Fork", Stats: &SessionStats{
		InputTokens: 30, TotalTokens: 30, CostUSD: 3, Provider: "anthropic", SessionCount: 2,
	}}}}

	s := tr.AggregateUsage()

	if s.SessionCount != 2 {
		t.Errorf("SessionCount = %d, want 2: the parallel node sums two branch sessions", s.SessionCount)
	}
	if got := s.ProviderTotals["anthropic"]; got.TotalTokens != 30 || got.CostUSD != 3 || got.SessionCount != 2 {
		t.Errorf("anthropic = %+v, want 30 tokens, $3, 2 sessions", got)
	}
}
