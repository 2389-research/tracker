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

// Cache multipliers used when a catalog entry states none.
//
// Reads default to 0.1x — the published Anthropic rate, which the GPT-5 family
// also matches, and a sane floor for any model that omits a read override.
//
// Writes default to 0 (no premium) because a per-token cache-write premium is
// an Anthropic billing concept: OpenAI prompt-cache writes are free and Gemini
// charges time-based storage, not a per-token write. Models that DO charge a
// write premium carry an explicit CacheWriteMultiplier in the catalog — the
// Anthropic models set 1.25x (the published 5-minute rate). 1-hour Anthropic
// writes are 2x, which this cannot express because llm.Usage carries no TTL —
// a 1-hour-TTL run prices ~38% low, and threading TTL through the adapters is
// the fix if that matters.
const (
	defaultCacheReadMultiplier  = 0.1
	defaultCacheWriteMultiplier = 0
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
	cost, _ := EstimateCostChecked(model, usage)
	return cost
}

// EstimateCostChecked is EstimateCost plus the bit callers need to tell a
// genuinely-free run apart from an uncatalogued one: priced reports whether the
// cost was actually computed from a catalog entry (true) or defaulted to $0
// because the model is unknown (false). A run that costs $0 on a catalogued
// local model (Ollama via openaicompat) returns (0, true); a misspelled or
// uncatalogued model returns (0, false) so the "unpriced" signal can propagate
// into RunTotals and warn that a --max-cost ceiling could not bound this usage.
// The plain EstimateCost signature is kept for its many callers.
func EstimateCostChecked(model string, usage Usage) (float64, bool) {
	info := GetModelInfo(model)
	if info == nil {
		warnUnknownModel(model, usage)
		return 0, false
	}
	input := float64(usage.InputTokens) / 1_000_000 * info.InputCostPerM
	output := float64(usage.OutputTokens) / 1_000_000 * info.OutputCostPerM
	return input + output + cacheCost(info, usage), true
}

// IsPriced reports whether model has a catalog entry (by ID or alias) and so can
// be priced. False for an unknown or empty model name — an empty name is the
// subscription-auth backends' "no model set" case, which is not something a
// --max-cost ceiling can bound either. Cheap enough to call per usage line.
func IsPriced(model string) bool {
	return GetModelInfo(model) != nil
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

// warnUnknownModel emits a single diagnostic per unknown model name, and only
// when there is actually something to price — a zero-usage call (e.g. a probe)
// shouldn't produce a log line. Extracted from EstimateCost to keep that hot
// path's cognitive complexity within the ratchet.
func warnUnknownModel(model string, usage Usage) {
	if !usage.anyTokens() {
		return
	}
	if _, already := unknownModelWarned.LoadOrStore(model, struct{}{}); already {
		return
	}
	diag.Warnf("[llm] EstimateCost: unknown model %q (no catalog entry); returning $0 — budget --max-cost ceiling will not apply to usage priced under this model", model)
}
