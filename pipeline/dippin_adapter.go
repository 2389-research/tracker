// ABOUTME: Adapter that converts Dippin IR (from dippin-lang parser) to Tracker's Graph model.
// ABOUTME: Provides FromDippinIR() to enable tracker to execute .dip files natively.
package pipeline

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/2389-research/dippin-lang/ir"
)

var (
	ErrNilWorkflow            = errors.New("nil workflow")
	ErrMissingStart           = errors.New("workflow missing Start node")
	ErrMissingExit            = errors.New("workflow missing Exit node")
	ErrUnknownNodeKind        = errors.New("unknown node kind")
	ErrUnknownConfig          = errors.New("unknown config type")
	ErrInvalidSteerContextKey = errors.New("steer_context key contains ':' which breaks block-form round-trip through the .dip formatter")
	ErrMissingManagerLoopCfg  = errors.New("manager_loop node is missing required ir.ManagerLoopConfig")
	ErrOverrideOnNonHumanEdge = errors.New("override: true is only valid on edges from wait.human gate nodes")
	// ErrParenthesizedParsedCondition is a deprecated alias for
	// ErrParenthesizedCondition (condition_ast.go), retained so existing
	// errors.Is checks keep working. A Parsed tree never produces parentheses
	// any more — SerializeDippinCondition flattens nesting to DNF (#647) — so
	// this only fires for a Raw-only condition that tracker's parser rejects.
	ErrParenthesizedParsedCondition = ErrParenthesizedCondition
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
		return nil, ErrNilWorkflow
	}
	if workflow.Start == "" {
		return nil, ErrMissingStart
	}
	if workflow.Exit == "" {
		return nil, ErrMissingExit
	}

	g := buildGraphFromWorkflow(workflow)

	if err := addIRNodes(g, workflow.Nodes); err != nil {
		return nil, err
	}
	if err := addIREdges(g, workflow.Edges); err != nil {
		return nil, err
	}

	// Synthesize implicit edges from parallel fan-out targets and fan-in sources.
	synthesizeImplicitEdges(g, workflow)

	// Ensure start/exit nodes exist
	if err := ensureStartExitNodes(g); err != nil {
		return nil, err
	}

	return g, nil
}

// buildGraphFromWorkflow initializes a Graph from top-level workflow metadata.
func buildGraphFromWorkflow(workflow *ir.Workflow) *Graph {
	g := NewGraph(workflow.Name)
	g.StartNode = workflow.Start
	g.ExitNode = workflow.Exit
	if workflow.Goal != "" {
		g.Attrs["goal"] = workflow.Goal
	}
	if workflow.Version != "" {
		g.Attrs["version"] = workflow.Version
	}
	extractWorkflowDefaults(workflow.Defaults, g.Attrs)
	extractWorkflowVars(workflow.Vars, g.Attrs)
	extractRequires(workflow.Requires, g.Attrs)
	if len(workflow.Stylesheet) > 0 {
		g.Attrs["model_stylesheet"] = serializeStylesheet(workflow.Stylesheet)
	}
	g.Inputs = inputsFromIR(workflow.Inputs)
	// Section-level `else -> <node>` (#649). Stored graph-level, not as a
	// synthesized Edge: a synthesized unconditional edge would (a) show up in
	// edge listings/coverage as if the author wrote it and (b) collide with
	// the strict-failure rule, which treats an unconditional edge on a failed
	// node as "no failure route" — the opposite of dippin's success-side-only
	// contract. Consumed by Engine.selectByElse after every explicit edge
	// selection step has failed.
	g.ElseTarget = workflow.ElseTarget
	return g
}

// addIRNodes converts IR nodes and adds them to the graph in declaration order.
func addIRNodes(g *Graph, irNodes []*ir.Node) error {
	for _, irNode := range irNodes {
		if irNode == nil {
			continue
		}
		gNode, err := convertNode(irNode)
		if err != nil {
			return fmt.Errorf("node %s: %w", irNode.ID, err)
		}
		g.AddNode(gNode)
		g.NodeOrder = append(g.NodeOrder, irNode.ID)
	}
	return nil
}

// addIREdges converts IR edges and adds them to the graph.
// Propagates convertEdge errors (e.g. ErrParenthesizedParsedCondition) so
// adapter-time rejection surfaces to the caller rather than silently losing
// or mis-evaluating the edge at runtime.
func addIREdges(g *Graph, irEdges []*ir.Edge) error {
	for _, irEdge := range irEdges {
		if irEdge == nil {
			continue
		}
		if irEdge.Override {
			src, ok := g.Nodes[irEdge.From]
			if !ok || src.Shape != "hexagon" {
				return fmt.Errorf("edge %s -> %s: %w", irEdge.From, irEdge.To, ErrOverrideOnNonHumanEdge)
			}
		}
		gEdge, err := convertEdge(irEdge)
		if err != nil {
			return err
		}
		g.AddEdge(gEdge)
	}
	return nil
}

// nodeKindToShapeMap maps IR NodeKind to DOT shape strings.
// This mapping ensures the Graph produced by FromDippinIR matches
// the shape convention used by ParseDOT, maintaining handler compatibility.
var nodeKindToShapeMap = map[ir.NodeKind]string{
	ir.NodeAgent:       "box",           // → codergen
	ir.NodeHuman:       "hexagon",       // → wait.human
	ir.NodeTool:        "parallelogram", // → tool
	ir.NodeParallel:    "component",     // → parallel
	ir.NodeFanIn:       "tripleoctagon", // → parallel.fan_in
	ir.NodeSubgraph:    "tab",           // → subgraph
	ir.NodeConditional: "diamond",       // → conditional (pure routing, no LLM call)
	ir.NodeManagerLoop: "house",         // → stack.manager_loop (dippin-lang v0.22.0+)
}

// nodeKindToShape returns the DOT shape for a given NodeKind.
// Returns ("", false) if the kind is not recognized.
func nodeKindToShape(kind ir.NodeKind) (string, bool) {
	shape, ok := nodeKindToShapeMap[kind]
	return shape, ok
}

// convertNode transforms an IR Node to a Graph Node.
// Extracts configuration from the NodeConfig union into flat string attrs.
func convertNode(irNode *ir.Node) (*Node, error) {
	shape, ok := nodeKindToShape(irNode.Kind)
	if !ok {
		return nil, fmt.Errorf("%s: %w", irNode.Kind, ErrUnknownNodeKind)
	}

	// Kind-specific required-config guard. A manager_loop node with a nil
	// Config would otherwise flow through extractNodeAttrs as a no-op and
	// produce a graph node without subgraph_ref, surfacing later at Execute
	// time as a vague runtime error. Fail loudly at build time instead.
	// Scoped to manager_loop for now — we may extend the same guard to other
	// kinds as follow-ups if they exhibit the same silent-degrade pattern.
	//
	// Return the sentinel bare — addIRNodes wraps all convertNode errors with
	// `node <id>: ...`, so wrapping here too would produce `node mgr: node mgr: ...`.
	if irNode.Kind == ir.NodeManagerLoop && irNode.Config == nil {
		return nil, ErrMissingManagerLoopCfg
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
	if ok, err := extractValueNodeAttrs(config, attrs); ok {
		return err
	}
	return extractPtrNodeAttrs(config, attrs)
}

// extractValueNodeAttrs handles value (non-pointer) IR config types.
// Returns (true, err) if the type was recognized; (false, nil) otherwise.
func extractValueNodeAttrs(config ir.NodeConfig, attrs map[string]string) (bool, error) {
	switch cfg := config.(type) {
	case ir.AgentConfig:
		return true, extractAgentAttrs(cfg, attrs)
	case ir.HumanConfig:
		extractHumanAttrs(cfg, attrs)
	case ir.ToolConfig:
		extractToolAttrs(cfg, attrs)
	case ir.ParallelConfig:
		extractParallelAttrs(cfg, attrs)
		// branch.<n>.writable_paths_mode can arrive via the params spill
		// (#648); it overrides the target's mode per-branch, so it gets the
		// same load-time fail-closed check as the agent-level attr.
		return true, validateWritablePathsModeAttr(attrs)
	case ir.FanInConfig:
		extractFanInAttrs(cfg, attrs)
	case ir.SubgraphConfig:
		extractSubgraphAttrs(cfg, attrs)
	case ir.ConditionalConfig:
		// Conditional nodes are pure routing — no config to extract.
	case ir.ManagerLoopConfig:
		return true, extractManagerLoopAttrs(cfg, attrs)
	default:
		return false, nil
	}
	return true, nil
}

// extractPtrNodeAttrs handles pointer IR config types.
// Returns an error for unrecognized types.
func extractPtrNodeAttrs(config ir.NodeConfig, attrs map[string]string) error {
	switch cfg := config.(type) {
	case *ir.AgentConfig:
		return extractNodeAttrsPtr(cfg, attrs)
	case *ir.HumanConfig:
		return extractNodeAttrsPtr(cfg, attrs)
	case *ir.ToolConfig:
		return extractNodeAttrsPtr(cfg, attrs)
	case *ir.ParallelConfig:
		return extractNodeAttrsPtr(cfg, attrs)
	case *ir.FanInConfig:
		return extractNodeAttrsPtr(cfg, attrs)
	case *ir.SubgraphConfig:
		return extractNodeAttrsPtr(cfg, attrs)
	case *ir.ConditionalConfig:
		// Conditional nodes are pure routing — no config to extract.
		return nil
	case *ir.ManagerLoopConfig:
		return extractNodeAttrsPtr(cfg, attrs)
	default:
		return fmt.Errorf("%T: %w", config, ErrUnknownConfig)
	}
}

// extractNodeAttrsPtr dereferences a pointer IR config and dispatches to extractNodeAttrs.
// Returns nil immediately if the pointer is nil.
func extractNodeAttrsPtr[T ir.NodeConfig](cfg *T, attrs map[string]string) error {
	if cfg == nil {
		return nil
	}
	return extractNodeAttrs(*cfg, attrs)
}

func extractAgentAttrs(cfg ir.AgentConfig, attrs map[string]string) error {
	extractAgentPromptAttrs(cfg, attrs)
	extractAgentExecutionAttrs(cfg, attrs)
	extractAgentOutputAttrs(cfg, attrs)
	extractAgentBackendAttrs(cfg.Params, attrs)
	if cfg.Backend != "" {
		attrs["backend"] = cfg.Backend
	}
	if cfg.WorkingDir != "" {
		attrs["working_dir"] = cfg.WorkingDir
	}
	// writable_paths: typed IR field is authoritative. If empty but Params
	// carries the key, claim the attr with "" so the spill below cannot
	// land an attacker-supplied value — configureJail (Task 14) then fail-
	// CLOSES on Set && len==0 at the accessor layer. See issue #272 § 8 D8.
	if len(cfg.WritablePaths) > 0 {
		attrs["writable_paths"] = strings.Join(cfg.WritablePaths, ",")
	} else if _, paramsHasIt := cfg.Params["writable_paths"]; paramsHasIt {
		attrs["writable_paths"] = ""
	}
	// tool_access: typed IR field is authoritative over Params (issue #366).
	// No claim-the-attr-on-empty defense needed here: downstream fail-closes
	// on ANY non-empty value (agent.SessionConfig.IsToolAccessRestricted),
	// so a Params spill can only restrict, never grant, tool access.
	if strings.TrimSpace(cfg.ToolAccess) != "" {
		attrs["tool_access"] = cfg.ToolAccess
	}
	for k, v := range cfg.Params {
		if _, exists := attrs[k]; !exists {
			attrs[k] = v
		}
	}
	return validateWritablePathsModeAttr(attrs)
}

// validateWritablePathsModeAttr rejects a writable_paths_mode (#648) that is
// not exactly one of the two modes. The key arrives via the Params spill
// (dippin has no typed field yet), so this is the load-time fail-closed point:
// a typo ("Prefer", "prefer ") can never reach the jail as a not-require
// value. addIRNodes prefixes the error with `node <id>:`.
func validateWritablePathsModeAttr(attrs map[string]string) error {
	for _, key := range writablePathsModeKeys(attrs) {
		if err := ValidateWritablePathsMode(attrs[key]); err != nil {
			return fmt.Errorf("%w (attr %q)", err, key)
		}
	}
	return nil
}

// extractAgentPromptAttrs sets prompt, system prompt, model, and provider attrs.
func extractAgentPromptAttrs(cfg ir.AgentConfig, attrs map[string]string) {
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
}

// extractAgentExecutionAttrs sets turn limits, timeouts, caching, compaction, and feature flags.
func extractAgentExecutionAttrs(cfg ir.AgentConfig, attrs map[string]string) {
	extractAgentLimitsAttrs(cfg, attrs)
	extractAgentFeatureAttrs(cfg, attrs)
}

// extractAgentLimitsAttrs sets turn limits, timeouts, and context management attrs.
func extractAgentLimitsAttrs(cfg ir.AgentConfig, attrs map[string]string) {
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
	if cfg.LastResponseTruncate > 0 {
		attrs["last_response_truncate"] = strconv.Itoa(cfg.LastResponseTruncate)
	}
}

// extractAgentFeatureAttrs sets reasoning, fidelity, and pipeline feature flag attrs.
func extractAgentFeatureAttrs(cfg ir.AgentConfig, attrs map[string]string) {
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

// extractAgentOutputAttrs sets structured output format attrs (v0.16.0).
func extractAgentOutputAttrs(cfg ir.AgentConfig, attrs map[string]string) {
	if cfg.ResponseFormat != "" {
		attrs["response_format"] = cfg.ResponseFormat
	}
	if cfg.ResponseSchema != "" {
		attrs["response_schema"] = cfg.ResponseSchema
	}
}

// extractAgentBackendAttrs maps backend-selection and backend-specific keys
// from a generic params map into node attrs consumed by CodergenHandler,
// ClaudeCodeBackend, and ACPBackend. The recognized keys are:
//
//   - backend         → attrs["backend"]          (e.g. "claude-code", "native", "acp")
//   - acp_agent       → attrs["acp_agent"]         (explicit ACP binary: "claude-code-acp", "codex-acp", "gemini")
//   - mcp_servers     → attrs["mcp_servers"]       (newline-separated name=cmd pairs)
//   - allowed_tools   → attrs["allowed_tools"]     (comma-separated tool names)
//   - disallowed_tools→ attrs["disallowed_tools"]  (comma-separated tool names)
//   - max_budget_usd  → attrs["max_budget_usd"]    (float string, e.g. "1.50")
//   - permission_mode → attrs["permission_mode"]   (plan|acceptEdits|bypassPermissions)
//   - tool_access     → attrs["tool_access"]       ("none" to disable tools entirely;
//     any non-empty value is fail-closed —
//     see issue #258 and dippin-lang#41)
//
// Unrecognized keys are silently ignored.
// A nil or empty params map is a no-op.
func extractAgentBackendAttrs(params map[string]string, attrs map[string]string) {
	keys := []string{
		"backend",
		"acp_agent",
		"mcp_servers",
		"allowed_tools",
		"disallowed_tools",
		"max_budget_usd",
		"permission_mode",
		"tool_access",
	}
	for _, k := range keys {
		if v, ok := params[k]; ok && v != "" {
			attrs[k] = v
		}
	}
}

func extractHumanAttrs(cfg ir.HumanConfig, attrs map[string]string) {
	if cfg.Mode != "" {
		attrs["mode"] = cfg.Mode
	}
	if cfg.Default != "" {
		attrs["default_choice"] = cfg.Default
	}
	if cfg.QuestionsKey != "" {
		attrs["questions_key"] = cfg.QuestionsKey
	}
	if cfg.AnswersKey != "" {
		attrs["answers_key"] = cfg.AnswersKey
	}
	if cfg.Prompt != "" {
		attrs["prompt"] = cfg.Prompt
	}
	if cfg.Timeout > 0 {
		attrs["timeout"] = cfg.Timeout.String()
	}
	if cfg.TimeoutAction != "" {
		attrs["timeout_action"] = cfg.TimeoutAction
	}
}

func extractToolAttrs(cfg ir.ToolConfig, attrs map[string]string) {
	if cfg.Command != "" {
		attrs["tool_command"] = cfg.Command
	}
	if cfg.Timeout > 0 {
		attrs["timeout"] = cfg.Timeout.String()
	}
	if cfg.MarkerGrep != "" {
		attrs["marker_grep"] = cfg.MarkerGrep
	}
	if cfg.RouteRequired {
		attrs["route_required"] = "true"
	}
	if cfg.OutputLimit > 0 {
		attrs["output_limit"] = strconv.Itoa(cfg.OutputLimit)
	}
	if len(cfg.Outputs) > 0 {
		attrs["outputs"] = strings.Join(cfg.Outputs, ",")
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
		// Per-branch security overrides (issue #368). Empty INHERITS the
		// target agent's value (per the IR doc comments) — the attr is
		// only written when non-empty, so the parallel handler's clone
		// keeps the agent's own attr otherwise; it never resets to the
		// full tool catalog or unbounded writes. Encodings mirror the
		// agent-level attrs exactly: tool_access trims whitespace-only to
		// unset (#366), writable_paths comma-joins so AgentConfig parses
		// branch values identically (incl. the #272 fail-closed states).
		if strings.TrimSpace(branch.ToolAccess) != "" {
			attrs[prefix+"tool_access"] = branch.ToolAccess
		}
		if len(branch.WritablePaths) > 0 {
			attrs[prefix+"writable_paths"] = strings.Join(branch.WritablePaths, ",")
		}
		if branch.LastResponseTruncate > 0 {
			attrs[prefix+"last_response_truncate"] = strconv.Itoa(branch.LastResponseTruncate)
		}
	}
	// Generic params pass-through (dippin-lang v0.39.0, #313) — e.g.
	// fan_in_policy / quorum. Typed fields above take precedence.
	spillParams(cfg.Params, attrs)
}

func extractFanInAttrs(cfg ir.FanInConfig, attrs map[string]string) {
	if len(cfg.Sources) > 0 {
		attrs["fan_in_sources"] = strings.Join(cfg.Sources, ",")
	}
	// Generic params pass-through (dippin-lang v0.39.0, #313).
	spillParams(cfg.Params, attrs)
}

// spillParams copies params into attrs without overwriting keys already set
// by typed-field extraction (mirrors the AgentConfig.Params convention).
func spillParams(params, attrs map[string]string) {
	for k, v := range params {
		if _, exists := attrs[k]; !exists {
			attrs[k] = v
		}
	}
}

func extractSubgraphAttrs(cfg ir.SubgraphConfig, attrs map[string]string) {
	if cfg.Ref != "" {
		attrs["subgraph_ref"] = cfg.Ref
	}
	if len(cfg.Params) > 0 {
		// Serialize params as comma-separated key=value pairs (sorted for determinism).
		var pairs []string
		for _, k := range slices.Sorted(maps.Keys(cfg.Params)) {
			pairs = append(pairs, fmt.Sprintf("%s=%s", k, cfg.Params[k]))
		}
		attrs["subgraph_params"] = strings.Join(pairs, ",")
	}
}

// extractManagerLoopAttrs flattens ir.ManagerLoopConfig into the six DOT-style
// unprefixed attrs that the stack.manager_loop handler consumes (dippin-lang
// v0.22.0+ contract):
//
//   - subgraph_ref    — child .dip path (required at runtime)
//   - poll_interval   — duration string via time.Duration.String()
//   - max_cycles      — decimal int via strconv.Itoa
//   - stop_condition  — condition serialized from the Parsed AST into tracker's
//     dialect (dippinConditionText; Raw is only a fallback)
//   - steer_condition — same Parsed-then-Raw rule as stop_condition
//   - steer_context   — canonical sorted "k=v,k=v" with percent-encoding for
//     the three reserved chars (',', '=', '%') — mirrors dippin-lang v0.22.0
//     export.flattenSteerContext
//
// Zero/empty fields are omitted so the handler can apply its own defaults
// (45s poll, 1000 cycles). Writing empty strings would override the defaults
// with "" which the handler treats as "unset" today — but omitting is clearer.
//
// IR semantic divergence (contract-alignment follow-up): the dippin-lang IR
// documents PollInterval=0 as "event-driven" and MaxCycles=0 as "unbounded".
// Tracker's stack.manager_loop handler has no event-driven mode and no
// unbounded mode today — both degrade to the handler defaults (45s / 1000)
// because the zero values are omitted from node.Attrs here. Pipeline authors
// who want explicit control should set positive values; omitting yields the
// tracker defaults. If/when the handler grows those modes this extractor is
// the right place to translate the zero-value IR sentinels into the
// corresponding handler attrs.
func extractManagerLoopAttrs(cfg ir.ManagerLoopConfig, attrs map[string]string) error {
	if cfg.SubgraphRef != "" {
		attrs["subgraph_ref"] = cfg.SubgraphRef
	}
	if cfg.PollInterval > 0 {
		attrs["poll_interval"] = cfg.PollInterval.String()
	}
	if cfg.MaxCycles > 0 {
		attrs["max_cycles"] = strconv.Itoa(cfg.MaxCycles)
	}
	if err := setConditionAttr(attrs, "stop_condition", cfg.StopCondition); err != nil {
		return err
	}
	if err := setConditionAttr(attrs, "steer_condition", cfg.SteerCondition); err != nil {
		return err
	}
	s, err := flattenSteerContext(cfg.SteerContext)
	if err != nil {
		return err
	}
	if s != "" {
		attrs["steer_context"] = s
	}
	return nil
}

// steerContextEncoder mirrors the encoder in dippin-lang v0.22.0
// export/dot.go. '%' must be replaced first so it is not double-encoded.
var steerContextEncoder = strings.NewReplacer(
	"%", "%25",
	",", "%2C",
	"=", "%3D",
)

// encodeSteerContextToken percent-encodes the three reserved characters used
// as delimiters in the flattened steer_context representation (',', '=')
// and the escape character itself ('%'). Keeps DOT round-trip lossless even
// when keys or values contain reserved characters. Mirror of dippin-lang
// v0.22.0 export.encodeSteerContextToken.
func encodeSteerContextToken(s string) string {
	if !strings.ContainsAny(s, ",=%") {
		return s
	}
	return steerContextEncoder.Replace(s)
}

// flattenSteerContext produces canonical sorted "k=v,k=v" from the map.
// Empty map returns empty string (caller suppresses the attr). Reserved
// characters (',', '=', '%') in keys and values are percent-encoded so the
// round-trip through DOT → migrate stays lossless.
//
// Fails loudly when any key contains ':' — the dippin-lang v0.22.0 block-form
// formatter writes steer_context entries as "key: value" lines, so a colon in
// the key breaks the .dip→IR→.dip round-trip. The upstream parser silently
// drops such keys with a diagnostic; we reject at graph-build time instead so
// the author sees a clear, load-bearing error rather than a pipeline that
// quietly lost a steering key. The three reserved delimiter chars (',', '=',
// '%') are percent-encoded rather than rejected because the encoder can
// round-trip them losslessly.
//
// Mirrors dippin-lang v0.22.0 export.flattenSteerContext for the happy path.
func flattenSteerContext(m map[string]string) (string, error) {
	if len(m) == 0 {
		return "", nil
	}
	keys := slices.Sorted(maps.Keys(m))
	for _, k := range keys {
		if strings.Contains(k, ":") {
			return "", fmt.Errorf("%w: %q", ErrInvalidSteerContextKey, k)
		}
	}
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, encodeSteerContextToken(k)+"="+encodeSteerContextToken(m[k]))
	}
	return strings.Join(parts, ","), nil
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
	setIfNonEmpty(attrs, "llm_model", defaults.Model)
	setIfNonEmpty(attrs, "llm_provider", defaults.Provider)
	setIfNonEmpty(attrs, "tool_commands_allow", defaults.ToolCommandsAllow)
	setIfNonEmpty(attrs, "tool_denylist_add", defaults.ToolDenylistAdd)
	setIfNonEmpty(attrs, "default_retry_policy", defaults.RetryPolicy)
	if defaults.MaxRetries > 0 {
		attrs["default_max_retry"] = strconv.Itoa(defaults.MaxRetries)
	}
	setIfNonEmpty(attrs, "default_fidelity", defaults.Fidelity)
	if defaults.MaxRestarts > 0 {
		attrs["max_restarts"] = strconv.Itoa(defaults.MaxRestarts)
	}
	setIfNonEmpty(attrs, "restart_target", defaults.RestartTarget)
	// #309: dippin's graph-level `on_failure: <NodeID>` default failure route maps
	// onto the graph fallback key the engine's strict-failure path reads
	// (findFallbackTarget, #295). Node-level fallback_target lands in the node's
	// own fallback_retry_target and is consulted first, so it still wins.
	setIfNonEmpty(attrs, "fallback_target", defaults.OnFailure)
	if defaults.CacheTools {
		attrs["cache_tool_results"] = "true"
	}
	setIfNonEmpty(attrs, "context_compaction", defaults.Compaction)
	setIfNonEmpty(attrs, "on_resume", defaults.OnResume)
	// Budget ceilings (optional; zero means unset — tracker.resolveBudgetLimits
	// will honor these as fallback when Config.Budget and --max-* CLI flags
	// are not provided).
	if defaults.MaxTotalTokens > 0 {
		attrs["max_total_tokens"] = strconv.Itoa(defaults.MaxTotalTokens)
	}
	if defaults.MaxCostCents > 0 {
		attrs["max_cost_cents"] = strconv.Itoa(defaults.MaxCostCents)
	}
	if defaults.MaxWallTime > 0 {
		attrs["max_wall_time"] = defaults.MaxWallTime.String()
	}
	if defaults.StallTimeout > 0 {
		attrs["stall_timeout"] = defaults.StallTimeout.String()
	}
}

func extractWorkflowVars(vars map[string]string, attrs map[string]string) {
	for key, value := range vars {
		attrs[GraphParamAttrKey(key)] = value
	}
}

// extractRequires writes the workflow's environmental-dependency list to
// graph.Attrs["requires"] as a comma-separated string. Empty / nil input
// is a no-op. The v0.29.0 git preflight reads this via Graph.RequiredDeps().
//
// The dippin-lang IR exposes this list as []string on Workflow.Requires
// (v0.26.0+). The flat-string round-trip through Attrs matches the rest of
// the adapter's IR-to-Graph projection — handlers (and Graph methods like
// RequiredDeps) read scalar strings from Attrs, not slices.
func extractRequires(requires []string, attrs map[string]string) {
	if len(requires) == 0 {
		return
	}
	// Trim, drop empties, and deduplicate while preserving declaration order.
	// Authors who write `requires: git, git, git` get a clean `git` in the
	// graph attr. Order preservation matters for the warn-on-unrecognized
	// path in pipeline.Preflight, which iterates the list and emits one
	// warning per entry — without dedup, duplicates would produce duplicate
	// warnings for the same dep.
	seen := make(map[string]struct{}, len(requires))
	cleaned := make([]string, 0, len(requires))
	for _, r := range requires {
		s := strings.TrimSpace(r)
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		cleaned = append(cleaned, s)
	}
	if len(cleaned) == 0 {
		return
	}
	attrs["requires"] = strings.Join(cleaned, ", ")
}

// setIfNonEmpty sets attrs[key] = value only when value is non-empty.
func setIfNonEmpty(attrs map[string]string, key, value string) {
	if value != "" {
		attrs[key] = value
	}
}

// convertEdge transforms an IR Edge to a Graph Edge.
// Serializes the Condition into tracker's dialect for the engine and validates
// it through the shared ParseCondition model.
//
// The AST (Condition.Parsed) is authoritative — see dippinConditionText —
// because tracker's parser historically split only on `&&` / `||`, so dippin's
// word forms (`a != x and b != y`) arrived via Raw and were re-parsed as ONE
// clause whose RHS was the literal `x and b != y` (#647). The emitted text is
// then run through ParseCondition, the same parser the runtime evaluator uses,
// so structural errors (unmatched quotes, parentheses in a Raw fallback, a
// dangling conjunction) are rejected at adapter time instead of silently
// mis-routing at runtime.
func convertEdge(irEdge *ir.Edge) (*Edge, error) {
	gEdge := &Edge{
		From:     irEdge.From,
		To:       irEdge.To,
		Label:    irEdge.Label,
		Choice:   irEdge.Choice,
		Override: irEdge.Override,
		Attrs:    make(map[string]string),
	}

	cond, err := dippinConditionText(irEdge.Condition, "condition")
	if err != nil {
		return nil, fmt.Errorf("edge %s -> %s: %w", irEdge.From, irEdge.To, err)
	}
	if cond != "" {
		cc, err := ParseCondition(cond)
		if err != nil {
			return nil, fmt.Errorf("edge %s -> %s: %w", irEdge.From, irEdge.To, err)
		}
		if err := checkNoBareWordConjunction(cc); err != nil {
			return nil, fmt.Errorf("edge %s -> %s: %w", irEdge.From, irEdge.To, err)
		}
		gEdge.Condition = cond
	}

	// Preserve weight
	if irEdge.Weight > 0 {
		gEdge.Attrs["weight"] = strconv.Itoa(irEdge.Weight)
	}

	// Mark restart edges
	if irEdge.Restart {
		gEdge.Attrs["restart"] = "true"
	}

	return gEdge, nil
}

// serializeStylesheet converts IR stylesheet rules to the CSS-like format
// expected by ParseStylesheet. Each rule becomes "selector { key: value; }".
func serializeStylesheet(rules []ir.StylesheetRule) string {
	var parts []string
	for _, rule := range rules {
		selector := serializeSelector(rule.Selector)
		var props []string
		for _, k := range slices.Sorted(maps.Keys(rule.Properties)) {
			props = append(props, fmt.Sprintf("%s: %s", k, rule.Properties[k]))
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
