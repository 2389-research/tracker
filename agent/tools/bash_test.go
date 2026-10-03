// ABOUTME: Tests for the Bash tool.
// ABOUTME: Validates command execution, exit codes, timeout, and output formatting.
package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/2389-research/tracker/agent/exec"
)

func TestBashToolExecute(t *testing.T) {
	env := exec.NewLocalEnvironment(t.TempDir())
	tool := NewBashTool(env, 5*time.Second, 10*time.Second)

	input := json.RawMessage(`{"command": "echo hello world"}`)
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "hello world") {
		t.Errorf("expected output to contain 'hello world', got %q", result)
	}
}

func TestBashToolNonZeroExit(t *testing.T) {
	env := exec.NewLocalEnvironment(t.TempDir())
	tool := NewBashTool(env, 5*time.Second, 10*time.Second)

	input := json.RawMessage(`{"command": "exit 1"}`)
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "exit code: 1") {
		t.Errorf("expected exit code info, got %q", result)
	}
}

func TestBashToolTimeout(t *testing.T) {
	env := exec.NewLocalEnvironment(t.TempDir())
	tool := NewBashTool(env, 200*time.Millisecond, 500*time.Millisecond)

	input := json.RawMessage(`{"command": "sleep 10"}`)
	_, err := tool.Execute(context.Background(), input)
	if err == nil {
		t.Error("expected timeout error")
	}
}

func TestBashToolCustomTimeout(t *testing.T) {
	env := exec.NewLocalEnvironment(t.TempDir())
	tool := NewBashTool(env, 200*time.Millisecond, 10*time.Second)

	input := json.RawMessage(`{"command": "sleep 0.1", "timeout": 5}`)
	_, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBashToolEmptyCommand(t *testing.T) {
	env := exec.NewLocalEnvironment(t.TempDir())
	tool := NewBashTool(env, 5*time.Second, 10*time.Second)

	input := json.RawMessage(`{"command": ""}`)
	_, err := tool.Execute(context.Background(), input)
	if err == nil {
		t.Error("expected error for empty command")
	}
}

// The bash tool runs model-chosen commands, so credential-shaped variables in
// Tracker's own environment (provider keys above all) must not reach them.
func TestBashToolStripsSensitiveEnv(t *testing.T) {
	t.Setenv("TRACKER_PASS_ENV", "")
	t.Setenv("OPENAI_COMPAT_API_KEY", "sentinel-provider-key")
	t.Setenv("TRACKER_TEST_PLAIN_VAR", "plain-value")
	env := exec.NewLocalEnvironment(t.TempDir())
	tool := NewBashTool(env, 5*time.Second, 10*time.Second)

	input := json.RawMessage(`{"command": "printf '%s|%s' \"${OPENAI_COMPAT_API_KEY-unset}\" \"${TRACKER_TEST_PLAIN_VAR-unset}\""}`)
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(result, "sentinel-provider-key") {
		t.Fatalf("bash command saw the provider key: %q", result)
	}
	if !strings.Contains(result, "unset|plain-value") {
		t.Errorf("expected the key unset and the plain variable kept, got %q", result)
	}
}

// TRACKER_PASS_ENV=1 is the documented escape hatch for tool nodes; the bash
// tool honors it the same way.
func TestBashToolPassEnvKeepsSensitiveEnv(t *testing.T) {
	t.Setenv("TRACKER_PASS_ENV", "1")
	t.Setenv("OPENAI_COMPAT_API_KEY", "sentinel-provider-key")
	env := exec.NewLocalEnvironment(t.TempDir())
	tool := NewBashTool(env, 5*time.Second, 10*time.Second)

	input := json.RawMessage(`{"command": "printf '%s' \"${OPENAI_COMPAT_API_KEY-unset}\""}`)
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "sentinel-provider-key") {
		t.Errorf("expected TRACKER_PASS_ENV=1 to pass the key through, got %q", result)
	}
}
