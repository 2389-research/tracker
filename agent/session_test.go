// ABOUTME: Tests for the agent Session and agentic loop.
// ABOUTME: Uses mock LLM client to validate turn execution, tool dispatch, and event emission.
package agent

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/2389-research/tracker/agent/tools"
	"github.com/2389-research/tracker/llm"
)

// stubTool is a minimal Tool implementation for unit tests that returns a fixed output.
type stubTool struct {
	name   string
	output string
}

func (s *stubTool) Name() string        { return s.name }
func (s *stubTool) Description() string { return "stub tool for testing" }
func (s *stubTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{}}`)
}
func (s *stubTool) Execute(_ context.Context, _ json.RawMessage) (string, error) {
	return s.output, nil
}

// Compile-time check that stubTool implements tools.Tool.
var _ tools.Tool = (*stubTool)(nil)

// mockCompleter is a mock llm.Client for testing the agentic loop.
type mockCompleter struct {
	responses []*llm.Response
	calls     int
}

func (m *mockCompleter) Complete(ctx context.Context, req *llm.Request) (*llm.Response, error) {
	if m.calls >= len(m.responses) {
		return &llm.Response{
			Message:      llm.AssistantMessage("done"),
			FinishReason: llm.FinishReason{Reason: "stop"},
		}, nil
	}
	resp := m.responses[m.calls]
	m.calls++
	return resp, nil
}

// mustNewSession creates a session and fails the test if config is invalid.
func mustNewSession(t *testing.T, client Completer, cfg SessionConfig, opts ...SessionOption) *Session {
	t.Helper()
	sess, err := NewSession(client, cfg, opts...)
	if err != nil {
		t.Fatalf("NewSession failed: %v", err)
	}
	return sess
}

func TestSessionTextOnlyResponse(t *testing.T) {
	client := &mockCompleter{
		responses: []*llm.Response{
			{
				Message:      llm.AssistantMessage("Hello, I can help!"),
				FinishReason: llm.FinishReason{Reason: "stop"},
				Usage:        llm.Usage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15},
			},
		},
	}

	cfg := DefaultConfig()
	cfg.SystemPrompt = "You are a helpful assistant."

	sess := mustNewSession(t, client, cfg)
	result, err := sess.Run(context.Background(), "Say hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Turns != 1 {
		t.Errorf("expected 1 turn, got %d", result.Turns)
	}
	if result.TotalToolCalls() != 0 {
		t.Errorf("expected 0 tool calls, got %d", result.TotalToolCalls())
	}
	if result.MaxTurnsUsed {
		t.Error("expected MaxTurnsUsed to be false for normal completion")
	}
}

func TestSessionToolCallLoop(t *testing.T) {
	toolCallResp := &llm.Response{
		Message: llm.Message{
			Role: llm.RoleAssistant,
			Content: []llm.ContentPart{
				{
					Kind: llm.KindToolCall,
					ToolCall: &llm.ToolCallData{
						ID:        "call_1",
						Name:      "read",
						Arguments: json.RawMessage(`{"path": "test.txt"}`),
					},
				},
			},
		},
		FinishReason: llm.FinishReason{Reason: "tool_calls"},
		Usage:        llm.Usage{InputTokens: 20, OutputTokens: 10, TotalTokens: 30},
	}

	textResp := &llm.Response{
		Message:      llm.AssistantMessage("I read the file."),
		FinishReason: llm.FinishReason{Reason: "stop"},
		Usage:        llm.Usage{InputTokens: 30, OutputTokens: 8, TotalTokens: 38},
	}

	client := &mockCompleter{
		responses: []*llm.Response{toolCallResp, textResp},
	}

	cfg := DefaultConfig()
	readTool := &stubTool{name: "read", output: "file contents here"}
	sess := mustNewSession(t, client, cfg, WithTools(readTool))

	result, err := sess.Run(context.Background(), "Read test.txt")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Turns != 2 {
		t.Errorf("expected 2 turns, got %d", result.Turns)
	}
	if result.ToolCalls["read"] != 1 {
		t.Errorf("expected 1 read call, got %d", result.ToolCalls["read"])
	}
}

func TestSessionMaxTurns(t *testing.T) {
	toolCallResp := &llm.Response{
		Message: llm.Message{
			Role: llm.RoleAssistant,
			Content: []llm.ContentPart{
				{
					Kind: llm.KindToolCall,
					ToolCall: &llm.ToolCallData{
						ID:        "call_1",
						Name:      "read",
						Arguments: json.RawMessage(`{"path": "test.txt"}`),
					},
				},
			},
		},
		FinishReason: llm.FinishReason{Reason: "tool_calls"},
	}

	responses := make([]*llm.Response, 100)
	for i := range responses {
		responses[i] = toolCallResp
	}

	client := &mockCompleter{responses: responses}

	cfg := DefaultConfig()
	cfg.MaxTurns = 3
	readTool := &stubTool{name: "read", output: "stub"}
	sess := mustNewSession(t, client, cfg, WithTools(readTool))

	result, err := sess.Run(context.Background(), "Loop forever")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Turns != 3 {
		t.Errorf("expected 3 turns (max), got %d", result.Turns)
	}
	if !result.MaxTurnsUsed {
		t.Error("expected MaxTurnsUsed to be true")
	}
}

func TestSessionEventEmission(t *testing.T) {
	client := &mockCompleter{
		responses: []*llm.Response{
			{
				Message:      llm.AssistantMessage("Hi"),
				FinishReason: llm.FinishReason{Reason: "stop"},
			},
		},
	}

	var events []Event
	handler := EventHandlerFunc(func(evt Event) {
		events = append(events, evt)
	})

	cfg := DefaultConfig()
	sess := mustNewSession(t, client, cfg, WithEventHandler(handler))
	_, err := sess.Run(context.Background(), "Hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	typeSet := make(map[EventType]bool)
	for _, e := range events {
		typeSet[e.Type] = true
	}
	for _, expected := range []EventType{EventSessionStart, EventTurnStart, EventTurnEnd, EventSessionEnd} {
		if !typeSet[expected] {
			t.Errorf("missing event type: %s", expected)
		}
	}
}

func TestSessionContextCancellation(t *testing.T) {
	client := &mockCompleter{
		responses: []*llm.Response{
			{
				Message:      llm.AssistantMessage("will not reach"),
				FinishReason: llm.FinishReason{Reason: "stop"},
			},
		},
	}

	cfg := DefaultConfig()
	sess := mustNewSession(t, client, cfg)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := sess.Run(ctx, "Hello")
	if err == nil {
		t.Error("expected error from cancelled context")
	}
}

func TestSessionDoubleRunErrors(t *testing.T) {
	client := &mockCompleter{
		responses: []*llm.Response{
			{
				Message:      llm.AssistantMessage("Hi"),
				FinishReason: llm.FinishReason{Reason: "stop"},
			},
		},
	}

	cfg := DefaultConfig()
	sess := mustNewSession(t, client, cfg)

	_, err := sess.Run(context.Background(), "First")
	if err != nil {
		t.Fatalf("first Run failed: %v", err)
	}

	_, err = sess.Run(context.Background(), "Second")
	if err == nil {
		t.Error("expected error on second Run call")
	}
}

func TestNewSessionInvalidConfig(t *testing.T) {
	client := &mockCompleter{}
	cfg := SessionConfig{MaxTurns: 0}

	_, err := NewSession(client, cfg)
	if err == nil {
		t.Error("expected error for invalid config")
	}
}

func TestSessionDurationIsSet(t *testing.T) {
	client := &mockCompleter{
		responses: []*llm.Response{
			{
				Message:      llm.AssistantMessage("Hi"),
				FinishReason: llm.FinishReason{Reason: "stop"},
			},
		},
	}

	cfg := DefaultConfig()
	sess := mustNewSession(t, client, cfg)
	result, err := sess.Run(context.Background(), "Hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Duration == 0 {
		t.Error("expected Duration to be non-zero")
	}
}

func TestSessionNaturalStopOnMaxTurn(t *testing.T) {
	// Model stops with text on the very last allowed turn — should NOT set MaxTurnsUsed.
	client := &mockCompleter{
		responses: []*llm.Response{
			{
				Message:      llm.AssistantMessage("Done."),
				FinishReason: llm.FinishReason{Reason: "stop"},
			},
		},
	}

	cfg := DefaultConfig()
	cfg.MaxTurns = 1
	sess := mustNewSession(t, client, cfg)

	result, err := sess.Run(context.Background(), "Quick task")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.MaxTurnsUsed {
		t.Error("expected MaxTurnsUsed to be false when model stops naturally on final turn")
	}
}
