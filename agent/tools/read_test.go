// ABOUTME: Tests for the ReadFile tool.
// ABOUTME: Validates file reading, missing file error, parameter parsing, and offset/limit paging.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/2389-research/tracker/agent/exec"
)

// fiveLineContent is the canonical 5-line test fixture used by paging tests.
const fiveLineContent = "line one\nline two\nline three\nline four\nline five"

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

// newFiveLineFile writes fiveLineContent to a temp dir and returns a ready tool.
func newFiveLineFile(t *testing.T) (*ReadTool, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "five.txt"), []byte(fiveLineContent), 0644); err != nil {
		t.Fatalf("setup: %v", err)
	}
	return NewReadTool(exec.NewLocalEnvironment(dir)), "five.txt"
}

func TestReadToolWithOffset(t *testing.T) {
	tool, name := newFiveLineFile(t)

	input := fmt.Sprintf(`{"path": %q, "offset": 3}`, name)
	result, err := tool.Execute(context.Background(), json.RawMessage(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "line three") {
		t.Errorf("expected line three in result, got: %q", result)
	}
	if strings.Contains(result, "line one") || strings.Contains(result, "line two") {
		t.Errorf("lines before offset should be excluded, got: %q", result)
	}
	if !strings.Contains(result, "[showing lines 3-") {
		t.Errorf("expected header with line range, got: %q", result)
	}
}

func TestReadToolWithLimit(t *testing.T) {
	tool, name := newFiveLineFile(t)

	input := fmt.Sprintf(`{"path": %q, "limit": 2}`, name)
	result, err := tool.Execute(context.Background(), json.RawMessage(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "line one") || !strings.Contains(result, "line two") {
		t.Errorf("expected first two lines, got: %q", result)
	}
	if strings.Contains(result, "line three") {
		t.Errorf("lines beyond limit should be excluded, got: %q", result)
	}
	if !strings.Contains(result, "[showing lines 1-2 of 5]") {
		t.Errorf("expected header '[showing lines 1-2 of 5]', got: %q", result)
	}
}

func TestReadToolWithOffsetAndLimit(t *testing.T) {
	tool, name := newFiveLineFile(t)

	input := fmt.Sprintf(`{"path": %q, "offset": 2, "limit": 2}`, name)
	result, err := tool.Execute(context.Background(), json.RawMessage(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "line two") || !strings.Contains(result, "line three") {
		t.Errorf("expected lines 2-3, got: %q", result)
	}
	if strings.Contains(result, "line one") || strings.Contains(result, "line four") {
		t.Errorf("out-of-range lines should be excluded, got: %q", result)
	}
	if !strings.Contains(result, "[showing lines 2-3 of 5]") {
		t.Errorf("expected header '[showing lines 2-3 of 5]', got: %q", result)
	}
}

func TestReadToolOffsetBeyondEOF(t *testing.T) {
	tool, name := newFiveLineFile(t)

	input := fmt.Sprintf(`{"path": %q, "offset": 999}`, name)
	_, err := tool.Execute(context.Background(), json.RawMessage(input))
	if err == nil {
		t.Fatal("expected error for offset beyond end of file")
	}
	if !strings.Contains(err.Error(), "beyond end of file") {
		t.Errorf("error should mention 'beyond end of file', got: %v", err)
	}
}
