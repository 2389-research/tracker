// ABOUTME: Core LLM client that routes requests to provider adapters.
// ABOUTME: Supports multiple providers, default routing, middleware chains, and env-based config.
package llm

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"
)

// Client manages LLM provider adapters and routes requests through a middleware chain.
type Client struct {
	providers       map[string]ProviderAdapter
	defaultProvider string
	middleware      []Middleware
	traceObservers  []TraceObserver
}

// ClientOption configures a Client during construction.
type ClientOption func(*clientConfig)

// clientConfig holds intermediate state during Client construction.
type clientConfig struct {
	providers       map[string]ProviderAdapter
	defaultProvider string
	middleware      []Middleware
	traceObservers  []TraceObserver
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

// WithTraceObserver registers a live trace observer for completions.
func WithTraceObserver(obs TraceObserver) ClientOption {
	return func(c *clientConfig) {
		if obs != nil {
			c.traceObservers = append(c.traceObservers, obs)
		}
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
		traceObservers:  cfg.traceObservers,
	}, nil
}

// NewClientFromEnv creates a Client by reading API keys from environment variables.
// The constructors map keys are provider names ("anthropic", "openai", "gemini") and
// values are factory functions that create adapters from an API key.
// providerEnvKeys maps provider names to their environment variable names.
var providerEnvKeys = map[string][]string{
	"anthropic": {"ANTHROPIC_API_KEY"},
	"openai":    {"OPENAI_API_KEY"},
	"gemini":    {"GEMINI_API_KEY", "GOOGLE_API_KEY"},
}

// providerPriority defines the deterministic order for default provider selection.
var providerPriority = []string{"anthropic", "openai", "gemini"}

func NewClientFromEnv(constructors map[string]func(apiKey string) (ProviderAdapter, error)) (*Client, error) {
	var opts []ClientOption
	var firstProvider string

	// Process standard providers in priority order.
	for _, name := range providerPriority {
		constructor, ok := constructors[name]
		if !ok {
			continue
		}
		opt, err := tryBuildProvider(name, constructor)
		if err != nil {
			return nil, err
		}
		if opt != nil {
			opts = append(opts, opt)
			if firstProvider == "" {
				firstProvider = name
			}
		}
	}

	// Process non-standard providers.
	for name, constructor := range constructors {
		if name == "anthropic" || name == "openai" || name == "gemini" {
			continue
		}
		opt, err := tryBuildProvider(name, constructor)
		if err != nil {
			return nil, err
		}
		if opt != nil {
			opts = append(opts, opt)
			if firstProvider == "" {
				firstProvider = name
			}
		}
	}

	if firstProvider != "" {
		opts = append(opts, WithDefaultProvider(firstProvider))
	}

	return NewClient(opts...)
}

// tryBuildProvider attempts to find an API key in the environment and build a provider adapter.
// Returns nil option if no API key is found.
func tryBuildProvider(name string, constructor func(string) (ProviderAdapter, error)) (ClientOption, error) {
	envVars, ok := providerEnvKeys[name]
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
		return nil, nil
	}

	adapter, err := constructor(apiKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create %s adapter: %w", name, err)
	}

	return WithProvider(adapter), nil
}

// DefaultProvider returns the name of the default provider, or empty string.
func (c *Client) DefaultProvider() string {
	return c.defaultProvider
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
	traceObservers := c.collectTraceObservers(req)

	// Build the innermost handler that calls the adapter.
	handler := CompleteHandler(func(ctx context.Context, req *Request) (*Response, error) {
		if len(traceObservers) > 0 {
			return c.completeWithTrace(ctx, req, adapter, traceObservers)
		}

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

func (c *Client) completeWithTrace(ctx context.Context, req *Request, adapter ProviderAdapter, observers []TraceObserver) (*Response, error) {
	start := time.Now()
	streamReq := cloneRequest(req)
	if streamReq.ProviderOptions == nil {
		streamReq.ProviderOptions = make(map[string]any)
	}
	streamReq.ProviderOptions["tracker_emit_provider_events"] = true
	traceBuilder := NewTraceBuilder(TraceOptions{
		Provider: adapter.Name(),
		Model:    req.Model,
		Verbose:  true,
	})
	acc := NewStreamAccumulator()

	for evt := range adapter.Stream(ctx, streamReq) {
		if evt.Err != nil {
			// Flush any buffered trace output before returning the error.
			for _, obs := range observers {
				if flusher, ok := obs.(interface{ Flush() }); ok {
					flusher.Flush()
				}
			}
			return nil, evt.Err
		}

		before := len(traceBuilder.events)
		traceBuilder.Process(evt)
		for _, traceEvt := range traceBuilder.events[before:] {
			notifyTraceObservers(observers, traceEvt)
		}

		acc.Process(evt)
	}

	resp := acc.Response()
	resp.Provider = adapter.Name()
	resp.Model = req.Model
	resp.Latency = time.Since(start)
	return &resp, nil
}

// Stream sends a streaming request to the resolved provider adapter.
// Middleware is NOT applied to streaming requests (middleware only wraps Complete).
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

// AddMiddleware appends a middleware to the client's existing middleware chain.
// This allows post-construction addition of middleware (e.g., adding a
// TokenTracker after the client is built from environment variables).
func (c *Client) AddMiddleware(mw Middleware) {
	c.middleware = append(c.middleware, mw)
}

// AddTraceObserver appends a live trace observer after client construction.
func (c *Client) AddTraceObserver(obs TraceObserver) {
	if obs != nil {
		c.traceObservers = append(c.traceObservers, obs)
	}
}

func (c *Client) collectTraceObservers(req *Request) []TraceObserver {
	total := make([]TraceObserver, 0, len(c.traceObservers)+len(req.TraceObservers))
	total = append(total, c.traceObservers...)
	total = append(total, req.TraceObservers...)
	return total
}

func notifyTraceObservers(observers []TraceObserver, evt TraceEvent) {
	for _, obs := range observers {
		if obs != nil {
			obs.HandleTraceEvent(evt)
		}
	}
}

func cloneRequest(req *Request) *Request {
	cp := *req
	if req.ProviderOptions != nil {
		cp.ProviderOptions = make(map[string]any, len(req.ProviderOptions))
		for k, v := range req.ProviderOptions {
			cp.ProviderOptions[k] = v
		}
	}
	if req.TraceObservers != nil {
		cp.TraceObservers = append([]TraceObserver(nil), req.TraceObservers...)
	}
	return &cp
}

// Close releases resources for all registered provider adapters.
func (c *Client) Close() error {
	var errs []error
	for _, adapter := range c.providers {
		if err := adapter.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
