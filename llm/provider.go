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

// UsageLabeler is implemented by a ProviderAdapter that bills its usage under
// a different accounting label than the name it is registered and routed
// under. For example, an openai adapter running in ChatGPT-subscription mode
// stays registered and routed as "openai" — so pipelines and dippin-lang need
// no change — but reports usage as OpenAISubscription so cost tracking prices
// it at $0. Client.UsageLabel consults this; an adapter that doesn't
// implement it, or returns "", is labeled by its own registered name.
type UsageLabeler interface {
	// UsageLabel returns the provider label to stamp on a Response and bucket
	// usage under. An empty return defers to the adapter's registered name.
	UsageLabel() string
}

// OpenAISubscription is the usage-accounting label for openai calls billed to
// a ChatGPT subscription rather than metered against the OpenAI API. The
// adapter stays registered and routed as "openai"; only the label its
// UsageLabel() method returns is this value. EstimateCostForProvider prices
// it at $0 regardless of model, with tokens still counted.
const OpenAISubscription = "openai-subscription"

// IsSubscriptionUsage reports whether provider is a subscription-billed usage
// label — usage a plan already covers, priced at $0 regardless of model.
func IsSubscriptionUsage(provider string) bool {
	return provider == OpenAISubscription
}
