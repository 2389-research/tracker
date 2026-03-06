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
	mu    sync.RWMutex
	usage map[string]Usage
}

// NewTokenTracker creates a new, zeroed token tracking middleware.
func NewTokenTracker() *TokenTracker {
	return &TokenTracker{usage: make(map[string]Usage)}
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

		t.mu.Lock()
		existing := t.usage[provider]
		t.usage[provider] = existing.Add(resp.Usage)
		t.mu.Unlock()

		return resp, nil
	}
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
