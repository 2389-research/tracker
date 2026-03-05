// ABOUTME: Defines the ProviderAdapter interface for LLM provider implementations.
// ABOUTME: Each provider (OpenAI, Anthropic, Gemini, etc.) implements this interface.
package llm

import "context"

// ProviderAdapter is the interface that LLM provider implementations must satisfy.
// It supports both synchronous completion and streaming responses.
type ProviderAdapter interface {
	// Name returns the provider's identifier (e.g. "openai", "anthropic").
	Name() string

	// Complete sends a request and returns the full response.
	Complete(ctx context.Context, req *Request) (*Response, error)

	// Stream sends a request and returns a channel of streaming events.
	Stream(ctx context.Context, req *Request) <-chan StreamEvent

	// Close releases any resources held by the adapter.
	Close() error
}
