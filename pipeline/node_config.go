// ABOUTME: Typed view over Node.Attrs for each handler kind — replaces ad-hoc
// ABOUTME: map[string]string parsing scattered across handlers and engine code.
package pipeline

import (
	"strconv"
	"strings"
	"time"
)

// AgentNodeConfig is a typed view over a codergen (agent) node's attributes.
//
// For the subset of attrs that support graph-level defaults (llm_model,
// llm_provider, reasoning_effort, verify_after_edit, verify_command,
// max_verify_retries, plan_before_execute, cache_tool_results,
// context_compaction, turn_breach_policy, max_cost_usd, no_progress_turns),
// AgentConfig resolves the value from graphAttrs first, then lets node.Attrs
// override when the same key is set on the node. The remaining fields are
// node-only and have no graph fallback.
//
// Unless documented otherwise on the specific field, fields absent from their
// applicable source use the Go zero value. ReflectOnError is the one
// exception: it defaults to true in the returned struct even when the attr is
// absent, matching the runtime default. The companion *Set bool on three-
// state booleans distinguishes "explicitly configured" from "absent" so
// consumers that want to treat absence as "leave the existing value" can.
type AgentNodeConfig struct {
	Backend         string
	WorkingDir      string
	McpServers      string // raw JSON; parsed by the handler
	AllowedTools    string
	DisallowedTools string
	MaxBudgetUSD    float64
	MaxCostUSD      float64 // #304: per-node cost ceiling in USD; 0 = unlimited
	NoProgressTurns int     // #304: halt after K consecutive tool-call-free turns; 0 = disabled
	PermissionMode  string
	ACPAgent        string

	// ToolAccess restricts the agent's tool surface. When non-empty (any
	// value), the runtime registers zero tools, sets ToolChoice=none on
	// LLM requests, scrubs tool-naming text from the system prompt, and
	// rejects Params bypass keys. Canonical: case-insensitive, whitespace-
	// trimmed. Fail-closed for typos. See agent.SessionConfig.ToolAccess.
	// Issue: github.com/2389-research/tracker#258.
	ToolAccess string

	AutoStatus        bool
	ReflectOnError    bool // initialized to true by AgentConfig; explicit "false" disables
	ReflectOnErrorSet bool // true when the attr was present on the node

	VerifyAfterEdit    bool
	VerifyAfterEditSet bool
	VerifyCommand      string
	MaxVerifyRetries   int

	PlanBeforeExecute    bool
	PlanBeforeExecuteSet bool

	Model            string
	Provider         string
	SystemPrompt     string
	MaxTurns         int
	TurnBreachPolicy string // #303: "guard" (default) or "fail" (opt-out)
	CommandTimeout   time.Duration
	ReasoningEffort  string

	ResponseFormat string
	ResponseSchema string

	// WritablePaths bounds the file paths this agent's tools may write,
	// as author-chosen globs resolved against the session root. Non-empty
	// triggers the runtime fs-jail (Linux Landlock for Bash subprocess +
	// openat2 for in-process Write/Edit/ApplyPatch). Distinguishing absent
	// from present-but-empty requires WritablePathsSet — the configureJail
	// gate refuses-to-start when Set && len == 0 so a malformed/whitespace
	// attr can never silently degrade to unbounded. See issue #272.
	WritablePaths []string

	// WritablePathsSet records whether the writable_paths attr was present
	// on the node. Distinguishes "absent" (Set=false, jail disabled) from
	// "present but parses to no entries" (Set=true, fail-CLOSED at the
	// codergen configureJail gate per issue #272). Mirrors the three-state
	// pattern of ReflectOnErrorSet et al. above.
	WritablePathsSet bool

	CacheToolResults    bool
	CacheToolResultsSet bool

	// ContextCompaction carries the raw attr value. "auto" enables automatic
	// compaction; any other non-empty value (e.g. "none") disables it; ""
	// means the attr was absent. Kept as string so future modes can be added
	// without a type change.
	ContextCompaction    string
	ContextCompactionSet bool
	CompactionThreshold  float64
}

// IsGoalGate reports whether the node is marked `goal_gate: true`. Shared by
// the engine's goal-gate retry logic and the codergen handler's fail-closed
// missing-STATUS rule (#346).
func (n *Node) IsGoalGate() bool {
	return n.Attrs["goal_gate"] == "true"
}

// AgentConfig returns the typed agent config for the node, merging graphAttrs
// defaults with node.Attrs overrides. Graph-level values apply to all agent
// nodes unless a node explicitly overrides the same attr. Unparseable numeric
// strings fall back to the zero value (matching the previous permissive
// behavior of the handler apply* methods).
func (n *Node) AgentConfig(graphAttrs map[string]string) AgentNodeConfig {
	cfg := AgentNodeConfig{
		// ReflectOnError semantically defaults to true. Setting it here
		// rather than leaving the zero-value means the struct's value
		// matches the documented behavior even for the "unset" case —
		// consumers that copy cfg.ReflectOnError directly don't accidentally
		// disable reflection on untouched nodes.
		ReflectOnError: true,
		// #303: graduated guard is the default; turn_breach_policy: fail opts out.
		TurnBreachPolicy: "guard",
	}
	// Non-overridable (node-only) simple strings.
	cfg.Backend = n.Attrs["backend"]
	cfg.WorkingDir = n.Attrs["working_dir"]
	cfg.McpServers = n.Attrs["mcp_servers"]
	cfg.AllowedTools = n.Attrs["allowed_tools"]
	cfg.DisallowedTools = n.Attrs["disallowed_tools"]
	cfg.PermissionMode = n.Attrs["permission_mode"]
	cfg.ToolAccess = n.Attrs["tool_access"]
	cfg.ACPAgent = n.Attrs["acp_agent"]
	cfg.SystemPrompt = n.Attrs["system_prompt"]
	cfg.ResponseFormat = n.Attrs["response_format"]
	cfg.ResponseSchema = n.Attrs["response_schema"]

	if v := n.Attrs["max_budget_usd"]; v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			cfg.MaxBudgetUSD = f
		}
	}
	// #304: max_cost_usd — per-node cost ceiling. Graph default (positive only)
	// then node override. Node-level "0" explicitly disables a graph default.
	if v, ok := graphAttrs["max_cost_usd"]; ok && v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f > 0 {
			cfg.MaxCostUSD = f
		}
	}
	if v, ok := n.Attrs["max_cost_usd"]; ok && v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f >= 0 {
			cfg.MaxCostUSD = f // 0 disables the inherited graph default
		}
	}
	// #304: no_progress_turns — no-progress detector K. Graph default (positive
	// only) then node override. Node-level "0" explicitly disables a graph default.
	if v, ok := graphAttrs["no_progress_turns"]; ok && v != "" {
		if i, err := strconv.Atoi(v); err == nil && i > 0 {
			cfg.NoProgressTurns = i
		}
	}
	if v, ok := n.Attrs["no_progress_turns"]; ok && v != "" {
		if i, err := strconv.Atoi(v); err == nil && i >= 0 {
			cfg.NoProgressTurns = i // 0 disables the inherited graph default
		}
	}
	if v := n.Attrs["max_turns"]; v != "" {
		if i, err := strconv.Atoi(v); err == nil && i > 0 {
			cfg.MaxTurns = i
		}
	}
	// #303 turn_breach_policy: graph default then node override. Arrives via a
	// dippin params: block, spilled into n.Attrs by the adapter.
	if v, ok := graphAttrs["turn_breach_policy"]; ok && v != "" {
		cfg.TurnBreachPolicy = v
	}
	if v, ok := n.Attrs["turn_breach_policy"]; ok && v != "" {
		cfg.TurnBreachPolicy = v
	}
	if v := n.Attrs["command_timeout"]; v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			cfg.CommandTimeout = d
		}
	}

	if v, ok := n.Attrs["auto_status"]; ok {
		cfg.AutoStatus = v == "true"
	}

	// Three-state: explicit "false" disables; any other value or absence leaves
	// ReflectOnError at its default. Set flag records "was present".
	if v, ok := n.Attrs["reflect_on_error"]; ok {
		cfg.ReflectOnError = v != "false"
		cfg.ReflectOnErrorSet = true
	}

	// verify_after_edit + verify_command + max_verify_retries: graph-level
	// defaults then node-level overrides.
	if v, ok := graphAttrs["verify_after_edit"]; ok {
		cfg.VerifyAfterEdit = v == "true"
		cfg.VerifyAfterEditSet = true
	}
	if v, ok := graphAttrs["verify_command"]; ok && v != "" {
		cfg.VerifyCommand = v
	}
	if v, ok := graphAttrs["max_verify_retries"]; ok && v != "" {
		if i, err := strconv.Atoi(v); err == nil && i > 0 {
			cfg.MaxVerifyRetries = i
		}
	}
	if v, ok := n.Attrs["verify_after_edit"]; ok {
		cfg.VerifyAfterEdit = v == "true"
		cfg.VerifyAfterEditSet = true
	}
	if v, ok := n.Attrs["verify_command"]; ok && v != "" {
		cfg.VerifyCommand = v
	}
	if v, ok := n.Attrs["max_verify_retries"]; ok && v != "" {
		if i, err := strconv.Atoi(v); err == nil && i > 0 {
			cfg.MaxVerifyRetries = i
		}
	}

	// plan_before_execute (with "plan" shorthand alias): graph then node.
	if v, ok := graphAttrs["plan_before_execute"]; ok {
		cfg.PlanBeforeExecute = v == "true"
		cfg.PlanBeforeExecuteSet = true
	}
	if v, ok := n.Attrs["plan_before_execute"]; ok {
		cfg.PlanBeforeExecute = v == "true"
		cfg.PlanBeforeExecuteSet = true
	} else if v, ok := n.Attrs["plan"]; ok {
		cfg.PlanBeforeExecute = v == "true"
		cfg.PlanBeforeExecuteSet = true
	}

	// Model/provider with graph defaults.
	if v, ok := graphAttrs["llm_model"]; ok {
		cfg.Model = v
	}
	if v, ok := n.Attrs["llm_model"]; ok {
		cfg.Model = v
	}
	if v, ok := graphAttrs["llm_provider"]; ok {
		cfg.Provider = v
	}
	if v, ok := n.Attrs["llm_provider"]; ok {
		cfg.Provider = v
	}

	// reasoning_effort: graph then node; non-empty wins.
	if v, ok := graphAttrs["reasoning_effort"]; ok && v != "" {
		cfg.ReasoningEffort = v
	}
	if v, ok := n.Attrs["reasoning_effort"]; ok && v != "" {
		cfg.ReasoningEffort = v
	}

	// cache_tool_results: graph default true-only; node-level tri-state override.
	if v, ok := graphAttrs["cache_tool_results"]; ok && v == "true" {
		cfg.CacheToolResults = true
		cfg.CacheToolResultsSet = true
	}
	if v, ok := n.Attrs["cache_tool_results"]; ok {
		cfg.CacheToolResults = v == "true"
		cfg.CacheToolResultsSet = true
	}

	// context_compaction: graph "auto" default, node override.
	if v, ok := graphAttrs["context_compaction"]; ok && v == "auto" {
		cfg.ContextCompaction = "auto"
		cfg.ContextCompactionSet = true
		cfg.CompactionThreshold = 0.6
	}
	if v, ok := n.Attrs["context_compaction"]; ok {
		cfg.ContextCompaction = v
		cfg.ContextCompactionSet = true
	}
	if v, ok := n.Attrs["context_compaction_threshold"]; ok && v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			cfg.CompactionThreshold = f
		}
	}

	if raw, ok := n.Attrs["writable_paths"]; ok {
		cfg.WritablePathsSet = true
		cfg.WritablePaths = splitCommaNoEmpty(raw)
	}

	return cfg
}

// splitCommaNoEmpty splits s on commas, trims whitespace from each entry, and
// drops empty entries. Mirrors dippin's parser/parse_nodes.go splitCommaNoEmpty
// so the round-trip from .dip → IR → adapter → AgentNodeConfig produces identical
// slices regardless of which path was taken.
func splitCommaNoEmpty(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// ToolNodeConfig is a typed view over a tool node's attributes. Tool nodes
// execute shell commands and surface stdout/stderr to the pipeline context.
type ToolNodeConfig struct {
	Command     string
	OutputLimit int // bytes; 0 means use default
	WorkingDir  string
	PassEnv     string        // comma-separated env var names to pass through
	Timeout     time.Duration // raw parsed timeout from node attrs; zero means the attr was absent, unparseable, or parsed to 0. ToolHandler.parseTimeout rejects non-positive values at execution time.
	MarkerGrep  string        // regex applied to captured stdout to extract a routing marker into ctx.tool_marker (issue #210). Empty disables. If non-empty and no match, the node fails with OutcomeFail and an EventToolMarkerMissing audit event is emitted.
	// RouteRequired is true when the node MUST receive a _TRACKER_ROUTE=
	// sentinel line in its captured stdout (issue #212). Sentinel
	// extraction itself runs unconditionally; this flag controls whether
	// the absence of a match fails the node. When true, no match →
	// OutcomeFail + EventToolRouteMissing. Symmetric to marker_grep's
	// failure path, but the matcher is built-in (no per-node regex).
	RouteRequired bool
}

// parseBoolAttr returns true if v is one of the accepted truthy spellings
// for a tracker node attribute: "true", "1", "yes", "y", "on", "TRUE", etc.
// All other values (including empty string) return false. Used by typed
// node-config accessors to read boolean attrs without per-call ParseBool
// boilerplate.
func parseBoolAttr(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "true", "1", "yes", "y", "on":
		return true
	}
	return false
}

// ToolConfig returns the typed tool config for the node.
func (n *Node) ToolConfig() ToolNodeConfig {
	cfg := ToolNodeConfig{
		Command:       n.Attrs["tool_command"],
		WorkingDir:    n.Attrs["working_dir"],
		PassEnv:       n.Attrs["tool_pass_env"],
		MarkerGrep:    n.Attrs["marker_grep"],
		RouteRequired: parseBoolAttr(n.Attrs["route_required"]),
	}
	if v := n.Attrs["output_limit"]; v != "" {
		if i, err := strconv.Atoi(v); err == nil && i > 0 {
			cfg.OutputLimit = i
		}
	}
	if v := n.Attrs["timeout"]; v != "" {
		// No positivity guard here — the accessor exposes whatever parses,
		// so the field matches the raw attr semantics. Non-positive values
		// are rejected at execution time by ToolHandler.parseTimeout, which
		// returns an error naming the node and the offending value.
		if d, err := time.ParseDuration(v); err == nil {
			cfg.Timeout = d
		}
	}
	return cfg
}

// HumanNodeConfig is a typed view over a wait.human node's attributes.
// DefaultChoice resolves "default_choice" with fallback to "default" so
// callers don't have to check two keys.
type HumanNodeConfig struct {
	Mode          string // "" or "choice" (default — presents outgoing edge labels), "yes_no", "interview", "freeform"
	DefaultChoice string // "default_choice" attr, falling back to "default"
	Prompt        string
	QuestionsKey  string
	AnswersKey    string
	Timeout       time.Duration
	TimeoutAction string // "fail", "default", or "" (unset — treated as "default")
	// Writes carries the raw "writes:" attr; consumers that need the parsed
	// key list should still call pipeline.ParseDeclaredKeys.
	Writes string
}

// HumanConfig returns the typed human-gate config for the node.
func (n *Node) HumanConfig() HumanNodeConfig {
	cfg := HumanNodeConfig{
		Mode:          n.Attrs["mode"],
		Prompt:        n.Attrs["prompt"],
		QuestionsKey:  n.Attrs["questions_key"],
		AnswersKey:    n.Attrs["answers_key"],
		TimeoutAction: n.Attrs["timeout_action"],
		Writes:        n.Attrs["writes"],
	}
	// default_choice takes precedence over default (matches pre-existing
	// handler behavior — e.g. human.go:handleHumanTimeout).
	if v := n.Attrs["default_choice"]; v != "" {
		cfg.DefaultChoice = v
	} else {
		cfg.DefaultChoice = n.Attrs["default"]
	}
	if v := n.Attrs["timeout"]; v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.Timeout = d
		}
	}
	return cfg
}

// ParallelNodeConfig is a typed view over a parallel node's attributes.
// ParallelTargets is the comma-separated list of branch target node IDs; the
// handler still splits and trims. FanInSources mirrors the same for a fan-in
// node that collects results from multiple upstream branches. JoinID is the
// fan-in node that branches should reconverge on. MaxConcurrency and
// BranchTimeout cap concurrent branches and per-branch wall time; zero means
// unlimited / no timeout.
type ParallelNodeConfig struct {
	ParallelTargets string
	FanInSources    string
	JoinID          string
	MaxConcurrency  int
	BranchTimeout   time.Duration
	// FanInPolicy selects the branch-aggregation policy (#313): "" / "any"
	// (success-if-any, the default), "all", or "quorum" (requires Quorum).
	// The accessor is lenient — handlers validate strictly at execution time.
	FanInPolicy string
	Quorum      int
}

// ParallelConfig returns the typed parallel/fan-in config for the node.
func (n *Node) ParallelConfig() ParallelNodeConfig {
	cfg := ParallelNodeConfig{
		ParallelTargets: n.Attrs["parallel_targets"],
		FanInSources:    n.Attrs["fan_in_sources"],
		JoinID:          n.Attrs["parallel_join"],
		FanInPolicy:     n.Attrs["fan_in_policy"],
	}
	if v := n.Attrs["quorum"]; v != "" {
		if i, err := strconv.Atoi(v); err == nil && i > 0 {
			cfg.Quorum = i
		}
	}
	if v := n.Attrs["max_concurrency"]; v != "" {
		if i, err := strconv.Atoi(v); err == nil && i > 0 {
			cfg.MaxConcurrency = i
		}
	}
	if v := n.Attrs["branch_timeout"]; v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			cfg.BranchTimeout = d
		}
	}
	return cfg
}

// RetryConfig is a typed view over the retry-related attributes shared
// across handlers and the engine's retry logic. It is a lightweight companion
// to RetryPolicy: RetryConfig carries the *raw configured values* (which may
// be absent), while ResolveRetryPolicy folds them into a concrete
// *RetryPolicy with defaults. Use RetryConfig when you need to distinguish
// "unset" from "set to zero"; use ResolveRetryPolicy when you need the
// effective policy to run with.
type RetryConfig struct {
	PolicyName    string // "" means "use graph default or 'standard'"
	MaxRetries    int    // 0 and MaxRetriesSet=false means "unset"
	MaxRetriesSet bool
	BaseDelay     time.Duration
	BaseDelaySet  bool
}

// RetryConfig returns the typed retry config parsed from node and graph
// attributes. Node-level values win over graph-level defaults for each field
// independently. Unparseable node values fall through to the graph default
// rather than silently dropping the whole field — matching the previous
// cascading behavior in Engine.maxRetries.
func (n *Node) RetryConfig(graphAttrs map[string]string) RetryConfig {
	cfg := RetryConfig{}

	if v, ok := n.Attrs["retry_policy"]; ok && v != "" {
		cfg.PolicyName = v
	} else if v, ok := graphAttrs["default_retry_policy"]; ok && v != "" {
		cfg.PolicyName = v
	}

	// max_retries: try node first; if present but unparseable, cascade to
	// graph default rather than leaving MaxRetriesSet=false. Preserves the
	// old engine_checkpoint.maxRetries fall-through semantics.
	if v, ok := n.Attrs["max_retries"]; ok && v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			cfg.MaxRetries = i
			cfg.MaxRetriesSet = true
		}
	}
	if !cfg.MaxRetriesSet {
		if v, ok := graphAttrs["default_max_retry"]; ok && v != "" {
			if i, err := strconv.Atoi(v); err == nil {
				cfg.MaxRetries = i
				cfg.MaxRetriesSet = true
			}
		}
	}

	// base_delay: same cascade pattern as max_retries. The graph key
	// `default_base_delay` is reserved for symmetry with the other retry
	// attrs even though the current codebase doesn't set it from pipelines
	// yet — honoring it here keeps the accessor's contract consistent.
	if v, ok := n.Attrs["base_delay"]; ok && v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.BaseDelay = d
			cfg.BaseDelaySet = true
		}
	}
	if !cfg.BaseDelaySet {
		if v, ok := graphAttrs["default_base_delay"]; ok && v != "" {
			if d, err := time.ParseDuration(v); err == nil {
				cfg.BaseDelay = d
				cfg.BaseDelaySet = true
			}
		}
	}

	return cfg
}
