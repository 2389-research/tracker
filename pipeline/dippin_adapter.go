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
	synthesizeImplicitEdges(g, workflow)

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

	// PLACEHOLDER: Backend attrs (backend, mcp_servers, permission_mode, etc.)
	// cannot be passed through from .dip files yet because ir.AgentConfig does
	// not have a Params map. The call below is intentionally a no-op (nil map).
	// When dippin-lang adds Params map[string]string to AgentConfig, replace
	// nil with cfg.Params. Until then, backend selection must use --backend
	// flag or node-level attrs set via DOT format.
	extractAgentBackendAttrs(nil, attrs)

	// Warn if the prompt or other fields contain backend-related keywords that
	// a user might expect to be forwarded but currently cannot be.
	warnUnpassableBackendKeys(cfg, attrs)
}

// extractAgentBackendAttrs maps backend-selection and Claude-Code-specific keys
// from a generic params map into node attrs consumed by CodergenHandler and
// ClaudeCodeBackend. The recognized keys are:
//
//   - backend         → attrs["backend"]          (e.g. "claude-code", "native")
//   - mcp_servers     → attrs["mcp_servers"]       (newline-separated name=cmd pairs)
//   - allowed_tools   → attrs["allowed_tools"]     (comma-separated tool names)
//   - disallowed_tools→ attrs["disallowed_tools"]  (comma-separated tool names)
//   - max_budget_usd  → attrs["max_budget_usd"]    (float string, e.g. "1.50")
//   - permission_mode → attrs["permission_mode"]   (plan|autoEdit|fullAuto)
//
// Unrecognized keys are silently ignored.
// A nil or empty params map is a no-op.
func extractAgentBackendAttrs(params map[string]string, attrs map[string]string) {
	keys := []string{
		"backend",
		"mcp_servers",
		"allowed_tools",
		"disallowed_tools",
		"max_budget_usd",
		"permission_mode",
	}
	for _, k := range keys {
		if v, ok := params[k]; ok && v != "" {
			attrs[k] = v
		}
	}
}

// warnUnpassableBackendKeys logs a warning if a .dip file appears to contain
// backend-related keys that cannot be passed through the current IR. This helps
// users discover that they need --backend flag or DOT-format attrs instead.
//
// Currently ir.AgentConfig has no Params field, so this is a no-op placeholder.
// When dippin-lang adds Params, this function should inspect cfg.Params for
// backend keys and log warnings for any that are present but not forwarded.
func warnUnpassableBackendKeys(_ ir.AgentConfig, _ map[string]string) {
	// No-op until ir.AgentConfig gains a Params field.
	// When it does, check for keys: backend, mcp_servers, allowed_tools,
	// disallowed_tools, max_budget_usd, permission_mode and warn:
	//   log.Printf("[dippin-adapter] warning: .dip file contains backend key %q but IR passthrough is not yet supported", k)
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

// synthesizeImplicitEdges creates edges for parallel fan-out targets and fan-in sources.
// The dippin IR stores these in ParallelConfig.Targets and FanInConfig.Sources
// Implicit edge synthesis and start/exit node validation are in dippin_adapter_edges.go
