// ABOUTME: Cost estimation for LLM usage — base prices come from dippin-lang/pricing
// ABOUTME: (the single source of truth, #558); tracker overlays cache rates until dippin ships them.
package llm

import (
	"sync"

	"github.com/2389-research/dippin-lang/pricing"
	"github.com/2389-research/tracker/internal/diag"
)

// unknownModelWarned tracks model names we've already warned about so a hot
// path that repeatedly calls EstimateCost with an unknown model doesn't spam
// the log. Empty-string keys represent the "no model set" case, which is
// common for subscription-auth backends (claude-code, ACP bridges).
var unknownModelWarned sync.Map

// Cache multipliers used when neither the dippin pricing entry nor the catalog
// states one.
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
	defaultCacheReadMultiplier  float64 = 0.1
	defaultCacheWriteMultiplier float64 = 0
)

// EstimateCost returns the estimated dollar cost for the given model and token
// usage. Base input/output prices come from dippin-lang/pricing (the single
// source of truth, #558); returns 0 for models it doesn't price, logging a
// single warning per unknown name so operators notice their --max-cost ceiling
// won't apply to usage priced under an unknown rate.
func EstimateCost(model string, usage Usage) float64 {
	cost, _ := EstimateCostChecked(model, usage)
	return cost
}

// EstimateCostChecked is EstimateCost plus the bit callers need to tell a
// genuinely-free run apart from an uncatalogued one: priced reports whether the
// cost was computed from a pricing entry (true) or defaulted to $0 because the
// model is unknown (false). A found-but-unpriced dippin entry (Priced=false,
// e.g. Qwen / a free tier) still returns (…, true) — it is priced, at $0.
func EstimateCostChecked(model string, usage Usage) (float64, bool) {
	p, ok := pricing.Lookup(model)
	if !ok {
		warnUnknownModel(model, usage)
		return 0, false
	}
	overlayCacheMultipliers(&p, model)
	return pricing.Cost(toPricingUsage(usage), p), true
}

// IsPriced reports whether model has a pricing entry (by ID, alias, or the
// version-separator fold) and so can be priced. False for an unknown or empty
// model name — an empty name is the subscription-auth backends' "no model set"
// case, which a --max-cost ceiling cannot bound either.
func IsPriced(model string) bool {
	_, ok := pricing.Lookup(model)
	return ok
}

// toPricingUsage maps llm.Usage to dippin's pricing.Usage.
//
// Reasoning is deliberately left 0: llm.Usage's OutputTokens ALREADY INCLUDES
// reasoning tokens (see the Usage doc in types.go), and pricing.Cost bills its
// Reasoning field as additional output — passing ReasoningTokens here would
// double-count it.
func toPricingUsage(u Usage) pricing.Usage {
	return pricing.Usage{
		Input:      u.InputTokens,
		Output:     u.OutputTokens,
		CacheRead:  derefInt(u.CacheReadTokens),
		CacheWrite: derefInt(u.CacheWriteTokens),
	}
}

// overlayCacheMultipliers injects tracker's retained per-model cache multipliers
// when the dippin pricing entry carries none. dippin's prices.json does not yet
// populate cache rates (#558 caveat), so without this cache traffic would price
// at $0; once dippin ships cache rates (a non-zero read mult or absolute cached
// rate) the overlay is skipped and dippin's numbers win.
func overlayCacheMultipliers(p *pricing.ModelPrice, model string) {
	if p.CachedInputPerM > 0 || p.CacheReadMult > 0 {
		return
	}
	readMult, writeMult := defaultCacheReadMultiplier, defaultCacheWriteMultiplier
	if info := GetModelInfo(model); info != nil {
		if info.CacheReadMultiplier != 0 {
			readMult = info.CacheReadMultiplier
		}
		if info.CacheWriteMultiplier != 0 {
			writeMult = info.CacheWriteMultiplier
		}
	}
	p.CacheReadMult = readMult
	p.CacheWriteMult = writeMult
}

// derefInt returns the pointed-to value, or 0 for a nil optional token count.
func derefInt(v *int) int {
	if v == nil {
		return 0
	}
	return *v
}

// warnUnknownModel emits a single diagnostic per unknown model name, and only
// when there is actually something to price — a zero-usage call (e.g. a probe)
// shouldn't produce a log line.
func warnUnknownModel(model string, usage Usage) {
	if !usage.anyTokens() {
		return
	}
	if _, already := unknownModelWarned.LoadOrStore(model, struct{}{}); already {
		return
	}
	diag.Warnf("[llm] EstimateCost: unknown model %q (no pricing entry); returning $0 — budget --max-cost ceiling will not apply to usage priced under this model", model)
}
