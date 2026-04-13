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
	"strings"
	"sync"
	"time"

	acp "github.com/coder/acp-go-sdk"

	"github.com/2389-research/tracker/agent"
	"github.com/2389-research/tracker/pipeline"
)

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
	agentPaths map[string]string // cached: agent name → resolved binary path
	mu         sync.Mutex
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
	result := buildACPResult(handler, promptResp)

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
// Returns true if the process was force-killed.
func waitForProcess(cmd *exec.Cmd, stdin io.WriteCloser) bool {
	_ = stdin.Close()

	waitCh := make(chan error, 1)
	go func() { waitCh <- cmd.Wait() }()

	select {
	case <-waitCh:
		return false
	case <-time.After(5 * time.Second):
		log.Printf("[acp] process did not exit after 5s, killing pid %d", cmd.Process.Pid)
		killProcess(cmd)
		<-waitCh
		return true
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

// buildACPResult constructs a SessionResult from the handler's accumulated state.
func buildACPResult(handler *acpClientHandler, resp acp.PromptResponse) agent.SessionResult {
	handler.mu.Lock()
	defer handler.mu.Unlock()

	result := agent.SessionResult{
		Turns:     handler.turnCount,
		ToolCalls: make(map[string]int),
	}

	// Count tool calls by name.
	for _, name := range handler.toolNames {
		result.ToolCalls[name]++
	}

	// Estimate turns: at minimum 1 if we got any response.
	if result.Turns == 0 && (len(handler.textParts) > 0 || handler.toolCount > 0) {
		result.Turns = 1
	}

	return result
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
// Strips LLM provider API keys and base URLs so the agents use their own
// native auth (subscription, OAuth, etc.) rather than tracker's credentials.
// TRACKER_PASS_API_KEYS=1 overrides this and passes everything through.
func buildEnvForACP() []string {
	if os.Getenv("TRACKER_PASS_API_KEYS") == "1" {
		return os.Environ()
	}
	return filterEnvForACP(os.Environ())
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
