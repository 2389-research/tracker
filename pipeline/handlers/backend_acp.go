// ABOUTME: ACPBackend spawns ACP-compatible coding agents (claude, codex, gemini) as subprocesses.
// ABOUTME: Implements pipeline.AgentBackend using the Agent Client Protocol over stdio.
package handlers

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	acp "github.com/coder/acp-go-sdk"

	"github.com/2389-research/tracker/agent"
	"github.com/2389-research/tracker/llm"
	"github.com/2389-research/tracker/pipeline"
)

// acpTokenEstimateRatio is the characters-per-token heuristic used to synthesize
// approximate token counts for ACP-backed sessions. The ACP protocol
// (github.com/coder/acp-go-sdk v0.6.x) has no usage surface — PromptResponse
// carries only StopReason + Meta, and no SessionUpdate subtype reports tokens —
// so tracker cannot observe real usage for ACP runs. A character-count estimate
// lets the existing TokenTracker / budget guard path still function for ACP
// nodes; accuracy is intentionally approximate. 4 is the conventional
// chars-per-token figure for English prose; code and structured output skew
// lower (closer to 3) so real usage may modestly exceed the estimate.
const acpTokenEstimateRatio = 4

// acpCacheReadRatioEnv is the env var operators can set to tell
// estimateACPUsage what fraction of the estimated input tokens to bill as
// cache-read (priced at 10% of the input rate by llm.EstimateCost)
// instead of fresh input. Values must be in (0, 1] — anything outside
// that range is treated as "no split". Default (unset or invalid) means
// the whole input is priced as fresh, which over-reports spend for
// heavy-cache workloads but never under-reports it, so budgets stay
// conservative.
const acpCacheReadRatioEnv = "TRACKER_ACP_CACHE_READ_RATIO"

// acpCacheRatioWarned dedupes the "ratio out of range" warning so a
// long-running pipeline with many ACP nodes doesn't spam the log with
// the same misconfiguration on every prompt.
var acpCacheRatioWarned sync.Once

// parseACPCacheReadRatio reads and sanitizes the cache-read fraction.
// Returns 0 when the env var is unset, malformed, or out of (0, 1].
// A single warning is logged the first time an invalid value is seen.
func parseACPCacheReadRatio() float64 {
	raw := os.Getenv(acpCacheReadRatioEnv)
	if raw == "" {
		return 0
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil || v <= 0 || v > 1 {
		acpCacheRatioWarned.Do(func() {
			log.Printf("[acp] %s=%q is outside (0, 1] or unparseable — ignoring; input will be priced as fresh", acpCacheReadRatioEnv, raw)
		})
		return 0
	}
	return v
}

// ACPUsageMarker is written into llm.Usage.Raw on ACP-backed SessionResults
// so buildSessionStats in this package can derive SessionStats.Estimated
// and SessionStats.EstimateSource before Usage.Raw is dropped by later
// llm.Usage.Add calls. Once the marker reaches SessionStats, it's preserved
// through Trace.AggregateUsage into ProviderUsage.Estimated and
// UsageSummary.Estimated, and the CLI summary / TUI header / NDJSON
// cost_updated event surface the flag so operators can distinguish
// heuristic spend from metered spend. Treat the shape as an implementation
// detail — the canonical "this was estimated" channel is SessionStats
// going forward; direct Usage.Raw type-assertions outside
// extractEstimateMarker are not supported.
type ACPUsageMarker struct {
	Estimated bool   `json:"estimated"`
	Source    string `json:"source"` // always "acp-chars-heuristic"
	Ratio     int    `json:"chars_per_token"`
}

// providerToAgent maps LLM provider names to ACP-compatible binary names.
// The official ACP bridge/agent packages are:
//   - anthropic → claude-agent-acp (npm: @agentclientprotocol/claude-agent-acp)
//   - openai   → codex-acp        (npm: @zed-industries/codex-acp)
//   - gemini   → gemini           (npm: @google/gemini-cli, native --acp mode)
var providerToAgent = map[string]string{
	"anthropic": "claude-agent-acp",
	"openai":    "codex-acp",
	"gemini":    "gemini",
}

// acpAgentArgs provides extra CLI arguments needed for specific ACP agents.
// Gemini CLI requires --acp to enter ACP mode; bridges need no extra args.
var acpAgentArgs = map[string][]string{
	"gemini": {"--acp"},
}

// ACPBackend implements pipeline.AgentBackend by spawning ACP-compatible agent
// processes and communicating via the Agent Client Protocol over stdio.
// It routes to the appropriate agent binary based on the node's llm_provider.
//
// Default binary mapping:
//
//   - anthropic → claude-agent-acp (bridge: npm i -g @agentclientprotocol/claude-agent-acp)
//   - openai   → codex-acp       (bridge: npm i -g @zed-industries/codex-acp)
//   - gemini   → gemini --acp    (native ACP mode)
//
// The acp_agent node attribute overrides the binary name.
type ACPBackend struct {
	agentPaths   map[string]string // cached: agent name → resolved binary path
	mu           sync.Mutex
	estimateOnce sync.Once // guards the once-per-backend "tokens are estimates" log line
}

// NewACPBackend creates an ACPBackend with empty path cache.
func NewACPBackend() *ACPBackend {
	return &ACPBackend{
		agentPaths: make(map[string]string),
	}
}

// Run spawns the appropriate ACP agent, initializes the protocol, sends the
// prompt, and collects results. The agent binary is selected based on the
// provider in cfg or the ACPConfig.Agent override.
func (b *ACPBackend) Run(ctx context.Context, cfg pipeline.AgentRunConfig, emit func(agent.Event)) (agent.SessionResult, error) {
	agentName := b.resolveAgentName(cfg)

	agentPath, err := b.ensureAgentPath(agentName)
	if err != nil {
		return agent.SessionResult{}, err
	}

	if cfg.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, cfg.Timeout)
		defer cancel()
	}

	emit(agent.Event{Type: agent.EventLLMRequestPreparing, Timestamp: time.Now(), Provider: "acp:" + agentName})

	proc, err := b.startProcess(ctx, agentPath, agentName, cfg.WorkingDir)
	if err != nil {
		return agent.SessionResult{}, err
	}

	handler := &acpClientHandler{
		emit:       emit,
		workingDir: cfg.WorkingDir,
		toolNames:  make(map[string]string),
	}
	defer handler.cleanup()

	conn := acp.NewClientSideConnection(handler, proc.stdin, proc.stdout)

	sessID, err := b.initSession(ctx, conn, proc, agentName, cfg)
	if err != nil {
		return agent.SessionResult{}, err
	}

	emit(agent.Event{Type: agent.EventTurnStart, Timestamp: time.Now()})
	return b.sendPromptAndCollect(ctx, conn, proc, sessID, agentName, cfg, handler)
}

// sendPromptAndCollect sends the prompt and handles the response/error.
func (b *ACPBackend) sendPromptAndCollect(ctx context.Context, conn *acp.ClientSideConnection, proc *acpProcess, sessID acp.SessionId, agentName string, cfg pipeline.AgentRunConfig, handler *acpClientHandler) (agent.SessionResult, error) {
	promptResp, promptErr := conn.Prompt(ctx, acp.PromptRequest{
		SessionId: sessID,
		Prompt:    buildACPPromptBlocks(cfg),
	})

	forceKilled := waitForProcess(proc.cmd, proc.stdin)
	result := buildACPResult(handler, promptResp, cfg)
	if result.Usage.TotalTokens > 0 {
		// Announce the approximation once per backend instance. Per-session
		// token numbers already land in cost_updated events / activity.jsonl,
		// so repeating them in a log line per prompt is pure noise for
		// pipelines with many ACP nodes.
		b.estimateOnce.Do(func() {
			log.Printf("[acp] token/cost numbers for ACP sessions are character-count estimates (ACP protocol has no native token surface); accuracy varies — see docs/architecture/backends.md")
		})
	}

	if promptErr != nil {
		logStderr(agentName, "prompt", &proc.stderr)
		if ctx.Err() != nil {
			return result, fmt.Errorf("acp: prompt cancelled for %s: %w", agentName, ctx.Err())
		}
		return result, fmt.Errorf("acp: prompt failed for %s: %w", agentName, promptErr)
	}

	if forceKilled {
		log.Printf("[acp] %s force-killed after successful prompt (bridge did not exit on stdin close)", agentName)
	}

	if handler.isEmpty() {
		return result, fmt.Errorf("acp: %s returned empty response (0 text, 0 tool calls)", agentName)
	}

	return result, nil
}

// acpProcess bundles the subprocess handles for an ACP agent.
type acpProcess struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr bytes.Buffer
}

// startProcess launches the ACP agent binary and returns pipe handles.
func (b *ACPBackend) startProcess(ctx context.Context, agentPath, agentName, workingDir string) (*acpProcess, error) {
	args := acpAgentArgs[agentName]
	cmd := exec.CommandContext(ctx, agentPath, args...)
	cmd.Env = buildEnvForACP()
	if workingDir != "" {
		cmd.Dir = workingDir
	}

	proc := &acpProcess{cmd: cmd}
	cmd.Stderr = &proc.stderr

	var err error
	proc.stdin, err = cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("acp: failed to create stdin pipe: %w", err)
	}
	proc.stdout, err = cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("acp: failed to create stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("acp: failed to start %s: %w", agentName, err)
	}
	return proc, nil
}

// initSession initializes the ACP connection, creates a session, and optionally
// sets the model. Returns the session ID.
func (b *ACPBackend) initSession(ctx context.Context, conn *acp.ClientSideConnection, proc *acpProcess, agentName string, cfg pipeline.AgentRunConfig) (acp.SessionId, error) {
	log.Printf("[acp] initializing %s (pid %d)", agentName, proc.cmd.Process.Pid)
	initResp, initErr := conn.Initialize(ctx, acp.InitializeRequest{
		ProtocolVersion: acp.ProtocolVersionNumber,
		ClientCapabilities: acp.ClientCapabilities{
			Fs: acp.FileSystemCapability{
				ReadTextFile:  true,
				WriteTextFile: true,
			},
			Terminal: true,
		},
	})
	if initErr != nil {
		killProcess(proc.cmd)
		logStderr(agentName, "initialize", &proc.stderr)
		return "", fmt.Errorf("acp: initialize failed for %s: %w", agentName, initErr)
	}

	if initResp.ProtocolVersion != acp.ProtocolVersionNumber {
		log.Printf("[acp] warning: %s protocol version mismatch: got %q, want %q", agentName, initResp.ProtocolVersion, acp.ProtocolVersionNumber)
	}

	cwd := cfg.WorkingDir
	if cwd == "" {
		cwd, _ = os.Getwd()
	}
	sessResp, sessErr := conn.NewSession(ctx, acp.NewSessionRequest{
		Cwd:        cwd,
		McpServers: buildACPMcpServers(cfg),
	})
	if sessErr != nil {
		killProcess(proc.cmd)
		logStderr(agentName, "new session", &proc.stderr)
		return "", fmt.Errorf("acp: new session failed for %s: %w", agentName, sessErr)
	}

	if cfg.Model != "" {
		setSessionModel(ctx, conn, sessResp, agentName, cfg.Model)
	}

	return sessResp.SessionId, nil
}

// setSessionModel attempts to set the model on the ACP session. Non-fatal on failure.
func setSessionModel(ctx context.Context, conn *acp.ClientSideConnection, sessResp acp.NewSessionResponse, agentName, model string) {
	bridgeModel := mapModelToBridge(model, sessResp.Models)
	if bridgeModel == "" {
		log.Printf("[acp] skipping SetSessionModel — no bridge match for %q", model)
		return
	}
	_, err := conn.SetSessionModel(ctx, acp.SetSessionModelRequest{
		SessionId: sessResp.SessionId,
		ModelId:   acp.ModelId(bridgeModel),
	})
	if err != nil {
		log.Printf("[acp] warning: SetSessionModel failed for %s (model=%s→%s): %v", agentName, model, bridgeModel, err)
	}
}

// waitForProcess closes stdin and waits for the process to exit with a 5s grace period.
// Returns true if the process was force-killed. Logs non-zero exit status.
func waitForProcess(cmd *exec.Cmd, stdin io.WriteCloser) bool {
	_ = stdin.Close()

	waitCh := make(chan error, 1)
	go func() { waitCh <- cmd.Wait() }()

	select {
	case waitErr := <-waitCh:
		logWaitError(waitErr)
		return false
	case <-time.After(5 * time.Second):
		log.Printf("[acp] process did not exit after 5s, killing pid %d", cmd.Process.Pid)
		killProcess(cmd)
		<-waitCh
		return true
	}
}

// logWaitError logs a non-nil wait error (e.g., non-zero exit status).
func logWaitError(err error) {
	if err != nil {
		log.Printf("[acp] process exited with error: %v", err)
	}
}

// logStderr logs the stderr output of an ACP agent if non-empty.
func logStderr(agentName, phase string, stderr *bytes.Buffer) {
	if s := strings.TrimSpace(stderr.String()); s != "" {
		log.Printf("[acp] %s stderr during %s: %s", agentName, phase, s)
	}
}

// isEmpty returns true if the handler collected no text and no tool calls.
func (h *acpClientHandler) isEmpty() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.textParts) == 0 && h.toolCount == 0
}

// resolveAgentName determines which ACP agent binary to use.
// Priority: ACPConfig.Agent > provider mapping > default (claude).
func (b *ACPBackend) resolveAgentName(cfg pipeline.AgentRunConfig) string {
	if acpCfg, ok := cfg.Extra.(*pipeline.ACPConfig); ok && acpCfg != nil && acpCfg.Agent != "" {
		return acpCfg.Agent
	}
	if cfg.Provider != "" {
		if name, ok := providerToAgent[cfg.Provider]; ok {
			return name
		}
	}
	return "claude-agent-acp" // default fallback
}

// ensureAgentPath resolves and caches the binary path for an ACP agent.
// Thread-safe via mutex. Retries on failure (binary may be installed mid-run).
func (b *ACPBackend) ensureAgentPath(name string) (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if path, ok := b.agentPaths[name]; ok {
		return path, nil
	}

	path, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("acp agent %q not found in PATH: %w", name, err)
	}

	// Verify the binary responds to --version (skip for bridge binaries that may not support it).
	verifyCtx, verifyCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer verifyCancel()
	verifyCmd := exec.CommandContext(verifyCtx, path, "--version")
	if verifyErr := verifyCmd.Run(); verifyErr != nil {
		// Some ACP bridge binaries (e.g. claude-code-acp) may not support --version.
		// LookPath success is sufficient for bridges.
		log.Printf("[acp] note: %s --version failed (%v), proceeding with LookPath result", name, verifyErr)
	}

	b.agentPaths[name] = path
	return path, nil
}

// mapModelToBridge maps a tracker-native model name to an ACP bridge model ID.
// Returns the matching bridge model ID, or "" if no match is found.
// Mapping: tracker names like "claude-sonnet-4-5" are matched by substring
// against bridge IDs like "sonnet", "haiku", "default".
func mapModelToBridge(trackerModel string, models *acp.SessionModelState) string {
	if models == nil {
		return ""
	}
	if id := matchModelDirect(trackerModel, models.AvailableModels); id != "" {
		return id
	}
	return matchModelSubstring(trackerModel, models.AvailableModels)
}

// matchModelDirect returns the first model ID that case-insensitively equals trackerModel.
func matchModelDirect(trackerModel string, models []acp.ModelInfo) string {
	for _, m := range models {
		if strings.EqualFold(string(m.ModelId), trackerModel) {
			return string(m.ModelId)
		}
	}
	return ""
}

// matchModelSubstring returns the first non-default model ID whose name is
// a substring of trackerModel (e.g. "sonnet" in "claude-sonnet-4-5").
func matchModelSubstring(trackerModel string, models []acp.ModelInfo) string {
	lower := strings.ToLower(trackerModel)
	for _, m := range models {
		id := strings.ToLower(string(m.ModelId))
		if id != "default" && strings.Contains(lower, id) {
			return string(m.ModelId)
		}
	}
	return ""
}

// buildACPPromptBlocks constructs ACP ContentBlock slices from the run config.
// If a system prompt is set, it's prepended as a separate text block.
func buildACPPromptBlocks(cfg pipeline.AgentRunConfig) []acp.ContentBlock {
	var blocks []acp.ContentBlock
	if cfg.SystemPrompt != "" {
		blocks = append(blocks, acp.TextBlock("System: "+cfg.SystemPrompt))
	}
	blocks = append(blocks, acp.TextBlock(cfg.Prompt))
	return blocks
}

// acpRuneCounts bundles the four rune-count channels that flow into the ACP
// usage estimate. Splitting them explicitly (instead of passing a single
// pre-summed output count) lets estimateACPUsage attribute reasoning to
// Usage.ReasoningTokens and route tool-result payloads to the input side
// where they're actually billed — the bridge re-sends tool output as
// next-turn input context.
type acpRuneCounts struct {
	MessageOutput int // handler.textParts — visible assistant text (billable output)
	Reasoning     int // handler.reasoningRunes — thought-chunk text (billable output + ReasoningTokens)
	ToolArgs      int // handler.toolArgRunes — LLM-produced tool invocations (billable output)
	ToolResults   int // handler.toolResultRunes — tool output fed back to the model (billable input)
}

// buildACPResult constructs a SessionResult from the handler's accumulated state.
// Usage is populated with a character-count-derived estimate (see
// estimateACPUsage) since the ACP protocol does not surface real token counts.
func buildACPResult(handler *acpClientHandler, resp acp.PromptResponse, cfg pipeline.AgentRunConfig) agent.SessionResult {
	handler.mu.Lock()
	// Snapshot everything we need from handler state under the lock. Rune
	// counting is O(totalOutputSize) over handler.textParts, but iteration
	// over a string with utf8.RuneCountInString doesn't allocate — unlike
	// strings.Join, which would copy the whole output. Model-catalog
	// lookups (llm.EstimateCost) happen below, after the lock is released.
	turns := handler.turnCount
	toolCount := handler.toolCount
	textPartsLen := len(handler.textParts)
	counts := acpRuneCounts{
		Reasoning:   handler.reasoningRunes,
		ToolArgs:    handler.toolArgRunes,
		ToolResults: handler.toolResultRunes,
	}
	for _, part := range handler.textParts {
		counts.MessageOutput += utf8.RuneCountInString(part)
	}
	toolNames := make(map[string]int, len(handler.toolNames))
	for _, name := range handler.toolNames {
		toolNames[name]++
	}
	handler.mu.Unlock()

	result := agent.SessionResult{
		Turns:     turns,
		Provider:  "acp",
		ToolCalls: toolNames,
	}
	// Estimate turns: at minimum 1 if we got any response.
	if result.Turns == 0 && (textPartsLen > 0 || toolCount > 0) {
		result.Turns = 1
	}
	result.Usage = estimateACPUsage(cfg, counts)

	return result
}

// estimateACPUsage synthesizes an approximate llm.Usage from rune counts of
// the input prompt (system + user + tool-result payloads fed back to the
// model) and the collected output channels (message text + reasoning +
// tool-call arguments). The ACP spec exposes no native usage surface, so a
// heuristic is the only option. Reasoning gets its own ReasoningTokens
// field AND counts in OutputTokens, matching how providers price extended
// thinking today (output rate). Returns a zero-valued Usage when every
// channel is empty. Token counts use ceiling division so 1–3-rune inputs
// round to 1 token rather than 0, preventing TokenTracker's
// zero-early-return from dropping the session.
func estimateACPUsage(cfg pipeline.AgentRunConfig, counts acpRuneCounts) llm.Usage {
	promptRunes := utf8.RuneCountInString(cfg.Prompt) + utf8.RuneCountInString(cfg.SystemPrompt)
	inputRunes := promptRunes + counts.ToolResults
	outputRunes := counts.MessageOutput + counts.Reasoning + counts.ToolArgs
	if inputRunes == 0 && outputRunes == 0 {
		return llm.Usage{}
	}

	inTokens := ceilDiv(inputRunes, acpTokenEstimateRatio)
	outTokens := ceilDiv(outputRunes, acpTokenEstimateRatio)

	// Optional operator tuning: TRACKER_ACP_CACHE_READ_RATIO splits the
	// estimated input tokens between fresh InputTokens and CacheReadTokens
	// so llm.EstimateCost can price cache reads at 10% of the input rate.
	// Defaults to 0 (no split, all input priced as fresh). Typical values
	// for stable-context Claude workloads (CLAUDE.md + tool defs on every
	// turn with good caching): 0.5–0.8. See docs/architecture/backends.md.
	cacheReadRatio := parseACPCacheReadRatio()
	cacheReadTokens := 0
	if cacheReadRatio > 0 && inTokens > 0 {
		cacheReadTokens = int(float64(inTokens) * cacheReadRatio)
		if cacheReadTokens > inTokens {
			cacheReadTokens = inTokens
		}
		inTokens -= cacheReadTokens
	}

	usage := llm.Usage{
		InputTokens:  inTokens,
		OutputTokens: outTokens,
		TotalTokens:  inTokens + outTokens + cacheReadTokens,
		Raw: ACPUsageMarker{
			Estimated: true,
			Source:    "acp-chars-heuristic",
			Ratio:     acpTokenEstimateRatio,
		},
	}
	if cacheReadTokens > 0 {
		usage.CacheReadTokens = &cacheReadTokens
	}
	if reasoningTokens := ceilDiv(counts.Reasoning, acpTokenEstimateRatio); reasoningTokens > 0 {
		usage.ReasoningTokens = &reasoningTokens
	}
	usage.EstimatedCost = llm.EstimateCost(cfg.Model, usage)
	return usage
}

// ceilDiv returns 0 when runes is 0, otherwise ceil(runes/ratio). The ceiling
// matters for short inputs: chars÷4 of a 1–3-rune prompt rounds to 0 under
// floor division, and a zero-token Usage is treated as "nothing to track" by
// trackExternalBackendUsage. Callers must pass ratio ≥ 1; with runes ≥ 1 the
// result is always ≥ 1, so no additional clamp is needed.
func ceilDiv(runes, ratio int) int {
	if runes <= 0 {
		return 0
	}
	return (runes + ratio - 1) / ratio
}

// buildACPMcpServers converts the AgentRunConfig's MCP server list (if any)
// into ACP McpServer objects. Returns an empty (non-nil) slice when none are
// configured — the ACP SDK requires McpServers to be non-nil.
func buildACPMcpServers(cfg pipeline.AgentRunConfig) []acp.McpServer {
	// Check if the Extra config carries MCP server definitions.
	// For now, return an empty slice. MCP server passthrough from node attrs
	// is tracked as issue C8 in the review doc.
	return []acp.McpServer{}
}

// acpStrippedPrefixes are env var prefixes stripped from ACP agent subprocesses.
// ACP agents (Claude Code, Codex, Gemini CLI) handle their own auth natively
// via subscription/OAuth — injecting tracker's API keys and base URLs overrides
// the agent's own auth and can redirect it to the wrong endpoint (e.g. a
// Cloudflare AI Gateway that doesn't support the agent's protocol).
var acpStrippedPrefixes = []string{
	"ANTHROPIC_API_KEY=",
	"OPENAI_API_KEY=",
	"OPENAI_COMPAT_API_KEY=",
	"GEMINI_API_KEY=",
	"GOOGLE_API_KEY=",
	"ANTHROPIC_BASE_URL=",
	"OPENAI_BASE_URL=",
	"OPENAI_COMPAT_BASE_URL=",
	"GEMINI_BASE_URL=",
	"GOOGLE_BASE_URL=",
	"OPENROUTER_API_KEY=",
}

// buildEnvForACP returns the environment for ACP agent subprocesses.
// ACP bridges handle their own credential routing internally, so the full
// environment (including API keys) is passed through by default.
// Set TRACKER_STRIP_ACP_KEYS=1 to strip provider keys (e.g., when bridges
// should use subscription auth instead of API key auth).
func buildEnvForACP() []string {
	if os.Getenv("TRACKER_STRIP_ACP_KEYS") == "1" {
		return filterEnvForACP(os.Environ())
	}
	return os.Environ()
}

// filterEnvForACP strips API key and base URL env vars from the given environment.
func filterEnvForACP(env []string) []string {
	clean := make([]string, 0, len(env))
	for _, e := range env {
		if !hasACPStrippedPrefix(e) {
			clean = append(clean, e)
		}
	}
	return clean
}

// hasACPStrippedPrefix returns true if the env var should be stripped for ACP agents.
func hasACPStrippedPrefix(envVar string) bool {
	for _, prefix := range acpStrippedPrefixes {
		if strings.HasPrefix(envVar, prefix) {
			return true
		}
	}
	return false
}

// killProcess sends SIGKILL to the process if it's running.
func killProcess(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
