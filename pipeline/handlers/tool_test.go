// ABOUTME: Tests for the tool handler, verifying shell command execution via ExecutionEnvironment.
// ABOUTME: Covers success, failure, missing command, timeout, and custom timeout scenarios.
package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/2389-research/tracker/agent/exec"
	"github.com/2389-research/tracker/pipeline"
)

// mockExecEnv is a test-only ExecutionEnvironment that returns canned results
// based on the command, without needing an actual shell.
type mockExecEnv struct {
	workdir  string
	results  map[string]exec.CommandResult // keyed by command content
	execErr  error
	timedOut bool
}

func (m *mockExecEnv) ReadFile(ctx context.Context, path string) (string, error) {
	return "", fmt.Errorf("not implemented")
}
func (m *mockExecEnv) WriteFile(ctx context.Context, path, content string) error {
	return fmt.Errorf("not implemented")
}
func (m *mockExecEnv) Glob(ctx context.Context, pattern string) ([]string, error) {
	return nil, fmt.Errorf("not implemented")
}
func (m *mockExecEnv) WorkingDir() string { return m.workdir }

// toolTestEnv returns a real LocalEnvironment if sh is available, otherwise
// a mock that returns canned results. This ensures tests pass in sandboxed
// environments without sh while still exercising real shell execution when possible.
func toolTestEnv(t *testing.T, results map[string]exec.CommandResult) exec.ExecutionEnvironment {
	t.Helper()
	if _, err := osexec.LookPath("sh"); err == nil {
		return exec.NewLocalEnvironment(t.TempDir())
	}
	return &mockExecEnv{workdir: t.TempDir(), results: results}
}

func (m *mockExecEnv) ExecCommand(ctx context.Context, command string, args []string, timeout time.Duration) (exec.CommandResult, error) {
	if m.timedOut {
		return exec.CommandResult{}, fmt.Errorf("command timed out after %v", timeout)
	}
	if m.execErr != nil {
		return exec.CommandResult{}, m.execErr
	}
	// Match by the shell command content (args[1] for "sh -c <cmd>").
	key := ""
	if len(args) >= 2 {
		key = args[1]
	}
	if r, ok := m.results[key]; ok {
		return r, nil
	}
	return exec.CommandResult{Stdout: "", ExitCode: 0}, nil
}

func TestToolHandlerName(t *testing.T) {
	env := exec.NewLocalEnvironment(t.TempDir())
	h := NewToolHandler(env)
	if h.Name() != "tool" {
		t.Errorf("expected name %q, got %q", "tool", h.Name())
	}
}

func TestToolHandlerImplementsHandler(t *testing.T) {
	env := exec.NewLocalEnvironment(t.TempDir())
	var _ pipeline.Handler = NewToolHandler(env)
}

func TestToolHandlerSuccess(t *testing.T) {
	env := toolTestEnv(t, map[string]exec.CommandResult{
		"echo hello": {Stdout: "hello\n", ExitCode: 0},
	})
	h := NewToolHandler(env)
	node := &pipeline.Node{
		ID:    "t1",
		Shape: "parallelogram",
		Attrs: map[string]string{"tool_command": "echo hello"},
	}
	pctx := pipeline.NewPipelineContext()

	outcome, err := h.Execute(context.Background(), node, pctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Status != pipeline.OutcomeSuccess {
		t.Errorf("expected status %q, got %q", pipeline.OutcomeSuccess, outcome.Status)
	}
	stdout := outcome.ContextUpdates[pipeline.ContextKeyToolStdout]
	if stdout != "hello" {
		t.Errorf("expected stdout %q (trimmed), got %q", "hello", stdout)
	}
}

func TestToolHandlerFailure(t *testing.T) {
	env := toolTestEnv(t, map[string]exec.CommandResult{
		"exit 1": {ExitCode: 1},
	})
	h := NewToolHandler(env)
	node := &pipeline.Node{
		ID:    "t2",
		Shape: "parallelogram",
		Attrs: map[string]string{"tool_command": "exit 1"},
	}
	pctx := pipeline.NewPipelineContext()

	outcome, err := h.Execute(context.Background(), node, pctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Status != pipeline.OutcomeFail {
		t.Errorf("expected status %q, got %q", pipeline.OutcomeFail, outcome.Status)
	}
}

func TestToolHandlerMissingCommand(t *testing.T) {
	env := exec.NewLocalEnvironment(t.TempDir())
	h := NewToolHandler(env)
	node := &pipeline.Node{
		ID:    "t3",
		Shape: "parallelogram",
		Attrs: map[string]string{},
	}
	pctx := pipeline.NewPipelineContext()

	_, err := h.Execute(context.Background(), node, pctx)
	if err == nil {
		t.Fatal("expected error for missing tool_command")
	}
	if !strings.Contains(err.Error(), "tool_command") {
		t.Errorf("expected error to mention tool_command, got: %v", err)
	}
}

func TestToolHandlerTimeout(t *testing.T) {
	env := &mockExecEnv{workdir: t.TempDir(), timedOut: true}
	h := NewToolHandlerWithTimeout(env, 100*time.Millisecond)
	node := &pipeline.Node{
		ID:    "t4",
		Shape: "parallelogram",
		Attrs: map[string]string{"tool_command": "sleep 30"},
	}
	pctx := pipeline.NewPipelineContext()

	_, err := h.Execute(context.Background(), node, pctx)
	if err == nil {
		t.Fatal("expected error for timeout")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("expected timeout error, got: %v", err)
	}
}

func TestToolHandlerCustomTimeout(t *testing.T) {
	env := toolTestEnv(t, map[string]exec.CommandResult{
		"echo fast": {Stdout: "fast\n", ExitCode: 0},
	})
	h := NewToolHandler(env)
	node := &pipeline.Node{
		ID:    "t5",
		Shape: "parallelogram",
		Attrs: map[string]string{
			"tool_command": "echo fast",
			"timeout":      "5s",
		},
	}
	pctx := pipeline.NewPipelineContext()

	outcome, err := h.Execute(context.Background(), node, pctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Status != pipeline.OutcomeSuccess {
		t.Errorf("expected status %q, got %q", pipeline.OutcomeSuccess, outcome.Status)
	}
	stdout := outcome.ContextUpdates[pipeline.ContextKeyToolStdout]
	if strings.TrimSpace(stdout) != "fast" {
		t.Errorf("expected stdout %q, got %q", "fast", stdout)
	}
}

func TestToolHandlerDefaultTimeout(t *testing.T) {
	env := exec.NewLocalEnvironment(t.TempDir())
	customTimeout := 10 * time.Second
	h := NewToolHandlerWithTimeout(env, customTimeout)
	if h.defaultTimeout != customTimeout {
		t.Errorf("expected default timeout %v, got %v", customTimeout, h.defaultTimeout)
	}
}

func TestToolHandlerWritesStatusArtifact(t *testing.T) {
	env := toolTestEnv(t, map[string]exec.CommandResult{
		"echo hello": {Stdout: "hello\n", ExitCode: 0},
	})
	h := NewToolHandler(env)
	node := &pipeline.Node{
		ID:    "toolstep",
		Shape: "parallelogram",
		Attrs: map[string]string{"tool_command": "echo hello"},
	}
	pctx := pipeline.NewPipelineContext()

	outcome, err := h.Execute(context.Background(), node, pctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Status != pipeline.OutcomeSuccess {
		t.Fatalf("expected success, got %q", outcome.Status)
	}

	workdir := env.WorkingDir()
	statusBytes, err := os.ReadFile(filepath.Join(workdir, "toolstep", "status.json"))
	if err != nil {
		t.Fatalf("expected status artifact: %v", err)
	}
	var status map[string]any
	if err := json.Unmarshal(statusBytes, &status); err != nil {
		t.Fatalf("status artifact should be valid json: %v", err)
	}
	if status["outcome"] != pipeline.OutcomeSuccess {
		t.Fatalf("status outcome = %v", status["outcome"])
	}
}

func TestToolHandlerWritesStatusArtifactToPipelineArtifactDir(t *testing.T) {
	workdir := t.TempDir()
	artifactRoot := filepath.Join(t.TempDir(), "runs", "run-123")
	env := toolTestEnv(t, map[string]exec.CommandResult{
		"echo hello": {Stdout: "hello\n", ExitCode: 0},
	})
	if m, ok := env.(*mockExecEnv); ok {
		m.workdir = workdir
	}
	h := NewToolHandler(env)
	node := &pipeline.Node{
		ID:    "toolstep",
		Shape: "parallelogram",
		Attrs: map[string]string{"tool_command": "echo hello"},
	}
	pctx := pipeline.NewPipelineContext()
	pctx.SetInternal(pipeline.InternalKeyArtifactDir, artifactRoot)

	outcome, err := h.Execute(context.Background(), node, pctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Status != pipeline.OutcomeSuccess {
		t.Fatalf("expected success, got %q", outcome.Status)
	}

	statusPath := filepath.Join(artifactRoot, "toolstep", "status.json")
	statusBytes, err := os.ReadFile(statusPath)
	if err != nil {
		t.Fatalf("expected status artifact in pipeline artifact dir: %v", err)
	}
	var status map[string]any
	if err := json.Unmarshal(statusBytes, &status); err != nil {
		t.Fatalf("status artifact should be valid json: %v", err)
	}
	if status["outcome"] != pipeline.OutcomeSuccess {
		t.Fatalf("status outcome = %v", status["outcome"])
	}

	if _, err := os.Stat(filepath.Join(workdir, "toolstep", "status.json")); !os.IsNotExist(err) {
		t.Fatalf("expected no fallback artifact in workdir, got err=%v", err)
	}
}

func TestBuildToolEnv_StripsAPIKeys(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-secret")
	t.Setenv("OPENAI_API_KEY", "sk-openai")
	t.Setenv("MY_CUSTOM_TOKEN", "tok-123")
	t.Setenv("DATABASE_PASSWORD", "dbpass")
	t.Setenv("SAFE_VAR", "keep-me")
	t.Setenv("TRACKER_PASS_ENV", "")

	env := buildToolEnv()
	envMap := make(map[string]string)
	for _, e := range env {
		parts := strings.SplitN(e, "=", 2)
		if len(parts) == 2 {
			envMap[parts[0]] = parts[1]
		}
	}

	if _, ok := envMap["ANTHROPIC_API_KEY"]; ok {
		t.Error("ANTHROPIC_API_KEY should be stripped")
	}
	if _, ok := envMap["OPENAI_API_KEY"]; ok {
		t.Error("OPENAI_API_KEY should be stripped")
	}
	if _, ok := envMap["MY_CUSTOM_TOKEN"]; ok {
		t.Error("MY_CUSTOM_TOKEN should be stripped")
	}
	if _, ok := envMap["DATABASE_PASSWORD"]; ok {
		t.Error("DATABASE_PASSWORD should be stripped")
	}
	if v, ok := envMap["SAFE_VAR"]; !ok || v != "keep-me" {
		t.Error("SAFE_VAR should be preserved")
	}
}

func TestBuildToolEnv_PassEnvOverride(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-secret")
	t.Setenv("TRACKER_PASS_ENV", "1")

	env := buildToolEnv()
	envMap := make(map[string]string)
	for _, e := range env {
		parts := strings.SplitN(e, "=", 2)
		if len(parts) == 2 {
			envMap[parts[0]] = parts[1]
		}
	}

	if _, ok := envMap["ANTHROPIC_API_KEY"]; !ok {
		t.Error("TRACKER_PASS_ENV=1 should preserve API keys")
	}
}

func TestToolHandlerTrimsStdout(t *testing.T) {
	env := toolTestEnv(t, map[string]exec.CommandResult{
		"printf '  validation-pass  \n\n'": {Stdout: "  validation-pass  \n\n", ExitCode: 0},
	})
	h := NewToolHandler(env)
	// printf adds no newline, but echo and other commands do.
	// Only trailing whitespace should be trimmed; leading whitespace is preserved.
	node := &pipeline.Node{
		ID:    "trim",
		Shape: "parallelogram",
		Attrs: map[string]string{"tool_command": "printf '  validation-pass  \n\n'"},
	}
	pctx := pipeline.NewPipelineContext()

	outcome, err := h.Execute(context.Background(), node, pctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	stdout := outcome.ContextUpdates[pipeline.ContextKeyToolStdout]
	if stdout != "  validation-pass" {
		t.Errorf("expected right-trimmed stdout %q, got %q", "  validation-pass", stdout)
	}
}
