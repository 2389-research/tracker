// ABOUTME: Handler that executes a referenced sub-pipeline as a single node step.
// ABOUTME: Enables composition of pipelines via the "subgraph" node shape.
package pipeline

import (
	"context"
	"fmt"
)

// SubgraphHandler executes a named sub-pipeline inline as a single handler step.
// It looks up the referenced graph by the node's "subgraph_ref" attribute and runs
// it with the parent's context values as initial context.
type SubgraphHandler struct {
	graphs   map[string]*Graph
	registry *HandlerRegistry
}

// NewSubgraphHandler creates a handler that can execute any of the provided named graphs.
func NewSubgraphHandler(graphs map[string]*Graph, registry *HandlerRegistry) *SubgraphHandler {
	return &SubgraphHandler{graphs: graphs, registry: registry}
}

// Name returns the handler name used for registry lookup.
func (h *SubgraphHandler) Name() string {
	return "subgraph"
}

// Execute runs the referenced sub-pipeline and maps its result to an Outcome.
// If the subgraph node has params, they are injected into the child graph before execution.
func (h *SubgraphHandler) Execute(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
	ref, ok := node.Attrs["subgraph_ref"]
	if !ok || ref == "" {
		return Outcome{Status: OutcomeFail}, fmt.Errorf("node %q missing subgraph_ref attribute", node.ID)
	}

	subGraph, ok := h.graphs[ref]
	if !ok {
		return Outcome{Status: OutcomeFail}, fmt.Errorf("subgraph %q not found", ref)
	}

	// Parse params from node attrs if present
	params := ParseSubgraphParams(node.Attrs["subgraph_params"])
	
	// Inject params into graph (creates clone if params exist)
	subGraphWithParams, err := InjectParamsIntoGraph(subGraph, params)
	if err != nil {
		return Outcome{Status: OutcomeFail}, fmt.Errorf("failed to inject params into subgraph %q: %w", ref, err)
	}

	// Create a sub-engine with the parent's context snapshot as initial values.
	engine := NewEngine(subGraphWithParams, h.registry, WithInitialContext(pctx.Snapshot()))
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
