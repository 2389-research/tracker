// ABOUTME: Convenience factory for a HandlerRegistry pre-loaded with all built-in handlers.
// ABOUTME: Uses functional options to override codergen/tool/human handlers for testing.
package handlers

import (
	"context"

	"github.com/2389-research/mammoth-lite/pipeline"
)

// HandlerFunc is a function that implements the core logic of a pipeline handler.
// Used by functional option stubs to override built-in handler behavior.
type HandlerFunc func(ctx context.Context, node *pipeline.Node, pctx *pipeline.PipelineContext) (pipeline.Outcome, error)

// RegistryOption configures optional overrides when building a default registry.
type RegistryOption func(*registryConfig)

type registryConfig struct {
	codergenFunc  HandlerFunc
	toolExecFunc  HandlerFunc
	humanCallback HandlerFunc
}

// WithCodergenFunc overrides the codergen handler with a stub function.
func WithCodergenFunc(fn HandlerFunc) RegistryOption {
	return func(c *registryConfig) {
		c.codergenFunc = fn
	}
}

// WithToolExecFunc overrides the tool handler with a stub function.
func WithToolExecFunc(fn HandlerFunc) RegistryOption {
	return func(c *registryConfig) {
		c.toolExecFunc = fn
	}
}

// WithHumanCallback overrides the human handler with a stub function.
func WithHumanCallback(fn HandlerFunc) RegistryOption {
	return func(c *registryConfig) {
		c.humanCallback = fn
	}
}

// NewDefaultRegistry creates a HandlerRegistry pre-loaded with all built-in handlers.
// The graph is needed for the parallel handler (to look up branch targets) and the
// human handler (to look up outgoing edge labels). Optional RegistryOption funcs
// override codergen, tool, and human handlers for testing with stubs.
func NewDefaultRegistry(graph *pipeline.Graph, opts ...RegistryOption) *pipeline.HandlerRegistry {
	cfg := &registryConfig{}
	for _, opt := range opts {
		opt(cfg)
	}

	registry := pipeline.NewHandlerRegistry()

	// Simple no-dependency handlers.
	registry.Register(NewStartHandler())
	registry.Register(NewExitHandler())
	registry.Register(NewConditionalHandler())
	registry.Register(NewFanInHandler())

	// Parallel handler needs the graph and registry for branch dispatch.
	registry.Register(NewParallelHandler(graph, registry))

	// Codergen: use stub or skip registration (requires LLM client in production).
	if cfg.codergenFunc != nil {
		registry.Register(&funcHandler{name: "codergen", fn: cfg.codergenFunc})
	}

	// Tool: use stub or skip registration (requires exec environment in production).
	if cfg.toolExecFunc != nil {
		registry.Register(&funcHandler{name: "tool", fn: cfg.toolExecFunc})
	}

	// Human: use stub or skip registration (requires interviewer in production).
	if cfg.humanCallback != nil {
		registry.Register(&funcHandler{name: "wait.human", fn: cfg.humanCallback})
	}

	return registry
}

// funcHandler wraps a HandlerFunc into a pipeline.Handler for use in the registry.
type funcHandler struct {
	name string
	fn   HandlerFunc
}

func (h *funcHandler) Name() string { return h.name }

func (h *funcHandler) Execute(ctx context.Context, node *pipeline.Node, pctx *pipeline.PipelineContext) (pipeline.Outcome, error) {
	return h.fn(ctx, node, pctx)
}
