// ABOUTME: Tests for the ActivityTracker middleware.
package llm

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestActivityTrackerCapturesModel(t *testing.T) {
	var captured ActivityEvent
	tracker := NewActivityTracker(func(evt ActivityEvent) {
		captured = evt
	})

	handler := tracker.WrapComplete(func(_ context.Context, req *Request) (*Response, error) {
		return &Response{
			Provider: "anthropic",
			Usage:    Usage{InputTokens: 100, OutputTokens: 50},
			Message:  Message{Role: RoleAssistant, Content: []ContentPart{{Kind: KindText, Text: "hello"}}},
		}, nil
	})

	_, _ = handler(context.Background(), &Request{Model: "claude-sonnet-4-20250514", Provider: "anthropic"})

	if captured.Model != "claude-sonnet-4-20250514" {
		t.Errorf("expected model 'claude-sonnet-4-20250514', got %q", captured.Model)
	}
	if captured.Provider != "anthropic" {
		t.Errorf("expected provider 'anthropic', got %q", captured.Provider)
	}
	if captured.InputTokens != 100 {
		t.Errorf("expected 100 input tokens, got %d", captured.InputTokens)
	}
	if captured.OutputTokens != 50 {
		t.Errorf("expected 50 output tokens, got %d", captured.OutputTokens)
	}
}

func TestActivityTrackerCapturesToolCalls(t *testing.T) {
	var captured ActivityEvent
	tracker := NewActivityTracker(func(evt ActivityEvent) {
		captured = evt
	})

	handler := tracker.WrapComplete(func(_ context.Context, req *Request) (*Response, error) {
		return &Response{
			Provider: "openai",
			Message: Message{
				Role: RoleAssistant,
				Content: []ContentPart{
					{
						Kind: KindToolCall,
						ToolCall: &ToolCallData{
							ID:        "call_1",
							Name:      "read_file",
							Arguments: json.RawMessage(`{"path":"main.go"}`),
						},
					},
					{
						Kind: KindToolCall,
						ToolCall: &ToolCallData{
							ID:        "call_2",
							Name:      "write_file",
							Arguments: json.RawMessage(`{"path":"out.go"}`),
						},
					},
				},
			},
		}, nil
	})

	_, _ = handler(context.Background(), &Request{Model: "gpt-4o"})

	if len(captured.ToolCalls) != 2 {
		t.Fatalf("expected 2 tool calls, got %d", len(captured.ToolCalls))
	}
	if captured.ToolCalls[0] != "read_file" {
		t.Errorf("expected tool call 'read_file', got %q", captured.ToolCalls[0])
	}
	if captured.ToolCalls[1] != "write_file" {
		t.Errorf("expected tool call 'write_file', got %q", captured.ToolCalls[1])
	}
}

func TestActivityTrackerCapturesError(t *testing.T) {
	var captured ActivityEvent
	tracker := NewActivityTracker(func(evt ActivityEvent) {
		captured = evt
	})

	expectedErr := errors.New("rate limited")
	handler := tracker.WrapComplete(func(_ context.Context, req *Request) (*Response, error) {
		return nil, expectedErr
	})

	_, _ = handler(context.Background(), &Request{Model: "gpt-4o"})

	if captured.Err == nil {
		t.Fatal("expected error in activity event")
	}
	if captured.Err.Error() != "rate limited" {
		t.Errorf("expected 'rate limited', got %q", captured.Err.Error())
	}
}

func TestActivityTrackerSnippetTruncation(t *testing.T) {
	var captured ActivityEvent
	tracker := NewActivityTracker(func(evt ActivityEvent) {
		captured = evt
	})

	longText := "This is a very long response text that should be truncated to a reasonable length for display in the activity feed. It continues on and on."
	handler := tracker.WrapComplete(func(_ context.Context, req *Request) (*Response, error) {
		return &Response{
			Message: Message{Role: RoleAssistant, Content: []ContentPart{{Kind: KindText, Text: longText}}},
		}, nil
	})

	_, _ = handler(context.Background(), &Request{Model: "test"})

	if len(captured.ResponseSnippet) > 80 {
		t.Errorf("expected snippet <= 80 chars, got %d", len(captured.ResponseSnippet))
	}
}

func TestActivityEventSummaryWithToolCalls(t *testing.T) {
	evt := ActivityEvent{
		Model:     "gpt-4o",
		Provider:  "openai",
		ToolCalls: []string{"read_file", "write_file"},
	}
	summary := evt.Summary()
	if summary == "" {
		t.Error("expected non-empty summary")
	}
}

func TestActivityEventSummaryWithError(t *testing.T) {
	evt := ActivityEvent{
		Model: "gpt-4o",
		Err:   errors.New("timeout"),
	}
	summary := evt.Summary()
	if summary == "" {
		t.Error("expected non-empty summary")
	}
}
