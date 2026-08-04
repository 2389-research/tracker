// ABOUTME: Token/cost table helpers for the run summary — the per-provider rows
// ABOUTME: and trailing cost line, extracted from printTokensByProvider (complexity gate).
package main

import (
	"fmt"

	"github.com/2389-research/tracker/pipeline"
)

// printProviderUsageRows prints one input/output token row per provider,
// tagging estimated rows. Extracted from printTokensByProvider (complexity gate).
func printProviderUsageRows(providers []string, usage *pipeline.UsageSummary) {
	for _, p := range providers {
		u := usage.ProviderTotals[p]
		label := p
		if u.Estimated {
			label = p + " (estimated)"
		}
		fmt.Printf("  %-20s  %10s  %10s\n", label, formatNumber(u.InputTokens), formatNumber(u.OutputTokens))
	}
}

// printUsageCostSummary prints the trailing cost line, distinguishing the
// claude-code Max-subscription case and estimated spend. Extracted from
// printTokensByProvider (complexity gate).
func printUsageCostSummary(usage *pipeline.UsageSummary, providers []string) {
	if usage.TotalCostUSD <= 0 {
		return
	}
	switch {
	case len(providers) == 1 && providers[0] == "claude-code":
		fmt.Printf("  Est. usage: ~$%.4f (Max subscription — no actual charge)\n", usage.TotalCostUSD)
	case usage.Estimated:
		fmt.Printf("  Cost: ~$%.4f  (estimated — heuristic spend on at least one provider)\n", usage.TotalCostUSD)
	default:
		fmt.Printf("  Cost: $%.4f\n", usage.TotalCostUSD)
	}
}
