// ABOUTME: Tests for the agent-runner binary entry point.
// ABOUTME: Covers config parsing defaults, env-driven overrides, and system prompt presence.
package main

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/2389-research/tracker/agent"
)

func TestParseConfig_Defaults(t *testing.T) {
	// Clear env vars that might affect defaults.
	t.Setenv("SWEBENCH_INSTANCE", "django__django-12345")
	t.Setenv("SWEBENCH_REPO_DIR", "")
	t.Setenv("SWEBENCH_MODEL", "")
	t.Setenv("SWEBENCH_PROVIDER", "")
	t.Setenv("SWEBENCH_MAX_TURNS", "")
	t.Setenv("SWEBENCH_TIMEOUT", "")

	cfg := parseConfig()

	if cfg.RepoDir != "/workspace" {
		t.Errorf("expected RepoDir=/workspace, got %q", cfg.RepoDir)
	}
	if cfg.Model != "claude-sonnet-4-6" {
		t.Errorf("expected Model=claude-sonnet-4-6, got %q", cfg.Model)
	}
	if cfg.Provider != "anthropic" {
		t.Errorf("expected Provider=anthropic, got %q", cfg.Provider)
	}
	if cfg.MaxTurns != 80 {
		t.Errorf("expected MaxTurns=80, got %d", cfg.MaxTurns)
	}
	if cfg.Timeout != 30*time.Minute {
		t.Errorf("expected Timeout=30m, got %v", cfg.Timeout)
	}
	if cfg.Instance != "django__django-12345" {
		t.Errorf("expected Instance=django__django-12345, got %q", cfg.Instance)
	}
}

func TestParseConfig_FromEnv(t *testing.T) {
	t.Setenv("SWEBENCH_INSTANCE", "flask__flask-9999")
	t.Setenv("SWEBENCH_REPO_DIR", "/code/repo")
	t.Setenv("SWEBENCH_MODEL", "gpt-4o")
	t.Setenv("SWEBENCH_PROVIDER", "openai")
	t.Setenv("SWEBENCH_MAX_TURNS", "30")
	t.Setenv("SWEBENCH_TIMEOUT", "20m")

	cfg := parseConfig()

	if cfg.Instance != "flask__flask-9999" {
		t.Errorf("expected Instance=flask__flask-9999, got %q", cfg.Instance)
	}
	if cfg.RepoDir != "/code/repo" {
		t.Errorf("expected RepoDir=/code/repo, got %q", cfg.RepoDir)
	}
	if cfg.Model != "gpt-4o" {
		t.Errorf("expected Model=gpt-4o, got %q", cfg.Model)
	}
	if cfg.Provider != "openai" {
		t.Errorf("expected Provider=openai, got %q", cfg.Provider)
	}
	if cfg.MaxTurns != 30 {
		t.Errorf("expected MaxTurns=30, got %d", cfg.MaxTurns)
	}
	if cfg.Timeout != 20*time.Minute {
		t.Errorf("expected Timeout=20m, got %v", cfg.Timeout)
	}
}

func TestSystemPrompt(t *testing.T) {
	if swebenchSystemPrompt == "" {
		t.Error("swebenchSystemPrompt must not be empty")
	}
}

func TestBuildLLMClient_UnsupportedProvider(t *testing.T) {
	_, err := buildLLMClient("gemini", "")
	if err == nil {
		t.Fatal("expected error for unsupported provider, got nil")
	}
	if !strings.Contains(err.Error(), "unsupported provider") {
		t.Errorf("expected 'unsupported provider' in error, got: %v", err)
	}
}

func TestClassifyTerminationReason(t *testing.T) {
	tests := []struct {
		name   string
		result agent.SessionResult
		err    error
		want   string
	}{
		{
			name: "explicit_finish",
			want: "explicit_finish",
		},
		{
			name:   "max_turns",
			result: agent.SessionResult{MaxTurnsUsed: true},
			want:   "max_turns_reached",
		},
		{
			name: "empty_response",
			err:  errors.New("agent session failed: 2 consecutive empty API responses"),
			want: "empty_response",
		},
		{
			name: "generic_error",
			err:  errors.New("tool failed"),
			want: "tool_error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := classifyTerminationReason(tt.result, tt.err); got != tt.want {
				t.Fatalf("classifyTerminationReason() = %q, want %q", got, tt.want)
			}
		})
	}
}
