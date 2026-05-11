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

func TestExecCommandWithLimit_Truncates(t *testing.T) {
	env := NewLocalEnvironment(t.TempDir())
	result, err := env.ExecCommandWithLimit(
		context.Background(), "sh", []string{"-c", "yes hello | head -c 200000"},
		5*time.Second, 1024,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Stdout) != 1024 {
		t.Errorf("stdout len = %d, want exactly 1024 (tail window)", len(result.Stdout))
	}
	if !result.StdoutTruncated {
		t.Error("expected StdoutTruncated=true")
	}
	if result.StdoutBytesDropped != 200000-1024 {
		t.Errorf("StdoutBytesDropped = %d, want %d", result.StdoutBytesDropped, 200000-1024)
	}
	// `yes hello | head -c 200000` cuts a "hello\n"-repeating stream at an
	// arbitrary byte boundary, so the kept tail may end mid-line. Sanity
	// check: every byte in the captured tail must come from the alphabet
	// of "hello\n", proving no garbage and that the ring wrap is coherent.
	for i, b := range []byte(result.Stdout) {
		if !strings.ContainsRune("hello\n", rune(b)) {
			t.Errorf("byte %d = %q not in 'hello\\n' alphabet", i, b)
			break
		}
	}
}

func TestExecCommandWithLimit_NoTruncation(t *testing.T) {
	env := NewLocalEnvironment(t.TempDir())
	result, err := env.ExecCommandWithLimit(
		context.Background(), "sh", []string{"-c", "echo hello"},
		5*time.Second, 65536,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.StdoutTruncated {
		t.Error("small output should not set StdoutTruncated")
	}
	if result.StdoutBytesDropped != 0 {
		t.Errorf("StdoutBytesDropped = %d, want 0", result.StdoutBytesDropped)
	}
	if result.Stdout != "hello\n" {
		t.Errorf("got %q, want %q", result.Stdout, "hello\n")
	}
}

// Direct regression test for issue #208: a routing marker emitted after a
// flood of stdout must survive capture so downstream conditional edges can
// match on it. Mirrors the notebook_smoke failure shape (pytest stack
// traces followed by a trailing `printf` of the routing token).
func TestExecCommandWithLimit_RoutingMarkerSurvivesFlood(t *testing.T) {
	env := NewLocalEnvironment(t.TempDir())
	result, err := env.ExecCommandWithLimit(
		context.Background(), "sh",
		[]string{"-c", "head -c 120000 /dev/zero | tr '\\0' '.'; printf 'tests-fail-cloud'"},
		5*time.Second, 65536,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.StdoutTruncated {
		t.Error("expected StdoutTruncated=true for 120KB+marker output")
	}
	if !strings.HasSuffix(result.Stdout, "tests-fail-cloud") {
		t.Errorf("routing marker must appear at end of captured tail; got tail = %q", result.Stdout[len(result.Stdout)-40:])
	}
}

// Stderr parity: closes a pre-existing coverage gap. Tail-window semantics
// must apply identically to stderr because conditional edges can route on
// `ctx.tool_stderr`.
func TestExecCommandWithLimit_StderrTailParity(t *testing.T) {
	env := NewLocalEnvironment(t.TempDir())
	result, err := env.ExecCommandWithLimit(
		context.Background(), "sh",
		[]string{"-c", "head -c 120000 /dev/zero | tr '\\0' '.' 1>&2; printf 'stderr-marker' 1>&2"},
		5*time.Second, 65536,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.StderrTruncated {
		t.Error("expected StderrTruncated=true")
	}
	if result.StderrBytesDropped == 0 {
		t.Error("expected StderrBytesDropped > 0")
	}
	if !strings.HasSuffix(result.Stderr, "stderr-marker") {
		t.Errorf("stderr marker must survive truncation; got tail = %q", result.Stderr[len(result.Stderr)-40:])
	}
	// Stdout was never written to; its truncation flag must be false.
	if result.StdoutTruncated {
		t.Error("StdoutTruncated must remain false when only stderr was written")
	}
}

func TestExecCommandWithLimit_CustomEnv(t *testing.T) {
	env := NewLocalEnvironment(t.TempDir())
	customEnv := []string{"MY_VAR=hello"}
	result, err := env.ExecCommandWithLimit(
		context.Background(), "sh", []string{"-c", "echo $MY_VAR"},
		5*time.Second, 65536, customEnv,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.TrimSpace(result.Stdout) != "hello" {
		t.Errorf("stdout = %q, want %q", strings.TrimSpace(result.Stdout), "hello")
	}
}
