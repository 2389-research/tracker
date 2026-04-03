// ABOUTME: AgentBackend interface and config types for pluggable execution backends.
// ABOUTME: Supports native (agent.Session), Claude Code (CLI subprocess), and ACP (Agent Client Protocol) backends.
package pipeline

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/2389-research/tracker/agent"
)

// AgentBackend executes an agent session and streams events.
type AgentBackend interface {
	Run(ctx context.Context, cfg AgentRunConfig, emit func(agent.Event)) (agent.SessionResult, error)
}

// AgentRunConfig carries common config all backends need.
type AgentRunConfig struct {
	Prompt       string
	SystemPrompt string
	Model        string
	Provider     string
	WorkingDir   string
	MaxTurns     int
	Timeout      time.Duration
	Extra        any // backend-specific: *ClaudeCodeConfig for claude-code backend
}

// ClaudeCodeConfig holds Claude-Code-specific settings.
type ClaudeCodeConfig struct {
	MCPServers      []MCPServerConfig
	AllowedTools    []string
	DisallowedTools []string
	MaxBudgetUSD    float64
	PermissionMode  PermissionMode
}

// PermissionMode controls Claude Code's tool approval behavior.
type PermissionMode string

const (
	PermissionPlan              PermissionMode = "plan"
	PermissionAcceptEdits       PermissionMode = "acceptEdits"
	PermissionBypassPermissions PermissionMode = "bypassPermissions"
	PermissionDefault           PermissionMode = "default"
	PermissionDontAsk           PermissionMode = "dontAsk"
	PermissionAuto              PermissionMode = "auto"
)

// Valid returns true if the permission mode is a recognized Claude Code value.
// Empty string is not valid — callers should default to PermissionBypassPermissions
// before validation (see buildClaudeCodeConfig).
func (m PermissionMode) Valid() bool {
	switch m {
	case PermissionPlan, PermissionAcceptEdits, PermissionBypassPermissions,
		PermissionDefault, PermissionDontAsk, PermissionAuto:
		return true
	}
	return false
}

// ACPConfig holds ACP-backend-specific settings.
type ACPConfig struct {
	Agent string // explicit agent binary: "claude-agent-acp", "codex-agent-acp", "gemini" (overrides provider mapping)
}

// MCPServerConfig defines an MCP server to attach to a session.
type MCPServerConfig struct {
	Name    string
	Command string
	Args    []string
}

// ParseMCPServers parses the mcp_servers attr format: one server per line,
// name=command arg1 arg2. Splits on first = only.
func ParseMCPServers(raw string) ([]MCPServerConfig, error) {
	var servers []MCPServerConfig
	seen := make(map[string]bool)
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		idx := strings.Index(line, "=")
		if idx < 0 {
			return nil, fmt.Errorf("malformed mcp_servers entry: %q (missing '=')", line)
		}
		name := strings.TrimSpace(line[:idx])
		cmdStr := strings.TrimSpace(line[idx+1:])
		if name == "" {
			return nil, fmt.Errorf("malformed mcp_servers entry: %q (empty name)", line)
		}
		if cmdStr == "" {
			return nil, fmt.Errorf("malformed mcp_servers entry: %q (empty command)", line)
		}
		if seen[name] {
			return nil, fmt.Errorf("duplicate mcp_servers name: %q", name)
		}
		seen[name] = true
		parts := strings.Fields(cmdStr)
		servers = append(servers, MCPServerConfig{
			Name:    name,
			Command: parts[0],
			Args:    parts[1:],
		})
	}
	return servers, nil
}

// ParseToolList splits a comma-separated tool list, trimming whitespace.
func ParseToolList(csv string) []string {
	if strings.TrimSpace(csv) == "" {
		return nil
	}
	var tools []string
	for _, t := range strings.Split(csv, ",") {
		t = strings.TrimSpace(t)
		if t != "" {
			tools = append(tools, t)
		}
	}
	return tools
}

// ValidateToolLists returns an error if both allowed and disallowed are set.
func ValidateToolLists(allowed, disallowed []string) error {
	if len(allowed) > 0 && len(disallowed) > 0 {
		return fmt.Errorf("cannot set both allowed_tools and disallowed_tools")
	}
	return nil
}
