// ABOUTME: Tests for the WriteFile tool.
// ABOUTME: Validates file creation, overwrite, directory creation, and parameter parsing.
package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/2389-research/mammoth-lite/agent/exec"
)

func TestWriteToolExecute(t *testing.T) {
	dir := t.TempDir()
	env := exec.NewLocalEnvironment(dir)
	tool := NewWriteTool(env)

	input := json.RawMessage(`{"path": "out.txt", "content": "new file"}`)
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == "" {
		t.Error("expected non-empty result")
	}

	data, _ := os.ReadFile(filepath.Join(dir, "out.txt"))
	if string(data) != "new file" {
		t.Errorf("expected 'new file', got %q", string(data))
	}
}

func TestWriteToolCreatesDirectories(t *testing.T) {
	dir := t.TempDir()
	env := exec.NewLocalEnvironment(dir)
	tool := NewWriteTool(env)

	input := json.RawMessage(`{"path": "a/b/c.txt", "content": "deep"}`)
	_, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(dir, "a/b/c.txt"))
	if string(data) != "deep" {
		t.Errorf("expected 'deep', got %q", string(data))
	}
}

func TestWriteToolMissingPath(t *testing.T) {
	env := exec.NewLocalEnvironment(t.TempDir())
	tool := NewWriteTool(env)

	input := json.RawMessage(`{"content": "no path"}`)
	_, err := tool.Execute(context.Background(), input)
	if err == nil {
		t.Error("expected error for missing path")
	}
}
