// ABOUTME: ClaudeCodeBackend spawns the claude CLI as a subprocess and parses NDJSON output.
// ABOUTME: Implements pipeline.AgentBackend with zero external dependencies beyond the claude binary.
package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/2389-research/tracker/agent"
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
	toolUseIDs   map[string]string
	lastResult   *agent.SessionResult
	decodeErrors int // NDJSON lines that failed json.Decode or json.Unmarshal
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

	cmd := exec.Command(b.claudePath, args...)
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
		decodeNDJSON(stdout, state, emit)
	}()

	wg.Wait()
	return collectResult(cmd, state, &stderr)
}

// decodeNDJSON reads NDJSON from stdout, parses messages, and emits events.
func decodeNDJSON(stdout io.Reader, state *runState, emit func(agent.Event)) {
	decoder := json.NewDecoder(stdout)
	for decoder.More() {
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			state.decodeErrors++
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
}

// collectResult waits for the subprocess to exit and returns the accumulated result.
func collectResult(cmd *exec.Cmd, state *runState, stderr *bytes.Buffer) (agent.SessionResult, error) {
	waitErr := cmd.Wait()

	exitCode := 0
	if waitErr != nil {
		if exitErr, ok := waitErr.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
			log.Printf("[claude-code] exit error: code=%d, state=%v, err=%v", exitCode, exitErr.ProcessState, exitErr)
		} else {
			r, _ := buildResult(state)
			return r, fmt.Errorf("claude CLI wait error: %w", waitErr)
		}
	}

	outcome := classifyError(stderr.String(), exitCode)
	if outcome != pipeline.OutcomeSuccess {
		r, _ := buildResult(state)
		return r, fmt.Errorf("claude CLI failed (exit %d, outcome=%s): %s",
			exitCode, outcome, strings.TrimSpace(stderr.String()))
	}

	result, err := buildResult(state)
	if err != nil {
		return result, err
	}

	if state.decodeErrors > 0 && state.lastResult == nil {
		return result, fmt.Errorf("claude CLI produced %d NDJSON decode errors and no result message", state.decodeErrors)
	}

	return result, nil
}

// buildResult returns the SessionResult accumulated from NDJSON parsing.
// Returns an error if the subprocess produced no "result" message.
func buildResult(state *runState) (agent.SessionResult, error) {
	if state.lastResult != nil {
		return *state.lastResult, nil
	}
	return agent.SessionResult{}, fmt.Errorf("claude CLI exited successfully but produced no result message")
}

// buildArgs constructs the CLI arguments for the claude command from the run config.
// Returns an error if ClaudeCodeConfig contains invalid values.
func buildArgs(cfg pipeline.AgentRunConfig) ([]string, error) {
	args := []string{
		"--print",
		"--verbose",
		"-p", cfg.Prompt,
		"--output-format", "stream-json",
	}

	if cfg.Model != "" && isClaudeModel(cfg.Model) {
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
		mcpJSON, err := buildMCPServersJSON(ccCfg.MCPServers)
		if err != nil {
			return nil, fmt.Errorf("mcp_servers: %w", err)
		}
		args = append(args, "--mcpServers", mcpJSON)
	}

	return args, nil
}

// providerKeyPrefixes are environment variable prefixes that should be stripped
// from the claude subprocess environment. When these keys are present, the
// claude CLI uses them for API auth instead of the user's Max/Pro subscription
// OAuth token. Stripping them ensures the subprocess uses subscription auth.
// Users who need API key auth can set TRACKER_PASS_API_KEYS=1 to override.
var providerKeyPrefixes = []string{
	"ANTHROPIC_API_KEY=",
	"OPENAI_API_KEY=",
	"OPENAI_COMPAT_API_KEY=",
	"GEMINI_API_KEY=",
	"GOOGLE_API_KEY=",
}

// buildEnv constructs the environment for the claude subprocess.
// Strips LLM provider API keys so the claude CLI uses subscription auth
// (Max/Pro OAuth) instead of consuming API credits. The full parent
// environment is passed through otherwise — Claude Code needs access to
// its config directory, SSH agent, and other system state.
func buildEnv() []string {
	if os.Getenv("TRACKER_PASS_API_KEYS") != "" {
		return os.Environ()
	}

	env := os.Environ()
	clean := make([]string, 0, len(env))
	for _, e := range env {
		stripped := false
		for _, prefix := range providerKeyPrefixes {
			if strings.HasPrefix(e, prefix) {
				stripped = true
				break
			}
		}
		if !stripped {
			clean = append(clean, e)
		}
	}
	return clean
}

// isClaudeModel returns true if the model name is an Anthropic model that the
// claude CLI understands. Non-Anthropic models (gpt-*, gemini-*) are stripped
// so the CLI uses its default model under the user's subscription.
func isClaudeModel(model string) bool {
	lower := strings.ToLower(model)
	return strings.HasPrefix(lower, "claude") ||
		strings.HasPrefix(lower, "anthropic")
}

// NDJSON types, parseMessage, and storeResult are in backend_claudecode_ndjson.go

// classifyError maps stderr content and exit codes to pipeline outcome strings.
// Returns the outcome status that should be used for retry/fail decisions.
func classifyError(stderr string, exitCode int) string {
	if exitCode == 0 {
		return pipeline.OutcomeSuccess
	}
	lower := strings.ToLower(stderr)
	trimmed := strings.TrimSpace(stderr)

	if isAuthError(lower) {
		log.Printf("[claude-code] auth error (exit %d): %s", exitCode, trimmed)
		return pipeline.OutcomeFail
	}
	if isCreditError(lower) {
		log.Printf("[claude-code] API credit balance exhausted — claude CLI may be using ANTHROPIC_API_KEY instead of Max subscription. Unset ANTHROPIC_API_KEY to use subscription auth. stderr: %s", trimmed)
		return pipeline.OutcomeFail
	}
	if isRateLimitError(lower) {
		log.Printf("[claude-code] rate limited (exit %d), will retry", exitCode)
		return pipeline.OutcomeRetry
	}
	if isBudgetError(lower) {
		log.Printf("[claude-code] budget/spending limit hit (exit %d): %s", exitCode, trimmed)
		return pipeline.OutcomeFail
	}
	if isNetworkError(lower) {
		log.Printf("[claude-code] network error (exit %d), will retry", exitCode)
		return pipeline.OutcomeRetry
	}
	if exitCode == 137 {
		log.Printf("[claude-code] process killed (exit 137)")
		return pipeline.OutcomeFail
	}
	log.Printf("[claude-code] unclassified error (exit %d): %s", exitCode, trimmed)
	return pipeline.OutcomeFail
}

func isAuthError(lower string) bool {
	return strings.Contains(lower, "authentication") ||
		strings.Contains(lower, "unauthorized") ||
		strings.Contains(lower, "invalid api key")
}

func isCreditError(lower string) bool {
	return strings.Contains(lower, "credit balance") ||
		strings.Contains(lower, "too low to access")
}

func isRateLimitError(lower string) bool {
	return strings.Contains(lower, "rate limit") ||
		strings.Contains(lower, "429") ||
		containsThrottle(lower)
}

func isBudgetError(lower string) bool {
	return strings.Contains(lower, "budget") ||
		strings.Contains(lower, "spending limit")
}

func isNetworkError(lower string) bool {
	return strings.Contains(lower, "econnrefused") ||
		strings.Contains(lower, "network") ||
		strings.Contains(lower, "connection")
}

// containsThrottle returns true if lower contains "throttled" or "throttling"
// but not when preceded by "un" (e.g. "unthrottled" is not a throttle error).
func containsThrottle(lower string) bool {
	for _, word := range []string{"throttled", "throttling"} {
		idx := strings.Index(lower, word)
		if idx < 0 {
			continue
		}
		// Reject if preceded by "un" (e.g. "unthrottled").
		if idx >= 2 && lower[idx-2:idx] == "un" {
			continue
		}
		return true
	}
	return false
}

// buildMCPServersJSON converts MCPServerConfig slice to the JSON format expected
// by claude CLI's --mcpServers flag. Returns an error if serialization fails
// rather than falling back to empty JSON, so callers can surface the problem.
func buildMCPServersJSON(servers []pipeline.MCPServerConfig) (string, error) {
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
		return "", fmt.Errorf("failed to marshal MCP servers to JSON: %w", err)
	}
	return string(data), nil
}
