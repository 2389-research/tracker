// ABOUTME: Cost estimation for LLM usage based on the model catalog.
// ABOUTME: Prices input, output, and cache tokens using per-model rates from the catalog.
package llm

import (
	"sync"

	"github.com/2389-research/tracker/internal/diag"
)

// unknownModelWarned tracks model names we've already warned about so a hot
// path that repeatedly calls EstimateCost with an unknown model doesn't spam
// the log. Empty-string keys represent the "no model set" case, which is
// common for subscription-auth backends (claude-code, ACP bridges).
var unknownModelWarned sync.Map

// EstimateCost returns the estimated dollar cost for the given model and token
// usage. Looks up pricing from the model catalog (supports ID and aliases).
// Returns 0 for unknown models, logging a single warning per unknown name so
// operators notice that their --max-cost ceiling will not apply to usage
// priced at an unknown rate. Cache read tokens are priced at 10% of input
// rate; cache write tokens at 25% of input rate (Anthropic pricing
// convention, applied uniformly since other providers use similar discounts).
func EstimateCost(model string, usage Usage) float64 {
	info := GetModelInfo(model)
	if info == nil {
		warnUnknownModel(model, usage)
		return 0
	}
	input := float64(usage.InputTokens) / 1_000_000 * info.InputCostPerM
	output := float64(usage.OutputTokens) / 1_000_000 * info.OutputCostPerM

	var cacheRead, cacheWrite float64
	if usage.CacheReadTokens != nil {
		cacheRead = float64(*usage.CacheReadTokens) / 1_000_000 * info.InputCostPerM * 0.1
	}
	if usage.CacheWriteTokens != nil {
		cacheWrite = float64(*usage.CacheWriteTokens) / 1_000_000 * info.InputCostPerM * 0.25
	}

	return input + output + cacheRead + cacheWrite
}

// warnUnknownModel emits a single diagnostic per unknown model name, and only
// when there is actually something to price — a zero-usage call (e.g. a probe)
// shouldn't produce a log line. Extracted from EstimateCost to keep that hot
// path's cognitive complexity within the ratchet.
func warnUnknownModel(model string, usage Usage) {
	if usage.TotalTokens == 0 && usage.InputTokens == 0 && usage.OutputTokens == 0 {
		return
	}
	if _, already := unknownModelWarned.LoadOrStore(model, struct{}{}); already {
		return
	}
	diag.Warnf("[llm] EstimateCost: unknown model %q (no catalog entry); returning $0 — budget --max-cost ceiling will not apply to usage priced under this model", model)
}
