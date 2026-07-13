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

	subGraphWithParams, err := injectSubgraphParams(subGraph, node, ref)
	if err != nil {
		return Outcome{Status: OutcomeFail}, err
	}

	engine := h.buildSubgraphChildEngine(ctx, pctx, subGraphWithParams, node.ID)
	result, err := engine.Run(ctx)
	if err != nil {
		return Outcome{Status: OutcomeFail}, fmt.Errorf("subgraph %q execution failed: %w", ref, err)
	}

	return mapSubgraphResult(node.ID, result), nil
}

// injectSubgraphParams merges the child workflow's own vars defaults with
// parent-provided subgraph_params (parent wins — the child's declared vars
// only supply fallbacks for keys the parent didn't explicitly pass; without
// this merge, the pre-expansion pass in InjectParamsIntoGraph would resolve
// ${params.foo} to "" when foo is declared as a child var but not passed
// from the parent, silently losing the default), injects them into a clone
// of subGraph, and writes the merged effective params back onto the clone's
// Attrs so any runtime handler reading graph.Attrs sees the overridden
// values (not just the child's original defaults).
func injectSubgraphParams(subGraph *Graph, node *Node, ref string) (*Graph, error) {
	childDefaults := ExtractParamsFromGraphAttrs(subGraph.Attrs)
	parentOverrides := ParseSubgraphParams(node.Attrs["subgraph_params"])
	params := make(map[string]string, len(childDefaults)+len(parentOverrides))
	for k, v := range childDefaults {
		params[k] = v
	}
	for k, v := range parentOverrides {
		params[k] = v
	}

	subGraphWithParams, err := InjectParamsIntoGraph(subGraph, params)
	if err != nil {
		return nil, fmt.Errorf("failed to inject params into subgraph %q: %w", ref, err)
	}

	if subGraphWithParams.Attrs == nil {
		subGraphWithParams.Attrs = make(map[string]string)
	}
	for k, v := range params {
		subGraphWithParams.Attrs[GraphParamAttrKey(k)] = v
	}
	return subGraphWithParams, nil
}

// buildSubgraphChildEngine assembles the child engine: a scoped pipeline
// event handler (child node IDs prefixed with the parent node ID) and
// registry, the parent's context snapshot, and — when the parent engine
// stashed a budget guard + usage baseline on ctx — propagation of both to
// the child so (a) the child engine halts if combined parent+child spend
// breaches a --max-tokens / --max-cost ceiling mid-subgraph, and (b) child
// usage still flows back via Outcome.ChildUsage. Prior to #183 neither was
// done, which made subgraphs a full bypass of operator-configured budgets.
func (h *SubgraphHandler) buildSubgraphChildEngine(ctx context.Context, pctx *PipelineContext, subGraphWithParams *Graph, nodeID string) *Engine {
	scopedPipeline := NodeScopedPipelineHandler(nodeID, h.pipelineEvents)
	childRegistry := h.registry
	if h.registryFactory != nil {
		childRegistry = h.registryFactory(subGraphWithParams, nodeID)
	}

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
	return NewEngine(subGraphWithParams, childRegistry, childOpts...)
}

// mapSubgraphResult maps a completed child EngineResult to the subgraph
// node's Outcome.
//
// A child-side budget halt is not translated to OutcomeFail: the parent's
// own between-node budget check will see the rolled-up ChildUsage and fire
// with the correct OutcomeBudgetExceeded status. Mapping it to fail here
// would trip the engine's strict-failure-edges rule before the parent's
// budget check runs, masking the real reason for the halt.
//
// OutcomeValidationOverridden gets the same Success treatment for parent
// routing — the child's override doesn't redirect the parent's edge
// selection (parent decides its own routing). The override flag is
// propagated up via ChildOverride; the parent's engine absorbs it into the
// sticky list and the terminal-status rule flips Success →
// ValidationOverridden at the parent level.
//
// ChildOverride prepends this subgraph node's ID to each ValidationOverride
// entry's SubgraphPath (outermost-to-innermost ordering: a child override
// originating at "Gate" inside a "L2" subgraph that itself runs inside an
// "L1" subgraph terminates at the outermost engine with
// SubgraphPath=["L1", "L2"] and GateNodeID="Gate"). The recursive prepend
// lives in PrependSubgraphPath — each level adds its own ID to the front, so
// by the time control returns to the outermost run the path enumerates the
// nesting chain leaf-up.
//
// ChildUsage propagates the child's aggregated usage up to the parent trace
// so BudgetGuard checks between parent nodes, per-provider CLI rollups, and
// library-level EngineResult.Usage all see subgraph spend.
func mapSubgraphResult(nodeID string, result *EngineResult) Outcome {
	var status TerminalStatus
	switch result.Status {
	case OutcomeSuccess, OutcomeBudgetExceeded, OutcomeValidationOverridden:
		status = OutcomeSuccess
	default:
		status = OutcomeFail
	}

	return Outcome{
		Status:         status,
		ContextUpdates: result.Context,
		ChildUsage:     result.Usage,
		ChildOverride:  PrependSubgraphPath(result.ValidationOverrides, nodeID),
	}
}
