// ABOUTME: Typed view over Node.Attrs for each handler kind — replaces ad-hoc
// ABOUTME: map[string]string parsing scattered across handlers and engine code.
package pipeline

import (
	"strconv"
	"time"
)

// AgentNodeConfig is a typed view over a codergen (agent) node's attributes.
//
// For the subset of attrs that support graph-level defaults (llm_model,
// llm_provider, reasoning_effort, verify_after_edit, verify_command,
// max_verify_retries, plan_before_execute, cache_tool_results,
// context_compaction), AgentConfig resolves the value from graphAttrs first,
// then lets node.Attrs override when the same key is set on the node. The
// remaining fields are node-only and have no graph fallback.
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
	PermissionMode  string
	ACPAgent        string

	AutoStatus        bool
	ReflectOnError    bool // initialized to true by AgentConfig; explicit "false" disables
	ReflectOnErrorSet bool // true when the attr was present on the node

	VerifyAfterEdit    bool
	VerifyAfterEditSet bool
	VerifyCommand      string
	MaxVerifyRetries   int

	PlanBeforeExecute    bool
	PlanBeforeExecuteSet bool

	Model           string
	Provider        string
	SystemPrompt    string
	MaxTurns        int
	CommandTimeout  time.Duration
	ReasoningEffort string

	ResponseFormat string
	ResponseSchema string

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
	}
	// Non-overridable (node-only) simple strings.
	cfg.Backend = n.Attrs["backend"]
	cfg.WorkingDir = n.Attrs["working_dir"]
	cfg.McpServers = n.Attrs["mcp_servers"]
	cfg.AllowedTools = n.Attrs["allowed_tools"]
	cfg.DisallowedTools = n.Attrs["disallowed_tools"]
	cfg.PermissionMode = n.Attrs["permission_mode"]
	cfg.ACPAgent = n.Attrs["acp_agent"]
	cfg.SystemPrompt = n.Attrs["system_prompt"]
	cfg.ResponseFormat = n.Attrs["response_format"]
	cfg.ResponseSchema = n.Attrs["response_schema"]

	if v := n.Attrs["max_budget_usd"]; v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			cfg.MaxBudgetUSD = f
		}
	}
	if v := n.Attrs["max_turns"]; v != "" {
		if i, err := strconv.Atoi(v); err == nil && i > 0 {
			cfg.MaxTurns = i
		}
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

	return cfg
}

// ToolNodeConfig is a typed view over a tool node's attributes. Tool nodes
// execute shell commands and surface stdout/stderr to the pipeline context.
type ToolNodeConfig struct {
	Command     string
	OutputLimit int // bytes; 0 means use default
	WorkingDir  string
	PassEnv     string        // comma-separated env var names to pass through
	Timeout     time.Duration // command timeout; 0 means use default
}

// ToolConfig returns the typed tool config for the node.
func (n *Node) ToolConfig() ToolNodeConfig {
	cfg := ToolNodeConfig{
		Command:    n.Attrs["tool_command"],
		WorkingDir: n.Attrs["working_dir"],
		PassEnv:    n.Attrs["tool_pass_env"],
	}
	if v := n.Attrs["output_limit"]; v != "" {
		if i, err := strconv.Atoi(v); err == nil && i > 0 {
			cfg.OutputLimit = i
		}
	}
	if v := n.Attrs["timeout"]; v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			cfg.Timeout = d
		}
	}
	return cfg
}

// HumanNodeConfig is a typed view over a wait.human node's attributes.
// DefaultChoice resolves "default_choice" with fallback to "default" so
// callers don't have to check two keys.
type HumanNodeConfig struct {
	Mode          string // "" (default), "yes_no", "interview", "freeform"
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
// fan-in node that branches should reconverge on.
type ParallelNodeConfig struct {
	ParallelTargets string
	FanInSources    string
	JoinID          string
}

// ParallelConfig returns the typed parallel/fan-in config for the node.
func (n *Node) ParallelConfig() ParallelNodeConfig {
	return ParallelNodeConfig{
		ParallelTargets: n.Attrs["parallel_targets"],
		FanInSources:    n.Attrs["fan_in_sources"],
		JoinID:          n.Attrs["parallel_join"],
	}
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
