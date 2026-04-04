// ABOUTME: ACPBackend spawns ACP-compatible coding agents (claude, codex, gemini) as subprocesses.
// ABOUTME: Implements pipeline.AgentBackend using the Agent Client Protocol over stdio.
package handlers

import (
	"bytes"
	"context"
	"fmt"
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
//   - anthropic → claude-code-acp (bridge: npm i -g claude-code-acp)
//   - openai   → codex-acp       (bridge: cargo install codex-acp)
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

	// Apply timeout from config.
	if cfg.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, cfg.Timeout)
		defer cancel()
	}

	emit(agent.Event{
		Type:      agent.EventLLMRequestPreparing,
		Timestamp: time.Now(),
		Provider:  "acp:" + agentName,
	})

	args := acpAgentArgs[agentName]
	cmd := exec.CommandContext(ctx, agentPath, args...)
	// ACP bridges need the full environment (including API keys) because the
	// wrapped agents (e.g. Claude Code via claude-agent-acp) handle their own
	// auth. Unlike the direct claude-code backend which strips keys to force
	// subscription auth, ACP bridges manage credential routing internally.
	cmd.Env = buildEnvForACP()

	if cfg.WorkingDir != "" {
		cmd.Dir = cfg.WorkingDir
	}

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return agent.SessionResult{}, fmt.Errorf("acp: failed to create stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return agent.SessionResult{}, fmt.Errorf("acp: failed to create stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return agent.SessionResult{}, fmt.Errorf("acp: failed to start %s: %w", agentName, err)
	}

	handler := &acpClientHandler{
		emit:       emit,
		workingDir: cfg.WorkingDir,
		toolNames:  make(map[string]string),
	}
	defer handler.cleanup()

	conn := acp.NewClientSideConnection(handler, stdin, stdout)

	// Step 1: Initialize the ACP connection.
	log.Printf("[acp] initializing %s (pid %d)", agentName, cmd.Process.Pid)
	_, initErr := conn.Initialize(ctx, acp.InitializeRequest{
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
		killProcess(cmd)
		if stderrStr := strings.TrimSpace(stderr.String()); stderrStr != "" {
			log.Printf("[acp] %s stderr during initialize: %s", agentName, stderrStr)
		}
		return agent.SessionResult{}, fmt.Errorf("acp: initialize failed for %s: %w", agentName, initErr)
	}

	// Step 2: Create a session. McpServers is required by the ACP SDK
	// (nil fails validation), so pass an empty slice when none are configured.
	mcpServers := buildACPMcpServers(cfg)
	sessResp, sessErr := conn.NewSession(ctx, acp.NewSessionRequest{
		Cwd:        cfg.WorkingDir,
		McpServers: mcpServers,
	})
	if sessErr != nil {
		killProcess(cmd)
		if stderrStr := strings.TrimSpace(stderr.String()); stderrStr != "" {
			log.Printf("[acp] %s stderr during new session: %s", agentName, stderrStr)
		}
		return agent.SessionResult{}, fmt.Errorf("acp: new session failed for %s: %w", agentName, sessErr)
	}

	// Step 3: Optionally set the model if the bridge advertises it.
	// ACP bridges expose their own model IDs (e.g. "sonnet", "default") which
	// differ from native tracker model names (e.g. "claude-sonnet-4-5").
	// Only send SetSessionModel if the requested model matches one the bridge
	// listed in its NewSession response — otherwise the bridge accepts the
	// unknown model silently but may fail internally on prompt.
	if cfg.Model != "" {
		bridgeModel := mapModelToBridge(cfg.Model, sessResp.Models)
		if bridgeModel != "" {
			_, modelErr := conn.SetSessionModel(ctx, acp.SetSessionModelRequest{
				SessionId: sessResp.SessionId,
				ModelId:   acp.ModelId(bridgeModel),
			})
			if modelErr != nil {
				// Non-fatal: agent may not support SetSessionModel. Log and continue.
				log.Printf("[acp] warning: SetSessionModel failed for %s (model=%s→%s): %v", agentName, cfg.Model, bridgeModel, modelErr)
			}
		} else {
			log.Printf("[acp] skipping SetSessionModel — no bridge match for %q", cfg.Model)
		}
	}

	emit(agent.Event{
		Type:      agent.EventTurnStart,
		Timestamp: time.Now(),
	})

	// Step 4: Send the prompt. This blocks until the agent completes.
	prompt := buildACPPromptBlocks(cfg)
	promptResp, promptErr := conn.Prompt(ctx, acp.PromptRequest{
		SessionId: sessResp.SessionId,
		Prompt:    prompt,
	})

	// Close stdin to signal the agent to exit, then wait with a timeout.
	// Some ACP bridges (e.g. claude-agent-acp) don't exit immediately after
	// stdin closes — they may have cleanup or background work. We give a
	// grace period then force-kill.
	_ = stdin.Close()

	waitCh := make(chan error, 1)
	go func() { waitCh <- cmd.Wait() }()

	forceKilled := false
	select {
	case <-waitCh:
	case <-time.After(5 * time.Second):
		log.Printf("[acp] process did not exit after 5s, killing pid %d", cmd.Process.Pid)
		killProcess(cmd)
		<-waitCh
		forceKilled = true
	}

	// Build result from collected handler state.
	result := buildACPResult(handler, promptResp)

	if promptErr != nil {
		if stderrStr := strings.TrimSpace(stderr.String()); stderrStr != "" {
			log.Printf("[acp] %s stderr during prompt: %s", agentName, stderrStr)
		}
		if ctx.Err() != nil {
			return result, fmt.Errorf("acp: prompt cancelled for %s: %w", agentName, ctx.Err())
		}
		return result, fmt.Errorf("acp: prompt failed for %s: %w", agentName, promptErr)
	}

	// If the prompt succeeded but we had to force-kill the bridge, that's OK.
	// The bridge simply didn't exit cleanly after completing its work.
	if forceKilled {
		log.Printf("[acp] %s force-killed after successful prompt (bridge did not exit on stdin close)", agentName)
	}

	// Empty agent responses (0 text, 0 tool calls) are failures per project
	// rules — the agent ran but produced nothing useful.
	if len(handler.textParts) == 0 && handler.toolCount == 0 {
		return result, fmt.Errorf("acp: %s returned empty response (0 text, 0 tool calls)", agentName)
	}

	return result, nil
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
	verifyCmd := exec.Command(path, "--version")
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
	lower := strings.ToLower(trackerModel)

	// Direct match first.
	for _, m := range models.AvailableModels {
		if strings.EqualFold(string(m.ModelId), trackerModel) {
			return string(m.ModelId)
		}
	}

	// Substring match: "claude-sonnet-4-5" contains "sonnet".
	for _, m := range models.AvailableModels {
		id := strings.ToLower(string(m.ModelId))
		if id == "default" {
			continue // skip default for substring matching
		}
		if strings.Contains(lower, id) {
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

// buildEnvForACP returns the environment for ACP agent subprocesses.
// Unlike the claude-code backend which strips API keys to force subscription
// auth, ACP bridges handle credential routing internally — the wrapped agent
// (Claude Code, Codex, Gemini) manages its own auth. We pass the full
// environment so the bridge can authenticate normally.
func buildEnvForACP() []string {
	return os.Environ()
}

// killProcess sends SIGKILL to the process if it's running.
func killProcess(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
