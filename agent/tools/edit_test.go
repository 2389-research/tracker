// ABOUTME: Tests for the EditFile tool (search/replace).
// ABOUTME: Validates exact replacement, multi-occurrence, missing match, and file creation.
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

func TestEditToolEmptyOldStringExistingFile(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "existing.txt"), []byte("existing content"), 0644)

	env := exec.NewLocalEnvironment(dir)
	tool := NewEditTool(env)

	input := json.RawMessage(`{
		"path": "existing.txt",
		"old_string": "",
		"new_string": "new content"
	}`)
	_, err := tool.Execute(context.Background(), input)
	if err == nil {
		t.Error("expected error when old_string is empty and file exists")
	}
}

func TestEditToolNotFoundShowsContext(t *testing.T) {
	dir := t.TempDir()
	fileContent := "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n"
	os.WriteFile(filepath.Join(dir, "main.go"), []byte(fileContent), 0644)

	env := exec.NewLocalEnvironment(dir)
	tool := NewEditTool(env)

	input := json.RawMessage(`{
		"path": "main.go",
		"old_string": "func main() {\n\tfmt.Println(\"goodbye\")\n}",
		"new_string": "func main() {}"
	}`)
	_, err := tool.Execute(context.Background(), input)
	if err == nil {
		t.Fatal("expected error when old_string not found")
	}
	errMsg := err.Error()
	if !strings.Contains(errMsg, "Closest content") {
		t.Errorf("expected error to contain 'Closest content', got: %s", errMsg)
	}
	// Should include a line number (e.g. "5:")
	if !strings.Contains(errMsg, ":") {
		t.Errorf("expected error to contain line numbers, got: %s", errMsg)
	}
	if !strings.Contains(errMsg, "Re-read") {
		t.Errorf("expected error to contain re-read hint, got: %s", errMsg)
	}
}
