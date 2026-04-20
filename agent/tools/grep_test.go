// ABOUTME: Tests for the GrepSearch tool.
// ABOUTME: Validates regex search, path containment, result limits, and error handling.
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
	if !strings.Contains(result, "showing first 100") {
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

func TestGrepSearchContextLines(t *testing.T) {
	dir := t.TempDir()
	// Lines: 1:aaa 2:bbb 3:MATCH 4:ddd 5:eee
	content := "aaa\nbbb\nMATCH\nddd\neee\n"
	if err := os.WriteFile(filepath.Join(dir, "ctx.txt"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	env := exec.NewLocalEnvironment(dir)
	tool := NewGrepSearchTool(env)

	input := json.RawMessage(`{"pattern": "MATCH", "path": "ctx.txt", "context_lines": 1}`)
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Line 2 (bbb) should appear as context with `-` separator.
	if !strings.Contains(result, "ctx.txt-2-bbb") {
		t.Errorf("expected context line before match (ctx.txt-2-bbb), got %q", result)
	}
	// Line 3 (MATCH) should appear with `:` separator.
	if !strings.Contains(result, "ctx.txt:3:MATCH") {
		t.Errorf("expected match line (ctx.txt:3:MATCH), got %q", result)
	}
	// Line 4 (ddd) should appear as context with `-` separator.
	if !strings.Contains(result, "ctx.txt-4-ddd") {
		t.Errorf("expected context line after match (ctx.txt-4-ddd), got %q", result)
	}
	// Line 1 (aaa) should not be included — outside the context window.
	if strings.Contains(result, "aaa") {
		t.Errorf("should not include aaa (outside context window), got %q", result)
	}
}

func TestGrepSearchContextLinesMerge(t *testing.T) {
	dir := t.TempDir()
	// Lines: 1:aaa 2:MATCH1 3:bbb 4:ccc 5:MATCH2 6:ddd
	// With context_lines=2: match1 window=[1..4], match2 window=[3..6] — they overlap, should merge.
	content := "aaa\nMATCH1\nbbb\nccc\nMATCH2\nddd\n"
	if err := os.WriteFile(filepath.Join(dir, "merge.txt"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	env := exec.NewLocalEnvironment(dir)
	tool := NewGrepSearchTool(env)

	input := json.RawMessage(`{"pattern": "MATCH", "path": "merge.txt", "context_lines": 2}`)
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Both matches must be present.
	if !strings.Contains(result, "merge.txt:2:MATCH1") {
		t.Errorf("expected MATCH1 at line 2, got %q", result)
	}
	if !strings.Contains(result, "merge.txt:5:MATCH2") {
		t.Errorf("expected MATCH2 at line 5, got %q", result)
	}
	// No `--` separator because windows overlap/merge.
	if strings.Contains(result, "--") {
		t.Errorf("expected no -- separator for merged windows, got %q", result)
	}
	// bbb (line 3) should appear exactly once as a context line.
	count := strings.Count(result, "bbb")
	if count != 1 {
		t.Errorf("expected bbb to appear exactly once (no duplicate context), got count=%d in %q", count, result)
	}
}

func TestGrepSearchSkipsPycache(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "__pycache__"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "__pycache__", "cached.py"), []byte("HIDDEN_MATCH\n"), 0644); err != nil {
		t.Fatal(err)
	}
	// A real file with the same pattern to confirm search works at all.
	if err := os.WriteFile(filepath.Join(dir, "visible.py"), []byte("REAL_MATCH\n"), 0644); err != nil {
		t.Fatal(err)
	}

	env := exec.NewLocalEnvironment(dir)
	tool := NewGrepSearchTool(env)

	input := json.RawMessage(`{"pattern": "MATCH"}`)
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(result, "__pycache__") {
		t.Errorf("should not search inside __pycache__, got %q", result)
	}
	if !strings.Contains(result, "REAL_MATCH") {
		t.Errorf("expected REAL_MATCH in visible.py, got %q", result)
	}
}

func TestGrepSearchSkipsNodeModules(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "node_modules", "lib"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "node_modules", "lib", "index.js"), []byte("HIDDEN_MATCH\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "app.js"), []byte("REAL_MATCH\n"), 0644); err != nil {
		t.Fatal(err)
	}

	env := exec.NewLocalEnvironment(dir)
	tool := NewGrepSearchTool(env)

	input := json.RawMessage(`{"pattern": "MATCH"}`)
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(result, "node_modules") {
		t.Errorf("should not search inside node_modules, got %q", result)
	}
	if !strings.Contains(result, "REAL_MATCH") {
		t.Errorf("expected REAL_MATCH in app.js, got %q", result)
	}
}

func TestGrepSearchTotalMatchCount(t *testing.T) {
	dir := t.TempDir()
	// Create a file with 150 matching lines — exceeds maxGrepResults (100).
	var content strings.Builder
	for i := 0; i < 150; i++ {
		fmt.Fprintf(&content, "match line %d\n", i)
	}
	os.WriteFile(filepath.Join(dir, "big.txt"), []byte(content.String()), 0644)

	env := exec.NewLocalEnvironment(dir)
	tool := NewGrepSearchTool(env)

	input := json.RawMessage(`{"pattern": "match line", "path": "big.txt"}`)
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should mention that there are 150 total matches, not just "more matches".
	if !strings.Contains(result, "150") {
		t.Errorf("expected total match count 150 in output, got:\n%s", result)
	}
	if !strings.Contains(result, "showing first 100") {
		t.Errorf("expected truncation message, got:\n%s", result)
	}
}

func TestGrepSearchSkipsBuild(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "build"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "build", "output.js"), []byte("HIDDEN_MATCH\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "src.go"), []byte("REAL_MATCH\n"), 0644); err != nil {
		t.Fatal(err)
	}

	env := exec.NewLocalEnvironment(dir)
	tool := NewGrepSearchTool(env)

	input := json.RawMessage(`{"pattern": "MATCH"}`)
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(result, "build/") {
		t.Errorf("should not search inside build/, got %q", result)
	}
	if !strings.Contains(result, "REAL_MATCH") {
		t.Errorf("expected REAL_MATCH in src.go, got %q", result)
	}
}
