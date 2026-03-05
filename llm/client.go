// ABOUTME: Core LLM client that routes requests to provider adapters.
// ABOUTME: Supports multiple providers, default routing, middleware chains, and env-based config.
package llm

import (
	"context"
	"fmt"
	"os"
	"time"
)

// Client manages LLM provider adapters and routes requests through a middleware chain.
type Client struct {
	providers       map[string]ProviderAdapter
	defaultProvider string
	middleware      []Middleware
}

// ClientOption configures a Client during construction.
type ClientOption func(*clientConfig)

// clientConfig holds intermediate state during Client construction.
type clientConfig struct {
	providers       map[string]ProviderAdapter
	defaultProvider string
	middleware      []Middleware
}

// WithProvider registers a provider adapter with the client.
func WithProvider(adapter ProviderAdapter) ClientOption {
	return func(c *clientConfig) {
		c.providers[adapter.Name()] = adapter
	}
}

// WithDefaultProvider sets the provider name used when a request does not specify one.
func WithDefaultProvider(name string) ClientOption {
	return func(c *clientConfig) {
		c.defaultProvider = name
	}
}

// WithMiddleware appends a middleware to the client's middleware chain.
func WithMiddleware(mw Middleware) ClientOption {
	return func(c *clientConfig) {
		c.middleware = append(c.middleware, mw)
	}
}

// NewClient creates a Client from the given options.
func NewClient(opts ...ClientOption) (*Client, error) {
	cfg := &clientConfig{
		providers: make(map[string]ProviderAdapter),
	}

	for _, opt := range opts {
		opt(cfg)
	}

	if len(cfg.providers) == 0 {
		return nil, &ConfigurationError{SDKError: SDKError{Msg: "no providers configured"}}
	}

	// If only one provider and no default set, use it as the default.
	if cfg.defaultProvider == "" && len(cfg.providers) == 1 {
		for name := range cfg.providers {
			cfg.defaultProvider = name
		}
	}

	return &Client{
		providers:       cfg.providers,
		defaultProvider: cfg.defaultProvider,
		middleware:      cfg.middleware,
	}, nil
}

// NewClientFromEnv creates a Client by reading API keys from environment variables.
// The constructors map keys are provider names ("anthropic", "openai", "gemini") and
// values are factory functions that create adapters from an API key.
func NewClientFromEnv(constructors map[string]func(apiKey string) (ProviderAdapter, error)) (*Client, error) {
	envKeys := map[string][]string{
		"anthropic": {"ANTHROPIC_API_KEY"},
		"openai":    {"OPENAI_API_KEY"},
		"gemini":    {"GEMINI_API_KEY", "GOOGLE_API_KEY"},
	}

	var opts []ClientOption
	var firstProvider string

	for name, constructor := range constructors {
		envVars, ok := envKeys[name]
		if !ok {
			envVars = []string{fmt.Sprintf("%s_API_KEY", name)}
		}

		var apiKey string
		for _, envVar := range envVars {
			if v := os.Getenv(envVar); v != "" {
				apiKey = v
				break
			}
		}

		if apiKey == "" {
			continue
		}

		adapter, err := constructor(apiKey)
		if err != nil {
			return nil, fmt.Errorf("failed to create %s adapter: %w", name, err)
		}

		opts = append(opts, WithProvider(adapter))
		if firstProvider == "" {
			firstProvider = name
		}
	}

	if firstProvider != "" {
		opts = append(opts, WithDefaultProvider(firstProvider))
	}

	return NewClient(opts...)
}

// resolveProvider determines which provider adapter to use for a request.
func (c *Client) resolveProvider(req *Request) (ProviderAdapter, error) {
	name := req.Provider
	if name == "" {
		name = c.defaultProvider
	}
	if name == "" {
		return nil, &ConfigurationError{SDKError: SDKError{Msg: "no provider specified and no default provider configured"}}
	}

	adapter, ok := c.providers[name]
	if !ok {
		return nil, &ConfigurationError{SDKError: SDKError{
			Msg: fmt.Sprintf("unknown provider: %q", name),
		}}
	}
	return adapter, nil
}

// Complete sends a completion request through the middleware chain and returns the response.
func (c *Client) Complete(ctx context.Context, req *Request) (*Response, error) {
	adapter, err := c.resolveProvider(req)
	if err != nil {
		return nil, err
	}

	// Build the innermost handler that calls the adapter.
	handler := CompleteHandler(func(ctx context.Context, req *Request) (*Response, error) {
		start := time.Now()
		resp, err := adapter.Complete(ctx, req)
		if err != nil {
			return nil, err
		}
		resp.Latency = time.Since(start)
		resp.Provider = adapter.Name()
		return resp, nil
	})

	// Wrap in middleware (reverse order for onion pattern).
	for i := len(c.middleware) - 1; i >= 0; i-- {
		handler = c.middleware[i].WrapComplete(handler)
	}

	return handler(ctx, req)
}

// Stream sends a streaming request to the resolved provider adapter.
// On provider resolution failure, it returns a channel containing a single error event.
func (c *Client) Stream(ctx context.Context, req *Request) <-chan StreamEvent {
	adapter, err := c.resolveProvider(req)
	if err != nil {
		ch := make(chan StreamEvent, 1)
		ch <- StreamEvent{Type: EventError, Err: err}
		close(ch)
		return ch
	}

	return adapter.Stream(ctx, req)
}

// Close releases resources for all registered provider adapters.
func (c *Client) Close() error {
	var lastErr error
	for _, adapter := range c.providers {
		if err := adapter.Close(); err != nil {
			lastErr = err
		}
	}
	return lastErr
}
