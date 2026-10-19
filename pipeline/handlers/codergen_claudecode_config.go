// ABOUTME: Node-attribute parsing for the claude-code backend (tools, MCP servers,
// ABOUTME: tool_access deny list, budget, permission mode), split out of codergen.go.
package handlers

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/2389-research/tracker/pipeline"
)

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
	raw := node.AgentConfig(nil).McpServers
	if raw == "" {
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
	if raw := node.AgentConfig(nil).AllowedTools; raw != "" {
		ccCfg.AllowedTools = pipeline.ParseToolList(raw)
	}
	if raw := node.AgentConfig(nil).DisallowedTools; raw != "" {
		ccCfg.DisallowedTools = pipeline.ParseToolList(raw)
	}
}

// isNodeToolAccessRestricted reports whether the node's `tool_access` attr
// is set to any non-empty (canonical) value. Mirrors
// agent.SessionConfig.IsToolAccessRestricted; defined here to avoid an
// import cycle. Issue: github.com/2389-research/tracker#258.
func isNodeToolAccessRestricted(node *pipeline.Node) bool {
	return strings.TrimSpace(node.AgentConfig(nil).ToolAccess) != ""
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
	raw := node.AgentConfig(nil).PermissionMode
	if raw == "" {
		return nil
	}
	mode := pipeline.PermissionMode(raw)
	if !mode.Valid() {
		return fmt.Errorf("node %q: invalid permission_mode %q (valid: plan, acceptEdits, bypassPermissions, default, dontAsk, auto)", node.ID, raw)
	}
	ccCfg.PermissionMode = mode
	return nil
}
