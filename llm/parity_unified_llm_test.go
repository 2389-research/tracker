package llm

import (
	"context"
	"testing"
	"time"
)

type delayedAdapter struct {
	name     string
	delay    time.Duration
	response *Response
}

func (d *delayedAdapter) Name() string { return d.name }

func (d *delayedAdapter) Complete(_ context.Context, _ *Request) (*Response, error) {
	time.Sleep(d.delay)
	return d.response, nil
}

func (d *delayedAdapter) Stream(_ context.Context, _ *Request) <-chan StreamEvent {
	ch := make(chan StreamEvent)
	close(ch)
	return ch
}

func (d *delayedAdapter) Close() error { return nil }

func TestParityNewClientFromEnvUsesDeterministicDefaultProviderPriority(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "anthropic-key")
	t.Setenv("OPENAI_API_KEY", "openai-key")
	t.Setenv("GEMINI_API_KEY", "gemini-key")

	client, err := NewClientFromEnv(map[string]func(string) (ProviderAdapter, error){
		"gemini": func(apiKey string) (ProviderAdapter, error) {
			return &mockAdapter{
				name:     "gemini",
				response: &Response{ID: "g-1", Message: AssistantMessage("from gemini")},
			}, nil
		},
		"openai": func(apiKey string) (ProviderAdapter, error) {
			return &mockAdapter{
				name:     "openai",
				response: &Response{ID: "o-1", Message: AssistantMessage("from openai")},
			}, nil
		},
		"anthropic": func(apiKey string) (ProviderAdapter, error) {
			return &mockAdapter{
				name:     "anthropic",
				response: &Response{ID: "a-1", Message: AssistantMessage("from anthropic")},
			}, nil
		},
	})
	if err != nil {
		t.Fatalf("NewClientFromEnv failed: %v", err)
	}
	defer client.Close()

	resp, err := client.Complete(context.Background(), &Request{
		Model:    "test-model",
		Messages: []Message{UserMessage("hi")},
	})
	if err != nil {
		t.Fatalf("Complete failed: %v", err)
	}
	if resp.Provider != "anthropic" {
		t.Fatalf("provider = %q, want anthropic", resp.Provider)
	}
}

func TestParityCompletePopulatesProviderAndLatency(t *testing.T) {
	client, err := NewClient(
		WithProvider(&delayedAdapter{
			name:     "openai",
			delay:    2 * time.Millisecond,
			response: &Response{ID: "resp-1", Message: AssistantMessage("hello")},
		}),
		WithDefaultProvider("openai"),
	)
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	defer client.Close()

	resp, err := client.Complete(context.Background(), &Request{
		Model:    "test-model",
		Messages: []Message{UserMessage("hi")},
	})
	if err != nil {
		t.Fatalf("Complete failed: %v", err)
	}
	if resp.Provider != "openai" {
		t.Fatalf("provider = %q, want openai", resp.Provider)
	}
	if resp.Latency <= 0 {
		t.Fatalf("latency = %v, want > 0", resp.Latency)
	}
}

func TestParityStreamReturnsResolutionErrorEvent(t *testing.T) {
	client, err := NewClient(
		WithProvider(&mockAdapter{name: "openai"}),
		WithDefaultProvider("openai"),
	)
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}
	defer client.Close()

	ch := client.Stream(context.Background(), &Request{
		Model:    "test-model",
		Messages: []Message{UserMessage("hi")},
		Provider: "missing",
	})

	evt, ok := <-ch
	if !ok {
		t.Fatal("expected one error event")
	}
	if evt.Type != EventError {
		t.Fatalf("event type = %s, want %s", evt.Type, EventError)
	}
	if evt.Err == nil {
		t.Fatal("expected non-nil error")
	}
	if _, ok := <-ch; ok {
		t.Fatal("expected channel to close after the error event")
	}
}

func TestParityProviderErrorsMapToSharedRetryability(t *testing.T) {
	testCases := []struct {
		name      string
		status    int
		assert    func(error) bool
		retryable bool
	}{
		{
			name:      "authentication is terminal",
			status:    401,
			assert:    func(err error) bool { _, ok := err.(*AuthenticationError); return ok },
			retryable: false,
		},
		{
			name:      "timeout is retryable",
			status:    408,
			assert:    func(err error) bool { _, ok := err.(*RequestTimeoutError); return ok },
			retryable: true,
		},
		{
			name:      "context length is terminal",
			status:    413,
			assert:    func(err error) bool { _, ok := err.(*ContextLengthError); return ok },
			retryable: false,
		},
		{
			name:      "rate limit is retryable",
			status:    429,
			assert:    func(err error) bool { _, ok := err.(*RateLimitError); return ok },
			retryable: true,
		},
		{
			name:      "server failures are retryable",
			status:    503,
			assert:    func(err error) bool { _, ok := err.(*ServerError); return ok },
			retryable: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := ErrorFromStatusCode(tc.status, "boom", "test-provider")
			if !tc.assert(err) {
				t.Fatalf("ErrorFromStatusCode(%d) = %T, wrong mapped type", tc.status, err)
			}

			providerErr, ok := err.(ProviderErrorInterface)
			if !ok {
				t.Fatalf("expected provider error interface, got %T", err)
			}
			if providerErr.Retryable() != tc.retryable {
				t.Fatalf("Retryable() = %v, want %v", providerErr.Retryable(), tc.retryable)
			}
		})
	}
}
