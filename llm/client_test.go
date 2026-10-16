// ABOUTME: Tests for the core LLM Client covering provider routing, error cases, and streaming.
// ABOUTME: Verifies explicit routing, default fallback, unknown provider errors, and stream behavior.
package llm

import (
	"context"
	"errors"
	"testing"
)

func TestClientRouting(t *testing.T) {
	providerA := &mockAdapter{
		name:     "alpha",
		response: &Response{ID: "a-1", Message: AssistantMessage("from alpha")},
	}
	providerB := &mockAdapter{
		name:     "beta",
		response: &Response{ID: "b-1", Message: AssistantMessage("from beta")},
	}

	client, err := NewClient(
		WithProvider(providerA),
		WithProvider(providerB),
		WithDefaultProvider("alpha"),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer client.Close()

	// Explicit provider routing to beta.
	resp, err := client.Complete(context.Background(), &Request{
		Model:    "model-b",
		Messages: []Message{UserMessage("hi")},
		Provider: "beta",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Provider != "beta" {
		t.Errorf("expected provider 'beta', got %q", resp.Provider)
	}
	if resp.Text() != "from beta" {
		t.Errorf("expected 'from beta', got %q", resp.Text())
	}

	// Default provider routing to alpha.
	resp, err = client.Complete(context.Background(), &Request{
		Model:    "model-a",
		Messages: []Message{UserMessage("hi")},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Provider != "alpha" {
		t.Errorf("expected provider 'alpha', got %q", resp.Provider)
	}
	if resp.Text() != "from alpha" {
		t.Errorf("expected 'from alpha', got %q", resp.Text())
	}
}

func TestClientNoProvider(t *testing.T) {
	_, err := NewClient()
	if err == nil {
		t.Fatal("expected error for no providers, got nil")
	}

	var cfgErr *ConfigurationError
	if !errors.As(err, &cfgErr) {
		t.Errorf("expected ConfigurationError, got %T: %v", err, err)
	}
}

func TestClientUnknownProvider(t *testing.T) {
	provider := &mockAdapter{
		name:     "known",
		response: &Response{ID: "k-1", Message: AssistantMessage("ok")},
	}

	client, err := NewClient(
		WithProvider(provider),
		WithDefaultProvider("known"),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer client.Close()

	_, err = client.Complete(context.Background(), &Request{
		Model:    "m",
		Messages: []Message{UserMessage("hi")},
		Provider: "nonexistent",
	})
	if err == nil {
		t.Fatal("expected error for unknown provider, got nil")
	}

	var cfgErr *ConfigurationError
	if !errors.As(err, &cfgErr) {
		t.Errorf("expected ConfigurationError, got %T: %v", err, err)
	}
}

func TestClientStream(t *testing.T) {
	events := []StreamEvent{
		{Type: EventStreamStart},
		{Type: EventTextDelta, Delta: "streamed"},
		{Type: EventFinish, FinishReason: &FinishReason{Reason: "stop"}},
	}

	provider := &mockAdapter{
		name:   "streamer",
		events: events,
	}

	client, err := NewClient(
		WithProvider(provider),
		WithDefaultProvider("streamer"),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer client.Close()

	ch := client.Stream(context.Background(), &Request{
		Model:    "m",
		Messages: []Message{UserMessage("hi")},
	})

	var received []StreamEvent
	for evt := range ch {
		received = append(received, evt)
	}

	if len(received) != 3 {
		t.Fatalf("expected 3 events, got %d", len(received))
	}
	if received[0].Type != EventStreamStart {
		t.Errorf("expected StreamStart, got %s", received[0].Type)
	}
	if received[1].Delta != "streamed" {
		t.Errorf("expected delta 'streamed', got %q", received[1].Delta)
	}
}

func TestClientStream_UnknownProvider(t *testing.T) {
	provider := &mockAdapter{
		name:   "valid",
		events: []StreamEvent{{Type: EventStreamStart}},
	}

	client, err := NewClient(
		WithProvider(provider),
		WithDefaultProvider("valid"),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer client.Close()

	ch := client.Stream(context.Background(), &Request{
		Model:    "m",
		Messages: []Message{UserMessage("hi")},
		Provider: "ghost",
	})

	evt := <-ch
	if evt.Type != EventError {
		t.Errorf("expected EventError, got %s", evt.Type)
	}
	if evt.Err == nil {
		t.Error("expected non-nil error in error event")
	}
}

func TestClientSingleProviderDefaultsToIt(t *testing.T) {
	provider := &mockAdapter{
		name:     "solo",
		response: &Response{ID: "s-1", Message: AssistantMessage("solo response")},
	}

	client, err := NewClient(WithProvider(provider))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer client.Close()

	resp, err := client.Complete(context.Background(), &Request{
		Model:    "m",
		Messages: []Message{UserMessage("hi")},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Provider != "solo" {
		t.Errorf("expected provider 'solo', got %q", resp.Provider)
	}
}

// TestClient_HasMiddleware covers the identity check that prevents the
// TokenTracker double-count foot-gun (a caller re-attaching the same tracker).
func TestClient_HasMiddleware(t *testing.T) {
	client, err := NewClient(
		WithProvider(&mockAdapter{name: "alpha", response: &Response{ID: "x"}}),
		WithDefaultProvider("alpha"),
	)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer client.Close()

	tt := NewTokenTracker()
	if client.HasMiddleware(tt) {
		t.Fatal("fresh client should not report the tracker present")
	}
	client.AddMiddleware(tt)
	if !client.HasMiddleware(tt) {
		t.Fatal("tracker should be present after AddMiddleware")
	}
	if client.HasMiddleware(NewTokenTracker()) {
		t.Fatal("a different tracker instance must not be reported present")
	}
}

// labeledMockAdapter augments mockAdapter with a UsageLabel so tests can
// exercise Client.UsageLabel and the resp.Provider stamping without a real
// ChatGPT-mode adapter (a later task implements UsageLabeler on the openai
// adapter itself).
type labeledMockAdapter struct {
	mockAdapter
	label string
}

func (m *labeledMockAdapter) UsageLabel() string { return m.label }

// clearProviderEnv clears every provider API-key env var client.go knows
// about (providerEnvKeys), so a test asserting default-provider selection
// isn't polluted by real keys set in the developer's shell. t.Setenv restores
// the prior value when the test ends.
func clearProviderEnv(t *testing.T) {
	t.Helper()
	for _, envVars := range providerEnvKeys {
		for _, v := range envVars {
			t.Setenv(v, "")
		}
	}
}

// TestClientStampsUsageLabel pins the #590/D6 contract: both Complete paths
// (untraced and traced) stamp resp.Provider with the resolved adapter's usage
// label — not its registered name — and a TokenTracker middleware wrapping
// the call buckets usage under that same label. An adapter that doesn't
// implement UsageLabeler keeps reporting its own name, so every adapter
// shipped today (none of which implement it) sees no behavior change.
func TestClientStampsUsageLabel(t *testing.T) {
	traceEvents := []StreamEvent{
		{Type: EventStreamStart},
		{Type: EventTextDelta, Delta: "hi"},
		{Type: EventFinish, FinishReason: &FinishReason{Reason: "stop"}, Usage: &Usage{InputTokens: 7, OutputTokens: 3}},
	}

	t.Run("labeled adapter stamps the label on both paths", func(t *testing.T) {
		adapter := &labeledMockAdapter{
			mockAdapter: mockAdapter{
				name:     "openai",
				response: &Response{ID: "r-1", Message: AssistantMessage("hi"), Usage: Usage{InputTokens: 7, OutputTokens: 3}},
				events:   traceEvents,
			},
			label: OpenAISubscription,
		}
		client, err := NewClient(WithProvider(adapter), WithDefaultProvider("openai"))
		if err != nil {
			t.Fatalf("NewClient: %v", err)
		}
		defer client.Close()
		tt := NewTokenTracker()
		client.AddMiddleware(tt)

		resp, err := client.Complete(context.Background(), &Request{
			Model:    "m",
			Messages: []Message{UserMessage("hi")},
		})
		if err != nil {
			t.Fatalf("Complete (plain path): %v", err)
		}
		if resp.Provider != OpenAISubscription {
			t.Errorf("plain path: resp.Provider = %q, want %q", resp.Provider, OpenAISubscription)
		}

		resp, err = client.Complete(context.Background(), &Request{
			Model:          "m",
			Messages:       []Message{UserMessage("hi")},
			TraceObservers: []TraceObserver{TraceObserverFunc(func(TraceEvent) {})},
		})
		if err != nil {
			t.Fatalf("Complete (traced path): %v", err)
		}
		if resp.Provider != OpenAISubscription {
			t.Errorf("traced path: resp.Provider = %q, want %q", resp.Provider, OpenAISubscription)
		}

		if got := tt.ProviderUsage(OpenAISubscription); got.InputTokens == 0 {
			t.Errorf("TokenTracker did not bucket usage under %q: %+v", OpenAISubscription, got)
		}
	})

	t.Run("unlabeled adapter keeps its own name", func(t *testing.T) {
		adapter := &mockAdapter{name: "openai", response: &Response{ID: "r-2", Message: AssistantMessage("hi")}}
		client, err := NewClient(WithProvider(adapter), WithDefaultProvider("openai"))
		if err != nil {
			t.Fatalf("NewClient: %v", err)
		}
		defer client.Close()

		resp, err := client.Complete(context.Background(), &Request{
			Model:    "m",
			Messages: []Message{UserMessage("hi")},
		})
		if err != nil {
			t.Fatalf("Complete: %v", err)
		}
		if resp.Provider != "openai" {
			t.Errorf("resp.Provider = %q, want %q", resp.Provider, "openai")
		}
	})
}

// TestClientUsageLabel covers Client.UsageLabel directly: a registered
// labeler's label, an unregistered name's fallback to itself, and "" in / ""
// out.
func TestClientUsageLabel(t *testing.T) {
	adapter := &labeledMockAdapter{mockAdapter: mockAdapter{name: "openai"}, label: OpenAISubscription}
	client, err := NewClient(WithProvider(adapter), WithDefaultProvider("openai"))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer client.Close()

	if got := client.UsageLabel("openai"); got != OpenAISubscription {
		t.Errorf("UsageLabel(openai) = %q, want %q", got, OpenAISubscription)
	}
	if got := client.UsageLabel("gemini"); got != "gemini" {
		t.Errorf("UsageLabel(gemini) [unregistered] = %q, want %q", got, "gemini")
	}
	if got := client.UsageLabel(""); got != "" {
		t.Errorf("UsageLabel(\"\") = %q, want \"\"", got)
	}
}

// TestNewClientFromEnvWithKeylessExtra covers the keyless-provider contract:
// an adapter built outside the env-key flow (e.g. from a ChatGPT-subscription
// token file, not OPENAI_API_KEY) still registers and can become the default.
func TestNewClientFromEnvWithKeylessExtra(t *testing.T) {
	clearProviderEnv(t)

	extra := &mockAdapter{name: "openai", response: &Response{ID: "x-1"}}
	client, err := NewClientFromEnvWith(map[string]func(string) (ProviderAdapter, error){
		"anthropic": func(string) (ProviderAdapter, error) { return &mockAdapter{name: "anthropic"}, nil },
		"openai":    func(string) (ProviderAdapter, error) { return &mockAdapter{name: "openai"}, nil },
	}, extra)
	if err != nil {
		t.Fatalf("NewClientFromEnvWith: %v", err)
	}
	defer client.Close()

	if client.DefaultProvider() != "openai" {
		t.Errorf("DefaultProvider() = %q, want %q", client.DefaultProvider(), "openai")
	}
}

// TestNewClientFromEnvWithExtraTakesPriority checks the extra adapter takes
// its name's slot in providerPriority for default-provider selection: it
// outranks a lower-priority env-built provider (gemini) but is outranked by
// a higher-priority one (anthropic) — exactly as if "openai" had been
// env-built in that position.
func TestNewClientFromEnvWithExtraTakesPriority(t *testing.T) {
	t.Run("extra openai outranks env-built gemini", func(t *testing.T) {
		clearProviderEnv(t)
		t.Setenv("GEMINI_API_KEY", "gemini-key")

		extra := &mockAdapter{name: "openai"}
		client, err := NewClientFromEnvWith(map[string]func(string) (ProviderAdapter, error){
			"gemini": func(string) (ProviderAdapter, error) { return &mockAdapter{name: "gemini"}, nil },
		}, extra)
		if err != nil {
			t.Fatalf("NewClientFromEnvWith: %v", err)
		}
		defer client.Close()
		if client.DefaultProvider() != "openai" {
			t.Errorf("DefaultProvider() = %q, want %q", client.DefaultProvider(), "openai")
		}
	})

	t.Run("env-built anthropic outranks extra openai", func(t *testing.T) {
		clearProviderEnv(t)
		t.Setenv("ANTHROPIC_API_KEY", "anthropic-key")

		extra := &mockAdapter{name: "openai"}
		client, err := NewClientFromEnvWith(map[string]func(string) (ProviderAdapter, error){
			"anthropic": func(string) (ProviderAdapter, error) { return &mockAdapter{name: "anthropic"}, nil },
		}, extra)
		if err != nil {
			t.Fatalf("NewClientFromEnvWith: %v", err)
		}
		defer client.Close()
		if client.DefaultProvider() != "anthropic" {
			t.Errorf("DefaultProvider() = %q, want %q", client.DefaultProvider(), "anthropic")
		}
	})
}

// TestNewClientFromEnvWithExtraReplacesEnvBuilt checks that when an extra
// adapter's name matches a configured env key, the env constructor is never
// invoked — the extra adapter wins outright, it isn't merely preferred as
// default.
func TestNewClientFromEnvWithExtraReplacesEnvBuilt(t *testing.T) {
	clearProviderEnv(t)
	t.Setenv("OPENAI_API_KEY", "openai-key")

	calls := 0
	extra := &mockAdapter{name: "openai"}
	client, err := NewClientFromEnvWith(map[string]func(string) (ProviderAdapter, error){
		"openai": func(string) (ProviderAdapter, error) {
			calls++
			return &mockAdapter{name: "openai"}, nil
		},
	}, extra)
	if err != nil {
		t.Fatalf("NewClientFromEnvWith: %v", err)
	}
	defer client.Close()

	if calls != 0 {
		t.Errorf("openai constructor called %d times, want 0", calls)
	}
	if client.DefaultProvider() != "openai" {
		t.Errorf("DefaultProvider() = %q, want %q", client.DefaultProvider(), "openai")
	}
}
