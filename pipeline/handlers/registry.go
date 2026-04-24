// ABOUTME: Convenience factory for a HandlerRegistry pre-loaded with all built-in handlers.
// ABOUTME: Uses functional options to override codergen/tool/human handlers for testing.
package handlers

import (
	"context"
	"strings"

	"github.com/2389-research/tracker/agent"
	"github.com/2389-research/tracker/agent/exec"
	"github.com/2389-research/tracker/llm"
	"github.com/2389-research/tracker/pipeline"
)

// GraphAttrToolCommandsAllow is the graph-level attribute key that authors set
// on a workflow (via DOT `graph [tool_commands_allow="..."]` or programmatically
// on `Graph.Attrs`) to restrict which tool_command invocations may run.
//
// The value is a comma-separated list of glob patterns in the same format as
// the `--tool-allowlist` CLI flag. registerToolHandler takes the UNION of the
// CLI-supplied allowlist (ToolHandlerConfig.Allowlist) and the graph attribute
// — both sources apply. An empty/missing attr means no graph-level restriction.
//
// The denylist (checkCommandDenylist) is evaluated BEFORE the allowlist inside
// CheckToolCommand, so the union never softens the security posture: a graph
// attr of `*` does not unblock `eval` or `curl | sh`.
//
// NOTE: dippin-lang v0.21.0 does not expose this field in
// ir.WorkflowDefaults — authoring it under `defaults:` in a `.dip` file
// currently trips the parser's "unknown defaults field" diagnostic. Until the
// upstream IR ships the field and pipeline/dippin_adapter.go threads it into
// Graph.Attrs, graph-attr authors must use DOT or set Attrs programmatically.
const GraphAttrToolCommandsAllow = "tool_commands_allow"

// GraphAttrToolDenylistAdd is the graph-level attribute key that authors set
// on a workflow (via DOT `graph [tool_denylist_add="..."]`, via a `defaults:
// tool_denylist_add:` block in a `.dip` file once dippin-lang v0.23.0 routes
// it into WorkflowDefaults, or programmatically on `Graph.Attrs`) to add
// extra patterns to the tool_command denylist. Patterns are additive —
// they cannot remove any built-in pattern; they can only extend the
// block list for defense in depth.
//
// The value is a comma-separated list of glob patterns in the same format
// as the `--tool-denylist-add` CLI flag. registerToolHandler takes the
// UNION of the CLI-supplied patterns (ToolHandlerConfig.DenylistAdd) and
// the graph attribute — both sources apply.
//
// `--bypass-denylist` still disables both the built-in denylist AND these
// user-added patterns (it's the intentional all-or-nothing escape hatch
// for sandboxed environments).
const GraphAttrToolDenylistAdd = "tool_denylist_add"

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
	toolSafety     ToolHandlerConfig
	toolSafetySet  bool
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

// WithToolHandlerConfig supplies security configuration for the tool handler:
// denylist bypass, allowlist patterns, and the hard output-limit ceiling. These
// values thread from CLI flags into NewToolHandlerWithConfig. When this option
// is not supplied, the tool handler is built with NewToolHandler (default safe
// behavior: denylist active, no allowlist restriction, 10MB output ceiling).
func WithToolHandlerConfig(cfg ToolHandlerConfig) RegistryOption {
	return func(c *registryConfig) {
		c.toolSafety = cfg
		c.toolSafetySet = true
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

	// Parallel handler needs the graph and registry for branch dispatch.
	registry.Register(NewParallelHandler(graph, registry, cfg.pipelineEvents))

	registerCodergenHandler(registry, cfg, graph)
	registerToolHandler(registry, cfg, graph)
	registerHumanHandler(registry, cfg, graph)
	registerSubgraphHandler(registry, cfg, opts)
	registerManagerLoopHandler(registry, cfg, opts)

	return registry
}

// registerCodergenHandler registers the codergen (LLM agent) handler or a stub.
// The stub (codergenFunc) takes priority for testing. Otherwise, the full
// CodergenHandler is registered whenever any backend is accessible — including
// when the global default is native but per-node "backend" attrs could select
// claude-code or acp. selectBackend will surface clear errors for nodes that
// need a native client when none is available.
func registerCodergenHandler(registry *pipeline.HandlerRegistry, cfg *registryConfig, graph *pipeline.Graph) {
	if cfg.codergenFunc != nil {
		registry.Register(&funcHandler{name: "codergen", fn: cfg.codergenFunc})
		return
	}
	// Register the full CodergenHandler whenever any backend could be used:
	// native (llmClient set), external (claude-code/acp global), or per-node
	// backend attrs that override the global default.
	if cfg.llmClient != nil || cfg.defaultBackend == "claude-code" || cfg.defaultBackend == "acp" || graphHasPerNodeBackend(graph) {
		handler := NewCodergenHandler(cfg.llmClient, cfg.workingDir, WithGraphAttrs(graph.Attrs))
		handler.env = cfg.execEnv
		handler.eventHandler = cfg.agentEvents
		if cfg.llmClient != nil {
			handler.nativeBackend = NewNativeBackend(cfg.llmClient, cfg.execEnv)
		}
		handler.defaultBackendName = cfg.defaultBackend
		handler.tokenTracker = cfg.tokenTracker
		registry.Register(handler)
	}
}

// graphHasPerNodeBackend returns true if any node in the graph specifies a
// non-empty "backend" attribute. Used by the handler registry to decide
// whether to register the full codergen handler even when the global default
// is native, so mixed-backend pipelines work.
func graphHasPerNodeBackend(graph *pipeline.Graph) bool {
	if graph == nil {
		return false
	}
	for _, n := range graph.Nodes {
		if n.Attrs["backend"] != "" {
			return true
		}
	}
	return false
}

// registerToolHandler registers the tool handler or a stub.
// When WithToolHandlerConfig was supplied, the handler is built with the
// CLI-provided safety config (denylist bypass, allowlist, output ceiling).
// Otherwise the default-safe handler is used.
//
// Allowlist source-of-truth is the UNION of:
//  1. CLI-supplied patterns (ToolHandlerConfig.Allowlist via --tool-allowlist)
//  2. Graph attribute patterns (graph.Attrs[GraphAttrToolCommandsAllow])
//
// A command must match ANY pattern from the combined list to run. Match
// semantics do not depend on pattern order, but the merged list preserves a
// deterministic order (CLI patterns first, then graph patterns) because it
// is user-visible in the allowlist error message via strings.Join. Duplicates
// are de-duplicated regardless of source. An empty combined list means "no
// allowlist gate" and all non-denylisted commands pass. The denylist is
// always evaluated first inside CheckToolCommand — the union never softens it.
func registerToolHandler(registry *pipeline.HandlerRegistry, cfg *registryConfig, graph *pipeline.Graph) {
	if cfg.execEnv != nil {
		mergedAllow := mergeToolAllowlist(cfg.toolSafety.Allowlist, graph)
		mergedDenyAdd := mergeToolDenylistAdd(cfg.toolSafety.DenylistAdd, graph)
		graphInjected := len(mergedAllow) > len(cfg.toolSafety.Allowlist) ||
			len(mergedDenyAdd) > len(cfg.toolSafety.DenylistAdd)
		if cfg.toolSafetySet || graphInjected {
			safety := cfg.toolSafety
			safety.Allowlist = mergedAllow
			safety.DenylistAdd = mergedDenyAdd
			registry.Register(NewToolHandlerWithConfig(cfg.execEnv, safety))
		} else {
			registry.Register(NewToolHandler(cfg.execEnv))
		}
	} else if cfg.toolExecFunc != nil {
		registry.Register(&funcHandler{name: "tool", fn: cfg.toolExecFunc})
	}
}

// mergeToolAllowlist returns the union of the CLI-supplied allowlist and the
// patterns parsed from graph.Attrs[GraphAttrToolCommandsAllow]. The graph attr
// value is a comma-separated glob list (same format as --tool-allowlist);
// whitespace around each pattern is trimmed and empty tokens are dropped.
// De-duplication is order-preserving: CLI patterns retain their position,
// then new graph patterns append in declaration order. De-dup runs on ALL
// inputs, including the CLI-only path where the graph attr is empty —
// duplicates in the CLI list alone are still collapsed.
func mergeToolAllowlist(cliAllowlist []string, graph *pipeline.Graph) []string {
	graphPatterns := parseGraphAllowlist(graph)
	if len(cliAllowlist) == 0 && len(graphPatterns) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(cliAllowlist)+len(graphPatterns))
	merged := make([]string, 0, len(cliAllowlist)+len(graphPatterns))
	for _, p := range cliAllowlist {
		if _, dup := seen[p]; dup {
			continue
		}
		seen[p] = struct{}{}
		merged = append(merged, p)
	}
	for _, p := range graphPatterns {
		if _, dup := seen[p]; dup {
			continue
		}
		seen[p] = struct{}{}
		merged = append(merged, p)
	}
	return merged
}

// parseGraphAllowlist extracts a cleaned pattern list from the
// tool_commands_allow graph attribute. Comma-separates, trims whitespace, and
// drops empty tokens. Returns nil when the attr is missing or yields no
// non-empty tokens.
func parseGraphAllowlist(graph *pipeline.Graph) []string {
	return parseGraphCommaList(graph, GraphAttrToolCommandsAllow)
}

// parseGraphDenylistAdd extracts the user-added denylist patterns from the
// tool_denylist_add graph attribute. Same format + trim semantics as
// parseGraphAllowlist.
func parseGraphDenylistAdd(graph *pipeline.Graph) []string {
	return parseGraphCommaList(graph, GraphAttrToolDenylistAdd)
}

// parseGraphCommaList is the shared comma-separated-glob parser for tool
// safety graph attrs. Factored out so the allowlist and denylist-add paths
// apply the same trim-and-drop-empty semantics — avoids the second site
// from silently diverging on whitespace handling.
func parseGraphCommaList(graph *pipeline.Graph, key string) []string {
	if graph == nil || graph.Attrs == nil {
		return nil
	}
	raw, ok := graph.Attrs[key]
	if !ok || strings.TrimSpace(raw) == "" {
		return nil
	}
	var patterns []string
	for _, part := range strings.Split(raw, ",") {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			patterns = append(patterns, trimmed)
		}
	}
	return patterns
}

// mergeToolDenylistAdd returns the union of the CLI-supplied user-denylist
// patterns and the patterns parsed from graph.Attrs[GraphAttrToolDenylistAdd].
// Same order-preserving de-dup rules as mergeToolAllowlist: CLI patterns
// first, then graph patterns in declaration order; duplicates collapse.
// These augment the built-in denylist — they never remove any built-in
// pattern, and --bypass-denylist still disables them alongside the
// built-ins.
func mergeToolDenylistAdd(cliPatterns []string, graph *pipeline.Graph) []string {
	graphPatterns := parseGraphDenylistAdd(graph)
	if len(cliPatterns) == 0 && len(graphPatterns) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(cliPatterns)+len(graphPatterns))
	merged := make([]string, 0, len(cliPatterns)+len(graphPatterns))
	for _, p := range cliPatterns {
		if _, dup := seen[p]; dup {
			continue
		}
		seen[p] = struct{}{}
		merged = append(merged, p)
	}
	for _, p := range graphPatterns {
		if _, dup := seen[p]; dup {
			continue
		}
		seen[p] = struct{}{}
		merged = append(merged, p)
	}
	return merged
}

// registerHumanHandler registers the human gate handler or a stub.
func registerHumanHandler(registry *pipeline.HandlerRegistry, cfg *registryConfig, graph *pipeline.Graph) {
	if cfg.interviewer != nil {
		humanGraph := cfg.graph
		if humanGraph == nil {
			humanGraph = graph
		}
		registry.Register(NewHumanHandler(cfg.interviewer, humanGraph))
	} else if cfg.humanCallback != nil {
		registry.Register(&funcHandler{name: "wait.human", fn: cfg.humanCallback})
	}
}

// registerSubgraphHandler registers the subgraph handler if subgraphs are provided.
func registerSubgraphHandler(registry *pipeline.HandlerRegistry, cfg *registryConfig, opts []RegistryOption) {
	if len(cfg.subgraphs) > 0 {
		factory := NewRegistryFactory(opts...)
		registry.Register(pipeline.NewSubgraphHandler(
			cfg.subgraphs, registry, cfg.pipelineEvents, factory,
		))
	}
}

// registerManagerLoopHandler registers the manager loop handler. When subgraphs are
// provided, the handler gets full dependencies for launching child pipelines. Otherwise
// a fallback handler is registered that returns clear errors at Execute time (keeps
// the handler name resolvable for conformance tests and validation).
func registerManagerLoopHandler(registry *pipeline.HandlerRegistry, cfg *registryConfig, opts []RegistryOption) {
	if len(cfg.subgraphs) > 0 {
		factory := NewRegistryFactory(opts...)
		registry.Register(NewManagerLoopHandler(
			cfg.subgraphs, registry, cfg.pipelineEvents, factory,
		))
	} else {
		registry.Register(NewManagerLoopHandler(nil, nil, cfg.pipelineEvents, nil))
	}
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
