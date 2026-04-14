// ABOUTME: Tests for the TokenTracker LLM middleware.
package llm

import (
	"context"
	"sync"
	"testing"
)

func TestTokenTrackerAccumulatesUsage(t *testing.T) {
	tracker := NewTokenTracker()

	handler := tracker.WrapComplete(func(ctx context.Context, req *Request) (*Response, error) {
		return &Response{
			Provider: "anthropic",
			Usage:    Usage{InputTokens: 100, OutputTokens: 50},
		}, nil
	})

	_, err := handler(context.Background(), &Request{Provider: "anthropic"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_, err = handler(context.Background(), &Request{Provider: "anthropic"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	usage := tracker.ProviderUsage("anthropic")
	if usage.InputTokens != 200 {
		t.Errorf("expected 200 input tokens, got %d", usage.InputTokens)
	}
	if usage.OutputTokens != 100 {
		t.Errorf("expected 100 output tokens, got %d", usage.OutputTokens)
	}
}

func TestTokenTrackerMultipleProviders(t *testing.T) {
	tracker := NewTokenTracker()

	for _, provider := range []string{"anthropic", "openai", "gemini"} {
		p := provider // capture
		handler := tracker.WrapComplete(func(ctx context.Context, req *Request) (*Response, error) {
			return &Response{
				Provider: p,
				Usage:    Usage{InputTokens: 10, OutputTokens: 5},
			}, nil
		})
		_, _ = handler(context.Background(), &Request{Provider: p})
	}

	providers := tracker.Providers()
	if len(providers) != 3 {
		t.Errorf("expected 3 providers, got %d", len(providers))
	}
}

func TestTokenTrackerConcurrentSafe(t *testing.T) {
	tracker := NewTokenTracker()
	handler := tracker.WrapComplete(func(ctx context.Context, req *Request) (*Response, error) {
		return &Response{
			Provider: "anthropic",
			Usage:    Usage{InputTokens: 1, OutputTokens: 1},
		}, nil
	})

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = handler(context.Background(), &Request{Provider: "anthropic"})
		}()
	}
	wg.Wait()

	usage := tracker.ProviderUsage("anthropic")
	if usage.InputTokens != 100 {
		t.Errorf("expected 100 input tokens, got %d", usage.InputTokens)
	}
}

func TestTokenTrackerTotalUsage(t *testing.T) {
	tracker := NewTokenTracker()

	for _, p := range []string{"anthropic", "openai"} {
		prov := p
		handler := tracker.WrapComplete(func(ctx context.Context, req *Request) (*Response, error) {
			return &Response{Provider: prov, Usage: Usage{InputTokens: 50, OutputTokens: 25}}, nil
		})
		_, _ = handler(context.Background(), &Request{Provider: prov})
	}

	total := tracker.TotalUsage()
	if total.InputTokens != 100 {
		t.Errorf("expected 100, got %d", total.InputTokens)
	}
	if total.OutputTokens != 50 {
		t.Errorf("expected 50, got %d", total.OutputTokens)
	}
}

func TestTokenTrackerZeroForUnknownProvider(t *testing.T) {
	tracker := NewTokenTracker()
	usage := tracker.ProviderUsage("unknown")
	if usage.InputTokens != 0 || usage.OutputTokens != 0 {
		t.Errorf("expected zero usage for unknown provider, got %+v", usage)
	}
}

func TestTokenTrackerFallsBackToRequestProvider(t *testing.T) {
	tracker := NewTokenTracker()
	handler := tracker.WrapComplete(func(ctx context.Context, req *Request) (*Response, error) {
		// Response has no Provider set — should use req.Provider
		return &Response{Usage: Usage{InputTokens: 20}}, nil
	})
	_, _ = handler(context.Background(), &Request{Provider: "openai"})
	usage := tracker.ProviderUsage("openai")
	if usage.InputTokens != 20 {
		t.Errorf("expected 20 from fallback, got %d", usage.InputTokens)
	}
}

func TestTokenTrackerAllProviderUsage(t *testing.T) {
	tracker := NewTokenTracker()

	for _, tc := range []struct {
		provider string
		input    int
		output   int
	}{
		{"anthropic", 100, 50},
		{"openai", 200, 80},
		{"gemini", 150, 60},
	} {
		p := tc.provider
		in := tc.input
		out := tc.output
		handler := tracker.WrapComplete(func(ctx context.Context, req *Request) (*Response, error) {
			return &Response{Provider: p, Usage: Usage{InputTokens: in, OutputTokens: out}}, nil
		})
		_, _ = handler(context.Background(), &Request{Provider: p})
	}

	all := tracker.AllProviderUsage()
	if len(all) != 3 {
		t.Fatalf("expected 3 providers, got %d", len(all))
	}
	if all["anthropic"].InputTokens != 100 {
		t.Errorf("anthropic InputTokens = %d, want 100", all["anthropic"].InputTokens)
	}
	if all["openai"].OutputTokens != 80 {
		t.Errorf("openai OutputTokens = %d, want 80", all["openai"].OutputTokens)
	}
	if all["gemini"].InputTokens != 150 {
		t.Errorf("gemini InputTokens = %d, want 150", all["gemini"].InputTokens)
	}

	// Verify returned map is a copy — mutations don't affect the tracker.
	all["anthropic"] = Usage{InputTokens: 9999}
	if tracker.ProviderUsage("anthropic").InputTokens != 100 {
		t.Error("AllProviderUsage should return a copy, not a reference")
	}
}

func TestTokenTrackerAllProviderUsageEmpty(t *testing.T) {
	tracker := NewTokenTracker()
	all := tracker.AllProviderUsage()
	if len(all) != 0 {
		t.Errorf("expected empty map, got %d entries", len(all))
	}
}
