// ABOUTME: Convenience factory for a HandlerRegistry pre-loaded with all built-in handlers.
// ABOUTME: Uses functional options to override codergen/tool/human handlers for testing.
package handlers

import (
	"context"

	"github.com/2389-research/mammoth-lite/agent"
	"github.com/2389-research/mammoth-lite/agent/exec"
	"github.com/2389-research/mammoth-lite/pipeline"
)

// HandlerFunc is a function that implements the core logic of a pipeline handler.
// Used by functional option stubs to override built-in handler behavior.
type HandlerFunc func(ctx context.Context, node *pipeline.Node, pctx *pipeline.PipelineContext) (pipeline.Outcome, error)

// RegistryOption configures optional overrides when building a default registry.
type RegistryOption func(*registryConfig)

type registryConfig struct {
	// Stub overrides (for testing)
	codergenFunc  HandlerFunc
	toolExecFunc  HandlerFunc
	humanCallback HandlerFunc
	// Production dependencies
	llmClient   agent.Completer
	workingDir  string
	execEnv     exec.ExecutionEnvironment
	interviewer Interviewer
	graph       *pipeline.Graph
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

// WithLLMClient sets the LLM client for the codergen handler.
func WithLLMClient(client agent.Completer, workingDir string) RegistryOption {
	return func(c *registryConfig) {
		c.llmClient = client
		c.workingDir = workingDir
	}
}

// WithExecEnvironment sets the execution environment for the tool handler.
func WithExecEnvironment(env exec.ExecutionEnvironment) RegistryOption {
	return func(c *registryConfig) {
		c.execEnv = env
	}
}

// WithInterviewer sets the interviewer for the human handler.
func WithInterviewer(interviewer Interviewer, graph *pipeline.Graph) RegistryOption {
	return func(c *registryConfig) {
		c.interviewer = interviewer
		c.graph = graph
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
	registry.Register(NewManagerLoopHandler())

	// Parallel handler needs the graph and registry for branch dispatch.
	registry.Register(NewParallelHandler(graph, registry))

	// Codergen: prefer real handler, fall back to stub.
	if cfg.llmClient != nil {
		handler := NewCodergenHandler(cfg.llmClient, cfg.workingDir)
		handler.env = cfg.execEnv
		registry.Register(handler)
	} else if cfg.codergenFunc != nil {
		registry.Register(&funcHandler{name: "codergen", fn: cfg.codergenFunc})
	}

	// Tool: prefer real handler, fall back to stub.
	if cfg.execEnv != nil {
		registry.Register(NewToolHandler(cfg.execEnv))
	} else if cfg.toolExecFunc != nil {
		registry.Register(&funcHandler{name: "tool", fn: cfg.toolExecFunc})
	}

	// Human: prefer real handler, fall back to stub.
	if cfg.interviewer != nil {
		humanGraph := cfg.graph
		if humanGraph == nil {
			humanGraph = graph
		}
		registry.Register(NewHumanHandler(cfg.interviewer, humanGraph))
	} else if cfg.humanCallback != nil {
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
