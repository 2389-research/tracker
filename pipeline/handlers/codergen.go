// ABOUTME: Codergen handler that creates agent.Session from node attributes and runs the agent loop.
// ABOUTME: Key integration point between Layer 3 (pipeline) and Layer 2 (agent), capturing LLM responses.
package handlers

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/2389-research/tracker/agent"
	"github.com/2389-research/tracker/agent/exec"
	"github.com/2389-research/tracker/llm"
	"github.com/2389-research/tracker/pipeline"
)

// CodergenHandler invokes an agent session with the prompt from node attributes,
// captures the response text, and maps it to a pipeline outcome.
type CodergenHandler struct {
	client             agent.Completer
	env                exec.ExecutionEnvironment
	workingDir         string
	eventHandler       agent.EventHandler
	graphAttrs         map[string]string
	tokenTracker       *llm.TokenTracker     // for reporting claude-code usage
	nativeBackend      pipeline.AgentBackend // always available
	claudeCodeBackend  pipeline.AgentBackend // lazy-init on first claude-code request
	acpBackend         pipeline.AgentBackend // lazy-init on first acp request
	defaultBackendName string                // from --backend flag, "" means native
	nativeOnce         sync.Once
	claudeMu           sync.Mutex // protects claudeCodeBackend lazy init (retryable)
	acpMu              sync.Mutex // protects acpBackend lazy init (retryable)
}

// NewCodergenHandler creates a CodergenHandler that will use the given LLM client
// and working directory for agent sessions.
func NewCodergenHandler(client agent.Completer, workingDir string, opts ...CodergenOption) *CodergenHandler {
	h := &CodergenHandler{
		client:     client,
		workingDir: workingDir,
	}
	for _, opt := range opts {
		opt(h)
	}
	return h
}

// CodergenOption configures optional CodergenHandler behavior.
type CodergenOption func(*CodergenHandler)

// WithGraphAttrs passes graph-level attributes to the handler for fidelity resolution.
func WithGraphAttrs(attrs map[string]string) CodergenOption {
	return func(h *CodergenHandler) {
		h.graphAttrs = attrs
	}
}

// Name returns the handler name used for registry lookup.
func (h *CodergenHandler) Name() string { return "codergen" }

// Execute creates an agent session from the node's attributes, runs it with the
// prompt, and captures the response. On LLM error, returns OutcomeFail (not a
// handler error). Supports auto_status parsing of STATUS:success/fail/retry
// from the response text.
func (h *CodergenHandler) Execute(ctx context.Context, node *pipeline.Node, pctx *pipeline.PipelineContext) (pipeline.Outcome, error) {
	prompt, err := h.resolvePrompt(node, pctx)
	if err != nil {
		return pipeline.Outcome{}, err
	}

	backend, backendErr := h.selectBackend(node)
	if backendErr != nil {
		return pipeline.Outcome{}, fmt.Errorf("node %q: %w", node.ID, backendErr)
	}
	runCfg, cfgErr := h.buildRunConfig(node, prompt, backend)
	if cfgErr != nil {
		return pipeline.Outcome{}, fmt.Errorf("node %q config: %w", node.ID, cfgErr)
	}

	var collector transcriptCollector
	scopedHandler := agent.NodeScopedHandler(node.ID, h.eventHandler)
	multiHandler := agent.MultiHandler(&collector, scopedHandler)
	emitCallback := func(evt agent.Event) {
		multiHandler.HandleEvent(evt)
	}

	artifactRoot := h.resolveArtifactRoot(pctx)

	sessResult, runErr := backend.Run(ctx, runCfg, emitCallback)

	// Report token usage from backends that bypass the LLM client middleware
	// (e.g., claude-code subprocess, ACP agents). Native backend usage flows
	// through the TokenTracker middleware automatically — skip to avoid double-counting.
	switch backend.(type) {
	case *ClaudeCodeBackend:
		if h.tokenTracker != nil && sessResult.Usage.TotalTokens > 0 {
			h.tokenTracker.AddUsage("claude-code", sessResult.Usage)
		}
	case *ACPBackend:
		if h.tokenTracker != nil && sessResult.Usage.TotalTokens > 0 {
			h.tokenTracker.AddUsage("acp", sessResult.Usage)
		}
	}

	if runErr != nil {
		return h.handleRunError(runErr, node, prompt, artifactRoot, sessResult, &collector)
	}

	return h.buildOutcome(node, prompt, artifactRoot, sessResult, &collector)
}

// selectBackend chooses the appropriate AgentBackend based on node attributes
// and global default settings. When --backend claude-code is set, ALL nodes
// go through the claude CLI — non-Anthropic provider/model attrs are stripped
// so the CLI uses its default model under the user's subscription.
func (h *CodergenHandler) selectBackend(node *pipeline.Node) (pipeline.AgentBackend, error) {
	// Check node-level backend attr (explicit override always wins).
	if backend := node.Attrs["backend"]; backend != "" {
		switch backend {
		case "claude-code":
			return h.ensureClaudeCodeBackend()
		case "acp":
			return h.ensureACPBackend()
		case "native", "codergen":
			return h.ensureNativeBackend()
		default:
			return nil, fmt.Errorf("unknown backend %q for node %q (valid: native, codergen, claude-code, acp)", backend, node.ID)
		}
	}
	// Global --backend flag applies to all nodes.
	switch h.defaultBackendName {
	case "claude-code":
		return h.ensureClaudeCodeBackend()
	case "acp":
		return h.ensureACPBackend()
	}
	return h.ensureNativeBackend()
}

// ensureClaudeCodeBackend returns the claude-code backend, lazily creating it
// on first use. Thread-safe via mutex. Retries on failure (unlike sync.Once)
// so installing claude mid-run can recover without restarting tracker.
func (h *CodergenHandler) ensureClaudeCodeBackend() (pipeline.AgentBackend, error) {
	h.claudeMu.Lock()
	defer h.claudeMu.Unlock()

	if h.claudeCodeBackend != nil {
		return h.claudeCodeBackend, nil
	}
	b, err := NewClaudeCodeBackend()
	if err != nil {
		return nil, fmt.Errorf("claude-code backend: %w", err)
	}
	h.claudeCodeBackend = b
	return b, nil
}

// ensureNativeBackend returns the native backend, lazily creating it if needed.
// Returns an error if no LLM client is available (e.g. --backend acp with no
// API keys configured). Thread-safe via sync.Once for parallel node execution.
func (h *CodergenHandler) ensureNativeBackend() (pipeline.AgentBackend, error) {
	if h.client == nil {
		return nil, fmt.Errorf("native backend requires an LLM client — configure API keys or use --backend acp/claude-code")
	}
	h.nativeOnce.Do(func() {
		if h.nativeBackend == nil {
			h.nativeBackend = NewNativeBackend(h.client, h.env)
		}
	})
	return h.nativeBackend, nil
}

// ensureACPBackend returns the ACP backend, lazily creating it on first use.
// Thread-safe via mutex. Retries on failure so installing an ACP agent mid-run
// can recover without restarting tracker.
func (h *CodergenHandler) ensureACPBackend() (pipeline.AgentBackend, error) {
	h.acpMu.Lock()
	defer h.acpMu.Unlock()

	if h.acpBackend != nil {
		return h.acpBackend, nil
	}
	b := NewACPBackend()
	h.acpBackend = b
	return b, nil
}

// buildRunConfig constructs an AgentRunConfig from node attributes for use with
// any AgentBackend implementation. The full SessionConfig is passed via Extra
// so the native backend can use it directly without losing fields. When the
// selected backend is claude-code, a *ClaudeCodeConfig is built from node attrs
// and placed in Extra instead.
func (h *CodergenHandler) buildRunConfig(node *pipeline.Node, prompt string, backend pipeline.AgentBackend) (pipeline.AgentRunConfig, error) {
	sessionCfg := h.buildConfig(node)
	if wd, ok := node.Attrs["working_dir"]; ok && wd != "" {
		sessionCfg.WorkingDir = wd
	}

	cfg := pipeline.AgentRunConfig{
		Prompt:       prompt,
		SystemPrompt: sessionCfg.SystemPrompt,
		Model:        sessionCfg.Model,
		Provider:     sessionCfg.Provider,
		WorkingDir:   sessionCfg.WorkingDir,
		MaxTurns:     sessionCfg.MaxTurns,
	}

	// Build backend-specific Extra config.
	// CommandTimeout is only applied to native backend (per-tool exec timeout).
	// External backends (claude-code, ACP) handle tool timeouts internally —
	// applying it as a subprocess timeout kills the agent prematurely.
	switch backend.(type) {
	case *ClaudeCodeBackend:
		ccCfg, err := h.buildClaudeCodeConfig(node)
		if err != nil {
			return pipeline.AgentRunConfig{}, err
		}
		cfg.Extra = ccCfg
	case *ACPBackend:
		cfg.Extra = buildACPConfig(node)
	default:
		// Native backend: apply CommandTimeout as the session-level timeout.
		if sessionCfg.CommandTimeout > 0 {
			cfg.Timeout = sessionCfg.CommandTimeout
		}
		cfg.Extra = &sessionCfg
	}
	return cfg, nil
}

// buildClaudeCodeConfig constructs a ClaudeCodeConfig from node attributes for
// the claude-code backend. Returns an error if any attr is malformed.
func (h *CodergenHandler) buildClaudeCodeConfig(node *pipeline.Node) (*pipeline.ClaudeCodeConfig, error) {
	ccCfg := &pipeline.ClaudeCodeConfig{
		// Default to bypassPermissions for headless pipeline use. Without this,
		// the Claude CLI may prompt for interactive approval and hang.
		PermissionMode: pipeline.PermissionBypassPermissions,
	}

	if err := parseClaudeCodeToolAttrs(node, ccCfg); err != nil {
		return nil, err
	}
	if err := parseClaudeCodeBudgetAttrs(node, ccCfg); err != nil {
		return nil, err
	}
	return ccCfg, nil
}

// parseClaudeCodeToolAttrs parses MCP servers, allowed/disallowed tools.
func parseClaudeCodeToolAttrs(node *pipeline.Node, ccCfg *pipeline.ClaudeCodeConfig) error {
	if raw, ok := node.Attrs["mcp_servers"]; ok && raw != "" {
		servers, err := pipeline.ParseMCPServers(raw)
		if err != nil {
			return fmt.Errorf("node %q: %w", node.ID, err)
		}
		ccCfg.MCPServers = servers
	}
	if raw, ok := node.Attrs["allowed_tools"]; ok && raw != "" {
		ccCfg.AllowedTools = pipeline.ParseToolList(raw)
	}
	if raw, ok := node.Attrs["disallowed_tools"]; ok && raw != "" {
		ccCfg.DisallowedTools = pipeline.ParseToolList(raw)
	}
	if err := pipeline.ValidateToolLists(ccCfg.AllowedTools, ccCfg.DisallowedTools); err != nil {
		return fmt.Errorf("node %q: %w", node.ID, err)
	}
	return nil
}

// parseClaudeCodeBudgetAttrs parses max_budget_usd and permission_mode.
func parseClaudeCodeBudgetAttrs(node *pipeline.Node, ccCfg *pipeline.ClaudeCodeConfig) error {
	if raw, ok := node.Attrs["max_budget_usd"]; ok && raw != "" {
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return fmt.Errorf("node %q: invalid max_budget_usd %q: %w", node.ID, raw, err)
		}
		if v > 0 {
			ccCfg.MaxBudgetUSD = v
		}
	}
	if raw, ok := node.Attrs["permission_mode"]; ok && raw != "" {
		mode := pipeline.PermissionMode(raw)
		if !mode.Valid() {
			return fmt.Errorf("node %q: invalid permission_mode %q (valid: plan, acceptEdits, bypassPermissions, default, dontAsk, auto)", node.ID, raw)
		}
		ccCfg.PermissionMode = mode
	}
	return nil
}

// buildACPConfig constructs an ACPConfig from node attributes.
func buildACPConfig(node *pipeline.Node) *pipeline.ACPConfig {
	cfg := &pipeline.ACPConfig{}
	if agent, ok := node.Attrs["acp_agent"]; ok && agent != "" {
		cfg.Agent = agent
	}
	return cfg
}

// resolvePrompt extracts, expands variables, and applies fidelity context to the node prompt.
func (h *CodergenHandler) resolvePrompt(node *pipeline.Node, pctx *pipeline.PipelineContext) (string, error) {
	artifactDir := h.workingDir
	if dir, ok := pctx.GetInternal(pipeline.InternalKeyArtifactDir); ok && dir != "" {
		artifactDir = dir
	}
	return ResolvePrompt(node, pctx, h.graphAttrs, artifactDir)
}

// resolveArtifactRoot returns the artifact directory from pipeline context or working dir.
func (h *CodergenHandler) resolveArtifactRoot(pctx *pipeline.PipelineContext) string {
	if dir, ok := pctx.GetInternal(pipeline.InternalKeyArtifactDir); ok && dir != "" {
		return dir
	}
	return h.workingDir
}

// handleRunError processes session run errors, distinguishing fatal from retryable.
func (h *CodergenHandler) handleRunError(runErr error, node *pipeline.Node, prompt, artifactRoot string, sessResult agent.SessionResult, collector *transcriptCollector) (pipeline.Outcome, error) {
	var cfgErr *llm.ConfigurationError
	if errors.As(runErr, &cfgErr) {
		return pipeline.Outcome{}, fmt.Errorf("node %q: %w", node.ID, runErr)
	}

	if pe, ok := runErr.(llm.ProviderErrorInterface); ok && !pe.Retryable() {
		return pipeline.Outcome{}, fmt.Errorf("node %q: %w", node.ID, runErr)
	}

	outcome := pipeline.Outcome{
		Status: pipeline.OutcomeRetry,
		ContextUpdates: map[string]string{
			pipeline.ContextKeyLastResponse:             runErr.Error(),
			pipeline.ContextKeyResponsePrefix + node.ID: runErr.Error(),
		},
		Stats: buildSessionStats(sessResult),
	}
	responseArtifact := collector.transcript()
	if responseArtifact == "" {
		responseArtifact = runErr.Error()
	}
	responseArtifact += "\n\n" + sessResult.String()
	if err := pipeline.WriteStageArtifacts(artifactRoot, node.ID, prompt, responseArtifact, outcome); err != nil {
		return pipeline.Outcome{}, err
	}
	return outcome, nil
}

// buildOutcome constructs the pipeline outcome from a completed session run.
// Returns OutcomeFail for turn-limit exhaustion and loop detection (routes
// through failure edges when present, stops on strict-failure-edge otherwise),
// OutcomeFail/OutcomeRetry for empty sessions, or OutcomeSuccess for normal
// completion. auto_status can override any of these.
func (h *CodergenHandler) buildOutcome(node *pipeline.Node, prompt, artifactRoot string, sessResult agent.SessionResult, collector *transcriptCollector) (pipeline.Outcome, error) {
	responseText := collector.text()
	responseArtifact := collector.transcript()
	if responseArtifact == "" {
		responseArtifact = responseText
	}
	responseArtifact += "\n\n" + sessResult.String()

	if outcome, ok, err := h.buildEmptyResponseOutcome(node, prompt, artifactRoot, responseText, responseArtifact, sessResult); ok {
		return outcome, err
	}

	return h.buildSuccessOutcome(node, prompt, artifactRoot, responseText, responseArtifact, sessResult)
}

// buildEmptyResponseOutcome handles the two empty-response cases and returns
// (outcome, true, err) when an empty-response condition is detected, or
// (zero, false, nil) when the session has real output and normal handling applies.
//
// Two empty cases:
//  1. Zero turns, zero tool calls → session never started → OutcomeFail
//  2. Has turns but zero output tokens AND zero text → API swallowed error → OutcomeRetry
//
// Case 2 does NOT apply when tool calls > 0 (agent did real work via tools).
func (h *CodergenHandler) buildEmptyResponseOutcome(node *pipeline.Node, prompt, artifactRoot, responseText, responseArtifact string, sessResult agent.SessionResult) (pipeline.Outcome, bool, error) {
	if strings.TrimSpace(responseText) != "" {
		return pipeline.Outcome{}, false, nil
	}

	emptySession := sessResult.TotalToolCalls() == 0 && sessResult.Turns == 0
	emptyAPIResponse := sessResult.Turns > 0 && sessResult.TotalToolCalls() == 0 && sessResult.Usage.OutputTokens == 0

	if !emptySession && !emptyAPIResponse {
		return pipeline.Outcome{}, false, nil
	}

	status := pipeline.OutcomeFail
	msg := fmt.Sprintf("node %q: agent session produced no output (0 tokens, 0 tool calls) — check provider/model configuration", node.ID)
	if emptyAPIResponse {
		status = pipeline.OutcomeRetry
		msg = fmt.Sprintf("node %q: provider returned empty API response (0 output tokens, 0 tool calls); retrying session", node.ID)
	}
	outcome := pipeline.Outcome{
		Status: status,
		ContextUpdates: map[string]string{
			pipeline.ContextKeyLastResponse:             msg,
			pipeline.ContextKeyResponsePrefix + node.ID: msg,
		},
		Stats: buildSessionStats(sessResult),
	}
	if err := pipeline.WriteStageArtifacts(artifactRoot, node.ID, prompt, responseArtifact, outcome); err != nil {
		return pipeline.Outcome{}, true, err
	}
	return outcome, true, nil
}

// buildSuccessOutcome handles the normal (non-empty) completion path, including
// turn-limit exhaustion and auto_status overrides.
func (h *CodergenHandler) buildSuccessOutcome(node *pipeline.Node, prompt, artifactRoot, responseText, responseArtifact string, sessResult agent.SessionResult) (pipeline.Outcome, error) {
	// Determine status. Turn-limit exhaustion and loop detection default to
	// OutcomeFail so the engine routes through explicit failure edges (e.g.
	// "when ctx.outcome = fail"). On nodes without failure edges, the
	// strict-failure-edge rule stops the pipeline — which is correct: if the
	// pipeline author didn't handle agent failure, silently continuing is worse.
	//
	// auto_status overrides the default for both turn-exhaustion and normal
	// completion: the agent's explicit STATUS line is authoritative.
	status := pipeline.OutcomeSuccess
	turnLimitMsg := buildTurnLimitMsg(node, sessResult)
	if turnLimitMsg != "" {
		status = pipeline.OutcomeFail
	}

	if node.Attrs["auto_status"] == "true" {
		status = parseAutoStatus(responseText)
	}

	outcome := pipeline.Outcome{
		Status: status,
		ContextUpdates: map[string]string{
			pipeline.ContextKeyLastResponse:             responseText,
			pipeline.ContextKeyResponsePrefix + node.ID: responseText,
		},
		Stats: buildSessionStats(sessResult),
	}
	if turnLimitMsg != "" {
		outcome.ContextUpdates[pipeline.ContextKeyTurnLimitMsg] = turnLimitMsg
	}
	if sessResult.Usage.EstimatedCost > 0 {
		outcome.ContextUpdates["last_cost"] = fmt.Sprintf("%.4f", sessResult.Usage.EstimatedCost)
	}
	if sessResult.Turns > 0 {
		outcome.ContextUpdates["last_turns"] = strconv.Itoa(sessResult.Turns)
	}
	if err := pipeline.WriteStageArtifacts(artifactRoot, node.ID, prompt, responseArtifact, outcome); err != nil {
		return pipeline.Outcome{}, err
	}
	return outcome, nil
}

// buildTurnLimitMsg returns a non-empty message when the session hit the turn limit,
// and an empty string otherwise.
func buildTurnLimitMsg(node *pipeline.Node, sessResult agent.SessionResult) string {
	if !sessResult.MaxTurnsUsed {
		return ""
	}
	if sessResult.LoopDetected {
		return fmt.Sprintf("node %q: agent entered tool call loop (detected after %d turns)", node.ID, sessResult.Turns)
	}
	return fmt.Sprintf("node %q: agent exhausted turn limit (%d turns) without completing", node.ID, sessResult.Turns)
}

// buildConfig constructs a SessionConfig from the node's attributes, using
// sensible defaults for any unspecified values.
func (h *CodergenHandler) buildConfig(node *pipeline.Node) agent.SessionConfig {
	config := agent.DefaultConfig()

	if h.workingDir != "" {
		config.WorkingDir = h.workingDir
	}

	h.applyModelProvider(&config, node)
	h.applySessionLimits(&config, node)
	h.applyReasoningEffort(&config, node)
	h.applyResponseFormat(&config, node)
	h.applyCacheAndCompaction(&config, node)

	return config
}

// applyModelProvider sets model and provider from graph-level defaults and node-level overrides.
func (h *CodergenHandler) applyModelProvider(config *agent.SessionConfig, node *pipeline.Node) {
	if model, ok := h.graphAttrs["llm_model"]; ok {
		config.Model = model
	}
	if model, ok := node.Attrs["llm_model"]; ok {
		config.Model = model
	}
	if provider, ok := h.graphAttrs["llm_provider"]; ok {
		config.Provider = provider
	}
	if provider, ok := node.Attrs["llm_provider"]; ok {
		config.Provider = provider
	}
}

// applySessionLimits sets system prompt, max turns, and command timeout.
func (h *CodergenHandler) applySessionLimits(config *agent.SessionConfig, node *pipeline.Node) {
	if sp, ok := node.Attrs["system_prompt"]; ok {
		config.SystemPrompt = sp
	}
	if mt, ok := node.Attrs["max_turns"]; ok {
		if v, err := strconv.Atoi(mt); err == nil && v > 0 {
			config.MaxTurns = v
		}
	}
	if ct, ok := node.Attrs["command_timeout"]; ok {
		if d, err := time.ParseDuration(ct); err == nil && d > 0 {
			config.CommandTimeout = d
		}
	}
}

// applyReasoningEffort sets reasoning effort from graph and node attrs.
func (h *CodergenHandler) applyReasoningEffort(config *agent.SessionConfig, node *pipeline.Node) {
	if re, ok := h.graphAttrs["reasoning_effort"]; ok && re != "" {
		config.ReasoningEffort = re
	}
	if re, ok := node.Attrs["reasoning_effort"]; ok && re != "" {
		config.ReasoningEffort = re
	}
}

// applyResponseFormat sets structured output format from node attrs.
// Supported values: "json_object" (any valid JSON) or "json_schema" (with response_schema).
func (h *CodergenHandler) applyResponseFormat(config *agent.SessionConfig, node *pipeline.Node) {
	if rf, ok := node.Attrs["response_format"]; ok && rf != "" {
		config.ResponseFormat = rf
	}
	if schema, ok := node.Attrs["response_schema"]; ok && schema != "" {
		config.ResponseSchema = schema
	}
}

// applyCacheAndCompaction configures tool result caching and context compaction.
func (h *CodergenHandler) applyCacheAndCompaction(config *agent.SessionConfig, node *pipeline.Node) {
	h.applyCacheConfig(config, node)
	h.applyCompactionConfig(config, node)
}

// applyCacheConfig sets CacheToolResults from graph and node attrs.
func (h *CodergenHandler) applyCacheConfig(config *agent.SessionConfig, node *pipeline.Node) {
	if v, ok := h.graphAttrs["cache_tool_results"]; ok && v == "true" {
		config.CacheToolResults = true
	}
	if v, ok := node.Attrs["cache_tool_results"]; ok {
		config.CacheToolResults = (v == "true")
	}
}

// applyCompactionConfig sets ContextCompaction and CompactionThreshold from graph and node attrs.
func (h *CodergenHandler) applyCompactionConfig(config *agent.SessionConfig, node *pipeline.Node) {
	if v, ok := h.graphAttrs["context_compaction"]; ok && v == "auto" {
		config.ContextCompaction = agent.CompactionAuto
		config.CompactionThreshold = 0.6
	}
	if v, ok := node.Attrs["context_compaction"]; ok {
		applyNodeCompaction(config, v)
	}
	if v, ok := node.Attrs["context_compaction_threshold"]; ok {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			config.CompactionThreshold = f
		}
	}
}

// applyNodeCompaction sets compaction mode from a node-level attribute value.
func applyNodeCompaction(config *agent.SessionConfig, v string) {
	if v == "auto" {
		config.ContextCompaction = agent.CompactionAuto
		if config.CompactionThreshold == 0 {
			config.CompactionThreshold = 0.6
		}
	} else {
		config.ContextCompaction = agent.CompactionNone
	}
}

// parseAutoStatus scans the response text for STATUS: directives and returns
// the last one found. Case-insensitive matching. Lines inside code fences
// (``` blocks) are skipped to avoid matching hallucinated STATUS lines.
// Falls back to success if no valid STATUS line is found.
func parseAutoStatus(text string) string {
	result := pipeline.OutcomeSuccess
	inCodeBlock := false
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inCodeBlock = !inCodeBlock
			continue
		}
		if inCodeBlock {
			continue
		}
		upper := strings.ToUpper(trimmed)
		if !strings.HasPrefix(upper, "STATUS:") {
			continue
		}
		status := strings.ToLower(strings.TrimSpace(trimmed[len("STATUS:"):]))
		switch status {
		case "success":
			result = pipeline.OutcomeSuccess
		case "fail":
			result = pipeline.OutcomeFail
		case "retry":
			result = pipeline.OutcomeRetry
		}
	}
	return result
}

// prependContextSummary adds a compacted context summary section to the prompt
// based on the fidelity level and compacted context values.
func prependContextSummary(prompt string, compacted map[string]string, fidelity pipeline.Fidelity) string {
	if len(compacted) == 0 {
		return prompt
	}

	var sections []string
	sections = append(sections, fmt.Sprintf("# Context Summary (fidelity: %s)", fidelity))
	for key, val := range compacted {
		if val == "" {
			continue
		}
		sections = append(sections, fmt.Sprintf("## %s\n%s", key, val))
	}

	if len(sections) <= 1 {
		return prompt
	}

	return strings.Join(sections, "\n\n") + "\n\n---\n\n" + prompt
}

// transcriptCollector and buildSessionStats are in transcript.go
