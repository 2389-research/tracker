// ABOUTME: Tests for the ReadFile tool.
// ABOUTME: Validates file reading, missing file error, and parameter parsing.
package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/2389-research/tracker/agent/exec"
)

func TestReadToolExecute(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("hello world"), 0644)
	env := exec.NewLocalEnvironment(dir)
	tool := NewReadTool(env)

	input := json.RawMessage(`{"path": "hello.txt"}`)
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "hello world" {
		t.Errorf("expected 'hello world', got %q", result)
	}
}

func TestReadToolMissingFile(t *testing.T) {
	env := exec.NewLocalEnvironment(t.TempDir())
	tool := NewReadTool(env)

	input := json.RawMessage(`{"path": "nope.txt"}`)
	_, err := tool.Execute(context.Background(), input)
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestReadToolInterface(t *testing.T) {
	env := exec.NewLocalEnvironment(t.TempDir())
	tool := NewReadTool(env)

	if tool.Name() != "read" {
		t.Errorf("expected name 'read', got %q", tool.Name())
	}
	if !strings.Contains(tool.Description(), "file") {
		t.Errorf("description should mention file: %q", tool.Description())
	}
	var params map[string]any
	json.Unmarshal(tool.Parameters(), &params)
	if params["type"] != "object" {
		t.Errorf("parameters should be an object schema")
	}
}
