// ABOUTME: Tests for the EditFile tool (search/replace).
// ABOUTME: Validates exact replacement, multi-occurrence, missing match, and file creation.
package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/2389-research/mammoth-lite/agent/exec"
)

func TestEditToolReplace(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "code.go"), []byte("func hello() {\n\treturn \"hi\"\n}"), 0644)

	env := exec.NewLocalEnvironment(dir)
	tool := NewEditTool(env)

	input := json.RawMessage(`{
		"path": "code.go",
		"old_string": "return \"hi\"",
		"new_string": "return \"hello world\""
	}`)
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == "" {
		t.Error("expected non-empty result")
	}

	data, _ := os.ReadFile(filepath.Join(dir, "code.go"))
	if string(data) != "func hello() {\n\treturn \"hello world\"\n}" {
		t.Errorf("unexpected content: %q", string(data))
	}
}

func TestEditToolNoMatch(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "file.txt"), []byte("original"), 0644)

	env := exec.NewLocalEnvironment(dir)
	tool := NewEditTool(env)

	input := json.RawMessage(`{
		"path": "file.txt",
		"old_string": "not found",
		"new_string": "replacement"
	}`)
	_, err := tool.Execute(context.Background(), input)
	if err == nil {
		t.Error("expected error when old_string not found")
	}
}

func TestEditToolMultipleMatches(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "file.txt"), []byte("aaa bbb aaa"), 0644)

	env := exec.NewLocalEnvironment(dir)
	tool := NewEditTool(env)

	input := json.RawMessage(`{
		"path": "file.txt",
		"old_string": "aaa",
		"new_string": "ccc"
	}`)
	_, err := tool.Execute(context.Background(), input)
	if err == nil {
		t.Error("expected error for ambiguous match (multiple occurrences)")
	}
}

func TestEditToolCreateNewFile(t *testing.T) {
	dir := t.TempDir()
	env := exec.NewLocalEnvironment(dir)
	tool := NewEditTool(env)

	input := json.RawMessage(`{
		"path": "new.txt",
		"old_string": "",
		"new_string": "brand new content"
	}`)
	_, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(dir, "new.txt"))
	if string(data) != "brand new content" {
		t.Errorf("expected 'brand new content', got %q", string(data))
	}
}
