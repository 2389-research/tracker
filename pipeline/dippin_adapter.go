// ABOUTME: Adapter that converts Dippin IR (from dippin-lang parser) to Tracker's Graph model.
// ABOUTME: Provides FromDippinIR() to enable tracker to execute .dip files natively.
package pipeline

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/2389-research/dippin-lang/ir"
)

// FromDippinIR converts a Dippin IR Workflow to a Tracker Graph.
// The resulting Graph is semantically equivalent to one produced by ParseDOT
// for the same workflow, enabling transparent interoperability.
//
// Field mappings:
//   - IR Workflow.Name → Graph.Name
//   - IR Workflow.Start → Graph.StartNode
//   - IR Workflow.Exit → Graph.ExitNode
//   - IR Workflow.Defaults → Graph.Attrs (flattened)
//   - IR Node → Graph.Node (with kind → shape mapping)
//   - IR Edge → Graph.Edge (with condition serialization)
//
// Returns an error if:
//   - workflow is nil
//   - Start or Exit are empty
//   - A node has an unknown NodeKind
func FromDippinIR(workflow *ir.Workflow) (*Graph, error) {
	if workflow == nil {
		return nil, fmt.Errorf("nil workflow")
	}
	if workflow.Start == "" {
		return nil, fmt.Errorf("workflow missing Start node")
	}
	if workflow.Exit == "" {
		return nil, fmt.Errorf("workflow missing Exit node")
	}

	g := NewGraph(workflow.Name)
	g.StartNode = workflow.Start
	g.ExitNode = workflow.Exit

	// Map workflow-level goal to graph attributes (used by prompt expansion, fidelity, context)
	if workflow.Goal != "" {
		g.Attrs["goal"] = workflow.Goal
	}

	// Map workflow-level defaults to graph attributes
	extractWorkflowDefaults(workflow.Defaults, g.Attrs)

	// Map IR nodes to Graph nodes, preserving declaration order.
	for _, irNode := range workflow.Nodes {
		gNode, err := convertNode(irNode)
		if err != nil {
			return nil, fmt.Errorf("node %s: %w", irNode.ID, err)
		}
		g.AddNode(gNode)
		g.NodeOrder = append(g.NodeOrder, irNode.ID)
	}

	// Map IR edges to Graph edges
	for _, irEdge := range workflow.Edges {
		gEdge := convertEdge(irEdge)
		g.AddEdge(gEdge)
	}

	// Synthesize implicit edges from parallel fan-out targets and fan-in sources.
	// The dippin IR stores these in ParallelConfig.Targets and FanInConfig.Sources
	// rather than as explicit edges, but tracker's Graph.OutgoingEdges requires
	// real Edge entries to traverse the graph. Skip if the edge already exists
	// from the explicit edge list to avoid duplicates.
	existingEdges := make(map[[2]string]bool)
	for _, e := range g.Edges {
		existingEdges[[2]string{e.From, e.To}] = true
	}
	// Build a lookup of fan-in nodes by their source sets, so we can link
	// parallel nodes directly to their corresponding fan-in join node.
	// The parallel handler dispatches branches internally — the engine only
	// needs an edge from the parallel node to the join node to advance.
	fanInBySource := make(map[string]string) // source node ID -> fan-in node ID
	for _, irNode := range workflow.Nodes {
		if cfg, ok := irNode.Config.(ir.FanInConfig); ok {
			for _, source := range cfg.Sources {
				fanInBySource[source] = irNode.ID
			}
		}
	}

	for _, irNode := range workflow.Nodes {
		switch cfg := irNode.Config.(type) {
		case ir.ParallelConfig:
			// Synthesize edges from parallel to each branch target (for BFS
			// node discovery in the TUI) AND from parallel to the fan-in
			// join node (for engine navigation after the handler completes).
			for _, target := range cfg.Targets {
				key := [2]string{irNode.ID, target}
				if !existingEdges[key] {
					g.AddEdge(&Edge{From: irNode.ID, To: target})
					existingEdges[key] = true
				}
			}
			// Also link parallel -> fan-in directly. The parallel handler
			// stores this as "parallel_join" so it can set suggested_next_nodes.
			if len(cfg.Targets) > 0 {
				if joinID, ok := fanInBySource[cfg.Targets[0]]; ok {
					key := [2]string{irNode.ID, joinID}
					if !existingEdges[key] {
						g.AddEdge(&Edge{From: irNode.ID, To: joinID})
						existingEdges[key] = true
					}
					// Store the join node ID so the parallel handler can hint
					// the engine to navigate there after execution.
					if node, ok := g.Nodes[irNode.ID]; ok {
						node.Attrs["parallel_join"] = joinID
					}
				}
			}
		case ir.FanInConfig:
			// Fan-in source edges are needed so BFS can discover the branch
			// nodes (for the TUI node list). These edges exist in the graph
			// but the engine skips them — after the parallel handler completes,
			// the engine follows the Parallel -> FanIn edge directly.
			for _, source := range cfg.Sources {
				key := [2]string{source, irNode.ID}
				if !existingEdges[key] {
					g.AddEdge(&Edge{From: source, To: irNode.ID})
					existingEdges[key] = true
				}
			}
		}
	}

	// Ensure start/exit nodes exist
	if err := ensureStartExitNodes(g); err != nil {
		return nil, err
	}

	// Convert stylesheet rules to graph attrs for engine resolution.
	if len(workflow.Stylesheet) > 0 {
		g.Attrs["model_stylesheet"] = serializeStylesheet(workflow.Stylesheet)
	}

	// Mark that this graph was produced from validated Dippin IR.
	// Tracker's validateGraph will skip structural checks that
	// Dippin already covers (DIP001–DIP006).
	g.DippinValidated = true

	return g, nil
}

// NodeKindToShape maps IR NodeKind to DOT shape strings.
// This mapping ensures the Graph produced by FromDippinIR matches
// the shape convention used by ParseDOT, maintaining handler compatibility.
var nodeKindToShapeMap = map[ir.NodeKind]string{
	ir.NodeAgent:    "box",           // → codergen
	ir.NodeHuman:    "hexagon",       // → wait.human
	ir.NodeTool:     "parallelogram", // → tool
	ir.NodeParallel: "component",     // → parallel
	ir.NodeFanIn:    "tripleoctagon", // → parallel.fan_in
	ir.NodeSubgraph: "tab",           // → subgraph
}

// NodeKindToShape returns the DOT shape for a given NodeKind.
// Returns ("", false) if the kind is not recognized.
func NodeKindToShape(kind ir.NodeKind) (string, bool) {
	shape, ok := nodeKindToShapeMap[kind]
	return shape, ok
}

// convertNode transforms an IR Node to a Graph Node.
// Extracts configuration from the NodeConfig union into flat string attrs.
func convertNode(irNode *ir.Node) (*Node, error) {
	shape, ok := NodeKindToShape(irNode.Kind)
	if !ok {
		return nil, fmt.Errorf("unknown node kind: %s", irNode.Kind)
	}

	gNode := &Node{
		ID:    irNode.ID,
		Shape: shape,
		Label: irNode.Label,
		Attrs: make(map[string]string),
	}

	// Extract kind-specific config into attrs
	if err := extractNodeAttrs(irNode.Config, gNode.Attrs); err != nil {
		return nil, err
	}

	// Extract retry config
	extractRetryAttrs(irNode.Retry, gNode.Attrs)

	// Extract IO declarations (reads/writes)
	extractNodeIO(irNode.IO, gNode.Attrs)

	return gNode, nil
}

// extractNodeAttrs flattens IR NodeConfig into string attributes.
// Each NodeConfig type maps to specific attribute keys expected by handlers.
// Handles both value and pointer types for compatibility.
func extractNodeAttrs(config ir.NodeConfig, attrs map[string]string) error {
	if config == nil {
		return nil
	}

	switch cfg := config.(type) {
	case ir.AgentConfig:
		extractAgentAttrs(cfg, attrs)
	case *ir.AgentConfig:
		extractAgentAttrs(*cfg, attrs)

	case ir.HumanConfig:
		extractHumanAttrs(cfg, attrs)
	case *ir.HumanConfig:
		extractHumanAttrs(*cfg, attrs)

	case ir.ToolConfig:
		extractToolAttrs(cfg, attrs)
	case *ir.ToolConfig:
		extractToolAttrs(*cfg, attrs)

	case ir.ParallelConfig:
		extractParallelAttrs(cfg, attrs)
	case *ir.ParallelConfig:
		extractParallelAttrs(*cfg, attrs)

	case ir.FanInConfig:
		extractFanInAttrs(cfg, attrs)
	case *ir.FanInConfig:
		extractFanInAttrs(*cfg, attrs)

	case ir.SubgraphConfig:
		extractSubgraphAttrs(cfg, attrs)
	case *ir.SubgraphConfig:
		extractSubgraphAttrs(*cfg, attrs)

	default:
		return fmt.Errorf("unknown config type: %T", config)
	}

	return nil
}

func extractAgentAttrs(cfg ir.AgentConfig, attrs map[string]string) {
	if cfg.Prompt != "" {
		attrs["prompt"] = cfg.Prompt
	}
	if cfg.SystemPrompt != "" {
		attrs["system_prompt"] = cfg.SystemPrompt
	}
	if cfg.Model != "" {
		attrs["llm_model"] = cfg.Model
	}
	if cfg.Provider != "" {
		attrs["llm_provider"] = cfg.Provider
	}
	if cfg.MaxTurns > 0 {
		attrs["max_turns"] = strconv.Itoa(cfg.MaxTurns)
	}
	if cfg.CmdTimeout > 0 {
		attrs["command_timeout"] = cfg.CmdTimeout.String()
	}
	if cfg.CacheTools {
		attrs["cache_tool_results"] = "true"
	}
	if cfg.Compaction != "" {
		attrs["context_compaction"] = cfg.Compaction
	}
	if cfg.CompactionThreshold > 0 {
		attrs["context_compaction_threshold"] = fmt.Sprintf("%.2f", cfg.CompactionThreshold)
	}
	if cfg.ReasoningEffort != "" {
		attrs["reasoning_effort"] = cfg.ReasoningEffort
	}
	if cfg.Fidelity != "" {
		attrs["fidelity"] = cfg.Fidelity
	}
	if cfg.AutoStatus {
		attrs["auto_status"] = "true"
	}
	if cfg.GoalGate {
		attrs["goal_gate"] = "true"
	}
}

func extractHumanAttrs(cfg ir.HumanConfig, attrs map[string]string) {
	if cfg.Mode != "" {
		attrs["mode"] = cfg.Mode
	}
	if cfg.Default != "" {
		attrs["default_choice"] = cfg.Default
	}
}

func extractToolAttrs(cfg ir.ToolConfig, attrs map[string]string) {
	if cfg.Command != "" {
		attrs["tool_command"] = cfg.Command
	}
	if cfg.Timeout > 0 {
		attrs["timeout"] = cfg.Timeout.String()
	}
}

func extractParallelAttrs(cfg ir.ParallelConfig, attrs map[string]string) {
	if len(cfg.Targets) > 0 {
		attrs["parallel_targets"] = strings.Join(cfg.Targets, ",")
	}
	// Per-branch config (block form) — serialize as namespaced attrs for handler use.
	// The parallel handler reads branch.N.* to override target node attrs per-branch.
	for i, branch := range cfg.Branches {
		prefix := fmt.Sprintf("branch.%d.", i)
		attrs[prefix+"target"] = branch.Target
		if branch.Model != "" {
			attrs[prefix+"llm_model"] = branch.Model
		}
		if branch.Provider != "" {
			attrs[prefix+"llm_provider"] = branch.Provider
		}
		if branch.Fidelity != "" {
			attrs[prefix+"fidelity"] = branch.Fidelity
		}
	}
}

func extractFanInAttrs(cfg ir.FanInConfig, attrs map[string]string) {
	if len(cfg.Sources) > 0 {
		attrs["fan_in_sources"] = strings.Join(cfg.Sources, ",")
	}
}

func extractSubgraphAttrs(cfg ir.SubgraphConfig, attrs map[string]string) {
	if cfg.Ref != "" {
		attrs["subgraph_ref"] = cfg.Ref
	}
	if len(cfg.Params) > 0 {
		// Serialize params as comma-separated key=value pairs
		var pairs []string
		for k, v := range cfg.Params {
			pairs = append(pairs, fmt.Sprintf("%s=%s", k, v))
		}
		attrs["subgraph_params"] = strings.Join(pairs, ",")
	}
}

// extractRetryAttrs converts IR RetryConfig to string attributes.
func extractRetryAttrs(retry ir.RetryConfig, attrs map[string]string) {
	if retry.Policy != "" {
		attrs["retry_policy"] = retry.Policy
	}
	if retry.MaxRetries > 0 {
		attrs["max_retries"] = strconv.Itoa(retry.MaxRetries)
	}
	if retry.BaseDelay > 0 {
		attrs["base_delay"] = retry.BaseDelay.String()
	}
	if retry.RetryTarget != "" {
		attrs["retry_target"] = retry.RetryTarget
	}
	if retry.FallbackTarget != "" {
		attrs["fallback_retry_target"] = retry.FallbackTarget
	}
}

// extractNodeIO converts IR NodeIO (reads/writes) to string attributes.
func extractNodeIO(io ir.NodeIO, attrs map[string]string) {
	if len(io.Reads) > 0 {
		attrs["reads"] = strings.Join(io.Reads, ",")
	}
	if len(io.Writes) > 0 {
		attrs["writes"] = strings.Join(io.Writes, ",")
	}
}

// extractWorkflowDefaults maps IR WorkflowDefaults to graph-level attributes.
// These provide fallback values for nodes that don't specify per-node config.
func extractWorkflowDefaults(defaults ir.WorkflowDefaults, attrs map[string]string) {
	if defaults.Model != "" {
		attrs["llm_model"] = defaults.Model
	}
	if defaults.Provider != "" {
		attrs["llm_provider"] = defaults.Provider
	}
	if defaults.RetryPolicy != "" {
		attrs["default_retry_policy"] = defaults.RetryPolicy
	}
	if defaults.MaxRetries > 0 {
		attrs["default_max_retry"] = strconv.Itoa(defaults.MaxRetries)
	}
	if defaults.Fidelity != "" {
		attrs["default_fidelity"] = defaults.Fidelity
	}
	if defaults.MaxRestarts > 0 {
		attrs["max_restarts"] = strconv.Itoa(defaults.MaxRestarts)
	}
	if defaults.RestartTarget != "" {
		attrs["restart_target"] = defaults.RestartTarget
	}
	if defaults.CacheTools {
		attrs["cache_tool_results"] = "true"
	}
	if defaults.Compaction != "" {
		attrs["context_compaction"] = defaults.Compaction
	}
	if defaults.OnResume != "" {
		attrs["on_resume"] = defaults.OnResume
	}
}

// convertEdge transforms an IR Edge to a Graph Edge.
// Serializes the parsed Condition back to a raw string for the tracker engine.
func convertEdge(irEdge *ir.Edge) *Edge {
	gEdge := &Edge{
		From:  irEdge.From,
		To:    irEdge.To,
		Label: irEdge.Label,
		Attrs: make(map[string]string),
	}

	// Serialize condition if present
	if irEdge.Condition != nil {
		gEdge.Condition = irEdge.Condition.Raw
		gEdge.Attrs["condition"] = irEdge.Condition.Raw
	}

	// Preserve weight
	if irEdge.Weight > 0 {
		gEdge.Attrs["weight"] = strconv.Itoa(irEdge.Weight)
	}

	// Mark restart edges
	if irEdge.Restart {
		gEdge.Attrs["restart"] = "true"
	}

	return gEdge
}

// serializeStylesheet converts IR stylesheet rules to the CSS-like format
// expected by ParseStylesheet. Each rule becomes "selector { key: value; }".
func serializeStylesheet(rules []ir.StylesheetRule) string {
	var parts []string
	for _, rule := range rules {
		selector := serializeSelector(rule.Selector)
		var props []string
		for k, v := range rule.Properties {
			props = append(props, fmt.Sprintf("%s: %s", k, v))
		}
		parts = append(parts, fmt.Sprintf("%s { %s; }", selector, strings.Join(props, "; ")))
	}
	return strings.Join(parts, " ")
}

// serializeSelector converts an IR StyleSelector to CSS-like syntax.
func serializeSelector(sel ir.StyleSelector) string {
	switch sel.Kind {
	case "universal":
		return "*"
	case "kind":
		return sel.Value
	case "class":
		return "." + sel.Value
	case "id":
		return "#" + sel.Value
	default:
		return sel.Value
	}
}

// ensureStartExitNodes verifies that the start and exit nodes exist in the graph.
// Returns an error if either is missing.
func ensureStartExitNodes(g *Graph) error {
	if _, ok := g.Nodes[g.StartNode]; !ok {
		return fmt.Errorf("start node %q not found in graph", g.StartNode)
	}
	if _, ok := g.Nodes[g.ExitNode]; !ok {
		return fmt.Errorf("exit node %q not found in graph", g.ExitNode)
	}

	// Ensure start node has Mdiamond shape and start handler.
	// The node may have been added with a different shape (e.g. "box" for agent kind)
	// so we override both shape and handler to match the start/exit convention.
	startNode := g.Nodes[g.StartNode]
	if startNode.Shape != "Mdiamond" {
		startNode.Shape = "Mdiamond"
		startNode.Handler = "start"
	}

	// Ensure exit node has Msquare shape and exit handler.
	exitNode := g.Nodes[g.ExitNode]
	if exitNode.Shape != "Msquare" {
		exitNode.Shape = "Msquare"
		exitNode.Handler = "exit"
	}

	return nil
}
