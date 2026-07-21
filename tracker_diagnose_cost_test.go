// ABOUTME: Tests the cost-asymmetry detector — the #353 case-study shape trips it,
// ABOUTME: healthy runs (single provider, cached, small, cheap) never do.
package tracker

import (
	"strings"
	"testing"

	"github.com/2389-research/tracker/pipeline"
)

// caseStudyEntry mirrors the #353 run: three reviewers, one uncached backend
// ("unknown"/Codex) dominating cost, the others small and cached.
func caseStudyEntry() costLogEntry {
	return costLogEntry{
		Type:         string(pipeline.EventCostUpdated),
		TotalCostUSD: 22.90,
		ProviderTotals: map[string]pipeline.ProviderUsage{
			"unknown":   {CostUSD: 9.07, InputTokens: 2_614_932, CacheReadTokens: 0},
			"anthropic": {CostUSD: 1.56, InputTokens: 500_000, CacheReadTokens: 18_000_000},
			"gemini":    {CostUSD: 0.17, InputTokens: 40_000, CacheReadTokens: 0},
		},
	}
}

func TestAnalyzeCostDomination_DetectsCaseStudy(t *testing.T) {
	cd := analyzeCostDomination(caseStudyEntry())
	if cd == nil {
		t.Fatal("expected the uncached dominator to be detected")
	}
	if cd.Provider != "unknown" {
		t.Errorf("dominator = %q, want \"unknown\"", cd.Provider)
	}
	if cd.Share < 0.35 {
		t.Errorf("share = %.2f, want >= 0.35", cd.Share)
	}
}

func TestAnalyzeCostDomination_HealthyRunsDoNotTrip(t *testing.T) {
	cases := []struct {
		name  string
		entry costLogEntry
	}{
		{"single provider (no fan-out)", costLogEntry{
			TotalCostUSD:   10,
			ProviderTotals: map[string]pipeline.ProviderUsage{"anthropic": {CostUSD: 10, InputTokens: 3_000_000, CacheReadTokens: 0}},
		}},
		{"dominator is cached", costLogEntry{
			TotalCostUSD: 10,
			ProviderTotals: map[string]pipeline.ProviderUsage{
				"anthropic": {CostUSD: 8, InputTokens: 3_000_000, CacheReadTokens: 5_000_000},
				"gemini":    {CostUSD: 2, InputTokens: 100_000},
			},
		}},
		{"trivial total cost", costLogEntry{
			TotalCostUSD: 0.50,
			ProviderTotals: map[string]pipeline.ProviderUsage{
				"unknown": {CostUSD: 0.40, InputTokens: 300_000, CacheReadTokens: 0},
				"gemini":  {CostUSD: 0.10, InputTokens: 5_000},
			},
		}},
		{"dominator input volume small", costLogEntry{
			TotalCostUSD: 5,
			ProviderTotals: map[string]pipeline.ProviderUsage{
				"unknown": {CostUSD: 4, InputTokens: 50_000, CacheReadTokens: 0},
				"gemini":  {CostUSD: 1, InputTokens: 10_000},
			},
		}},
		{"balanced fan-out", costLogEntry{
			TotalCostUSD: 6,
			ProviderTotals: map[string]pipeline.ProviderUsage{
				"a": {CostUSD: 2, InputTokens: 300_000, CacheReadTokens: 0},
				"b": {CostUSD: 2, InputTokens: 300_000, CacheReadTokens: 0},
				"c": {CostUSD: 2, InputTokens: 300_000, CacheReadTokens: 0},
			},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if cd := analyzeCostDomination(tc.entry); cd != nil {
				t.Errorf("healthy run tripped the detector: %+v", cd)
			}
		})
	}
}

func TestCostAsymmetrySuggestions(t *testing.T) {
	if s := costAsymmetrySuggestions(nil); s != nil {
		t.Errorf("nil dominator should produce no suggestion, got %v", s)
	}
	got := costAsymmetrySuggestions(analyzeCostDomination(caseStudyEntry()))
	if len(got) != 1 || got[0].Kind != SuggestionCostAsymmetry {
		t.Fatalf("expected one cost_asymmetry suggestion, got %+v", got)
	}
	for _, want := range []string{"unknown", "max_cost_usd", "uncached"} {
		if !strings.Contains(got[0].Message, want) {
			t.Errorf("suggestion missing %q:\n%s", want, got[0].Message)
		}
	}
}
