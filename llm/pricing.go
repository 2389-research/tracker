// ABOUTME: Cost estimation for LLM usage based on the model catalog.
// ABOUTME: Prices input, output, and cache tokens using per-model rates from the catalog.
package llm

import (
	"log"
	"sync"
)

// unknownModelWarned tracks model names we've already warned about so a hot
// path that repeatedly calls EstimateCost with an unknown model doesn't spam
// the log. Empty-string keys represent the "no model set" case, which is
// common for subscription-auth backends (claude-code, ACP bridges).
var unknownModelWarned sync.Map

// Cache multipliers used when a catalog entry states none. Both are the
// published Anthropic rates, which the GPT-5 family also matches on reads.
//
// The write default is a *premium*, not a discount: a 5-minute cache write is
// 1.25x base input. 1-hour writes are 2x, which this cannot express because
// llm.Usage carries no TTL — a 1-hour-TTL run prices ~38% low, and threading
// TTL through the adapters is the fix if that matters. The previous value here
// was 0.25, reading "+25% premium" as "0.25x rate", which understated the
// cache-write cost of every cached Anthropic run by 5x.
const (
	defaultCacheReadMultiplier  = 0.1
	defaultCacheWriteMultiplier = 1.25
)

// EstimateCost returns the estimated dollar cost for the given model and token
// usage. Looks up pricing from the model catalog (supports ID and aliases).
// Returns 0 for unknown models, logging a single warning per unknown name so
// operators notice that their --max-cost ceiling will not apply to usage
// priced at an unknown rate.
//
// Cached prompt tokens are priced from the model's own multipliers, since the
// discount varies by model rather than by provider — see ModelInfo. Assumes
// InputTokens excludes the cache buckets, the llm.Usage invariant every adapter
// normalizes to.
func EstimateCost(model string, usage Usage) float64 {
	info := GetModelInfo(model)
	if info == nil {
		warnUnknownModel(model, usage)
		return 0
	}
	input := float64(usage.InputTokens) / 1_000_000 * info.InputCostPerM
	output := float64(usage.OutputTokens) / 1_000_000 * info.OutputCostPerM
	return input + output + cacheCost(info, usage)
}

// cacheCost prices the cache buckets, falling back to the package defaults for
// a model that states no multiplier of its own.
func cacheCost(info *ModelInfo, usage Usage) float64 {
	perM := func(tokens *int, mult, fallback float64) float64 {
		if tokens == nil {
			return 0
		}
		if mult == 0 {
			mult = fallback
		}
		return float64(*tokens) / 1_000_000 * info.InputCostPerM * mult
	}
	return perM(usage.CacheReadTokens, info.CacheReadMultiplier, defaultCacheReadMultiplier) +
		perM(usage.CacheWriteTokens, info.CacheWriteMultiplier, defaultCacheWriteMultiplier)
}

// warnUnknownModel logs once per unrecognized model name. Only warns when there
// is actually something to price — a zero-usage call (e.g. a probe) shouldn't
// produce a log line.
func warnUnknownModel(model string, usage Usage) {
	if !usage.anyTokens() {
		return
	}
	if _, already := unknownModelWarned.LoadOrStore(model, struct{}{}); already {
		return
	}
	log.Printf("[llm] EstimateCost: unknown model %q (no catalog entry); returning $0 — budget --max-cost ceiling will not apply to usage priced under this model", model)
}
