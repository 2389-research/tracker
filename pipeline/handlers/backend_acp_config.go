// ABOUTME: Validates Tracker directives before starting an ACP agent process.
// ABOUTME: Refuses native-only turn policy and unsupported tool-access restrictions.
package handlers

import (
	"fmt"
	"strings"

	"github.com/2389-research/tracker/pipeline"
)

func validateACPConfig(cfg pipeline.AgentRunConfig) error {
	if cfg.MaxTurns < 0 {
		return fmt.Errorf("negative max_turns requires the native backend (-1 means unlimited)")
	}
	// tool_access enforcement (issue #258): the ACP backend has no verified
	// deny-equivalent for the directive — the protocol exposes session
	// permissions but tracker has not yet audited the spelling against the
	// upstream ACP agents (claude-code-acp, codex-acp, gemini). Per the
	// spec's "fallback unsupported → refuse" rule, refuse session creation
	// with a clear error rather than ship a soft-no that silently allows
	// tool execution. Lifting this requires a follow-up that pins ACP's
	// session.permission_modes (or equivalent) against each known agent.
	if strings.TrimSpace(cfg.ToolAccess) != "" {
		return fmt.Errorf("acp backend cannot enforce tool_access=%q yet — see github.com/2389-research/tracker#258 (use the native backend, or open a follow-up issue once the ACP deny-equivalent is verified for your agent)", cfg.ToolAccess)
	}

	return nil
}
