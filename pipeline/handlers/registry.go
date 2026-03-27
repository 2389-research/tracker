// ABOUTME: Convenience factory for a HandlerRegistry pre-loaded with all built-in handlers.
// ABOUTME: Uses functional options to override codergen/tool/human handlers for testing.
package handlers

import (
	"context"

	"github.com/2389-research/tracker/agent"
	"github.com/2389-research/tracker/agent/exec"
	"github.com/2389-research/tracker/llm"
	"github.com/2389-research/tracker/pipeline"
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
	llmClient      agent.Completer
	workingDir     string
	execEnv        exec.ExecutionEnvironment
	interviewer    Interviewer
	graph          *pipeline.Graph
	agentEvents    agent.EventHandler
	pipelineEvents pipeline.PipelineEventHandler
	subgraphs      map[string]*pipeline.Graph
	defaultBackend string
	tokenTracker   *llm.TokenTracker
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

// WithAgentEventHandler forwards live agent session events from codergen runs.
func WithAgentEventHandler(handler agent.EventHandler) RegistryOption {
	return func(c *registryConfig) {
		c.agentEvents = handler
	}
}

// WithPipelineEventHandler forwards pipeline events from handlers that emit them (e.g. parallel).
func WithPipelineEventHandler(handler pipeline.PipelineEventHandler) RegistryOption {
	return func(c *registryConfig) {
		c.pipelineEvents = handler
	}
}

// WithDefaultBackend sets the default backend name (e.g., "native", "claude-code")
// for codergen nodes that don't specify one explicitly.
func WithDefaultBackend(name string) RegistryOption {
	return func(c *registryConfig) {
		c.defaultBackend = name
	}
}

// WithTokenTracker provides a token tracker for backends that bypass the LLM client.
func WithTokenTracker(tracker *llm.TokenTracker) RegistryOption {
	return func(c *registryConfig) {
		c.tokenTracker = tracker
	}
}

// WithSubgraphs provides named sub-graphs that can be executed by subgraph nodes.
func WithSubgraphs(graphs map[string]*pipeline.Graph) RegistryOption {
	return func(c *registryConfig) {
		c.subgraphs = graphs
	}
}

// NewRegistryFactory returns a RegistryFactory that creates child registries
// with event handlers scoped to the parent node ID. This enables subgraph nodes
// to emit events that are namespaced (e.g., "SubgraphNode/ChildAgent") so the
// TUI can distinguish events from nested pipelines.
func NewRegistryFactory(opts ...RegistryOption) pipeline.RegistryFactory {
	return func(childGraph *pipeline.Graph, parentNodeID string) *pipeline.HandlerRegistry {
		// Build a config from the original options to extract event handlers.
		parentCfg := &registryConfig{}
		for _, opt := range opts {
			opt(parentCfg)
		}

		// Create scoped options: wrap agent events with node namespacing.
		scopedOpts := make([]RegistryOption, len(opts))
		copy(scopedOpts, opts)

		if parentCfg.agentEvents != nil {
			scopedOpts = append(scopedOpts, WithAgentEventHandler(
				agent.NodeScopedHandler(parentNodeID, parentCfg.agentEvents),
			))
		}
		if parentCfg.pipelineEvents != nil {
			scopedOpts = append(scopedOpts, WithPipelineEventHandler(
				pipeline.NodeScopedPipelineHandler(parentNodeID, parentCfg.pipelineEvents),
			))
		}

		// Override the interviewer graph to use the child graph, not the parent.
		// WithInterviewer's graph field is used by the human handler to look up
		// outgoing edge labels — it must match the graph being executed.
		if parentCfg.interviewer != nil {
			scopedOpts = append(scopedOpts, WithInterviewer(parentCfg.interviewer, childGraph))
		}

		return NewDefaultRegistry(childGraph, scopedOpts...)
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
	registry.Register(NewParallelHandler(graph, registry, cfg.pipelineEvents))

	// Codergen: prefer real handler, fall back to stub.
	if cfg.llmClient != nil {
		handler := NewCodergenHandler(cfg.llmClient, cfg.workingDir, WithGraphAttrs(graph.Attrs))
		handler.env = cfg.execEnv
		handler.eventHandler = cfg.agentEvents
		handler.nativeBackend = NewNativeBackend(cfg.llmClient, cfg.execEnv)
		handler.defaultBackendName = cfg.defaultBackend
		handler.tokenTracker = cfg.tokenTracker
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

	// Subgraph handler: register if subgraphs are provided.
	if cfg.subgraphs != nil && len(cfg.subgraphs) > 0 {
		factory := NewRegistryFactory(opts...)
		registry.Register(pipeline.NewSubgraphHandler(
			cfg.subgraphs, registry, cfg.pipelineEvents, factory,
		))
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
