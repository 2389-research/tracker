// ABOUTME: Tests for the agent-runner binary entry point.
// ABOUTME: Covers config parsing defaults, env-driven overrides, and system prompt presence.
package main

import (
	"testing"
	"time"
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
	if cfg.MaxTurns != 50 {
		t.Errorf("expected MaxTurns=50, got %d", cfg.MaxTurns)
	}
	if cfg.Timeout != 10*time.Minute {
		t.Errorf("expected Timeout=10m, got %v", cfg.Timeout)
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
