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

	// #556: bind the parent's subgraph_params to the child's declared inputs —
	// validate against the child's `inputs` signature (fail closed on a missing
	// required or invalid value) and seed the child's closed inputs.* namespace,
	// so a subgraph call site drives a child's inputs the same way a top-level
	// run does. dippin lints the arity cross-file (DIP160); this is the runtime
	// half.
	inputSeed, ierr := bindSubgraphInputs(subGraph, node, ref)
	if ierr != nil {
		return Outcome{Status: OutcomeFail}, ierr
	}

	engine := h.buildSubgraphChildEngine(ctx, pctx, subGraphWithParams, node.ID, inputSeed)
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

// bindSubgraphInputs validates the parent's subgraph_params against the child's
// declared VALUE-kind inputs (text/number/bool/enum) and returns the seed for
// the child's inputs.* namespace (name → "inputs.<name>"). It fails closed on a
// missing-required or constraint-violating input, mirroring the top-level
// bindInputs. A params key that is not a declared input is a workflow var, not
// an error — the unknown_input class is dropped.
//
// file/secret inputs are NOT bindable from a subgraph call site: a params value
// is an inline string, but a file/secret input must be staged to a 0600 file
// (which needs a workdir and byte/path source the call site doesn't have). Such
// inputs are filtered out here — the child resolves them by its own means (a
// staged top-level input, a repo file, a default). See #556 / #555.
func bindSubgraphInputs(subGraph *Graph, node *Node, ref string) (map[string]string, error) {
	valueInputs := valueKindInputs(subGraph.Inputs)
	if len(valueInputs) == 0 {
		return nil, nil
	}
	params := ParseSubgraphParams(node.Attrs["subgraph_params"])
	seed, errs := ValidateInputValues(valueInputs, params)
	if fatal := fatalSubgraphInputErrors(errs); len(fatal) > 0 {
		return nil, fmt.Errorf("subgraph %q: invalid inputs: %v", ref, fatal)
	}
	if len(seed) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(seed))
	for name, v := range seed {
		out[inputContextPrefix+name] = v
	}
	return out, nil
}

// valueKindInputs returns the declared inputs that can be bound from an inline
// params string — text/number/bool/enum. file/secret are excluded (they require
// staging; see bindSubgraphInputs).
func valueKindInputs(inputs []InputSpec) []InputSpec {
	var out []InputSpec
	for _, in := range inputs {
		switch in.Kind {
		case InputText, InputNumber, InputBool, InputEnum:
			out = append(out, in)
		}
	}
	return out
}

// fatalSubgraphInputErrors drops the unknown_input class (a params key that is a
// workflow var, not a declared input) and returns the errors that must fail the
// subgraph — missing required, type/constraint/enum violations.
func fatalSubgraphInputErrors(errs []InputError) []InputError {
	var fatal []InputError
	for _, e := range errs {
		if e.Kind != ErrUnknownInput {
			fatal = append(fatal, e)
		}
	}
	return fatal
}

// buildSubgraphChildEngine assembles the child engine: a scoped pipeline
// event handler (child node IDs prefixed with the parent node ID) and
// registry, the parent's context snapshot, and — when the parent engine
// stashed a budget guard + usage baseline on ctx — propagation of both to
// the child so (a) the child engine halts if combined parent+child spend
// breaches a --max-tokens / --max-cost ceiling mid-subgraph, and (b) child
// usage still flows back via Outcome.ChildUsage. Prior to #183 neither was
// done, which made subgraphs a full bypass of operator-configured budgets.
func (h *SubgraphHandler) buildSubgraphChildEngine(ctx context.Context, pctx *PipelineContext, subGraphWithParams *Graph, nodeID string, inputSeed map[string]string) *Engine {
	scopedPipeline := NodeScopedPipelineHandler(nodeID, h.pipelineEvents)
	childRegistry := h.registry
	if h.registryFactory != nil {
		childRegistry = h.registryFactory(subGraphWithParams, nodeID)
	}

	// Start from the parent's context snapshot (existing subgraph inheritance),
	// then overlay the child's bound inputs.* so the child's declared inputs win
	// over any same-named key inherited from the parent (#556).
	initCtx := pctx.Snapshot()
	for k, v := range inputSeed {
		initCtx[k] = v
	}
	childOpts := []EngineOption{
		WithInitialContext(initCtx),
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
