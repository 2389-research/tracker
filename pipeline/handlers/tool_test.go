// ABOUTME: Tests for the tool handler, verifying shell command execution via ExecutionEnvironment.
// ABOUTME: Covers success, failure, missing command, timeout, and custom timeout scenarios.
package handlers

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/2389-research/tracker/agent/exec"
	"github.com/2389-research/tracker/pipeline"
)

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
	env := exec.NewLocalEnvironment(t.TempDir())
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
	env := exec.NewLocalEnvironment(t.TempDir())
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
	env := exec.NewLocalEnvironment(t.TempDir())
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
	env := exec.NewLocalEnvironment(t.TempDir())
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
	workdir := t.TempDir()
	env := exec.NewLocalEnvironment(workdir)
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
	env := exec.NewLocalEnvironment(workdir)
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

func TestToolHandlerTrimsStdout(t *testing.T) {
	env := exec.NewLocalEnvironment(t.TempDir())
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
