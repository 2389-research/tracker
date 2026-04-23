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

	// Build child-engine options, starting with the usual context snapshot
	// + scoped event handler. Then, if the parent engine stashed a budget
	// guard + usage baseline on ctx, propagate both to the child so:
	//   (a) the child engine halts if combined parent+child spend breaches
	//       a --max-tokens / --max-cost ceiling mid-subgraph, and
	//   (b) child usage still flows back via Outcome.ChildUsage below,
	//       closing the between-node rollup.
	// Prior to #183 neither was done, which made subgraphs a full bypass
	// of operator-configured budgets.
	childOpts := []EngineOption{
		WithInitialContext(pctx.Snapshot()),
		WithPipelineEventHandler(scopedPipeline),
	}
	if runCtx := ChildRunContextFromContext(ctx); runCtx != nil {
		if runCtx.BudgetGuard != nil {
			childOpts = append(childOpts, WithBudgetGuard(runCtx.BudgetGuard))
		}
		if runCtx.Baseline != nil {
			childOpts = append(childOpts, WithBaselineUsage(runCtx.Baseline))
		}
	}

	engine := NewEngine(subGraphWithParams, childRegistry, childOpts...)
	result, err := engine.Run(ctx)
	if err != nil {
		return Outcome{Status: OutcomeFail}, fmt.Errorf("subgraph %q execution failed: %w", ref, err)
	}

	// Map engine result status to outcome. A child-side budget halt is not
	// translated to OutcomeFail: the parent's own between-node budget
	// check will see the rolled-up ChildUsage (appended below) and fire
	// with the correct OutcomeBudgetExceeded status. Mapping it to fail
	// here would trip the engine's strict-failure-edges rule before the
	// parent's budget check runs, masking the real reason for the halt.
	status := OutcomeSuccess
	switch result.Status {
	case OutcomeSuccess, OutcomeBudgetExceeded:
		status = OutcomeSuccess
	default:
		status = OutcomeFail
	}

	// Propagate the child's aggregated usage up to the parent trace so
	// BudgetGuard checks between parent nodes, per-provider CLI rollups,
	// and library-level EngineResult.Usage all see subgraph spend.
	return Outcome{
		Status:         status,
		ContextUpdates: result.Context,
		ChildUsage:     result.Usage,
	}, nil
}
