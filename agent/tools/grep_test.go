// ABOUTME: Tests for the GrepSearch tool.
// ABOUTME: Validates regex search, path containment, result limits, and error handling.
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

func TestGrepSearchToolName(t *testing.T) {
	env := exec.NewLocalEnvironment(t.TempDir())
	tool := NewGrepSearchTool(env)
	if tool.Name() != "grep_search" {
		t.Errorf("expected name %q, got %q", "grep_search", tool.Name())
	}
}

func TestGrepSearchSingleFile(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "hello.go"), []byte("package main\n\nfunc Hello() string {\n\treturn \"hello\"\n}\n"), 0644)

	env := exec.NewLocalEnvironment(dir)
	tool := NewGrepSearchTool(env)

	input := json.RawMessage(`{"pattern": "Hello", "path": "hello.go"}`)
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "hello.go:3:") {
		t.Errorf("expected match at line 3, got %q", result)
	}
	if !strings.Contains(result, "func Hello()") {
		t.Errorf("expected matching content, got %q", result)
	}
}

func TestGrepSearchDirectory(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n\nfunc Foo() {}\n"), 0644)
	os.MkdirAll(filepath.Join(dir, "sub"), 0755)
	os.WriteFile(filepath.Join(dir, "sub", "b.go"), []byte("package b\n\nfunc Foo() {}\n"), 0644)
	os.WriteFile(filepath.Join(dir, "c.txt"), []byte("no match here\n"), 0644)

	env := exec.NewLocalEnvironment(dir)
	tool := NewGrepSearchTool(env)

	input := json.RawMessage(`{"pattern": "Foo"}`)
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "a.go:3:") {
		t.Errorf("expected match in a.go, got %q", result)
	}
	if !strings.Contains(result, "sub/b.go:3:") {
		t.Errorf("expected match in sub/b.go, got %q", result)
	}
	if strings.Contains(result, "c.txt") {
		t.Errorf("should not match c.txt, got %q", result)
	}
}

func TestGrepSearchInvalidRegex(t *testing.T) {
	env := exec.NewLocalEnvironment(t.TempDir())
	tool := NewGrepSearchTool(env)

	input := json.RawMessage(`{"pattern": "[invalid"}`)
	_, err := tool.Execute(context.Background(), input)
	if err == nil {
		t.Error("expected error for invalid regex")
	}
	if !strings.Contains(err.Error(), "invalid regex") {
		t.Errorf("expected 'invalid regex' in error, got %q", err.Error())
	}
}

func TestGrepSearchNonExistentPath(t *testing.T) {
	env := exec.NewLocalEnvironment(t.TempDir())
	tool := NewGrepSearchTool(env)

	input := json.RawMessage(`{"pattern": "foo", "path": "nonexistent"}`)
	_, err := tool.Execute(context.Background(), input)
	if err == nil {
		t.Error("expected error for non-existent path")
	}
}

func TestGrepSearchNoMatches(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "hello.go"), []byte("package main\n"), 0644)

	env := exec.NewLocalEnvironment(dir)
	tool := NewGrepSearchTool(env)

	input := json.RawMessage(`{"pattern": "zzz_never_match"}`)
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "no matches") {
		t.Errorf("expected 'no matches' message, got %q", result)
	}
}

func TestGrepSearchMaxResults(t *testing.T) {
	dir := t.TempDir()
	// Create a file with more lines than the max result limit.
	var lines []string
	for i := 0; i < 150; i++ {
		lines = append(lines, "match_this_line")
	}
	os.WriteFile(filepath.Join(dir, "big.txt"), []byte(strings.Join(lines, "\n")), 0644)

	env := exec.NewLocalEnvironment(dir)
	tool := NewGrepSearchTool(env)

	input := json.RawMessage(`{"pattern": "match_this_line"}`)
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Count result lines (matches). Should be capped at maxGrepResults (100).
	resultLines := strings.Split(strings.TrimSpace(result), "\n")
	// The last line might be a truncation message, so count actual match lines.
	matchCount := 0
	for _, line := range resultLines {
		if strings.Contains(line, "match_this_line") {
			matchCount++
		}
	}
	if matchCount > 100 {
		t.Errorf("expected at most 100 matches, got %d", matchCount)
	}
	if !strings.Contains(result, "truncated") {
		t.Errorf("expected truncation message, got %q", result)
	}
}

func TestGrepSearchEmptyPattern(t *testing.T) {
	env := exec.NewLocalEnvironment(t.TempDir())
	tool := NewGrepSearchTool(env)

	input := json.RawMessage(`{"pattern": ""}`)
	_, err := tool.Execute(context.Background(), input)
	if err == nil {
		t.Error("expected error for empty pattern")
	}
}

func TestGrepSearchPathOutsideWorkDir(t *testing.T) {
	env := exec.NewLocalEnvironment(t.TempDir())
	tool := NewGrepSearchTool(env)

	input := json.RawMessage(`{"pattern": "foo", "path": "../../../etc/passwd"}`)
	_, err := tool.Execute(context.Background(), input)
	if err == nil {
		t.Error("expected error for path escaping working directory")
	}
}

func TestGrepSearchRegexCapture(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "data.txt"), []byte("foo123bar\nbaz456qux\nfoo789bar\n"), 0644)

	env := exec.NewLocalEnvironment(dir)
	tool := NewGrepSearchTool(env)

	input := json.RawMessage(`{"pattern": "foo\\d+bar"}`)
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result, "data.txt:1:") {
		t.Errorf("expected match at line 1, got %q", result)
	}
	if !strings.Contains(result, "data.txt:3:") {
		t.Errorf("expected match at line 3, got %q", result)
	}
	if strings.Contains(result, "baz456qux") {
		t.Errorf("should not match line 2, got %q", result)
	}
}
