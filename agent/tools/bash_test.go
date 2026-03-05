// ABOUTME: Tests for the Bash tool.
// ABOUTME: Validates command execution, exit codes, timeout, and output formatting.
package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/2389-research/mammoth-lite/agent/exec"
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
