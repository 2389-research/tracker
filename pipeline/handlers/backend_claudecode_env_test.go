// ABOUTME: Tests for buildEnv API key stripping and model detection.
// ABOUTME: Verifies that provider API keys are removed and non-Anthropic models are detected.
package handlers

import (
	"os"
	"strings"
	"testing"
)

func TestIsClaudeModel(t *testing.T) {
	tests := []struct {
		model string
		want  bool
	}{
		{"claude-sonnet-4-6", true},
		{"claude-opus-4-6", true},
		{"Claude-Haiku-4-5", true},
		{"anthropic-model", true},
		{"gpt-5.4", false},
		{"gemini-2.5-pro", false},
		{"llama-3", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := isClaudeModel(tt.model); got != tt.want {
			t.Errorf("isClaudeModel(%q) = %v, want %v", tt.model, got, tt.want)
		}
	}
}

func TestClassifyErrorCreditBalance(t *testing.T) {
	got := classifyError("Your credit balance is too low", 1)
	if got != "fail" {
		t.Errorf("expected fail for credit balance error, got %q", got)
	}
}

func TestClassifyErrorRateLimit(t *testing.T) {
	got := classifyError("rate limit exceeded", 1)
	if got != "retry" {
		t.Errorf("expected retry for rate limit, got %q", got)
	}
}

func TestClassifyErrorSuccess(t *testing.T) {
	got := classifyError("", 0)
	if got != "success" {
		t.Errorf("expected success for exit 0, got %q", got)
	}
}

func TestClassifyErrorUnknown(t *testing.T) {
	got := classifyError("something weird", 1)
	if got != "fail" {
		t.Errorf("expected fail for unknown error, got %q", got)
	}
}

func TestBuildEnvStripsAPIKeys(t *testing.T) {
	// Set test API keys.
	os.Setenv("ANTHROPIC_API_KEY", "sk-ant-test")
	os.Setenv("OPENAI_API_KEY", "sk-test")
	os.Setenv("GEMINI_API_KEY", "gem-test")
	os.Setenv("GOOGLE_API_KEY", "goog-test")
	defer func() {
		os.Unsetenv("ANTHROPIC_API_KEY")
		os.Unsetenv("OPENAI_API_KEY")
		os.Unsetenv("GEMINI_API_KEY")
		os.Unsetenv("GOOGLE_API_KEY")
	}()

	env := buildEnv()

	for _, e := range env {
		for _, prefix := range providerKeyPrefixes {
			if strings.HasPrefix(e, prefix) {
				t.Errorf("expected %s stripped from env, but found: %s", prefix, e)
			}
		}
	}
}

func TestBuildEnvPreservesNonAPIKeys(t *testing.T) {
	os.Setenv("ANTHROPIC_API_KEY", "sk-ant-test")
	os.Setenv("HOME", "/test/home")
	os.Setenv("PATH", "/usr/bin")
	defer os.Unsetenv("ANTHROPIC_API_KEY")

	env := buildEnv()

	foundHome := false
	foundPath := false
	for _, e := range env {
		if strings.HasPrefix(e, "HOME=") {
			foundHome = true
		}
		if strings.HasPrefix(e, "PATH=") {
			foundPath = true
		}
	}
	if !foundHome {
		t.Error("expected HOME preserved in env")
	}
	if !foundPath {
		t.Error("expected PATH preserved in env")
	}
}

func TestBuildEnvPassthroughWithOverride(t *testing.T) {
	os.Setenv("ANTHROPIC_API_KEY", "sk-ant-test")
	os.Setenv("TRACKER_PASS_API_KEYS", "1")
	defer func() {
		os.Unsetenv("ANTHROPIC_API_KEY")
		os.Unsetenv("TRACKER_PASS_API_KEYS")
	}()

	env := buildEnv()

	found := false
	for _, e := range env {
		if strings.HasPrefix(e, "ANTHROPIC_API_KEY=") {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected ANTHROPIC_API_KEY preserved with TRACKER_PASS_API_KEYS=1")
	}
}

func TestBuildEnv_StripsOpenAICompatKey(t *testing.T) {
	t.Setenv("OPENAI_COMPAT_API_KEY", "test-compat-key-12345")
	t.Setenv("TRACKER_PASS_API_KEYS", "")

	env := buildEnv()

	for _, e := range env {
		if strings.HasPrefix(e, "OPENAI_COMPAT_API_KEY=") {
			t.Errorf("expected OPENAI_COMPAT_API_KEY stripped from env, but found: %s", e)
		}
	}
}

func TestBuildEnvNoKeysNoop(t *testing.T) {
	// Ensure no API keys are set.
	os.Unsetenv("ANTHROPIC_API_KEY")
	os.Unsetenv("OPENAI_API_KEY")
	os.Unsetenv("GEMINI_API_KEY")
	os.Unsetenv("GOOGLE_API_KEY")
	os.Unsetenv("TRACKER_PASS_API_KEYS")

	env := buildEnv()
	if len(env) == 0 {
		t.Error("expected non-empty env even without API keys")
	}
}
