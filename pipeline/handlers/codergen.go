// ABOUTME: Codergen handler that creates agent.Session from node attributes and runs the agent loop.
// ABOUTME: Key integration point between Layer 3 (pipeline) and Layer 2 (agent), capturing LLM responses.
package handlers

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"

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
	// #303: turn-breach guard applies only to the native backend (claude-code /
	// acp don't drive agent.Session's turn loop and never set MaxTurnsUsed/
	// BreachVerify). Make that an explicit guard, not an accident of field
	// population. Mirrors trackExternalBackendUsage's type switch.
	_, native := backend.(*NativeBackend)
	// writable_paths gate (#272 G2) at the dispatcher layer. NativeBackend.Run
	// also runs configureJail with the same gate, but only the native path
	// reaches it — buildRunConfig switches Extra to ClaudeCodeConfig / ACPConfig
	// for the other backends, dropping the SessionConfig that carried
	// WritablePathsSet. Without this earlier check a node with
	// writable_paths + backend:claude-code / acp would silently start
	// unjailed instead of refuse-to-start (#275 review, Copilot
	// codergen.go:647).
	if err := refuseWritablePathsOnUnsupportedBackend(node, backend); err != nil {
		return pipeline.Outcome{}, fmt.Errorf("node %q: %w", node.ID, err)
	}
	runCfg, cfgErr := h.buildRunConfig(node, prompt, backend)
	if cfgErr != nil {
		return pipeline.Outcome{}, fmt.Errorf("node %q config: %w", node.ID, cfgErr)
	}
	priorEpisodes := h.injectPriorEpisodes(runCfg, pctx)

	var collector transcriptCollector
	scopedHandler := agent.NodeScopedHandler(node.ID, h.eventHandler)
	multiHandler := agent.MultiHandler(&collector, scopedHandler)
	emitCallback := func(evt agent.Event) {
		multiHandler.HandleEvent(evt)
	}

	artifactRoot := h.resolveArtifactRoot(pctx)
	sessResult, runErr := backend.Run(ctx, runCfg, emitCallback)
	h.trackExternalBackendUsage(backend, sessResult.Usage, runCfg.Model)

	if runErr != nil {
		return h.handleRunError(runErr, node, prompt, artifactRoot, sessResult, &collector, priorEpisodes)
	}
	return h.buildOutcome(node, prompt, artifactRoot, sessResult, &collector, priorEpisodes, native)
}

// trackExternalBackendUsage reports token usage for backends that bypass the LLM middleware.
// Native backend usage is tracked by the middleware automatically — skip to avoid double-counting.
// The model is threaded through so TokenTracker.CostByProvider can resolve
// per-provider cost directly instead of falling back to graph.Attrs["llm_model"]
// (which is often empty for workflows that set the model per-node), which would
// leave ProviderTotals["claude-code"|"acp"].USD = 0 and silently break --max-cost
// enforcement for these backends.
func (h *CodergenHandler) trackExternalBackendUsage(backend pipeline.AgentBackend, usage llm.Usage, model string) {
	if h.tokenTracker == nil {
		return
	}
	if usage.TotalTokens == 0 && usage.InputTokens == 0 && usage.OutputTokens == 0 {
		return
	}
	switch backend.(type) {
	case *ClaudeCodeBackend:
		h.tokenTracker.AddUsage("claude-code", usage, model)
	case *ACPBackend:
		h.tokenTracker.AddUsage("acp", usage, model)
	}
}

// selectBackend chooses the appropriate AgentBackend based on node attributes
// and global default settings. Priority:
//  1. Per-node "backend" attribute (highest — always wins over global flag)
//  2. Global --backend flag (fallback for nodes without explicit backend attr)
//  3. Default: native
//
// This means a node with "backend: native" always uses native even when the
// global --backend flag is "claude-code", enabling mixed-backend pipelines.
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
// Returns an error if no LLM client is available. This can happen when a node
// has "backend: native" but the global --backend is "claude-code" and no API
// keys are configured. Thread-safe via sync.Once for parallel node execution.
func (h *CodergenHandler) ensureNativeBackend() (pipeline.AgentBackend, error) {
	if h.client == nil {
		if h.defaultBackendName == "claude-code" || h.defaultBackendName == "acp" {
			return nil, fmt.Errorf(
				"node requests native backend via \"backend: native\" (alias \"backend: codergen\") attr, but no API keys are configured — "+
					"configure LLM provider API keys (ANTHROPIC_API_KEY, OPENAI_API_KEY, etc.) "+
					"to use native backend alongside --backend %s",
				h.defaultBackendName,
			)
		}
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

	cfg := pipeline.AgentRunConfig{
		Prompt:       prompt,
		SystemPrompt: sessionCfg.SystemPrompt,
		Model:        sessionCfg.Model,
		Provider:     sessionCfg.Provider,
		WorkingDir:   sessionCfg.WorkingDir,
		MaxTurns:     sessionCfg.MaxTurns,
		ToolAccess:   sessionCfg.ToolAccess,
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
	if err := applyMCPServers(node, ccCfg); err != nil {
		return err
	}
	applyToolLists(node, ccCfg)
	applyClaudeCodeToolAccess(node, ccCfg)
	if err := pipeline.ValidateToolLists(ccCfg.AllowedTools, ccCfg.DisallowedTools); err != nil {
		return fmt.Errorf("node %q: %w", node.ID, err)
	}
	return nil
}

// applyMCPServers parses and sets MCPServers from node attrs if present.
func applyMCPServers(node *pipeline.Node, ccCfg *pipeline.ClaudeCodeConfig) error {
	raw, ok := node.Attrs["mcp_servers"]
	if !ok || raw == "" {
		return nil
	}
	servers, err := pipeline.ParseMCPServers(raw)
	if err != nil {
		return fmt.Errorf("node %q: %w", node.ID, err)
	}
	ccCfg.MCPServers = servers
	return nil
}

// applyToolLists sets AllowedTools and DisallowedTools from node attrs if present.
//
// tool_access enforcement (issue #258): when the node carries
// `tool_access: <any>`, the Params bypass keys `allowed_tools` and
// `disallowed_tools` are ignored — they could otherwise re-enable tools
// the directive intends to deny. For the claude-code backend, the deny
// list is set explicitly by applyClaudeCodeToolAccess (best-effort
// enumeration of canonical tool names).
func applyToolLists(node *pipeline.Node, ccCfg *pipeline.ClaudeCodeConfig) {
	if isNodeToolAccessRestricted(node) {
		return
	}
	if raw := node.Attrs["allowed_tools"]; raw != "" {
		ccCfg.AllowedTools = pipeline.ParseToolList(raw)
	}
	if raw := node.Attrs["disallowed_tools"]; raw != "" {
		ccCfg.DisallowedTools = pipeline.ParseToolList(raw)
	}
}

// isNodeToolAccessRestricted reports whether the node's `tool_access` attr
// is set to any non-empty (canonical) value. Mirrors
// agent.SessionConfig.IsToolAccessRestricted; defined here to avoid an
// import cycle. Issue: github.com/2389-research/tracker#258.
func isNodeToolAccessRestricted(node *pipeline.Node) bool {
	return strings.TrimSpace(node.Attrs["tool_access"]) != ""
}

// canonicalClaudeCodeToolDenyList is the best-effort enumeration of
// Claude Code tool names used to populate the CLI's --disallowedTools
// flag (see appendToolFlags / buildArgs for the actual invocation) when
// `tool_access: <any>` is set on a node that targets the claude-code
// backend. Kept in sync with the names the Claude Code CLI recognizes;
// a stricter approach (fail backend creation) is taken for backends
// where we cannot verify the deny spelling — see backend_acp.go.
//
// Issue: github.com/2389-research/tracker#258.
var canonicalClaudeCodeToolDenyList = []string{
	"Bash",
	"Edit",
	"Glob",
	"Grep",
	"NotebookEdit",
	"Read",
	"Task",
	"TodoWrite",
	"WebFetch",
	"WebSearch",
	"Write",
}

// applyClaudeCodeToolAccess applies the `tool_access` directive to the
// claude-code backend by populating DisallowedTools with the canonical
// tool name list. If the node also carried `allowed_tools` or
// `disallowed_tools`, applyToolLists already short-circuited and the
// caller-supplied lists are ignored — by design.
func applyClaudeCodeToolAccess(node *pipeline.Node, ccCfg *pipeline.ClaudeCodeConfig) {
	if !isNodeToolAccessRestricted(node) {
		return
	}
	ccCfg.AllowedTools = nil
	// Clone so a downstream mutator on ccCfg.DisallowedTools can't drift
	// the package-level canonical list.
	ccCfg.DisallowedTools = slices.Clone(canonicalClaudeCodeToolDenyList)
}

// parseClaudeCodeBudgetAttrs parses max_budget_usd and permission_mode.
func parseClaudeCodeBudgetAttrs(node *pipeline.Node, ccCfg *pipeline.ClaudeCodeConfig) error {
	if err := applyMaxBudget(node, ccCfg); err != nil {
		return err
	}
	return applyPermissionMode(node, ccCfg)
}

// applyMaxBudget parses and applies the max_budget_usd attribute if present.
func applyMaxBudget(node *pipeline.Node, ccCfg *pipeline.ClaudeCodeConfig) error {
	raw, ok := node.Attrs["max_budget_usd"]
	if !ok || raw == "" {
		return nil
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return fmt.Errorf("node %q: invalid max_budget_usd %q: %w", node.ID, raw, err)
	}
	if v > 0 {
		ccCfg.MaxBudgetUSD = v
	}
	return nil
}

// applyPermissionMode parses and applies the permission_mode attribute if present.
//
// tool_access enforcement (issue #258): when the node carries
// `tool_access: <any>`, the Params bypass key `permission_mode` is
// ignored — `permission_mode: bypassPermissions` or `acceptEdits` could
// otherwise re-enable tool execution the directive intends to deny.
func applyPermissionMode(node *pipeline.Node, ccCfg *pipeline.ClaudeCodeConfig) error {
	if isNodeToolAccessRestricted(node) {
		return nil
	}
	raw, ok := node.Attrs["permission_mode"]
	if !ok || raw == "" {
		return nil
	}
	mode := pipeline.PermissionMode(raw)
	if !mode.Valid() {
		return fmt.Errorf("node %q: invalid permission_mode %q (valid: plan, acceptEdits, bypassPermissions, default, dontAsk, auto)", node.ID, raw)
	}
	ccCfg.PermissionMode = mode
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
func (h *CodergenHandler) handleRunError(runErr error, node *pipeline.Node, prompt, artifactRoot string, sessResult agent.SessionResult, collector *transcriptCollector, priorEpisodes []string) (pipeline.Outcome, error) {
	var cfgErr *llm.ConfigurationError
	if errors.As(runErr, &cfgErr) {
		return pipeline.Outcome{}, fmt.Errorf("node %q: %w", node.ID, runErr)
	}

	if pe, ok := runErr.(llm.ProviderErrorInterface); ok && !pe.Retryable() {
		return pipeline.Outcome{}, fmt.Errorf("node %q: %w", node.ID, runErr)
	}

	outcome := pipeline.Outcome{
		Status: string(pipeline.OutcomeRetry),
		ContextUpdates: map[string]string{
			pipeline.ContextKeyLastResponse:             runErr.Error(),
			pipeline.ContextKeyResponsePrefix + node.ID: runErr.Error(),
		},
		Stats: buildSessionStats(sessResult),
	}
	h.applyEpisodeContextUpdates(outcome.ContextUpdates, sessResult, priorEpisodes)
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
func (h *CodergenHandler) buildOutcome(node *pipeline.Node, prompt, artifactRoot string, sessResult agent.SessionResult, collector *transcriptCollector, priorEpisodes []string, native bool) (pipeline.Outcome, error) {
	responseText := collector.text()
	responseArtifact := collector.transcript()
	if responseArtifact == "" {
		responseArtifact = responseText
	}
	responseArtifact += "\n\n" + sessResult.String()

	if outcome, ok, err := h.buildEmptyResponseOutcome(node, prompt, artifactRoot, responseText, responseArtifact, sessResult); ok {
		return outcome, err
	}

	return h.buildSuccessOutcome(node, prompt, artifactRoot, responseText, responseArtifact, sessResult, priorEpisodes, native)
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

	status, msg := emptyResponseStatusMsg(node.ID, emptyAPIResponse)
	outcome := pipeline.Outcome{
		Status: string(status),
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

// emptyResponseStatusMsg returns the outcome status and diagnostic message for an empty response.
func emptyResponseStatusMsg(nodeID string, emptyAPIResponse bool) (pipeline.TerminalStatus, string) {
	if emptyAPIResponse {
		return pipeline.OutcomeRetry, fmt.Sprintf("node %q: provider returned empty API response (0 output tokens, 0 tool calls); retrying session", nodeID)
	}
	return pipeline.OutcomeFail, fmt.Sprintf("node %q: agent session produced no output (0 tokens, 0 tool calls) — check provider/model configuration", nodeID)
}

// buildSuccessOutcome handles the normal (non-empty) completion path, including
// turn-limit exhaustion and auto_status overrides.
func (h *CodergenHandler) buildSuccessOutcome(node *pipeline.Node, prompt, artifactRoot, responseText, responseArtifact string, sessResult agent.SessionResult, priorEpisodes []string, native bool) (pipeline.Outcome, error) {
	// Determine status, the turn-limit message, and the #303 breach class.
	// A turn-limit breach is classified (verify-green→success, loop→fail,
	// else→operator) rather than unconditionally failed; auto_status is honored
	// only on normal completion, never to rescue a breach. See
	// resolveTerminalStatus / classifyBreach.
	status, turnLimitMsg, breachClass := h.resolveTerminalStatus(node, responseText, sessResult, native)

	outcome := pipeline.Outcome{
		Status: string(status),
		ContextUpdates: map[string]string{
			pipeline.ContextKeyLastResponse:             responseText,
			pipeline.ContextKeyResponsePrefix + node.ID: responseText,
		},
		Stats: buildSessionStats(sessResult),
	}
	h.applyEpisodeContextUpdates(outcome.ContextUpdates, sessResult, priorEpisodes)
	if applyDeclaredWrites(node, outcome.ContextUpdates, responseText, "Response JSON") {
		outcome.Status = string(pipeline.OutcomeFail)
	}
	applyBreachMarker(outcome.ContextUpdates, outcome.Status, breachClass)
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

func (h *CodergenHandler) injectPriorEpisodes(runCfg pipeline.AgentRunConfig, pctx *pipeline.PipelineContext) []string {
	sc, ok := runCfg.Extra.(*agent.SessionConfig)
	if !ok || sc == nil {
		return nil
	}
	raw, ok := pctx.Get(pipeline.ContextKeyEpisodeSummaries)
	if !ok || strings.TrimSpace(raw) == "" {
		return nil
	}
	sc.PriorEpisodeSummaries = agent.ParseEpisodeSummaries(raw)
	return append([]string(nil), sc.PriorEpisodeSummaries...)
}

func (h *CodergenHandler) applyEpisodeContextUpdates(updates map[string]string, sessResult agent.SessionResult, existing []string) {
	if updates == nil {
		return
	}
	updates[pipeline.ContextKeyEpisodeSummary] = sessResult.EpisodeSummary
	if strings.TrimSpace(sessResult.EpisodeSummary) == "" {
		return
	}
	summaries := append(append([]string(nil), existing...), sessResult.EpisodeSummary)
	updates[pipeline.ContextKeyEpisodeSummaries] = agent.SerializeEpisodeSummaries(summaries)
}

// maxTurnsOverrideSubdir is the tracker-owned, working-dir-relative directory
// holding per-node warm-continue MaxTurns overrides (#318). One file per node
// ID; its integer contents replace the node's static max_turns on re-entry.
const maxTurnsOverrideSubdir = ".tracker/turn_overrides"

// resolveMaxTurns returns the warm-continue MaxTurns override for nodeID under
// workingDir when one is present, else base. Keeps the override branch out of
// buildConfig (which is already a long flat sequence of attr applies). #318.
func resolveMaxTurns(workingDir, nodeID string, base int) int {
	if override := readMaxTurnsOverride(workingDir, nodeID); override > 0 {
		return override
	}
	return base
}

// safeNodeFilename reports whether nodeID is usable as a single path element —
// a bare filename with no separators or parent refs. dippin IDs are identifiers,
// but readMaxTurnsOverride is a general working-dir file read, so it fails closed
// against an ID that could traverse out of the override dir. #318.
func safeNodeFilename(nodeID string) bool {
	return nodeID != "" && nodeID == filepath.Base(nodeID) && nodeID != "." && nodeID != ".."
}

// parsePositiveInt returns the positive integer encoded in s (trimmed), or 0.
func parsePositiveInt(s string) int {
	if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil && n > 0 {
		return n
	}
	return 0
}

// readMaxTurnsOverride returns the node-scoped warm-continue MaxTurns override
// for nodeID under workingDir, or 0 when absent/unreadable/non-positive (a
// no-op so normal runs keep their statically-configured budget). #318.
func readMaxTurnsOverride(workingDir, nodeID string) int {
	if workingDir == "" || !safeNodeFilename(nodeID) {
		return 0
	}
	path := filepath.Join(workingDir, maxTurnsOverrideSubdir, nodeID)
	// Only honor a regular file — a symlink planted here must not be followed.
	if fi, err := os.Lstat(path); err != nil || !fi.Mode().IsRegular() {
		return 0
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	return parsePositiveInt(string(data))
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

// resolveTerminalStatus determines the node's terminal status, the turn-limit
// message, and the #303 breach class. On a breach it classifies via the
// turn_breach_policy; auto_status (an explicit STATUS line) is authoritative on
// NORMAL completion only — it must NOT manufacture success on a breach, since a
// missing/early STATUS line defaults parseAutoStatus to success and would
// silently advance unverified work (#303 decision #5).
func (h *CodergenHandler) resolveTerminalStatus(node *pipeline.Node, responseText string, sessResult agent.SessionResult, native bool) (pipeline.TerminalStatus, string, string) {
	status := pipeline.OutcomeSuccess
	turnLimitMsg := buildTurnLimitMsg(node, sessResult)
	var breachClass string
	if turnLimitMsg != "" {
		policy := node.AgentConfig(h.graphAttrs).TurnBreachPolicy
		status, breachClass = classifyBreach(policy, sessResult, native)
	}
	if node.Attrs["auto_status"] == "true" && turnLimitMsg == "" {
		status = parseAutoStatus(responseText)
	}
	return status, turnLimitMsg, breachClass
}

// applyBreachMarker writes the #303 turn_breach_class to the context updates,
// using the FINAL status. It never leaves a verified_green marker on a Fail (a
// declared-writes failure can demote a green breach to fail). No-op when
// breachClass is empty (opt-out / non-native / non-breach).
func applyBreachMarker(updates map[string]string, status, breachClass string) {
	if breachClass == "" {
		return
	}
	if status == string(pipeline.OutcomeFail) && breachClass == pipeline.TurnBreachClassVerifiedGreen {
		breachClass = pipeline.TurnBreachClassOperatorDecision
	}
	updates[pipeline.ContextKeyTurnBreachClass] = breachClass
}

// classifyBreach maps a turn-limit breach to (status, turn_breach_class) under
// the turn_breach_policy (#303). Called only when buildTurnLimitMsg != "".
//   - policy "fail" or non-native backend → today's guillotine (fail, no marker).
//   - LoopDetected → pathological (fail).
//   - BreachVerifyPassed → verified-green (success; the pipeline's success edge
//     persists the tree, e.g. build_product's CommitIfDirty).
//   - everything else (Failed / NotRun) → operator_decision (fail; routes to
//     fallback / an operator gate). Never silently advances.
func classifyBreach(policy string, r agent.SessionResult, native bool) (pipeline.TerminalStatus, string) {
	if policy == "fail" || !native {
		return pipeline.OutcomeFail, ""
	}
	switch {
	case r.LoopDetected:
		return pipeline.OutcomeFail, pipeline.TurnBreachClassPathological
	case r.BreachVerify == agent.BreachVerifyPassed:
		return pipeline.OutcomeSuccess, pipeline.TurnBreachClassVerifiedGreen
	default:
		return pipeline.OutcomeFail, pipeline.TurnBreachClassOperatorDecision
	}
}

// buildConfig constructs a SessionConfig from the node's attributes, using
// sensible defaults for any unspecified values. Reads go through the typed
// AgentNodeConfig accessor so the graph-default-then-node-override dance is
// centralized and each field is parsed exactly once.
func (h *CodergenHandler) buildConfig(node *pipeline.Node) agent.SessionConfig {
	config := agent.DefaultConfig()

	if h.workingDir != "" {
		config.WorkingDir = h.workingDir
	}

	cfg := node.AgentConfig(h.graphAttrs)

	if cfg.WorkingDir != "" {
		config.WorkingDir = cfg.WorkingDir
	}

	if cfg.Backend != "" {
		// Normalize the documented "codergen" alias to "native" before the
		// jail check. selectBackend already treats both as native; carrying
		// the literal "codergen" alias into SessionConfig would make
		// configureJail's G2 gate reject the node when writable_paths is set
		// (#272 review, codex P2 codergen.go:634).
		if cfg.Backend == "codergen" {
			config.Backend = "native"
		} else {
			config.Backend = cfg.Backend
		}
	}
	if cfg.WritablePathsSet {
		config.WritablePathsSet = true
		config.WritablePaths = append([]string(nil), cfg.WritablePaths...) // defensive copy
	}

	if cfg.Model != "" {
		config.Model = cfg.Model
	}
	if cfg.Provider != "" {
		config.Provider = cfg.Provider
	}
	if cfg.SystemPrompt != "" {
		config.SystemPrompt = cfg.SystemPrompt
	}
	if cfg.MaxTurns > 0 {
		config.MaxTurns = cfg.MaxTurns
	}
	// #318 warm continue+N: a node-scoped, disk-driven override bumps MaxTurns
	// on warm re-entry of this agent node (the operator-decision "continue"
	// path writes it via a capped tool node). Consulted here because MaxTurns
	// is otherwise read statically and nothing reads context for it. A missing
	// or malformed override is a no-op, so normal runs are unaffected. (The
	// conditional lives in resolveMaxTurns to keep buildConfig's branch count flat.)
	config.MaxTurns = resolveMaxTurns(config.WorkingDir, node.ID, config.MaxTurns)
	if cfg.CommandTimeout > 0 {
		config.CommandTimeout = cfg.CommandTimeout
	}
	if cfg.ReasoningEffort != "" {
		config.ReasoningEffort = cfg.ReasoningEffort
	}
	if cfg.ResponseFormat != "" {
		config.ResponseFormat = cfg.ResponseFormat
	}
	if cfg.ResponseSchema != "" {
		config.ResponseSchema = cfg.ResponseSchema
	}
	if cfg.CacheToolResultsSet {
		config.CacheToolResults = cfg.CacheToolResults
	}
	applyTypedCompaction(&config, cfg)
	if cfg.ReflectOnErrorSet && !cfg.ReflectOnError {
		config.ReflectOnError = false
	}
	if cfg.VerifyAfterEditSet {
		config.VerifyAfterEdit = cfg.VerifyAfterEdit
	}
	if cfg.VerifyCommand != "" {
		config.VerifyCommand = cfg.VerifyCommand
	}
	if cfg.MaxVerifyRetries > 0 {
		config.MaxVerifyRetries = cfg.MaxVerifyRetries
	}
	// #303: run verify-on-breach unless the node opted into the guillotine.
	config.VerifyOnBreach = cfg.TurnBreachPolicy != "fail"
	if cfg.PlanBeforeExecuteSet {
		config.PlanBeforeExecute = cfg.PlanBeforeExecute
	}

	// tool_access enforcement (issue #258): thread the directive from the
	// node's AgentConfig into SessionConfig. The session's IsToolAccessRestricted
	// helper does the canonical case-insensitive, whitespace-trimmed check.
	// Any non-empty value disables tools (fail-closed for typos).
	if cfg.ToolAccess != "" {
		config.ToolAccess = cfg.ToolAccess
	}

	return config
}

// applyTypedCompaction applies the context-compaction mode + threshold from
// the typed AgentNodeConfig. The semantics preserve the previous permissive
// behavior: "auto" enables compaction with a 0.6 default threshold, any other
// non-empty value disables it, and an explicit threshold override wins.
func applyTypedCompaction(config *agent.SessionConfig, cfg pipeline.AgentNodeConfig) {
	if !cfg.ContextCompactionSet {
		return
	}
	if cfg.ContextCompaction == "auto" {
		config.ContextCompaction = agent.CompactionAuto
		if cfg.CompactionThreshold > 0 {
			config.CompactionThreshold = cfg.CompactionThreshold
		} else if config.CompactionThreshold == 0 {
			config.CompactionThreshold = 0.6
		}
	} else {
		config.ContextCompaction = agent.CompactionNone
		if cfg.CompactionThreshold > 0 {
			config.CompactionThreshold = cfg.CompactionThreshold
		}
	}
}

// parseAutoStatus scans the response text for STATUS: directives and returns
// the last one found. Case-insensitive matching. Lines inside code fences
// (``` blocks) are skipped to avoid matching hallucinated STATUS lines.
// Falls back to success if no valid STATUS line is found.
func parseAutoStatus(text string) pipeline.TerminalStatus {
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
		if s := parseStatusLine(trimmed); s != "" {
			result = s
		}
	}
	return result
}

// parseStatusLine extracts the status value from a "STATUS: ..." line.
// Returns "" if the line is not a valid STATUS directive.
//
// Markdown-emphasis tolerance (issue #233 Gap 5.1): LLMs commonly emit
// `**STATUS: fail**` or `STATUS: **fail**` when they want the directive
// to draw the eye. strings.Trim with the "*_" cutset strips any
// combination of leading/trailing markdown emphasis markers (bold `**`,
// italic `*`, underscore-bold `__`, underscore-italic `_`) from both
// the full line and the value portion, so the prefix check and value
// switch see the bare token.
func parseStatusLine(trimmed string) pipeline.TerminalStatus {
	trimmed = strings.Trim(trimmed, "*_")
	if !strings.HasPrefix(strings.ToUpper(trimmed), "STATUS:") {
		return ""
	}
	value := strings.TrimSpace(trimmed[len("STATUS:"):])
	value = strings.Trim(value, "*_")
	switch strings.ToLower(value) {
	case "success":
		return pipeline.OutcomeSuccess
	case "fail":
		return pipeline.OutcomeFail
	case "retry":
		return pipeline.OutcomeRetry
	}
	return ""
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
