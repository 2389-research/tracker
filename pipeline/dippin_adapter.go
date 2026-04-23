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
	// ErrParenthesizedParsedCondition is returned by convertEdge when a Parsed-only
	// ir.Condition formats to an expression containing parentheses. The pipeline
	// edge evaluator (pipeline/condition.go) does not support parens — it tokenizes
	// on plain strings.Split("||") and strings.Split("&&"), so `a || (b && c)`
	// would become tokens like `(b` and `c)`. In EvaluateCondition, those are not
	// hard runtime errors: they are treated as unknown variable names, a warning is
	// logged, and they evaluate as empty strings, which can silently produce false
	// or otherwise incorrect results. The adapter rejects these expressions up
	// front to avoid that mis-evaluation. Authors should populate Condition.Raw
	// with a flat form (e.g. `a=1 || b=2 || c=3`) or simplify the Parsed tree so
	// no parens are emitted.
	ErrParenthesizedParsedCondition = errors.New("formatted Parsed condition uses parentheses, which the pipeline edge evaluator does not support")
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
	if len(workflow.Stylesheet) > 0 {
		g.Attrs["model_stylesheet"] = serializeStylesheet(workflow.Stylesheet)
	}
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
		extractAgentAttrs(cfg, attrs)
	case ir.HumanConfig:
		extractHumanAttrs(cfg, attrs)
	case ir.ToolConfig:
		extractToolAttrs(cfg, attrs)
	case ir.ParallelConfig:
		extractParallelAttrs(cfg, attrs)
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

func extractAgentAttrs(cfg ir.AgentConfig, attrs map[string]string) {
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
	for k, v := range cfg.Params {
		if _, exists := attrs[k]; !exists {
			attrs[k] = v
		}
	}
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
//   - stop_condition  — raw condition expression; falls back to the formatted
//     Parsed tree when Raw is empty (dippin-lang's lazy-parse invariant)
//   - steer_condition — same Raw/Parsed fallback as stop_condition
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
	if s := managerLoopConditionText(cfg.StopCondition); s != "" {
		attrs["stop_condition"] = s
	}
	if s := managerLoopConditionText(cfg.SteerCondition); s != "" {
		attrs["steer_condition"] = s
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

// managerLoopConditionText extracts the best textual form of a condition:
// prefers Raw (set by the parser), falls back to formatting Parsed (set
// lazily by simulate.EnsureConditionsParsed). Returns "" for nil/empty.
//
// Mirrors dippin-lang v0.22.0 export.dotManagerLoopConditionText.
func managerLoopConditionText(c *ir.Condition) string {
	if c == nil {
		return ""
	}
	if c.Raw != "" {
		return c.Raw
	}
	// Parsed is typically populated by simulate.EnsureConditionsParsed; the
	// adapter runs before simulate, so in practice Raw will be set. We format
	// Parsed here as a defensive fallback — if neither is set we return "".
	if c.Parsed != nil {
		return formatManagerLoopCondition(c.Parsed)
	}
	return ""
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

// formatManagerLoopCondition re-serializes an ir.ConditionExpr back into its
// textual form. Mirrors dippin-lang v0.22.0 export.formatCondition with the
// same precedence rules so round-trips match.
func formatManagerLoopCondition(expr ir.ConditionExpr) string {
	return formatManagerLoopConditionExpr(expr, 0)
}

const (
	condPrecOr  = 1
	condPrecAnd = 2
	condPrecNot = 3
)

// formatManagerLoopConditionExpr formats a condition expression with the given
// parent precedence for disambiguation parens.
//
// Important: the emitted text feeds directly into `pipeline.EvaluateCondition`,
// which parses Go-style `&&` / `||` / `not` operators (see pipeline/condition.go).
// We intentionally diverge from dippin-lang's DOT formatter (which uses
// English `and` / `or`) because the evaluator has no `and`/`or` tokens — a
// `.Parsed`-only Condition formatted with English operators would be silently
// mis-evaluated to a single-clause no-op. The ctx. prefix is still stripped
// the same way.
func formatManagerLoopConditionExpr(expr ir.ConditionExpr, parentPrec int) string {
	switch e := expr.(type) {
	case ir.CondCompare:
		// Mirror dippin-lang's formatDOTCompare: strip the "ctx." prefix from
		// variables (manager_loop conditions reference stack.child.* which
		// has no prefix to strip, but we preserve the same rule for safety).
		variable := strings.TrimPrefix(e.Variable, "ctx.")
		return fmt.Sprintf("%s %s %s", variable, e.Op, e.Value)
	case ir.CondAnd:
		return formatManagerLoopBinaryOp(e.Left, e.Right, "&&", condPrecAnd, parentPrec)
	case ir.CondOr:
		return formatManagerLoopBinaryOp(e.Left, e.Right, "||", condPrecOr, parentPrec)
	case ir.CondNot:
		return "not " + formatManagerLoopConditionExpr(e.Inner, condPrecNot)
	default:
		return ""
	}
}

// formatManagerLoopBinaryOp formats an and/or expression with optional
// parenthesization when the parent precedence differs from this op's.
func formatManagerLoopBinaryOp(left, right ir.ConditionExpr, op string, prec, parentPrec int) string {
	s := fmt.Sprintf("%s %s %s",
		formatManagerLoopConditionExpr(left, prec),
		op,
		formatManagerLoopConditionExpr(right, prec))
	if parentPrec != 0 && parentPrec != prec {
		return "(" + s + ")"
	}
	return s
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
	setIfNonEmpty(attrs, "default_retry_policy", defaults.RetryPolicy)
	if defaults.MaxRetries > 0 {
		attrs["default_max_retry"] = strconv.Itoa(defaults.MaxRetries)
	}
	setIfNonEmpty(attrs, "default_fidelity", defaults.Fidelity)
	if defaults.MaxRestarts > 0 {
		attrs["max_restarts"] = strconv.Itoa(defaults.MaxRestarts)
	}
	setIfNonEmpty(attrs, "restart_target", defaults.RestartTarget)
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
}

func extractWorkflowVars(vars map[string]string, attrs map[string]string) {
	for key, value := range vars {
		attrs[GraphParamAttrKey(key)] = value
	}
}

// setIfNonEmpty sets attrs[key] = value only when value is non-empty.
func setIfNonEmpty(attrs map[string]string, key, value string) {
	if value != "" {
		attrs[key] = value
	}
}

// convertEdge transforms an IR Edge to a Graph Edge.
// Serializes the parsed Condition back to a raw string for the tracker engine.
//
// Returns ErrParenthesizedParsedCondition when the condition only has .Parsed
// populated and the formatted text contains parentheses. The pipeline edge
// evaluator has no paren support — a mixed-precedence Parsed tree like
// `a=1 || (b=2 && c=3)` would tokenize to garbage (`(b`, `c)`) at runtime.
// Flat all-AND / all-OR Parsed trees are still accepted because they don't
// require parens. Authors hitting this should populate Condition.Raw (the
// parser does this natively) or simplify the Parsed tree.
func convertEdge(irEdge *ir.Edge) (*Edge, error) {
	gEdge := &Edge{
		From:  irEdge.From,
		To:    irEdge.To,
		Label: irEdge.Label,
		Attrs: make(map[string]string),
	}

	// Serialize condition if present. We prefer Raw (set by the parser) and
	// fall back to formatting Parsed on the fly — the same Raw-then-Parsed
	// preference as managerLoopConditionText so an ir.Edge with only
	// .Parsed populated (e.g. constructed by tests or simulate without
	// running the parser) still produces a conditional edge rather than a
	// silent unconditional one.
	if cond := managerLoopConditionText(irEdge.Condition); cond != "" {
		// Parsed fallback without Raw may emit parens for mixed-precedence
		// expressions. The edge evaluator can't parse those — reject at
		// adapter time with a clear, actionable error rather than let a
		// garbage token like `(b` fail at runtime. Raw-sourced conditions
		// are trusted as-is (authors wrote them, and the evaluator is the
		// same one the parser targets).
		if irEdge.Condition != nil && irEdge.Condition.Raw == "" && strings.ContainsAny(cond, "()") {
			return nil, fmt.Errorf("edge %s -> %s: %w: formatted as %q; populate Condition.Raw with a flat form (e.g. 'a=1 || b=2 || c=3') or simplify the Parsed tree",
				irEdge.From, irEdge.To, ErrParenthesizedParsedCondition, cond)
		}
		gEdge.Condition = cond
		gEdge.Attrs["condition"] = cond
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
