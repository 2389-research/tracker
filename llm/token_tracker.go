// ABOUTME: Middleware that accumulates per-provider token usage across LLM calls.
// ABOUTME: Thread-safe; used by the TUI dashboard header for real-time token counts.
package llm

import (
	"context"
	"sort"
	"sync"
)

// providerModel keys accumulated usage by the (provider, model) pair so a
// pipeline running two models of the same provider prices each model at its own
// rate. Model may be "" when a backend reports usage without a model (e.g. the
// claude-code subprocess under subscription auth) — such buckets fall back to
// the CostByProvider resolver for pricing.
type providerModel struct {
	provider string
	model    string
}

// TokenTracker is a middleware that accumulates token usage per (provider,
// model) pair. It implements Middleware and can be passed to NewClient via
// WithMiddleware.
type TokenTracker struct {
	mu sync.RWMutex
	// usage is keyed by (provider, model) so each model is priced with its own
	// rate; per-provider views (ProviderUsage, AllProviderUsage, CostByProvider)
	// aggregate across a provider's models.
	usage map[providerModel]Usage
	// lastModel records the most-recently-observed model per provider, purely
	// for back-compat accessors (ModelForProvider / ObservedModelResolver).
	lastModel map[string]string
}

// NewTokenTracker creates a new, zeroed token tracking middleware.
func NewTokenTracker() *TokenTracker {
	return &TokenTracker{
		usage:     make(map[providerModel]Usage),
		lastModel: make(map[string]string),
	}
}

// WrapComplete implements the Middleware interface.
// It calls the next handler and, on success, adds the response's token usage
// to the per-(provider, model) accumulator.
func (t *TokenTracker) WrapComplete(next CompleteHandler) CompleteHandler {
	return func(ctx context.Context, req *Request) (*Response, error) {
		resp, err := next(ctx, req)
		if err == nil && resp != nil {
			t.recordResponse(req, resp)
		}
		return resp, err
	}
}

// recordResponse resolves the provider and model for a completed response
// (preferring the response's own values, falling back to the request) and
// records its usage. AddUsage handles catalog normalization of versioned
// provider-returned model strings (e.g. "claude-sonnet-4-5-20250514").
func (t *TokenTracker) recordResponse(req *Request, resp *Response) {
	provider := resp.Provider
	if provider == "" {
		provider = req.Provider
	}
	model := resp.Model
	if model == "" {
		model = req.Model
	}
	t.AddUsage(provider, resp.Usage, model)
}

// AddUsage manually adds token usage for a provider. Used by backends that
// bypass the LLM client middleware (e.g., claude-code subprocess backend).
// The model parameter is optional; pass "" to leave the provider's model unchanged.
// Model strings are normalized through the catalog so versioned provider-returned
// IDs resolve to canonical pricing entries — matching WrapComplete's behavior.
func (t *TokenTracker) AddUsage(provider string, usage Usage, model ...string) {
	if provider == "" {
		return
	}
	var normalized string
	if len(model) > 0 && model[0] != "" {
		normalized = normalizeModelID(model[0])
	}
	t.mu.Lock()
	key := providerModel{provider: provider, model: normalized}
	t.usage[key] = t.usage[key].Add(usage)
	if normalized != "" {
		t.lastModel[provider] = normalized
	}
	t.mu.Unlock()
}

// ProviderUsage returns the accumulated usage for a specific provider, summed
// across every model that provider ran. Returns a zero Usage if the provider
// has not been seen.
func (t *TokenTracker) ProviderUsage(provider string) Usage {
	t.mu.RLock()
	defer t.mu.RUnlock()
	var total Usage
	for key, u := range t.usage {
		if key.provider == provider {
			total = total.Add(u)
		}
	}
	return total
}

// TotalUsage returns accumulated usage summed across all providers and models.
func (t *TokenTracker) TotalUsage() Usage {
	t.mu.RLock()
	defer t.mu.RUnlock()
	var total Usage
	for _, u := range t.usage {
		total = total.Add(u)
	}
	return total
}

// AllProviderUsage returns a copy of the accumulated usage aggregated per
// provider (summed across each provider's models).
func (t *TokenTracker) AllProviderUsage() map[string]Usage {
	t.mu.RLock()
	defer t.mu.RUnlock()
	result := make(map[string]Usage)
	for key, v := range t.usage {
		result[key.provider] = result[key.provider].Add(v)
	}
	return result
}

// ModelForProvider returns the last-seen model for a provider. Returns "" if
// unknown. NOTE: a provider that ran multiple models has multiple buckets; this
// reports only the most-recently-observed one and is retained for back-compat
// (fallback resolution / display). For accurate multi-model pricing use
// CostByProvider, which prices each model bucket at its own rate.
func (t *TokenTracker) ModelForProvider(provider string) string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.lastModel[provider]
}

// ObservedModelResolver returns a ModelResolver that uses the tracker's
// last-observed per-provider model, falling back to the provided fallback model
// for providers where no model was observed. Since CostByProvider now prices
// each (provider, model) bucket at its own rate, this resolver only supplies a
// price for buckets whose model is unknown ("" — e.g. subscription-auth
// backends). For a provider that ran multiple models it reports the last one.
func (t *TokenTracker) ObservedModelResolver(fallback string) ModelResolver {
	return func(provider string) string {
		if m := t.ModelForProvider(provider); m != "" {
			return m
		}
		return fallback
	}
}

// normalizeModelID maps a model string to its canonical catalog ID if found.
// Provider-returned model strings may include version suffixes (e.g.
// "claude-sonnet-4-5-20250514") that don't match the catalog. If the exact
// string resolves via GetModelInfo (which checks IDs and aliases), we return
// the canonical ID. Otherwise we return the input unchanged.
func normalizeModelID(model string) string {
	if model == "" {
		return ""
	}
	info := GetModelInfo(model)
	if info != nil {
		return info.ID
	}
	return model
}

// Providers returns a sorted, de-duplicated slice of provider names that have
// recorded usage (a provider that ran multiple models appears once).
func (t *TokenTracker) Providers() []string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	seen := make(map[string]struct{}, len(t.usage))
	for key := range t.usage {
		seen[key.provider] = struct{}{}
	}
	names := make([]string, 0, len(seen))
	for name := range seen {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
