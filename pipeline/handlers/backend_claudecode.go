// ABOUTME: ClaudeCodeBackend spawns the claude CLI as a subprocess and parses NDJSON output.
// ABOUTME: Implements pipeline.AgentBackend with zero external dependencies beyond the claude binary.
package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/2389-research/tracker/agent"
	"github.com/2389-research/tracker/llm"
	"github.com/2389-research/tracker/pipeline"
)

// ClaudeCodeBackend implements pipeline.AgentBackend by spawning the claude CLI
// as a subprocess and parsing its NDJSON stream output.
type ClaudeCodeBackend struct {
	claudePath string // resolved path to claude binary
}

// runState holds per-invocation mutable state so that ClaudeCodeBackend itself
// is safe for concurrent use.
type runState struct {
	toolUseIDs map[string]string
	lastResult *agent.SessionResult
}

// NewClaudeCodeBackend creates a ClaudeCodeBackend, resolving the claude binary path.
func NewClaudeCodeBackend() (*ClaudeCodeBackend, error) {
	path, err := resolveClaudePath()
	if err != nil {
		return nil, err
	}
	return &ClaudeCodeBackend{claudePath: path}, nil
}

// resolveClaudePath finds the claude binary and verifies it responds to --version.
func resolveClaudePath() (string, error) {
	path, err := exec.LookPath("claude")
	if err != nil {
		return "", fmt.Errorf("claude CLI not found in PATH — install with: npm install -g @anthropic-ai/claude-code")
	}

	cmd := exec.Command(path, "--version")
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("claude CLI version check failed: %w", err)
	}

	return path, nil
}

// Run spawns the claude CLI, parses NDJSON from stdout, and emits agent.Event
// objects via the emit callback. It returns a SessionResult built from the
// NDJSON "result" message and any error from the subprocess.
func (b *ClaudeCodeBackend) Run(ctx context.Context, cfg pipeline.AgentRunConfig, emit func(agent.Event)) (agent.SessionResult, error) {
	// Per-run state: local to this invocation, safe for concurrent use.
	state := &runState{
		toolUseIDs: make(map[string]string),
	}

	args, err := buildArgs(cfg)
	if err != nil {
		return agent.SessionResult{}, fmt.Errorf("invalid claude-code config: %w", err)
	}

	// Apply timeout from config if set.
	if cfg.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, cfg.Timeout)
		defer cancel()
	}

	cmd := exec.CommandContext(ctx, b.claudePath, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	}
	cmd.WaitDelay = 5 * time.Second
	cmd.Env = buildEnv()

	if cfg.WorkingDir != "" {
		cmd.Dir = cfg.WorkingDir
	}

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return agent.SessionResult{}, fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return agent.SessionResult{}, fmt.Errorf("failed to start claude CLI: %w", err)
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		decoder := json.NewDecoder(stdout)
		for decoder.More() {
			var raw json.RawMessage
			if err := decoder.Decode(&raw); err != nil {
				log.Printf("[claude-code] warning: failed to decode NDJSON line: %v", err)
				continue
			}
			events := parseMessage(raw, state)
			for _, evt := range events {
				func() {
					defer func() {
						if r := recover(); r != nil {
							log.Printf("[claude-code] panic in event handler: %v", r)
						}
					}()
					emit(evt)
				}()
			}
		}
	}()

	wg.Wait()
	waitErr := cmd.Wait()

	exitCode := 0
	if waitErr != nil {
		if exitErr, ok := waitErr.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return buildResult(state), fmt.Errorf("claude CLI wait error: %w", waitErr)
		}
	}

	outcome := classifyError(stderr.String(), exitCode)
	if outcome != pipeline.OutcomeSuccess {
		return buildResult(state), fmt.Errorf("claude CLI failed (exit %d, outcome=%s): %s",
			exitCode, outcome, strings.TrimSpace(stderr.String()))
	}

	return buildResult(state), nil
}

// buildResult returns the SessionResult accumulated from NDJSON parsing, or a
// zero-value result if no "result" message was received.
func buildResult(state *runState) agent.SessionResult {
	if state.lastResult != nil {
		return *state.lastResult
	}
	return agent.SessionResult{}
}

// buildArgs constructs the CLI arguments for the claude command from the run config.
// Returns an error if ClaudeCodeConfig contains invalid values.
func buildArgs(cfg pipeline.AgentRunConfig) ([]string, error) {
	args := []string{
		"-p", cfg.Prompt,
		"--output-format", "stream-json",
	}

	if cfg.Model != "" {
		args = append(args, "--model", cfg.Model)
	}

	if cfg.MaxTurns > 0 {
		args = append(args, "--max-turns", fmt.Sprintf("%d", cfg.MaxTurns))
	}

	// System prompt is independent of ClaudeCodeConfig.
	if cfg.SystemPrompt != "" {
		args = append(args, "--system-prompt", cfg.SystemPrompt)
	}

	ccCfg, _ := cfg.Extra.(*pipeline.ClaudeCodeConfig)
	if ccCfg == nil {
		return args, nil
	}

	if ccCfg.PermissionMode != "" {
		if !ccCfg.PermissionMode.Valid() {
			return nil, fmt.Errorf("invalid permission mode: %q", ccCfg.PermissionMode)
		}
		args = append(args, "--permission-mode", string(ccCfg.PermissionMode))
	}

	if len(ccCfg.AllowedTools) > 0 {
		args = append(args, "--allowedTools", strings.Join(ccCfg.AllowedTools, ","))
	}

	if len(ccCfg.DisallowedTools) > 0 {
		args = append(args, "--disallowedTools", strings.Join(ccCfg.DisallowedTools, ","))
	}

	if ccCfg.MaxBudgetUSD > 0 {
		args = append(args, "--budget", fmt.Sprintf("%.2f", ccCfg.MaxBudgetUSD))
	}

	if len(ccCfg.MCPServers) > 0 {
		args = append(args, "--mcpServers", buildMCPServersJSON(ccCfg.MCPServers))
	}

	return args, nil
}

// buildEnv constructs a minimal environment for the claude subprocess.
func buildEnv() []string {
	env := []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + os.Getenv("HOME"),
		"TERM=" + os.Getenv("TERM"),
	}
	if key := os.Getenv("CLAUDE_API_KEY"); key != "" {
		env = append(env, "CLAUDE_API_KEY="+key)
	}
	if token := os.Getenv("CLAUDE_CODE_OAUTH_TOKEN"); token != "" {
		env = append(env, "CLAUDE_CODE_OAUTH_TOKEN="+token)
	}

	// Pass through commonly needed env vars if set.
	for _, name := range []string{
		"USER", "TMPDIR", "LANG", "SSH_AUTH_SOCK",
		"HTTPS_PROXY", "HTTP_PROXY", "NO_PROXY",
	} {
		if val := os.Getenv(name); val != "" {
			env = append(env, name+"="+val)
		}
	}
	return env
}

// ndjsonMessage represents a single NDJSON line from claude CLI output.
type ndjsonMessage struct {
	Type    string          `json:"type"`
	Content []ndjsonContent `json:"content,omitempty"`
	Turns   int             `json:"turns,omitempty"`
	Usage   *ndjsonUsage    `json:"usage,omitempty"`
}

// ndjsonContent represents a content block within an NDJSON message.
type ndjsonContent struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   string          `json:"content,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
}

// ndjsonUsage represents token usage from a result message.
type ndjsonUsage struct {
	InputTokens  int     `json:"input_tokens,omitempty"`
	OutputTokens int     `json:"output_tokens,omitempty"`
	CostUSD      float64 `json:"cost_usd,omitempty"`
}

// parseMessage converts a raw NDJSON message into zero or more agent.Event objects.
func parseMessage(raw json.RawMessage, state *runState) []agent.Event {
	var msg ndjsonMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		log.Printf("[claude-code] warning: failed to unmarshal NDJSON message: %v", err)
		return nil
	}

	now := time.Now()

	switch msg.Type {
	case "system":
		// Emit EventLLMRequestPreparing so the TUI shows the model name
		// and a thinking indicator when a claude-code session starts.
		return []agent.Event{{
			Type:      agent.EventLLMRequestPreparing,
			Timestamp: now,
			Provider:  "claude-code",
		}}

	case "assistant":
		return parseAssistantContent(msg.Content, now, state)

	case "user":
		return parseUserContent(msg.Content, now, state)

	case "result":
		storeResult(msg, state)
		return nil

	default:
		log.Printf("[claude-code] warning: unknown NDJSON message type: %q", msg.Type)
		return nil
	}
}

// parseAssistantContent processes content blocks from an assistant message.
func parseAssistantContent(content []ndjsonContent, now time.Time, state *runState) []agent.Event {
	var events []agent.Event
	for _, c := range content {
		switch c.Type {
		case "text":
			events = append(events, agent.Event{
				Type:      agent.EventTextDelta,
				Timestamp: now,
				Text:      c.Text,
			})

		case "thinking":
			events = append(events, agent.Event{
				Type:      agent.EventLLMReasoning,
				Timestamp: now,
				Text:      c.Text,
			})

		case "tool_use":
			state.toolUseIDs[c.ToolUseID] = c.Name
			events = append(events, agent.Event{
				Type:      agent.EventToolCallStart,
				Timestamp: now,
				ToolName:  c.Name,
				ToolInput: string(c.Input),
			})

		default:
			log.Printf("[claude-code] warning: unknown assistant content type: %q", c.Type)
		}
	}
	return events
}

// parseUserContent processes content blocks from a user message (tool results).
func parseUserContent(content []ndjsonContent, now time.Time, state *runState) []agent.Event {
	var events []agent.Event
	for _, c := range content {
		if c.Type != "tool_result" {
			continue
		}

		toolName := state.toolUseIDs[c.ToolUseID]

		evt := agent.Event{
			Type:      agent.EventToolCallEnd,
			Timestamp: now,
			ToolName:  toolName,
		}
		if c.IsError {
			evt.ToolError = c.Content
		} else {
			evt.ToolOutput = c.Content
		}
		events = append(events, evt)
	}
	return events
}

// storeResult saves the result message data into lastResult for later retrieval.
func storeResult(msg ndjsonMessage, state *runState) {
	result := &agent.SessionResult{
		Turns:     msg.Turns,
		ToolCalls: make(map[string]int),
	}

	if msg.Usage != nil {
		result.Usage = llm.Usage{
			InputTokens:   msg.Usage.InputTokens,
			OutputTokens:  msg.Usage.OutputTokens,
			TotalTokens:   msg.Usage.InputTokens + msg.Usage.OutputTokens,
			EstimatedCost: msg.Usage.CostUSD,
		}
	}

	state.lastResult = result
}

// classifyError maps stderr content and exit codes to pipeline outcome strings.
// Returns the outcome status that should be used for retry/fail decisions.
func classifyError(stderr string, exitCode int) string {
	if exitCode == 0 {
		return pipeline.OutcomeSuccess
	}

	lower := strings.ToLower(stderr)

	switch {
	case strings.Contains(lower, "authentication") ||
		strings.Contains(lower, "unauthorized") ||
		strings.Contains(lower, "invalid api key"):
		return pipeline.OutcomeFail

	case strings.Contains(lower, "rate limit") ||
		strings.Contains(lower, "429") ||
		strings.Contains(lower, "throttl"):
		return pipeline.OutcomeRetry

	case strings.Contains(lower, "budget") ||
		strings.Contains(lower, "spending limit"):
		return pipeline.OutcomeFail

	case strings.Contains(lower, "econnrefused") ||
		strings.Contains(lower, "network") ||
		strings.Contains(lower, "connection"):
		return pipeline.OutcomeRetry

	case exitCode == 137:
		return pipeline.OutcomeFail
	}

	return pipeline.OutcomeFail
}

// buildMCPServersJSON converts MCPServerConfig slice to the JSON format expected
// by claude CLI's --mcpServers flag.
func buildMCPServersJSON(servers []pipeline.MCPServerConfig) string {
	type mcpEntry struct {
		Command string   `json:"command"`
		Args    []string `json:"args"`
	}

	m := make(map[string]mcpEntry, len(servers))
	for _, s := range servers {
		m[s.Name] = mcpEntry{
			Command: s.Command,
			Args:    s.Args,
		}
	}

	data, err := json.Marshal(m)
	if err != nil {
		log.Printf("[claude-code] warning: failed to marshal MCP servers: %v", err)
		return "{}"
	}
	return string(data)
}
