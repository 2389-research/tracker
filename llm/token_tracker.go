// ABOUTME: Middleware that accumulates per-provider token usage across LLM calls.
// ABOUTME: Thread-safe; used by the TUI dashboard header for real-time token counts.
package llm

import (
	"context"
	"sort"
	"sync"
)

// TokenTracker is a middleware that accumulates token usage per provider.
// It implements Middleware and can be passed to NewClient via WithMiddleware.
type TokenTracker struct {
	mu     sync.RWMutex
	usage  map[string]Usage
	models map[string]string // last-seen model per provider
}

// NewTokenTracker creates a new, zeroed token tracking middleware.
func NewTokenTracker() *TokenTracker {
	return &TokenTracker{
		usage:  make(map[string]Usage),
		models: make(map[string]string),
	}
}

// WrapComplete implements the Middleware interface.
// It calls the next handler and, on success, adds the response's token usage
// to the per-provider accumulator.
func (t *TokenTracker) WrapComplete(next CompleteHandler) CompleteHandler {
	return func(ctx context.Context, req *Request) (*Response, error) {
		resp, err := next(ctx, req)
		if err != nil || resp == nil {
			return resp, err
		}

		provider := resp.Provider
		if provider == "" {
			provider = req.Provider
		}
		if provider == "" {
			return resp, nil
		}

		model := resp.Model
		if model == "" {
			model = req.Model
		}
		// Normalize to a canonical catalog ID so versioned provider-returned
		// model strings (e.g. "claude-sonnet-4-5-20250514") resolve to a
		// known pricing entry. If the model is unknown, keep it as-is so
		// the fallback resolver can still try.
		model = normalizeModelID(model)

		t.mu.Lock()
		existing := t.usage[provider]
		t.usage[provider] = existing.Add(resp.Usage)
		if model != "" {
			t.models[provider] = model
		}
		t.mu.Unlock()

		return resp, nil
	}
}

// AddUsage manually adds token usage for a provider. Used by backends that
// bypass the LLM client middleware (e.g., claude-code subprocess backend).
// The model parameter is optional; pass "" to leave the provider's model unchanged.
func (t *TokenTracker) AddUsage(provider string, usage Usage, model ...string) {
	if provider == "" {
		return
	}
	t.mu.Lock()
	existing := t.usage[provider]
	t.usage[provider] = existing.Add(usage)
	if len(model) > 0 && model[0] != "" {
		t.models[provider] = model[0]
	}
	t.mu.Unlock()
}

// ProviderUsage returns the accumulated usage for a specific provider.
// Returns a zero Usage if the provider has not been seen.
func (t *TokenTracker) ProviderUsage(provider string) Usage {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.usage[provider]
}

// TotalUsage returns accumulated usage summed across all providers.
func (t *TokenTracker) TotalUsage() Usage {
	t.mu.RLock()
	defer t.mu.RUnlock()
	var total Usage
	for _, u := range t.usage {
		total = total.Add(u)
	}
	return total
}

// AllProviderUsage returns a copy of the accumulated usage for all providers.
func (t *TokenTracker) AllProviderUsage() map[string]Usage {
	t.mu.RLock()
	defer t.mu.RUnlock()
	result := make(map[string]Usage, len(t.usage))
	for k, v := range t.usage {
		result[k] = v
	}
	return result
}

// ModelForProvider returns the last-seen model for a provider. Returns "" if unknown.
func (t *TokenTracker) ModelForProvider(provider string) string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.models[provider]
}

// ObservedModelResolver returns a ModelResolver that uses the tracker's observed
// per-provider models. Falls back to the provided fallback model for providers
// where no model was observed.
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

// Providers returns a sorted slice of provider names that have recorded usage.
func (t *TokenTracker) Providers() []string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	names := make([]string, 0, len(t.usage))
	for name := range t.usage {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
