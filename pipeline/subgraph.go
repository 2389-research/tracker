// ABOUTME: Handler that executes a referenced sub-pipeline as a single node step.
// ABOUTME: Enables composition of pipelines via the "subgraph" node shape.
package pipeline

import (
	"context"
	"fmt"
)

// RegistryFactory creates a child HandlerRegistry with event handlers scoped
// to the given parentNodeID. This allows subgraph handlers to create child
// registries where agent events are prefixed with the parent node's ID,
// without the pipeline package needing to import the agent package.
type RegistryFactory func(graph *Graph, parentNodeID string) *HandlerRegistry

// SubgraphHandler executes a named sub-pipeline inline as a single handler step.
// It looks up the referenced graph by the node's "subgraph_ref" attribute and runs
// it with the parent's context values as initial context.
type SubgraphHandler struct {
	graphs          map[string]*Graph
	registry        *HandlerRegistry
	pipelineEvents  PipelineEventHandler
	registryFactory RegistryFactory
}

// NewSubgraphHandler creates a handler that can execute any of the provided named graphs.
// The pipelineEvents handler receives scoped events from child engine execution.
// The registryFactory creates child registries with scoped agent event handlers.
func NewSubgraphHandler(
	graphs map[string]*Graph,
	registry *HandlerRegistry,
	pipelineEvents PipelineEventHandler,
	factory RegistryFactory,
) *SubgraphHandler {
	if registry == nil && factory == nil {
		panic("NewSubgraphHandler: registry and factory cannot both be nil")
	}
	if pipelineEvents == nil {
		pipelineEvents = PipelineNoopHandler
	}
	return &SubgraphHandler{
		graphs:          graphs,
		registry:        registry,
		pipelineEvents:  pipelineEvents,
		registryFactory: factory,
	}
}

// Name returns the handler name used for registry lookup.
func (h *SubgraphHandler) Name() string {
	return "subgraph"
}

// Execute runs the referenced sub-pipeline and maps its result to an Outcome.
// If the subgraph node has params, they are injected into the child graph before execution.
// Pipeline and agent events from the child engine are scoped with the parent node ID
// so the TUI can distinguish subgraph nodes from parent nodes.
func (h *SubgraphHandler) Execute(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
	ref, ok := node.Attrs["subgraph_ref"]
	if !ok || ref == "" {
		return Outcome{Status: OutcomeFail}, fmt.Errorf("node %q missing subgraph_ref attribute", node.ID)
	}

	subGraph, ok := h.graphs[ref]
	if !ok {
		return Outcome{Status: OutcomeFail}, fmt.Errorf("subgraph %q not found", ref)
	}

	// Merge child workflow's own vars defaults with parent-provided
	// subgraph_params. Parent overrides win — the child's declared vars
	// only supply fallbacks for keys the parent didn't explicitly pass.
	// Without this merge, the pre-expansion pass in InjectParamsIntoGraph
	// would resolve ${params.foo} to "" when foo is declared as a child
	// var but not passed from the parent, silently losing the default.
	childDefaults := ExtractParamsFromGraphAttrs(subGraph.Attrs)
	parentOverrides := ParseSubgraphParams(node.Attrs["subgraph_params"])
	params := make(map[string]string, len(childDefaults)+len(parentOverrides))
	for k, v := range childDefaults {
		params[k] = v
	}
	for k, v := range parentOverrides {
		params[k] = v
	}

	// Inject params into graph (creates clone if params exist)
	subGraphWithParams, err := InjectParamsIntoGraph(subGraph, params)
	if err != nil {
		return Outcome{Status: OutcomeFail}, fmt.Errorf("failed to inject params into subgraph %q: %w", ref, err)
	}

	// After pre-expansion, write the merged effective params back onto
	// the clone's Attrs so any runtime handler reading graph.Attrs sees
	// the overridden values (not just the child's original defaults).
	if subGraphWithParams.Attrs == nil {
		subGraphWithParams.Attrs = make(map[string]string)
	}
	for k, v := range params {
		subGraphWithParams.Attrs[GraphParamAttrKey(k)] = v
	}

	// Create scoped pipeline event handler that prefixes child node IDs
	// with the parent node ID, filtering child pipeline lifecycle events.
	scopedPipeline := NodeScopedPipelineHandler(node.ID, h.pipelineEvents)

	// Build child registry with scoped agent events if factory is available.
	// The factory creates a registry where agent events carry namespaced node IDs.
	childRegistry := h.registry
	if h.registryFactory != nil {
		childRegistry = h.registryFactory(subGraphWithParams, node.ID)
	}

	// Create a sub-engine with scoped events and the parent's context snapshot.
	engine := NewEngine(subGraphWithParams, childRegistry,
		WithInitialContext(pctx.Snapshot()),
		WithPipelineEventHandler(scopedPipeline),
	)
	result, err := engine.Run(ctx)
	if err != nil {
		return Outcome{Status: OutcomeFail}, fmt.Errorf("subgraph %q execution failed: %w", ref, err)
	}

	// Map engine result status to outcome.
	status := OutcomeSuccess
	if result.Status != OutcomeSuccess {
		status = OutcomeFail
	}

	return Outcome{
		Status:         status,
		ContextUpdates: result.Context,
	}, nil
}
