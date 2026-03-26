// ABOUTME: Tests for NativeBackend which wraps agent.Session behind the AgentBackend interface.
// ABOUTME: Verifies event emission, result fields, and config propagation using a fake completer.
package handlers

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/2389-research/tracker/agent"
	agentexec "github.com/2389-research/tracker/agent/exec"
	"github.com/2389-research/tracker/llm"
	"github.com/2389-research/tracker/pipeline"
)

func TestNativeBackendEmitsEvents(t *testing.T) {
	client := &fakeCompleter{responseText: "hello"}
	env := agentexec.NewLocalEnvironment(t.TempDir())
	backend := NewNativeBackend(client, env)

	cfg := pipeline.AgentRunConfig{
		Prompt: "say hello",
		Model:  "claude-sonnet-4-5",
	}

	var mu sync.Mutex
	var events []agent.Event
	emit := func(evt agent.Event) {
		mu.Lock()
		events = append(events, evt)
		mu.Unlock()
	}

	_, err := backend.Run(context.Background(), cfg, emit)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	if len(events) == 0 {
		t.Fatal("expected at least one event, got none")
	}

	// Should have session_start and session_end at minimum.
	var hasStart, hasEnd bool
	for _, evt := range events {
		if evt.Type == agent.EventSessionStart {
			hasStart = true
		}
		if evt.Type == agent.EventSessionEnd {
			hasEnd = true
		}
	}
	if !hasStart {
		t.Error("expected session_start event")
	}
	if !hasEnd {
		t.Error("expected session_end event")
	}
}

func TestNativeBackendReturnsResult(t *testing.T) {
	client := &fakeCompleter{responseText: "done"}
	env := agentexec.NewLocalEnvironment(t.TempDir())
	backend := NewNativeBackend(client, env)

	cfg := pipeline.AgentRunConfig{
		Prompt: "do something",
		Model:  "claude-sonnet-4-5",
	}

	result, err := backend.Run(context.Background(), cfg, func(agent.Event) {})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.SessionID == "" {
		t.Error("expected non-empty SessionID")
	}
	if result.Turns < 1 {
		t.Errorf("expected at least 1 turn, got %d", result.Turns)
	}
	if result.Duration == 0 {
		t.Error("expected non-zero duration")
	}
	if result.Usage.InputTokens == 0 {
		t.Error("expected non-zero input tokens")
	}
}

func TestNativeBackendRespectsConfig(t *testing.T) {
	// Use a scripted completer that records the request to verify model is passed through.
	var capturedModel string
	client := &modelCapturingCompleter{
		responseText: "ok",
		onComplete: func(req *llm.Request) {
			capturedModel = req.Model
		},
	}
	env := agentexec.NewLocalEnvironment(t.TempDir())
	backend := NewNativeBackend(client, env)

	cfg := pipeline.AgentRunConfig{
		Prompt:       "test config",
		Model:        "gpt-4o",
		Provider:     "openai",
		MaxTurns:     3,
		SystemPrompt: "be concise",
		WorkingDir:   t.TempDir(),
		Timeout:      5 * time.Minute,
	}

	_, err := backend.Run(context.Background(), cfg, func(agent.Event) {})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if capturedModel != "gpt-4o" {
		t.Errorf("expected model 'gpt-4o', got %q", capturedModel)
	}
}

func TestNativeBackendUsesDefaults(t *testing.T) {
	// When config fields are zero-valued, NativeBackend should fall back to defaults.
	client := &fakeCompleter{responseText: "ok"}
	env := agentexec.NewLocalEnvironment(t.TempDir())
	backend := NewNativeBackend(client, env)

	cfg := pipeline.AgentRunConfig{
		Prompt: "use defaults",
	}

	result, err := backend.Run(context.Background(), cfg, func(agent.Event) {})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.SessionID == "" {
		t.Error("expected session to run with defaults")
	}
}

// modelCapturingCompleter records the model from the request for verification.
type modelCapturingCompleter struct {
	responseText string
	onComplete   func(req *llm.Request)
}

func (c *modelCapturingCompleter) Complete(ctx context.Context, req *llm.Request) (*llm.Response, error) {
	if c.onComplete != nil {
		c.onComplete(req)
	}
	return &llm.Response{
		Message:      llm.AssistantMessage(c.responseText),
		FinishReason: llm.FinishReason{Reason: "stop"},
		Usage:        llm.Usage{InputTokens: 10, OutputTokens: 20, TotalTokens: 30},
	}, nil
}
