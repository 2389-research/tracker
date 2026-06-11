// ABOUTME: Tests for ExecutionEnvironment interface and LocalEnvironment implementation.
// ABOUTME: Validates file operations, command execution, and glob matching against real filesystem.
package exec

import (
	"context"
	"os"
	"os/exec"
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
		t.Errorf("routing marker must appear at end of captured tail; got tail = %q", tailPreview(result.Stdout, 40))
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
		t.Errorf("stderr marker must survive truncation; got tail = %q", tailPreview(result.Stderr, 40))
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

func TestLocalEnvironment_CommandWrapperApplied(t *testing.T) {
	env := NewLocalEnvironment(t.TempDir())
	wrapped := false
	env.CommandWrapper = func(cmd *exec.Cmd) *exec.Cmd {
		wrapped = true
		return cmd
	}
	// "sh -c true" rather than /bin/true — macOS has no /bin/true.
	_, err := env.ExecCommand(context.Background(), "sh", []string{"-c", "true"}, 5*time.Second)
	if err != nil {
		t.Fatalf("ExecCommand: %v", err)
	}
	if !wrapped {
		t.Error("CommandWrapper was not invoked")
	}
}

func TestLocalEnvironment_CommandWrapperAppliedWithLimit(t *testing.T) {
	// ExecCommandWithLimit takes the same wrap path; the jail must apply
	// uniformly across both Exec entry points.
	env := NewLocalEnvironment(t.TempDir())
	wrapped := false
	env.CommandWrapper = func(cmd *exec.Cmd) *exec.Cmd {
		wrapped = true
		return cmd
	}
	_, err := env.ExecCommandWithLimit(context.Background(), "sh", []string{"-c", "true"}, 5*time.Second, 1024)
	if err != nil {
		t.Fatalf("ExecCommandWithLimit: %v", err)
	}
	if !wrapped {
		t.Error("CommandWrapper was not invoked for ExecCommandWithLimit")
	}
}

func TestLocalEnvironment_WriteOpenerApplied(t *testing.T) {
	dir := t.TempDir()
	env := NewLocalEnvironment(dir)
	opened := false
	env.WriteOpener = func(abs string, perm os.FileMode) (*os.File, error) {
		opened = true
		return os.OpenFile(abs, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	}
	if err := env.WriteFile(context.Background(), "test.txt", "hello"); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if !opened {
		t.Error("WriteOpener was not invoked")
	}
	got, _ := os.ReadFile(filepath.Join(dir, "test.txt"))
	if string(got) != "hello" {
		t.Errorf("file contents = %q, want %q", string(got), "hello")
	}
}

func TestLocalEnvironment_HooksNilFallsThrough(t *testing.T) {
	env := NewLocalEnvironment(t.TempDir())
	// Both function fields nil — must fall through to existing behavior.
	if _, err := env.ExecCommand(context.Background(), "sh", []string{"-c", "true"}, 5*time.Second); err != nil {
		t.Errorf("ExecCommand with nil CommandWrapper = %v, want nil", err)
	}
	if _, err := env.ExecCommandWithLimit(context.Background(), "sh", []string{"-c", "true"}, 5*time.Second, 1024); err != nil {
		t.Errorf("ExecCommandWithLimit with nil CommandWrapper = %v, want nil", err)
	}
	if err := env.WriteFile(context.Background(), "test.txt", "hello"); err != nil {
		t.Errorf("WriteFile with nil WriteOpener = %v, want nil", err)
	}
}
