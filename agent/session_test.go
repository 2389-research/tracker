// ABOUTME: Tests for the agent Session and agentic loop.
// ABOUTME: Uses mock LLM client to validate turn execution, tool dispatch, and event emission.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	responses  []*llm.Response
	calls      int
	onComplete func(req *llm.Request)
}

func (m *mockCompleter) Complete(ctx context.Context, req *llm.Request) (*llm.Response, error) {
	if m.onComplete != nil {
		m.onComplete(req)
	}
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

func TestSessionPlanBeforeExecute_InjectsPlanningTurn(t *testing.T) {
	var requests []llm.Request
	client := &mockCompleter{
		responses: []*llm.Response{
			{
				Message:      llm.AssistantMessage("Plan: inspect files, then edit and test."),
				FinishReason: llm.FinishReason{Reason: "stop"},
				Usage:        llm.Usage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15},
			},
			{
				Message: llm.Message{
					Role: llm.RoleAssistant,
					Content: []llm.ContentPart{
						{
							Kind: llm.KindToolCall,
							ToolCall: &llm.ToolCallData{
								ID:        "call_1",
								Name:      "read",
								Arguments: json.RawMessage(`{"path":"x.txt"}`),
							},
						},
					},
				},
				FinishReason: llm.FinishReason{Reason: "tool_calls"},
				Usage:        llm.Usage{InputTokens: 20, OutputTokens: 10, TotalTokens: 30},
			},
		},
		onComplete: func(req *llm.Request) {
			copied := *req
			copied.Messages = append([]llm.Message(nil), req.Messages...)
			requests = append(requests, copied)
		},
	}

	cfg := DefaultConfig()
	cfg.PlanBeforeExecute = true
	cfg.MaxTurns = 1
	sess := mustNewSession(t, client, cfg, WithTools(&stubTool{name: "read", output: "ok"}))

	_, err := sess.Run(context.Background(), "Fix the bug")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(requests) != 2 {
		t.Fatalf("expected 2 LLM requests (plan + execute), got %d", len(requests))
	}
	if len(requests[0].Tools) != 0 {
		t.Fatalf("planning turn should not expose tools, got %d", len(requests[0].Tools))
	}
	if len(requests[0].Messages) == 0 {
		t.Fatal("planning turn request should include messages")
	}
	firstUserText := requests[0].Messages[len(requests[0].Messages)-1].Text()
	if firstUserText != planBeforeExecutePrompt {
		t.Fatalf("planning turn prompt mismatch: got %q", firstUserText)
	}

	if len(requests[1].Tools) == 0 {
		t.Fatal("execution turn should include tools")
	}
	foundPlan := false
	foundExecutePrompt := false
	for _, msg := range requests[1].Messages {
		if msg.Role == llm.RoleAssistant && msg.Text() == "Plan: inspect files, then edit and test." {
			foundPlan = true
		}
		if msg.Role == llm.RoleUser && msg.Text() == executeAfterPlanPrompt {
			foundExecutePrompt = true
		}
	}
	if !foundPlan {
		t.Fatal("expected plan to remain in conversation context for execution turn")
	}
	if !foundExecutePrompt {
		t.Fatal("expected execute-after-plan prompt in execution turn context")
	}
}

func TestSessionPlanBeforeExecute_Disabled_NoPlanningTurn(t *testing.T) {
	var requests []llm.Request
	client := &mockCompleter{
		responses: []*llm.Response{
			{
				Message:      llm.AssistantMessage("done"),
				FinishReason: llm.FinishReason{Reason: "stop"},
			},
		},
		onComplete: func(req *llm.Request) {
			copied := *req
			copied.Messages = append([]llm.Message(nil), req.Messages...)
			requests = append(requests, copied)
		},
	}

	cfg := DefaultConfig()
	cfg.MaxTurns = 1
	sess := mustNewSession(t, client, cfg, WithTools(&stubTool{name: "read", output: "ok"}))

	_, err := sess.Run(context.Background(), "Fix the bug")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(requests) != 1 {
		t.Fatalf("expected 1 LLM request when planning is disabled, got %d", len(requests))
	}
	if len(requests[0].Tools) == 0 {
		t.Fatal("expected tools on normal execution turn")
	}
	for _, msg := range requests[0].Messages {
		text := msg.Text()
		if strings.Contains(text, planBeforeExecutePrompt) || strings.Contains(text, executeAfterPlanPrompt) {
			t.Fatalf("unexpected planning message when disabled: %q", text)
		}
	}
}

func TestSessionRunPopulatesProvider(t *testing.T) {
	client := &mockCompleter{
		responses: []*llm.Response{
			{
				Message:      llm.AssistantMessage("Hello"),
				FinishReason: llm.FinishReason{Reason: "stop"},
				Usage:        llm.Usage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15},
			},
		},
	}

	cfg := DefaultConfig()
	cfg.Provider = "openai"
	cfg.MaxTurns = 1

	sess := mustNewSession(t, client, cfg)
	result, err := sess.Run(context.Background(), "hi")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result.Provider != "openai" {
		t.Errorf("expected result.Provider == \"openai\", got %q", result.Provider)
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

func TestSessionEmitsLLMTraceEventsInTurnOrder(t *testing.T) {
	client := &mockCompleter{
		responses: []*llm.Response{
			{
				Message: llm.Message{
					Role: llm.RoleAssistant,
					Content: []llm.ContentPart{
						{
							Kind: llm.KindToolCall,
							ToolCall: &llm.ToolCallData{
								ID:        "call_1",
								Name:      "read",
								Arguments: json.RawMessage(`{"path":"go.mod"}`),
							},
						},
					},
				},
				FinishReason: llm.FinishReason{Reason: "tool_calls"},
			},
			{
				Message:      llm.AssistantMessage("done"),
				FinishReason: llm.FinishReason{Reason: "stop"},
			},
		},
	}
	client.onComplete = func(req *llm.Request) {
		for _, obs := range req.TraceObservers {
			obs.HandleTraceEvent(llm.TraceEvent{
				Kind:     llm.TraceRequestStart,
				Provider: "anthropic",
				Model:    "claude-opus-4-6",
			})
			obs.HandleTraceEvent(llm.TraceEvent{
				Kind:     llm.TraceReasoning,
				Provider: "anthropic",
				Model:    "claude-opus-4-6",
				Preview:  "checking workspace",
			})
			obs.HandleTraceEvent(llm.TraceEvent{
				Kind:     llm.TraceToolPrepare,
				Provider: "anthropic",
				Model:    "claude-opus-4-6",
				ToolName: "read",
				Preview:  `{"path":"go.mod"}`,
			})
		}
	}

	var got []EventType
	handler := EventHandlerFunc(func(evt Event) {
		got = append(got, evt.Type)
	})

	cfg := DefaultConfig()
	sess := mustNewSession(t, client, cfg, WithEventHandler(handler), WithTools(&stubTool{name: "read", output: "ok"}))

	if _, err := sess.Run(context.Background(), "inspect"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	assertContainsInOrder(t, got,
		EventTurnStart,
		EventLLMRequestStart,
		EventLLMReasoning,
		EventLLMToolPrepare,
		EventToolCallStart,
		EventToolCallEnd,
		EventTurnEnd,
	)
}

func assertContainsInOrder(t *testing.T, got []EventType, want ...EventType) {
	t.Helper()

	idx := 0
	for _, evt := range got {
		if idx < len(want) && evt == want[idx] {
			idx++
		}
	}
	if idx != len(want) {
		t.Fatalf("got events %v, want subsequence %v", got, want)
	}
}

// countingReadTool tracks how many times Execute is called.
type countingReadTool struct {
	count *int
}

func (t *countingReadTool) Name() string                { return "read" }
func (t *countingReadTool) Description() string         { return "counting read" }
func (t *countingReadTool) Parameters() json.RawMessage { return json.RawMessage(`{}`) }
func (t *countingReadTool) CachePolicy() tools.CachePolicy {
	return tools.CachePolicyCacheable
}
func (t *countingReadTool) Execute(_ context.Context, _ json.RawMessage) (string, error) {
	*t.count++
	return "file contents", nil
}

// noopMutatingTool is a mutating tool that does nothing.
type noopMutatingTool struct{}

func (t *noopMutatingTool) Name() string                { return "noop_write" }
func (t *noopMutatingTool) Description() string         { return "mutating noop" }
func (t *noopMutatingTool) Parameters() json.RawMessage { return json.RawMessage(`{}`) }
func (t *noopMutatingTool) CachePolicy() tools.CachePolicy {
	return tools.CachePolicyMutating
}
func (t *noopMutatingTool) Execute(_ context.Context, _ json.RawMessage) (string, error) {
	return "wrote", nil
}

// failOnceReadTool fails on first call, succeeds on subsequent calls.
type failOnceReadTool struct {
	count *int
}

func (t *failOnceReadTool) Name() string                { return "read" }
func (t *failOnceReadTool) Description() string         { return "fail-once read" }
func (t *failOnceReadTool) Parameters() json.RawMessage { return json.RawMessage(`{}`) }
func (t *failOnceReadTool) CachePolicy() tools.CachePolicy {
	return tools.CachePolicyCacheable
}
func (t *failOnceReadTool) Execute(_ context.Context, _ json.RawMessage) (string, error) {
	*t.count++
	if *t.count == 1 {
		return "", fmt.Errorf("file not found")
	}
	return "file contents", nil
}

func TestSession_ToolCacheHit(t *testing.T) {
	callCount := 0
	countingTool := &countingReadTool{count: &callCount}

	client := &mockCompleter{
		responses: []*llm.Response{
			{
				Message: llm.Message{
					Role: llm.RoleAssistant,
					Content: []llm.ContentPart{{
						Kind: llm.KindToolCall,
						ToolCall: &llm.ToolCallData{
							ID:        "call_1",
							Name:      "read",
							Arguments: json.RawMessage(`{"path":"main.go"}`),
						},
					}},
				},
				FinishReason: llm.FinishReason{Reason: "tool_calls"},
			},
			{
				Message: llm.Message{
					Role: llm.RoleAssistant,
					Content: []llm.ContentPart{{
						Kind: llm.KindToolCall,
						ToolCall: &llm.ToolCallData{
							ID:        "call_2",
							Name:      "read",
							Arguments: json.RawMessage(`{"path":"main.go"}`),
						},
					}},
				},
				FinishReason: llm.FinishReason{Reason: "tool_calls"},
			},
			{
				Message:      llm.AssistantMessage("done"),
				FinishReason: llm.FinishReason{Reason: "stop"},
			},
		},
	}

	cfg := DefaultConfig()
	cfg.CacheToolResults = true
	cfg.MaxTurns = 10
	sess := mustNewSession(t, client, cfg, WithTools(countingTool))

	_, err := sess.Run(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}

	if callCount != 1 {
		t.Errorf("expected read to execute once (second call cached), got %d", callCount)
	}
}

func TestSession_ToolCacheHitEmitsEvent(t *testing.T) {
	callCount := 0
	countingTool := &countingReadTool{count: &callCount}

	client := &mockCompleter{
		responses: []*llm.Response{
			{
				Message: llm.Message{
					Role: llm.RoleAssistant,
					Content: []llm.ContentPart{{
						Kind: llm.KindToolCall,
						ToolCall: &llm.ToolCallData{
							ID:        "call_1",
							Name:      "read",
							Arguments: json.RawMessage(`{"path":"main.go"}`),
						},
					}},
				},
				FinishReason: llm.FinishReason{Reason: "tool_calls"},
			},
			{
				Message: llm.Message{
					Role: llm.RoleAssistant,
					Content: []llm.ContentPart{{
						Kind: llm.KindToolCall,
						ToolCall: &llm.ToolCallData{
							ID:        "call_2",
							Name:      "read",
							Arguments: json.RawMessage(`{"path":"main.go"}`),
						},
					}},
				},
				FinishReason: llm.FinishReason{Reason: "tool_calls"},
			},
			{
				Message:      llm.AssistantMessage("done"),
				FinishReason: llm.FinishReason{Reason: "stop"},
			},
		},
	}

	var events []Event
	handler := EventHandlerFunc(func(evt Event) {
		events = append(events, evt)
	})

	cfg := DefaultConfig()
	cfg.CacheToolResults = true
	cfg.MaxTurns = 10
	sess := mustNewSession(t, client, cfg, WithTools(countingTool), WithEventHandler(handler))

	_, err := sess.Run(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}

	cacheHitCount := 0
	for _, e := range events {
		if e.Type == EventToolCacheHit {
			cacheHitCount++
			if e.ToolName != "read" {
				t.Errorf("expected cache hit for 'read', got %q", e.ToolName)
			}
		}
	}
	if cacheHitCount != 1 {
		t.Errorf("expected 1 EventToolCacheHit, got %d", cacheHitCount)
	}

	// Verify event ordering: for the cached call, the sequence must be
	// tool_call_start -> tool_cache_hit -> tool_call_end.
	var eventTypes []EventType
	for _, e := range events {
		eventTypes = append(eventTypes, e.Type)
	}
	assertContainsInOrder(t, eventTypes,
		EventToolCallStart, // first read (real execution)
		EventToolCallEnd,
		EventToolCallStart, // second read (cache hit)
		EventToolCacheHit,
		EventToolCallEnd,
	)
}

func TestSession_CacheInvalidatedByMutatingTool(t *testing.T) {
	callCount := 0
	countingTool := &countingReadTool{count: &callCount}
	writeTool := &noopMutatingTool{}

	client := &mockCompleter{
		responses: []*llm.Response{
			{
				Message: llm.Message{
					Role: llm.RoleAssistant,
					Content: []llm.ContentPart{{
						Kind: llm.KindToolCall,
						ToolCall: &llm.ToolCallData{
							ID:        "call_1",
							Name:      "read",
							Arguments: json.RawMessage(`{"path":"main.go"}`),
						},
					}},
				},
				FinishReason: llm.FinishReason{Reason: "tool_calls"},
			},
			{
				Message: llm.Message{
					Role: llm.RoleAssistant,
					Content: []llm.ContentPart{{
						Kind: llm.KindToolCall,
						ToolCall: &llm.ToolCallData{
							ID:        "call_2",
							Name:      "noop_write",
							Arguments: json.RawMessage(`{}`),
						},
					}},
				},
				FinishReason: llm.FinishReason{Reason: "tool_calls"},
			},
			{
				Message: llm.Message{
					Role: llm.RoleAssistant,
					Content: []llm.ContentPart{{
						Kind: llm.KindToolCall,
						ToolCall: &llm.ToolCallData{
							ID:        "call_3",
							Name:      "read",
							Arguments: json.RawMessage(`{"path":"main.go"}`),
						},
					}},
				},
				FinishReason: llm.FinishReason{Reason: "tool_calls"},
			},
			{
				Message:      llm.AssistantMessage("done"),
				FinishReason: llm.FinishReason{Reason: "stop"},
			},
		},
	}

	cfg := DefaultConfig()
	cfg.CacheToolResults = true
	cfg.MaxTurns = 10
	sess := mustNewSession(t, client, cfg, WithTools(countingTool, writeTool))

	_, err := sess.Run(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}

	if callCount != 2 {
		t.Errorf("expected read to execute twice (invalidated by write), got %d", callCount)
	}
}

func TestSession_NoCacheWhenDisabled(t *testing.T) {
	callCount := 0
	countingTool := &countingReadTool{count: &callCount}

	client := &mockCompleter{
		responses: []*llm.Response{
			{
				Message: llm.Message{
					Role: llm.RoleAssistant,
					Content: []llm.ContentPart{{
						Kind: llm.KindToolCall,
						ToolCall: &llm.ToolCallData{
							ID:        "call_1",
							Name:      "read",
							Arguments: json.RawMessage(`{"path":"main.go"}`),
						},
					}},
				},
				FinishReason: llm.FinishReason{Reason: "tool_calls"},
			},
			{
				Message: llm.Message{
					Role: llm.RoleAssistant,
					Content: []llm.ContentPart{{
						Kind: llm.KindToolCall,
						ToolCall: &llm.ToolCallData{
							ID:        "call_2",
							Name:      "read",
							Arguments: json.RawMessage(`{"path":"main.go"}`),
						},
					}},
				},
				FinishReason: llm.FinishReason{Reason: "tool_calls"},
			},
			{
				Message:      llm.AssistantMessage("done"),
				FinishReason: llm.FinishReason{Reason: "stop"},
			},
		},
	}

	cfg := DefaultConfig()
	cfg.CacheToolResults = false
	cfg.MaxTurns = 10
	sess := mustNewSession(t, client, cfg, WithTools(countingTool))

	_, err := sess.Run(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}

	if callCount != 2 {
		t.Errorf("expected read to execute twice (no cache), got %d", callCount)
	}
}

func TestSession_CacheNotStoredOnError(t *testing.T) {
	callCount := 0
	failOnceTool := &failOnceReadTool{count: &callCount}

	client := &mockCompleter{
		responses: []*llm.Response{
			{
				Message: llm.Message{
					Role: llm.RoleAssistant,
					Content: []llm.ContentPart{{
						Kind: llm.KindToolCall,
						ToolCall: &llm.ToolCallData{
							ID:        "call_1",
							Name:      "read",
							Arguments: json.RawMessage(`{"path":"main.go"}`),
						},
					}},
				},
				FinishReason: llm.FinishReason{Reason: "tool_calls"},
			},
			{
				Message: llm.Message{
					Role: llm.RoleAssistant,
					Content: []llm.ContentPart{{
						Kind: llm.KindToolCall,
						ToolCall: &llm.ToolCallData{
							ID:        "call_2",
							Name:      "read",
							Arguments: json.RawMessage(`{"path":"main.go"}`),
						},
					}},
				},
				FinishReason: llm.FinishReason{Reason: "tool_calls"},
			},
			{
				Message:      llm.AssistantMessage("done"),
				FinishReason: llm.FinishReason{Reason: "stop"},
			},
		},
	}

	cfg := DefaultConfig()
	cfg.CacheToolResults = true
	cfg.MaxTurns = 10
	sess := mustNewSession(t, client, cfg, WithTools(failOnceTool))

	_, err := sess.Run(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}

	if callCount != 2 {
		t.Errorf("expected 2 executions (error result not cached), got %d", callCount)
	}
}

func TestSession_BatchToolCallsWithMidBatchInvalidation(t *testing.T) {
	readCount := 0
	countingTool := &countingReadTool{count: &readCount}
	writeTool := &noopMutatingTool{}

	client := &mockCompleter{
		responses: []*llm.Response{
			{
				Message: llm.Message{
					Role: llm.RoleAssistant,
					Content: []llm.ContentPart{
						{
							Kind: llm.KindToolCall,
							ToolCall: &llm.ToolCallData{
								ID:        "call_1",
								Name:      "read",
								Arguments: json.RawMessage(`{"path":"main.go"}`),
							},
						},
						{
							Kind: llm.KindToolCall,
							ToolCall: &llm.ToolCallData{
								ID:        "call_2",
								Name:      "noop_write",
								Arguments: json.RawMessage(`{}`),
							},
						},
						{
							Kind: llm.KindToolCall,
							ToolCall: &llm.ToolCallData{
								ID:        "call_3",
								Name:      "read",
								Arguments: json.RawMessage(`{"path":"main.go"}`),
							},
						},
					},
				},
				FinishReason: llm.FinishReason{Reason: "tool_calls"},
			},
			{
				Message:      llm.AssistantMessage("done"),
				FinishReason: llm.FinishReason{Reason: "stop"},
			},
		},
	}

	cfg := DefaultConfig()
	cfg.CacheToolResults = true
	cfg.MaxTurns = 10
	sess := mustNewSession(t, client, cfg, WithTools(countingTool, writeTool))

	_, err := sess.Run(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}

	if readCount != 2 {
		t.Errorf("expected 2 read executions (mid-batch invalidation), got %d", readCount)
	}
}

func TestSession_UnknownToolInvalidatesCache(t *testing.T) {
	// An unclassified tool (CachePolicyNone) should invalidate the cache
	// as a safe default, since it may have side effects.
	callCount := 0
	countingTool := &countingReadTool{count: &callCount}
	unknownTool := &stubTool{name: "custom_tool", output: "ok"}

	client := &mockCompleter{
		responses: []*llm.Response{
			{
				Message: llm.Message{
					Role: llm.RoleAssistant,
					Content: []llm.ContentPart{{
						Kind: llm.KindToolCall,
						ToolCall: &llm.ToolCallData{
							ID:        "call_1",
							Name:      "read",
							Arguments: json.RawMessage(`{"path":"main.go"}`),
						},
					}},
				},
				FinishReason: llm.FinishReason{Reason: "tool_calls"},
			},
			{
				Message: llm.Message{
					Role: llm.RoleAssistant,
					Content: []llm.ContentPart{{
						Kind: llm.KindToolCall,
						ToolCall: &llm.ToolCallData{
							ID:        "call_2",
							Name:      "custom_tool",
							Arguments: json.RawMessage(`{}`),
						},
					}},
				},
				FinishReason: llm.FinishReason{Reason: "tool_calls"},
			},
			{
				Message: llm.Message{
					Role: llm.RoleAssistant,
					Content: []llm.ContentPart{{
						Kind: llm.KindToolCall,
						ToolCall: &llm.ToolCallData{
							ID:        "call_3",
							Name:      "read",
							Arguments: json.RawMessage(`{"path":"main.go"}`),
						},
					}},
				},
				FinishReason: llm.FinishReason{Reason: "tool_calls"},
			},
			{
				Message:      llm.AssistantMessage("done"),
				FinishReason: llm.FinishReason{Reason: "stop"},
			},
		},
	}

	cfg := DefaultConfig()
	cfg.CacheToolResults = true
	cfg.MaxTurns = 10
	sess := mustNewSession(t, client, cfg, WithTools(countingTool, unknownTool))

	_, err := sess.Run(context.Background(), "test")
	if err != nil {
		t.Fatal(err)
	}

	// read should execute twice: unknown tool invalidates cache as safe default.
	if callCount != 2 {
		t.Errorf("expected read to execute twice (unknown tool invalidated cache), got %d", callCount)
	}
}

func TestSession_CompactsWhenAboveThreshold(t *testing.T) {
	// We need enough turns so that old tool results fall outside the protected window
	// (defaultProtectedTurns=5). We generate 7 tool-call turns plus a final stop,
	// with high utilization from turn 1 onward so compaction fires once old turns
	// become eligible.
	var responses []*llm.Response
	for i := 1; i <= 7; i++ {
		responses = append(responses, &llm.Response{
			Message: llm.Message{
				Role: llm.RoleAssistant,
				Content: []llm.ContentPart{{
					Kind: llm.KindToolCall,
					ToolCall: &llm.ToolCallData{
						ID:        fmt.Sprintf("call_%d", i),
						Name:      "read_file",
						Arguments: json.RawMessage(fmt.Sprintf(`{"path":"file%d.go"}`, i)),
					},
				}},
			},
			FinishReason: llm.FinishReason{Reason: "tool_calls"},
			// Report high utilization so compaction threshold (0.3) is exceeded.
			Usage: llm.Usage{InputTokens: 500, OutputTokens: 50},
		})
	}
	responses = append(responses, &llm.Response{
		Message:      llm.AssistantMessage("Done."),
		FinishReason: llm.FinishReason{Reason: "stop"},
		Usage:        llm.Usage{InputTokens: 600, OutputTokens: 10},
	})

	client := &mockCompleter{responses: responses}
	cfg := DefaultConfig()
	cfg.ContextWindowLimit = 1000
	cfg.ContextCompaction = CompactionAuto
	cfg.CompactionThreshold = 0.3
	cfg.MaxTurns = 20

	var events []Event
	handler := EventHandlerFunc(func(evt Event) {
		events = append(events, evt)
	})

	// Tool output must be longer than the compacted summary so that
	// totalToolResultBytes decreases after compaction.
	longOutput := strings.Repeat("package main // this is a long line of Go source code for testing compaction\n", 20)
	readTool := &stubTool{name: "read_file", output: longOutput}
	sess := mustNewSession(t, client, cfg, WithEventHandler(handler), WithTools(readTool))
	_, err := sess.Run(context.Background(), "Read files")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	compactionEvents := 0
	for _, evt := range events {
		if evt.Type == EventContextCompaction {
			compactionEvents++
		}
	}
	if compactionEvents == 0 {
		t.Error("expected at least one compaction event")
	}
}

func TestSession_NoCompactionWhenDisabled(t *testing.T) {
	responses := []*llm.Response{
		{
			Message: llm.Message{
				Role: llm.RoleAssistant,
				Content: []llm.ContentPart{{
					Kind: llm.KindToolCall,
					ToolCall: &llm.ToolCallData{
						ID: "call_1", Name: "read_file",
						Arguments: json.RawMessage(`{"path":"a.go"}`),
					},
				}},
			},
			FinishReason: llm.FinishReason{Reason: "tool_calls"},
			Usage:        llm.Usage{InputTokens: 900, OutputTokens: 50},
		},
		{
			Message:      llm.AssistantMessage("Done."),
			FinishReason: llm.FinishReason{Reason: "stop"},
			Usage:        llm.Usage{InputTokens: 950, OutputTokens: 10},
		},
	}

	client := &mockCompleter{responses: responses}
	cfg := DefaultConfig()
	cfg.ContextWindowLimit = 1000
	// CompactionNone is default.

	var events []Event
	handler := EventHandlerFunc(func(evt Event) {
		events = append(events, evt)
	})

	readTool := &stubTool{name: "read_file", output: "file content here"}
	sess := mustNewSession(t, client, cfg, WithEventHandler(handler), WithTools(readTool))
	_, err := sess.Run(context.Background(), "Read file")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, evt := range events {
		if evt.Type == EventContextCompaction {
			t.Error("should not emit compaction event when compaction is disabled")
		}
	}
}

// reflectionErrorTool is a local Tool implementation for reflection tests that
// always returns an error.  Defined here to avoid coupling to the errorTool type
// in parity_coding_agent_test.go.
type reflectionErrorTool struct {
	name string
	err  error
}

func (r *reflectionErrorTool) Name() string        { return r.name }
func (r *reflectionErrorTool) Description() string { return "tool that always fails" }
func (r *reflectionErrorTool) Parameters() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{}}`)
}
func (r *reflectionErrorTool) Execute(_ context.Context, _ json.RawMessage) (string, error) {
	return "", r.err
}

// countReflectionMessages counts how many user messages in msgs contain the
// reflection prompt text.
func countReflectionMessages(msgs []llm.Message) int {
	n := 0
	for _, m := range msgs {
		if m.Role != llm.RoleUser {
			continue
		}
		for _, part := range m.Content {
			if strings.Contains(part.Text, "What specifically went wrong") {
				n++
			}
		}
	}
	return n
}

// TestReflectionOnToolError verifies that the reflection prompt is injected as a
// user message after a tool call fails.
func TestReflectionOnToolError(t *testing.T) {
	tool := &reflectionErrorTool{name: "flaky", err: fmt.Errorf("compilation failed")}

	var capturedMessages []llm.Message
	client := &mockCompleter{
		responses: []*llm.Response{
			makeToolCallResponse("flaky"),
		},
		onComplete: func(req *llm.Request) {
			capturedMessages = append(capturedMessages, req.Messages...)
		},
	}

	cfg := DefaultConfig()
	cfg.ReflectOnError = true
	sess := mustNewSession(t, client, cfg, WithTools(tool))
	if _, err := sess.Run(context.Background(), "Run the build"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The second LLM call should see the reflection message in the request messages.
	if countReflectionMessages(capturedMessages) == 0 {
		t.Error("expected at least one reflection user message after tool error, got none")
	}
}

// TestReflectionDisabled verifies that no reflection prompt is injected when
// ReflectOnError is false.
func TestReflectionDisabled(t *testing.T) {
	tool := &reflectionErrorTool{name: "flaky", err: fmt.Errorf("compilation failed")}

	var capturedMessages []llm.Message
	client := &mockCompleter{
		responses: []*llm.Response{
			makeToolCallResponse("flaky"),
		},
		onComplete: func(req *llm.Request) {
			capturedMessages = append(capturedMessages, req.Messages...)
		},
	}

	cfg := DefaultConfig()
	cfg.ReflectOnError = false
	sess := mustNewSession(t, client, cfg, WithTools(tool))
	if _, err := sess.Run(context.Background(), "Run the build"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if countReflectionMessages(capturedMessages) != 0 {
		t.Error("expected no reflection messages when ReflectOnError is false")
	}
}

// TestReflectionCapAtThree verifies that reflection is injected for at most
// maxReflectionTurns consecutive error turns and not on subsequent ones.
func TestReflectionCapAtThree(t *testing.T) {
	tool := &reflectionErrorTool{name: "flaky", err: fmt.Errorf("always fails")}

	// Build 5 tool-call responses followed by a final text response so the
	// session terminates cleanly.  Total LLM calls = 6 (5 tool-call turns +
	// 1 stop turn).
	responses := make([]*llm.Response, 6)
	for i := range 5 {
		responses[i] = makeToolCallResponse("flaky")
	}
	responses[5] = &llm.Response{
		Message:      llm.AssistantMessage("giving up"),
		FinishReason: llm.FinishReason{Reason: "stop"},
	}

	// Capture the messages sent on each LLM call so we can see exactly which
	// calls include the reflection text.
	callMessages := [][]llm.Message{}
	client := &mockCompleter{
		responses: responses,
		onComplete: func(req *llm.Request) {
			snapshot := make([]llm.Message, len(req.Messages))
			copy(snapshot, req.Messages)
			callMessages = append(callMessages, snapshot)
		},
	}

	cfg := DefaultConfig()
	cfg.ReflectOnError = true
	cfg.LoopDetectionThreshold = 10 // higher than default so 5 identical calls don't trigger loop detection
	sess := mustNewSession(t, client, cfg, WithTools(tool))
	if _, err := sess.Run(context.Background(), "Run 5 times"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 5 tool-call responses + 1 stop response = 6 total LLM calls.
	// callMessages[0] is the initial call (before any tool result).
	// callMessages[1] is after first error, callMessages[2] after second, etc.
	if len(callMessages) != 6 {
		t.Fatalf("expected 6 LLM calls (5 tool-call turns + 1 stop), got %d", len(callMessages))
	}

	// Calls 2, 3, 4 (indices 1, 2, 3) should each carry a reflection message.
	for i, msgs := range callMessages[1:4] {
		if countReflectionMessages(msgs) == 0 {
			t.Errorf("call %d: expected reflection message, got none", i+2)
		}
	}

	// After the cap the reflection message count must be exactly maxReflectionTurns
	// (3).  No new reflections should be appended on calls 5 and 6.
	for i, msgs := range callMessages[4:] {
		n := countReflectionMessages(msgs)
		if n != maxReflectionTurns {
			t.Errorf("call %d: expected exactly %d reflection messages after cap, got %d",
				i+5, maxReflectionTurns, n)
		}
	}
}

// TestReflectionResetOnSuccess verifies that the consecutive-reflection counter
// resets after a successful turn so a later failure gets the full quota again.
func TestReflectionResetOnSuccess(t *testing.T) {
	failTool := &reflectionErrorTool{name: "flaky", err: fmt.Errorf("fail")}
	successTool := &stubTool{name: "ok", output: "success output"}

	// Pattern: fail(flaky) → succeed(ok) → fail(flaky) → done
	responses := []*llm.Response{
		// Turn 1: calls flaky (will error → reflection injected)
		{
			Message: llm.Message{
				Role: llm.RoleAssistant,
				Content: []llm.ContentPart{
					{Kind: llm.KindToolCall, ToolCall: &llm.ToolCallData{ID: "c1", Name: "flaky", Arguments: json.RawMessage(`{}`)}},
				},
			},
			FinishReason: llm.FinishReason{Reason: "tool_calls"},
			Usage:        llm.Usage{InputTokens: 10, OutputTokens: 5},
		},
		// Turn 2: calls ok (succeeds → counter resets)
		{
			Message: llm.Message{
				Role: llm.RoleAssistant,
				Content: []llm.ContentPart{
					{Kind: llm.KindToolCall, ToolCall: &llm.ToolCallData{ID: "c2", Name: "ok", Arguments: json.RawMessage(`{}`)}},
				},
			},
			FinishReason: llm.FinishReason{Reason: "tool_calls"},
			Usage:        llm.Usage{InputTokens: 10, OutputTokens: 5},
		},
		// Turn 3: calls flaky again (should trigger reflection again after reset)
		{
			Message: llm.Message{
				Role: llm.RoleAssistant,
				Content: []llm.ContentPart{
					{Kind: llm.KindToolCall, ToolCall: &llm.ToolCallData{ID: "c3", Name: "flaky", Arguments: json.RawMessage(`{}`)}},
				},
			},
			FinishReason: llm.FinishReason{Reason: "tool_calls"},
			Usage:        llm.Usage{InputTokens: 10, OutputTokens: 5},
		},
		{
			Message:      llm.AssistantMessage("done"),
			FinishReason: llm.FinishReason{Reason: "stop"},
		},
	}

	callMessages := [][]llm.Message{}
	client := &mockCompleter{
		responses: responses,
		onComplete: func(req *llm.Request) {
			snapshot := make([]llm.Message, len(req.Messages))
			copy(snapshot, req.Messages)
			callMessages = append(callMessages, snapshot)
		},
	}

	cfg := DefaultConfig()
	cfg.ReflectOnError = true
	sess := mustNewSession(t, client, cfg, WithTools(failTool, successTool))
	if _, err := sess.Run(context.Background(), "test reset"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(callMessages) < 4 {
		t.Fatalf("expected at least 4 LLM calls, got %d", len(callMessages))
	}

	// callMessages[1] = after first flaky error → reflection should appear
	if countReflectionMessages(callMessages[1]) == 0 {
		t.Error("expected reflection after first tool error")
	}
	// callMessages[2] = after successful ok call → no new reflection beyond what was already there
	reflectionsAfterSuccess := countReflectionMessages(callMessages[2])
	reflectionsBeforeReset := countReflectionMessages(callMessages[1])
	if reflectionsAfterSuccess > reflectionsBeforeReset {
		t.Error("reflection count should not increase after a successful turn")
	}
	// callMessages[3] = after second flaky error → counter was reset so reflection fires again
	if countReflectionMessages(callMessages[3]) <= reflectionsAfterSuccess {
		t.Error("expected reflection to be re-injected after counter reset on successful turn")
	}
}

// ---------------------------------------------------------------------------
// Verify-after-edit tests
// ---------------------------------------------------------------------------

// makeEditToolCallResp builds a response that contains a single "write" tool call.
func makeEditToolCallResp(id string) *llm.Response {
	return &llm.Response{
		Message: llm.Message{
			Role: llm.RoleAssistant,
			Content: []llm.ContentPart{
				{
					Kind: llm.KindToolCall,
					ToolCall: &llm.ToolCallData{
						ID:        id,
						Name:      "write",
						Arguments: json.RawMessage(`{"path":"main.go","content":"package main"}`),
					},
				},
			},
		},
		FinishReason: llm.FinishReason{Reason: "tool_calls"},
		Usage:        llm.Usage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15},
	}
}

// makeReadToolCallResp builds a response with a single "read" tool call (non-edit).
func makeReadToolCallResp(id string) *llm.Response {
	return &llm.Response{
		Message: llm.Message{
			Role: llm.RoleAssistant,
			Content: []llm.ContentPart{
				{
					Kind: llm.KindToolCall,
					ToolCall: &llm.ToolCallData{
						ID:        id,
						Name:      "read",
						Arguments: json.RawMessage(`{"path":"main.go"}`),
					},
				},
			},
		},
		FinishReason: llm.FinishReason{Reason: "tool_calls"},
		Usage:        llm.Usage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15},
	}
}

// makeStopResp returns a terminal response (no tool calls).
func makeStopResp(text string) *llm.Response {
	return &llm.Response{
		Message:      llm.AssistantMessage(text),
		FinishReason: llm.FinishReason{Reason: "stop"},
		Usage:        llm.Usage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15},
	}
}

// verifyPassCmd returns a shell one-liner that always exits 0.
func verifyPassCmd() string { return "true" }

// verifyFailCmd returns a shell one-liner that always exits 1 with error output.
func verifyFailCmd() string { return "false" }

// TestVerifyAfterEdit_Disabled ensures no repair prompts are injected when
// VerifyAfterEdit is false, even if edit tools were used.
func TestVerifyAfterEdit_Disabled(t *testing.T) {
	var capturedMessages [][]llm.Message

	client := &mockCompleter{
		responses: []*llm.Response{
			makeEditToolCallResp("call_1"),
			makeStopResp("done"),
		},
		onComplete: func(req *llm.Request) {
			snap := make([]llm.Message, len(req.Messages))
			copy(snap, req.Messages)
			capturedMessages = append(capturedMessages, snap)
		},
	}

	cfg := DefaultConfig()
	cfg.VerifyAfterEdit = false
	cfg.VerifyCommand = verifyFailCmd() // would fail if called

	writeTool := &stubTool{name: "write", output: "wrote"}
	sess := mustNewSession(t, client, cfg, WithTools(writeTool))

	_, err := sess.Run(context.Background(), "write something")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// No verify repair prompt should be present in any message.
	for _, msgs := range capturedMessages {
		for _, m := range msgs {
			if m.Role == llm.RoleUser && strings.Contains(m.Text(), "Verification failed") {
				t.Error("repair prompt should not be injected when VerifyAfterEdit is false")
			}
		}
	}
}

// TestVerifyAfterEdit_NoEdits ensures verification is skipped when the turn
// contains only non-edit tool calls.
func TestVerifyAfterEdit_NoEdits(t *testing.T) {
	var capturedMessages [][]llm.Message

	client := &mockCompleter{
		responses: []*llm.Response{
			makeReadToolCallResp("call_1"),
			makeStopResp("done"),
		},
		onComplete: func(req *llm.Request) {
			snap := make([]llm.Message, len(req.Messages))
			copy(snap, req.Messages)
			capturedMessages = append(capturedMessages, snap)
		},
	}

	cfg := DefaultConfig()
	cfg.VerifyAfterEdit = true
	cfg.VerifyCommand = verifyFailCmd() // would fail if called

	readTool := &stubTool{name: "read", output: "content"}
	sess := mustNewSession(t, client, cfg, WithTools(readTool))

	_, err := sess.Run(context.Background(), "read something")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// No verify repair prompt should appear since no edit tools were used.
	for _, msgs := range capturedMessages {
		for _, m := range msgs {
			if m.Role == llm.RoleUser && strings.Contains(m.Text(), "Verification failed") {
				t.Error("repair prompt should not be injected when no edit tools were used")
			}
		}
	}
}

// TestVerifyAfterEdit_FailedWriteSkipsVerify ensures that verification is NOT
// triggered when the edit tool call itself fails (e.g. permission denied).
// A failed write leaves the workspace unchanged, so running verification would
// test pre-existing failures unrelated to the current turn.
func TestVerifyAfterEdit_FailedWriteSkipsVerify(t *testing.T) {
	var capturedMessages [][]llm.Message

	client := &mockCompleter{
		responses: []*llm.Response{
			makeEditToolCallResp("call_1"),
			makeStopResp("done"),
		},
		onComplete: func(req *llm.Request) {
			snap := make([]llm.Message, len(req.Messages))
			copy(snap, req.Messages)
			capturedMessages = append(capturedMessages, snap)
		},
	}

	cfg := DefaultConfig()
	cfg.VerifyAfterEdit = true
	cfg.VerifyCommand = verifyFailCmd() // would fail if verification is (wrongly) triggered

	// errorTool simulates a write tool that fails (e.g. permission denied).
	failWrite := &errorTool{name: "write", err: fmt.Errorf("permission denied")}
	sess := mustNewSession(t, client, cfg, WithTools(failWrite))

	_, err := sess.Run(context.Background(), "write a file")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// No repair prompt — the write failed so the workspace was not modified.
	for _, msgs := range capturedMessages {
		for _, m := range msgs {
			if m.Role == llm.RoleUser && strings.Contains(m.Text(), "Verification failed") {
				t.Error("repair prompt must not be injected when the edit tool itself failed")
			}
		}
	}
}

// TestVerifyAfterEdit_PassingTest ensures normal completion when verification passes.
func TestVerifyAfterEdit_PassingTest(t *testing.T) {
	var capturedMessages [][]llm.Message

	client := &mockCompleter{
		responses: []*llm.Response{
			makeEditToolCallResp("call_1"),
			makeStopResp("done"),
		},
		onComplete: func(req *llm.Request) {
			snap := make([]llm.Message, len(req.Messages))
			copy(snap, req.Messages)
			capturedMessages = append(capturedMessages, snap)
		},
	}

	cfg := DefaultConfig()
	cfg.VerifyAfterEdit = true
	cfg.VerifyCommand = verifyPassCmd()
	cfg.MaxVerifyRetries = 2

	writeTool := &stubTool{name: "write", output: "wrote"}
	sess := mustNewSession(t, client, cfg, WithTools(writeTool))

	result, err := sess.Run(context.Background(), "write a file")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.MaxTurnsUsed {
		t.Error("expected normal completion, not turn-limit exhaustion")
	}

	// Verify that NO repair prompt was injected.
	for _, msgs := range capturedMessages {
		for _, m := range msgs {
			if m.Role == llm.RoleUser && strings.Contains(m.Text(), "Verification failed") {
				t.Error("repair prompt should not be injected when verification passes")
			}
		}
	}

	// The LLM should only have been called twice: once for the edit turn, once for the stop.
	if client.calls != 2 {
		t.Errorf("expected 2 LLM calls, got %d", client.calls)
	}
}

// TestVerifyAfterEdit_FailingTest_AutoRepair ensures the repair prompt is injected
// when verification fails, and that the LLM gets to respond.
func TestVerifyAfterEdit_FailingTest_AutoRepair(t *testing.T) {
	var capturedMessages [][]llm.Message

	client := &mockCompleter{
		responses: []*llm.Response{
			// Turn 1: LLM writes a file.
			makeEditToolCallResp("call_1"),
			// Repair turn 1: LLM writes a fixed file (edit tool again).
			makeEditToolCallResp("call_repair_1"),
			// Turn 2: LLM stops after the successful verification.
			makeStopResp("done"),
		},
		onComplete: func(req *llm.Request) {
			snap := make([]llm.Message, len(req.Messages))
			copy(snap, req.Messages)
			capturedMessages = append(capturedMessages, snap)
		},
	}

	// Verify command fails on the first call, passes on the second.
	verifyScript := buildCountedVerifyCmd(t, 1 /* fail first N times */)

	cfg := DefaultConfig()
	cfg.VerifyAfterEdit = true
	cfg.VerifyCommand = verifyScript
	cfg.MaxVerifyRetries = 2

	writeTool := &stubTool{name: "write", output: "wrote"}
	sess := mustNewSession(t, client, cfg, WithTools(writeTool))

	_, err := sess.Run(context.Background(), "write a file")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// A repair prompt must have been injected.
	foundRepairPrompt := false
	for _, msgs := range capturedMessages {
		for _, m := range msgs {
			if m.Role == llm.RoleUser && strings.Contains(m.Text(), "Verification failed") {
				foundRepairPrompt = true
			}
		}
	}
	if !foundRepairPrompt {
		t.Error("expected repair prompt to be injected after verification failure")
	}
}

// TestVerifyAfterEdit_MaxRetriesExhausted ensures the session proceeds normally
// after MaxVerifyRetries failures instead of blocking indefinitely.
// The mock includes edit tool calls in repair turns so the verify sub-loop is
// actually triggered (repair turns that only return text bypass the edit check).
func TestVerifyAfterEdit_MaxRetriesExhausted(t *testing.T) {
	client := &mockCompleter{
		responses: []*llm.Response{
			// Turn 1: edit tool call triggers verify loop.
			makeEditToolCallResp("call_1"),
			// Repair turn 1: LLM makes an edit (verify still fails after this).
			makeEditToolCallResp("call_repair_1"),
			// Repair turn 2: LLM makes another edit (verify still fails after this).
			makeEditToolCallResp("call_repair_2"),
			// Main loop continues after retries exhausted.
			makeStopResp("done"),
		},
	}

	cfg := DefaultConfig()
	cfg.VerifyAfterEdit = true
	cfg.VerifyCommand = verifyFailCmd() // always fails
	cfg.MaxVerifyRetries = 2

	writeTool := &stubTool{name: "write", output: "wrote"}
	sess := mustNewSession(t, client, cfg, WithTools(writeTool))

	result, err := sess.Run(context.Background(), "write something")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Session should complete (not be stuck in an infinite loop).
	if result.MaxTurnsUsed {
		t.Error("expected session to complete without hitting MaxTurns")
	}
}

func TestRunRepairTurnEstimatesCostWhenMissing(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Model = "gpt-4.1"
	client := &mockCompleter{
		responses: []*llm.Response{
			{
				Message:      llm.AssistantMessage("fixed"),
				FinishReason: llm.FinishReason{Reason: "stop"},
				Usage: llm.Usage{
					InputTokens:  100,
					OutputTokens: 50,
					TotalTokens:  150,
				},
			},
		},
	}

	sess := mustNewSession(t, client, cfg)
	result := &SessionResult{}
	if err := sess.runRepairTurn(context.Background(), result); err != nil {
		t.Fatalf("runRepairTurn returned error: %v", err)
	}

	if result.Usage.InputTokens != 100 || result.Usage.OutputTokens != 50 {
		t.Fatalf("usage totals wrong: %+v", result.Usage)
	}
	wantCost := llm.EstimateCost(cfg.Model, llm.Usage{InputTokens: 100, OutputTokens: 50, TotalTokens: 150})
	if result.Usage.EstimatedCost != wantCost {
		t.Fatalf("EstimatedCost = %f, want %f", result.Usage.EstimatedCost, wantCost)
	}
}

func TestCheckpointInjection(t *testing.T) {
	// Set up a mock that tracks injected messages.
	var capturedMessages []string
	client := &mockCompleter{
		onComplete: func(req *llm.Request) {
			for _, msg := range req.Messages {
				if msg.Role == llm.RoleUser {
					for _, part := range msg.Content {
						if part.Kind == llm.KindText && strings.Contains(part.Text, "CHECKPOINT") {
							capturedMessages = append(capturedMessages, part.Text)
						}
					}
				}
			}
		},
	}

	// Respond with tool calls for 10 turns, then stop.
	for i := 0; i < 10; i++ {
		client.responses = append(client.responses, &llm.Response{
			Message: llm.Message{
				Role: llm.RoleAssistant,
				Content: []llm.ContentPart{{
					Kind:     llm.KindToolCall,
					ToolCall: &llm.ToolCallData{ID: fmt.Sprintf("tc_%d", i), Name: "stub", Arguments: json.RawMessage(`{}`)},
				}},
			},
			FinishReason: llm.FinishReason{Reason: "tool_use"},
			Usage:        llm.Usage{InputTokens: 100, OutputTokens: 50},
		})
	}

	cfg := DefaultConfig()
	cfg.MaxTurns = 10
	// Raise loop detection above MaxTurns so repeated stub calls don't trigger it.
	cfg.LoopDetectionThreshold = 20
	cfg.Checkpoints = []Checkpoint{
		{Fraction: 0.5, Message: "[CHECKPOINT] halfway there"},
	}

	sess := mustNewSession(t, client, cfg, WithTools(&stubTool{name: "stub", output: "ok"}))
	_, _ = sess.Run(context.Background(), "do work")

	found := false
	for _, msg := range capturedMessages {
		if strings.Contains(msg, "halfway there") {
			found = true
		}
	}
	if !found {
		t.Errorf("checkpoint message not injected; captured: %v", capturedMessages)
	}
}

// buildCountedVerifyCmd creates a shell script in a temp dir that fails for the
// first failCount invocations and succeeds thereafter. Returns the script path.
func buildCountedVerifyCmd(t *testing.T, failCount int) string {
	t.Helper()
	dir := t.TempDir()
	// Use a counter file so successive shell invocations share state.
	counterFile := filepath.Join(dir, "count")
	scriptPath := filepath.Join(dir, "verify.sh")

	script := fmt.Sprintf(`#!/bin/sh
COUNT_FILE="%s"
FAIL_COUNT=%d
count=$(cat "$COUNT_FILE" 2>/dev/null || echo 0)
count=$((count + 1))
echo $count > "$COUNT_FILE"
if [ "$count" -le "$FAIL_COUNT" ]; then
  echo "test failed: attempt $count" >&2
  exit 1
fi
exit 0
`, counterFile, failCount)

	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		t.Fatalf("WriteFile script: %v", err)
	}
	return scriptPath
}
