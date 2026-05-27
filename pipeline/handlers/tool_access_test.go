// ABOUTME: Tests for tool_access enforcement at the pipeline/handlers layer
// ABOUTME: (issue #258). Covers the Params-bypass defense in codergen
// ABOUTME: (allowed_tools/disallowed_tools/permission_mode ignored when
// ABOUTME: tool_access is set), the claude-code best-effort DisallowedTools
// ABOUTME: enumeration, and the ACP backend refusal.
package handlers

import (
	"context"
	"strings"
	"testing"

	"github.com/2389-research/tracker/agent"
	"github.com/2389-research/tracker/pipeline"
)

// TestCodergenBuildConfig_ThreadsToolAccess confirms the directive flows from
// node.Attrs["tool_access"] through to agent.SessionConfig.ToolAccess.
func TestCodergenBuildConfig_ThreadsToolAccess(t *testing.T) {
	h := NewCodergenHandler(nil, "/tmp")
	node := &pipeline.Node{
		ID:    "test",
		Attrs: map[string]string{"tool_access": "none"},
	}
	cfg := h.buildConfig(node)
	if cfg.ToolAccess != "none" {
		t.Errorf("ToolAccess on SessionConfig = %q; expected %q", cfg.ToolAccess, "none")
	}
	if !cfg.IsToolAccessRestricted() {
		t.Error("IsToolAccessRestricted() = false; expected true under tool_access=none")
	}
}

// TestApplyToolLists_BypassDefense confirms that when tool_access is set,
// the allowed_tools and disallowed_tools Params keys are ignored. This is
// the v0.28.2 bypass defense — an LLM-authored .dip can't re-enable tools
// via the existing Params keys.
func TestApplyToolLists_BypassDefense(t *testing.T) {
	cases := []struct {
		name       string
		toolAccess string
	}{
		{"explicit none", "none"},
		{"typo noen", "noen"},
		{"uppercase NONE", "NONE"},
		{"whitespace", "  none  "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			node := &pipeline.Node{
				ID: "test",
				Attrs: map[string]string{
					"tool_access":      tc.toolAccess,
					"allowed_tools":    "Bash,Read",
					"disallowed_tools": "Write",
				},
			}
			ccCfg := &pipeline.ClaudeCodeConfig{}
			applyToolLists(node, ccCfg)
			if len(ccCfg.AllowedTools) != 0 {
				t.Errorf("AllowedTools = %v; expected nil under tool_access=%q (bypass defense)", ccCfg.AllowedTools, tc.toolAccess)
			}
			if len(ccCfg.DisallowedTools) != 0 {
				t.Errorf("DisallowedTools = %v; expected nil under tool_access=%q", ccCfg.DisallowedTools, tc.toolAccess)
			}
		})
	}
}

// TestApplyToolLists_Unrestricted confirms the bypass defense doesn't fire
// when tool_access is empty — the existing allowed_tools/disallowed_tools
// passthrough still works.
func TestApplyToolLists_Unrestricted(t *testing.T) {
	node := &pipeline.Node{
		ID: "test",
		Attrs: map[string]string{
			"allowed_tools":    "Bash,Read",
			"disallowed_tools": "Write",
		},
	}
	ccCfg := &pipeline.ClaudeCodeConfig{}
	applyToolLists(node, ccCfg)
	if len(ccCfg.AllowedTools) != 2 {
		t.Errorf("AllowedTools length = %d; expected 2 when tool_access is empty", len(ccCfg.AllowedTools))
	}
	if len(ccCfg.DisallowedTools) != 1 {
		t.Errorf("DisallowedTools length = %d; expected 1 when tool_access is empty", len(ccCfg.DisallowedTools))
	}
}

// TestApplyPermissionMode_BypassDefense confirms that when tool_access is
// set, the permission_mode Params key is ignored — a node author can't
// set permission_mode=bypassPermissions to re-enable tool execution.
func TestApplyPermissionMode_BypassDefense(t *testing.T) {
	node := &pipeline.Node{
		ID: "test",
		Attrs: map[string]string{
			"tool_access":     "none",
			"permission_mode": "bypassPermissions",
		},
	}
	ccCfg := &pipeline.ClaudeCodeConfig{}
	if err := applyPermissionMode(node, ccCfg); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ccCfg.PermissionMode != "" {
		t.Errorf("PermissionMode = %q; expected empty under tool_access=none (bypass defense)", ccCfg.PermissionMode)
	}
}

// TestApplyClaudeCodeToolAccess populates DisallowedTools with the canonical
// list when tool_access is set on a claude-code-backend node.
func TestApplyClaudeCodeToolAccess(t *testing.T) {
	node := &pipeline.Node{
		ID:    "test",
		Attrs: map[string]string{"tool_access": "none"},
	}
	ccCfg := &pipeline.ClaudeCodeConfig{}
	applyClaudeCodeToolAccess(node, ccCfg)

	if len(ccCfg.AllowedTools) != 0 {
		t.Errorf("AllowedTools should be nil after applyClaudeCodeToolAccess; got %v", ccCfg.AllowedTools)
	}
	if len(ccCfg.DisallowedTools) == 0 {
		t.Fatal("DisallowedTools is empty after applyClaudeCodeToolAccess; expected canonical deny list")
	}
	for _, expected := range []string{"Bash", "Read", "Write", "Edit", "Glob", "Grep"} {
		found := false
		for _, name := range ccCfg.DisallowedTools {
			if name == expected {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("canonical tool %q missing from DisallowedTools %v", expected, ccCfg.DisallowedTools)
		}
	}
}

// TestApplyClaudeCodeToolAccess_NoOpWhenUnrestricted confirms the deny list
// isn't populated when tool_access is empty — existing claude-code configs
// (with their own allowed_tools/disallowed_tools/permission_mode) are
// unaffected.
func TestApplyClaudeCodeToolAccess_NoOpWhenUnrestricted(t *testing.T) {
	node := &pipeline.Node{
		ID:    "test",
		Attrs: map[string]string{},
	}
	ccCfg := &pipeline.ClaudeCodeConfig{
		AllowedTools: []string{"PreviouslyAllowed"},
	}
	applyClaudeCodeToolAccess(node, ccCfg)
	if len(ccCfg.AllowedTools) != 1 || ccCfg.AllowedTools[0] != "PreviouslyAllowed" {
		t.Errorf("AllowedTools mutated under unrestricted tool_access: %v", ccCfg.AllowedTools)
	}
	if len(ccCfg.DisallowedTools) != 0 {
		t.Errorf("DisallowedTools populated under unrestricted tool_access: %v", ccCfg.DisallowedTools)
	}
}

// TestACPBackend_RefusesToolAccess confirms the ACP backend returns a clear
// error when tool_access is non-empty — no verified deny-equivalent yet, so
// per spec "fallback unsupported → refuse" the session is refused rather
// than allowed through.
func TestACPBackend_RefusesToolAccess(t *testing.T) {
	b := &ACPBackend{}
	cfg := pipeline.AgentRunConfig{
		Prompt:     "test",
		ToolAccess: "none",
	}
	_, err := b.Run(context.Background(), cfg, func(agent.Event) {})
	if err == nil {
		t.Fatal("expected error from ACP backend under tool_access=none; got nil")
	}
	if !strings.Contains(err.Error(), "tool_access") {
		t.Errorf("error did not mention tool_access: %v", err)
	}
	if !strings.Contains(err.Error(), "#258") {
		t.Errorf("error did not reference issue #258 for follow-up context: %v", err)
	}
}

// TestACPBackend_RefusesAnyNonEmptyToolAccess confirms fail-closed behavior
// — even a typo or unrecognized value triggers refusal.
func TestACPBackend_RefusesAnyNonEmptyToolAccess(t *testing.T) {
	b := &ACPBackend{}
	for _, val := range []string{"noen", "off", "X", "  none  "} {
		t.Run(val, func(t *testing.T) {
			cfg := pipeline.AgentRunConfig{
				Prompt:     "test",
				ToolAccess: val,
			}
			_, err := b.Run(context.Background(), cfg, func(agent.Event) {})
			if err == nil {
				t.Fatalf("ToolAccess=%q: expected refusal; got nil error", val)
			}
		})
	}
}

// TestNativeBackend_DirectRunBypass_AppliesToolAccess covers the bypass
// path Codex flagged on PR #259: a direct AgentRunConfig caller (not via
// CodergenHandler) sets cfg.ToolAccess but cfg.Extra is nil — the
// SessionConfig that NativeBackend ultimately constructs must still
// inherit the directive.
func TestNativeBackend_DirectRunBypass_AppliesToolAccess(t *testing.T) {
	b := &NativeBackend{}

	// No Extra → applyRunConfigOverrides path.
	cfg := pipeline.AgentRunConfig{ToolAccess: "none"}
	sc := b.buildSessionConfig(cfg)
	if !sc.IsToolAccessRestricted() {
		t.Error("buildSessionConfig with cfg.ToolAccess=none and nil Extra did not set SessionConfig.ToolAccess")
	}

	// Pre-built Extra without ToolAccess → must inherit from cfg.ToolAccess.
	cfg2 := pipeline.AgentRunConfig{
		ToolAccess: "none",
		Extra:      &agent.SessionConfig{Model: "claude-sonnet-4-6"}, // ToolAccess unset
	}
	sc2 := b.buildSessionConfig(cfg2)
	if !sc2.IsToolAccessRestricted() {
		t.Error("buildSessionConfig with cfg.ToolAccess=none and Extra missing ToolAccess did not inherit the directive")
	}
}

// TestApplyRunConfigOverrides_PropagatesToolAccess confirms the field is
// copied onto base when set. Without this, NativeBackend's direct-caller
// path (no Extra) would silently drop the directive.
func TestApplyRunConfigOverrides_PropagatesToolAccess(t *testing.T) {
	base := agent.SessionConfig{}
	cfg := pipeline.AgentRunConfig{ToolAccess: "none"}
	out := applyRunConfigOverrides(base, cfg)
	if out.ToolAccess != "none" {
		t.Errorf("applyRunConfigOverrides dropped ToolAccess; got %q expected %q", out.ToolAccess, "none")
	}
}

// TestClaudeCodeBuildArgs_DirectRunBypass_PopulatesDenyList covers the
// claude-code equivalent of the Codex-flagged bypass: a direct
// AgentRunConfig caller sets cfg.ToolAccess but cfg.Extra has no deny
// list. The CLI args must still carry --disallowedTools with the
// canonical names.
func TestClaudeCodeBuildArgs_DirectRunBypass_PopulatesDenyList(t *testing.T) {
	cases := []struct {
		name string
		cfg  pipeline.AgentRunConfig
	}{
		{
			"no Extra",
			pipeline.AgentRunConfig{ToolAccess: "none"},
		},
		{
			"Extra with empty ClaudeCodeConfig",
			pipeline.AgentRunConfig{
				ToolAccess: "none",
				Extra:      &pipeline.ClaudeCodeConfig{},
			},
		},
		{
			"Extra with caller-provided AllowedTools (must be cleared)",
			pipeline.AgentRunConfig{
				ToolAccess: "none",
				Extra: &pipeline.ClaudeCodeConfig{
					AllowedTools: []string{"Bash", "Read"}, // bypass attempt
				},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			args, err := buildArgs(tc.cfg)
			if err != nil {
				t.Fatalf("buildArgs error: %v", err)
			}
			joined := strings.Join(args, " ")
			if !strings.Contains(joined, "--disallowedTools") {
				t.Errorf("--disallowedTools missing from args under tool_access=none: %v", args)
			}
			if !strings.Contains(joined, "Bash") || !strings.Contains(joined, "Write") {
				t.Errorf("canonical tool names missing from --disallowedTools args: %v", args)
			}
			// AllowedTools must NOT be set even if a caller tried to bypass.
			if strings.Contains(joined, "--allowedTools") {
				t.Errorf("--allowedTools leaked into args under tool_access=none (bypass-defense regression): %v", args)
			}
		})
	}
}
