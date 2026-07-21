// ABOUTME: Diagnose detector for cost asymmetry — one backend with no prompt
// ABOUTME: caching dominating a run's cost, unnoticed until the bill (#353).
package tracker

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/2389-research/tracker/pipeline"
)

// SuggestionCostAsymmetry fires when one provider dominated a run's cost with no
// prompt caching — the failure mode where a slow, uncached backend in a fan-out
// silently drives most of the bill (#353). Actionable: cap it with per-node
// max_cost_usd, route the lane cheaper, or reshape a fan-out to escalate-on-fail.
const SuggestionCostAsymmetry SuggestionKind = "cost_asymmetry"

// Detection thresholds. Deliberately conservative so healthy runs never trip:
// the run must be non-trivial, the shape must be a fan-out (≥2 paying
// providers), and the dominator must be both a large uncached input load and a
// large share of the bill.
const (
	costDomMinTotalUSD    = 1.0
	costDomMinShare       = 0.35
	costDomMinInputTokens = 200_000
	costDomMinPayingProvs = 2
)

// costDomination describes a provider that dominated a run's cost without caching.
type costDomination struct {
	Provider     string
	CostUSD      float64
	TotalCostUSD float64
	Share        float64
	InputTokens  int
}

// costLogEntry is the slice of an activity-log line the cost scan needs.
type costLogEntry struct {
	Type           string                            `json:"type"`
	TotalCostUSD   float64                           `json:"total_cost_usd"`
	ProviderTotals map[string]pipeline.ProviderUsage `json:"provider_totals"`
}

// scanCostDomination reads the activity log for the final cumulative cost
// snapshot and reports a no-cache cost dominator, or nil. It re-reads the log
// (a one-shot diagnose cost, not a hot path) so the detection stays isolated
// from the main enrichment scan and its complexity budget.
func scanCostDomination(ctx context.Context, runDir string) *costDomination {
	path, _ := ResolveActivityLogPath(runDir)
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	peak, ok := scanPeakCost(ctx, f)
	if !ok {
		return nil // incomplete scan — don't risk a false positive on partial totals
	}
	return analyzeCostDomination(peak)
}

// scanPeakCost returns the cost_updated entry with the largest total (the run's
// final cumulative spend), and false if the scan was interrupted or errored.
func scanPeakCost(ctx context.Context, f io.Reader) (costLogEntry, bool) {
	var peak costLogEntry
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		if ctx != nil && ctx.Err() != nil {
			return peak, false
		}
		peak = higherCost(peak, scanner.Text())
	}
	return peak, scanner.Err() == nil
}

// higherCost returns whichever of peak / the parsed line has the larger total.
// Cost events are cumulative, so the largest total is the final spend regardless
// of line ordering; non-cost or unparseable lines leave peak unchanged.
func higherCost(peak costLogEntry, raw string) costLogEntry {
	body, _ := stripActivitySentinel(raw)
	line := strings.TrimSpace(body)
	if line == "" {
		return peak
	}
	var e costLogEntry
	if json.Unmarshal([]byte(line), &e) != nil {
		return peak
	}
	if e.Type == string(pipeline.EventCostUpdated) && e.TotalCostUSD > peak.TotalCostUSD {
		return e
	}
	return peak
}

// analyzeCostDomination returns a dominator if the final snapshot meets every
// threshold, else nil.
func analyzeCostDomination(e costLogEntry) *costDomination {
	if e.TotalCostUSD < costDomMinTotalUSD || len(e.ProviderTotals) < costDomMinPayingProvs {
		return nil
	}
	topName, top, paying := topProviderByCost(e.ProviderTotals)
	if paying < costDomMinPayingProvs {
		return nil
	}
	share := top.CostUSD / e.TotalCostUSD
	if !isNoCacheDominator(top, share) {
		return nil
	}
	return &costDomination{
		Provider:     topName,
		CostUSD:      top.CostUSD,
		TotalCostUSD: e.TotalCostUSD,
		Share:        share,
		InputTokens:  top.InputTokens,
	}
}

// topProviderByCost returns the most expensive provider, its usage, and how many
// providers had non-zero cost.
func topProviderByCost(totals map[string]pipeline.ProviderUsage) (name string, top pipeline.ProviderUsage, paying int) {
	for n, pu := range totals {
		if pu.CostUSD > 0 {
			paying++
		}
		if pu.CostUSD > top.CostUSD {
			name, top = n, pu
		}
	}
	return name, top, paying
}

// isNoCacheDominator reports whether a provider is a large-share, large-volume,
// zero-cache cost sink — the #353 signature.
func isNoCacheDominator(pu pipeline.ProviderUsage, share float64) bool {
	return share >= costDomMinShare &&
		pu.CacheReadTokens == 0 &&
		pu.InputTokens >= costDomMinInputTokens
}

// costAsymmetrySuggestions renders the dominator (if any) as a Suggestion.
func costAsymmetrySuggestions(cd *costDomination) []Suggestion {
	if cd == nil {
		return nil
	}
	return []Suggestion{{
		Kind: SuggestionCostAsymmetry,
		Message: fmt.Sprintf(
			"cost asymmetry: provider %q spent $%.2f (%.0f%% of the run's $%.2f) on %d uncached input tokens — one backend with no prompt caching dominated the run. Cap it with a per-node `max_cost_usd:` on the heavy node(s) (route on `ctx.node_cost_exceeded`), send that lane to a cheaper/smaller scope, or — for a review fan-out — reshape to a single-reviewer fast path that escalates the full panel only on failure.",
			cd.Provider, cd.CostUSD, cd.Share*100, cd.TotalCostUSD, cd.InputTokens),
	}}
}
