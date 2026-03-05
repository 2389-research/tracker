// ABOUTME: Tests for ExecutionEnvironment interface and LocalEnvironment implementation.
// ABOUTME: Validates file operations, command execution, and glob matching against real filesystem.
package exec

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLocalReadFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.txt")
	os.WriteFile(path, []byte("hello world"), 0644)

	env := NewLocalEnvironment(dir)
	content, err := env.ReadFile(context.Background(), "test.txt")
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	if content != "hello world" {
		t.Errorf("expected 'hello world', got %q", content)
	}
}

func TestLocalReadFileNotFound(t *testing.T) {
	env := NewLocalEnvironment(t.TempDir())
	_, err := env.ReadFile(context.Background(), "nonexistent.txt")
	if err == nil {
		t.Error("expected error for missing file")
	}
}

func TestLocalWriteFile(t *testing.T) {
	dir := t.TempDir()
	env := NewLocalEnvironment(dir)

	err := env.WriteFile(context.Background(), "output.txt", "content here")
	if err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(dir, "output.txt"))
	if string(data) != "content here" {
		t.Errorf("expected 'content here', got %q", string(data))
	}
}

func TestLocalWriteFileCreatesDirectories(t *testing.T) {
	dir := t.TempDir()
	env := NewLocalEnvironment(dir)

	err := env.WriteFile(context.Background(), "sub/dir/file.txt", "nested")
	if err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(dir, "sub/dir/file.txt"))
	if string(data) != "nested" {
		t.Errorf("expected 'nested', got %q", string(data))
	}
}

func TestLocalExecCommand(t *testing.T) {
	env := NewLocalEnvironment(t.TempDir())
	result, err := env.ExecCommand(context.Background(), "echo", []string{"hello"}, 5*time.Second)
	if err != nil {
		t.Fatalf("ExecCommand failed: %v", err)
	}
	if result.ExitCode != 0 {
		t.Errorf("expected exit code 0, got %d", result.ExitCode)
	}
	if !strings.Contains(result.Stdout, "hello") {
		t.Errorf("expected stdout to contain 'hello', got %q", result.Stdout)
	}
}

func TestLocalExecCommandTimeout(t *testing.T) {
	env := NewLocalEnvironment(t.TempDir())
	_, err := env.ExecCommand(context.Background(), "sleep", []string{"10"}, 100*time.Millisecond)
	if err == nil {
		t.Error("expected timeout error")
	}
}

func TestLocalExecCommandFailure(t *testing.T) {
	env := NewLocalEnvironment(t.TempDir())
	result, err := env.ExecCommand(context.Background(), "sh", []string{"-c", "exit 42"}, 5*time.Second)
	if err != nil {
		t.Fatalf("ExecCommand should not error on non-zero exit: %v", err)
	}
	if result.ExitCode != 42 {
		t.Errorf("expected exit code 42, got %d", result.ExitCode)
	}
}

func TestLocalGlob(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a.go"), []byte(""), 0644)
	os.WriteFile(filepath.Join(dir, "b.go"), []byte(""), 0644)
	os.WriteFile(filepath.Join(dir, "c.txt"), []byte(""), 0644)

	env := NewLocalEnvironment(dir)
	matches, err := env.Glob(context.Background(), "*.go")
	if err != nil {
		t.Fatalf("Glob failed: %v", err)
	}
	if len(matches) != 2 {
		t.Errorf("expected 2 matches, got %d: %v", len(matches), matches)
	}
}

func TestLocalPathEscapePrevention(t *testing.T) {
	dir := t.TempDir()
	env := NewLocalEnvironment(dir)

	_, err := env.ReadFile(context.Background(), "../../../etc/passwd")
	if err == nil {
		t.Error("expected error for path traversal")
	}
}
