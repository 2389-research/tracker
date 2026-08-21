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

// IsDeprecated reports whether model is retired on its first-party provider API
// (per dippin's ModelPrice.Deprecated) — it 404s on the first-party endpoint but
// is still billable through a passthrough platform like Bedrock or Vertex, so it
// remains in the catalog. A tracker consumer that treats the catalog as a
// first-party allowlist (e.g. `tracker doctor`) can warn on these unless a
// gateway/base-URL is routing the model to a passthrough platform. False for an
// unknown model.
func IsDeprecated(model string) bool {
	p, ok := pricing.Lookup(model)
	return ok && p.Deprecated
}

// ModelContextWindow returns dippin's per-model context window in tokens for the
// given provider/model, or 0 when dippin doesn't know it (dippin never guesses a
// window, so an absent value is genuinely unknown, not a real 0-token limit).
// A provider-scoped lookup wins when provider is non-empty; otherwise a bare
// model lookup is used. An un-pinned family@selector alias (e.g. "opus@latest")
// is resolved to a concrete id first so the window comes from the real model.
func ModelContextWindow(provider, model string) int {
	if concrete, prov, resolved, isAlias := pricing.ResolveModelRef(provider, model); isAlias {
		if !resolved {
			return 0
		}
		model, provider = concrete, prov
	}
	p, ok := lookupModelPrice(provider, model)
	if !ok || p.ContextWindow <= 0 {
		return 0
	}
	return p.ContextWindow
}

// lookupModelPrice resolves a dippin pricing entry provider-first, falling back
// to a bare model lookup when provider is empty.
func lookupModelPrice(provider, model string) (pricing.ModelPrice, bool) {
	if provider != "" {
		return pricing.LookupProvider(provider, model)
	}
	return pricing.Lookup(model)
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

// overlayCacheMultipliers fills cache pricing ONLY for models dippin doesn't
// price cache for. dippin's prices.json now carries verified cache rates for the
// vast majority of models, so this overlay SELF-DISABLES (the guard below) the
// moment dippin has any cache rate — cache prices then come straight from dippin,
// no drift. The exact set it still fills is dippin's own `pricing.CacheGaps()`
// (the priced models with neither a CachedInputPerM nor a CacheReadMult); a dippin
// test keeps that set shrinking, and TestCacheOverlayScopeMatchesDippinGaps here
// asserts tracker never guesses a rate for a model dippin actually prices. For a
// gap model, this falls back to a per-model catalog override if present, else
// tracker's default (0.1x read). Retire this entirely once CacheGaps() is empty.
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
