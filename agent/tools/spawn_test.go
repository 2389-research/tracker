// ABOUTME: Tests for the SpawnAgentTool that delegates subtasks to child sessions.
// ABOUTME: Validates parameter parsing, defaults, error propagation, and context cancellation.
package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// mockRunner implements SessionRunner for testing spawn_agent tool behavior.
type mockRunner struct {
	lastTask   string
	lastPrompt string
	lastTurns  int
	result     string
	err        error
}

func (m *mockRunner) RunChild(ctx context.Context, task, prompt string, turns int) (string, error) {
	m.lastTask = task
	m.lastPrompt = prompt
	m.lastTurns = turns
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return m.result, m.err
}

func TestSpawnAgentTool_Name(t *testing.T) {
	tool := NewSpawnAgentTool(&mockRunner{})
	if tool.Name() != "spawn_agent" {
		t.Errorf("expected name 'spawn_agent', got %q", tool.Name())
	}
}

func TestSpawnAgentTool_Parameters(t *testing.T) {
	tool := NewSpawnAgentTool(&mockRunner{})
	raw := tool.Parameters()

	var schema map[string]any
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatalf("failed to parse parameters JSON: %v", err)
	}

	if schema["type"] != "object" {
		t.Errorf("expected type 'object', got %v", schema["type"])
	}

	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatal("expected properties to be an object")
	}

	for _, field := range []string{"task", "system_prompt", "max_turns"} {
		if _, exists := props[field]; !exists {
			t.Errorf("expected property %q in schema", field)
		}
	}

	required, ok := schema["required"].([]any)
	if !ok {
		t.Fatal("expected required to be an array")
	}
	foundTask := false
	for _, r := range required {
		if r == "task" {
			foundTask = true
		}
	}
	if !foundTask {
		t.Error("expected 'task' to be in required fields")
	}
}

func TestSpawnAgentTool_Execute(t *testing.T) {
	runner := &mockRunner{result: "child completed successfully"}
	tool := NewSpawnAgentTool(runner)

	input := json.RawMessage(`{"task": "summarize the code", "system_prompt": "be concise", "max_turns": 5}`)
	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result != "child completed successfully" {
		t.Errorf("expected 'child completed successfully', got %q", result)
	}
	if runner.lastTask != "summarize the code" {
		t.Errorf("expected task 'summarize the code', got %q", runner.lastTask)
	}
	if runner.lastPrompt != "be concise" {
		t.Errorf("expected prompt 'be concise', got %q", runner.lastPrompt)
	}
	if runner.lastTurns != 5 {
		t.Errorf("expected turns 5, got %d", runner.lastTurns)
	}
}

func TestSpawnAgentTool_MaxTurnsDefault(t *testing.T) {
	runner := &mockRunner{result: "ok"}
	tool := NewSpawnAgentTool(runner)

	// No max_turns provided — should default to 10.
	input := json.RawMessage(`{"task": "do something"}`)
	_, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if runner.lastTurns != 10 {
		t.Errorf("expected default max_turns 10, got %d", runner.lastTurns)
	}
}

func TestSpawnAgentTool_MaxTurnsZeroDefaultsToTen(t *testing.T) {
	runner := &mockRunner{result: "ok"}
	tool := NewSpawnAgentTool(runner)

	// max_turns set to 0 should also default to 10.
	input := json.RawMessage(`{"task": "do something", "max_turns": 0}`)
	_, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if runner.lastTurns != 10 {
		t.Errorf("expected default max_turns 10 for zero value, got %d", runner.lastTurns)
	}
}

func TestSpawnAgentTool_ErrorPropagation(t *testing.T) {
	runner := &mockRunner{err: errors.New("child session failed")}
	tool := NewSpawnAgentTool(runner)

	input := json.RawMessage(`{"task": "fail please"}`)
	_, err := tool.Execute(context.Background(), input)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "child session failed") {
		t.Errorf("expected error to contain 'child session failed', got %q", err.Error())
	}
}

func TestSpawnAgentTool_EmptyTask(t *testing.T) {
	runner := &mockRunner{result: "should not reach"}
	tool := NewSpawnAgentTool(runner)

	input := json.RawMessage(`{"task": ""}`)
	_, err := tool.Execute(context.Background(), input)
	if err == nil {
		t.Fatal("expected error for empty task")
	}
	if !strings.Contains(err.Error(), "task is required") {
		t.Errorf("expected 'task is required' error, got %q", err.Error())
	}
}

func TestSpawnAgentTool_ContextCancellation(t *testing.T) {
	runner := &mockRunner{result: "should not reach"}
	tool := NewSpawnAgentTool(runner)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	input := json.RawMessage(`{"task": "do something"}`)
	_, err := tool.Execute(ctx, input)
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
}

func TestSpawnAgentTool_InvalidJSON(t *testing.T) {
	runner := &mockRunner{result: "should not reach"}
	tool := NewSpawnAgentTool(runner)

	input := json.RawMessage(`{bad json}`)
	_, err := tool.Execute(context.Background(), input)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestSpawnAgentTool_Description(t *testing.T) {
	tool := NewSpawnAgentTool(&mockRunner{})
	desc := tool.Description()
	if desc == "" {
		t.Error("expected non-empty description")
	}
	if !strings.Contains(strings.ToLower(desc), "child") || !strings.Contains(strings.ToLower(desc), "agent") {
		t.Errorf("description should mention child agent: %q", desc)
	}
}
