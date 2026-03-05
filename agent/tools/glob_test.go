// ABOUTME: Tests for the Glob tool.
// ABOUTME: Validates pattern matching, empty results, and parameter parsing.
package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/2389-research/mammoth-lite/agent/exec"
)

func TestGlobToolExecute(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.go"), []byte(""), 0644)
	os.WriteFile(filepath.Join(dir, "b.go"), []byte(""), 0644)
	os.WriteFile(filepath.Join(dir, "c.txt"), []byte(""), 0644)

	env := exec.NewLocalEnvironment(dir)
	tool := NewGlobTool(env)

	input := json.RawMessage(`{"pattern": "*.go"}`)
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "a.go") || !strings.Contains(result, "b.go") {
		t.Errorf("expected both .go files, got %q", result)
	}
	if strings.Contains(result, "c.txt") {
		t.Errorf("should not contain c.txt, got %q", result)
	}
}

func TestGlobToolNoMatches(t *testing.T) {
	env := exec.NewLocalEnvironment(t.TempDir())
	tool := NewGlobTool(env)

	input := json.RawMessage(`{"pattern": "*.xyz"}`)
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "no matches") {
		t.Errorf("expected 'no matches' message, got %q", result)
	}
}

func TestGlobToolEmptyPattern(t *testing.T) {
	env := exec.NewLocalEnvironment(t.TempDir())
	tool := NewGlobTool(env)

	input := json.RawMessage(`{"pattern": ""}`)
	_, err := tool.Execute(context.Background(), input)
	if err == nil {
		t.Error("expected error for empty pattern")
	}
}
