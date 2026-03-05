// ABOUTME: Tests for the message transform middleware.
// ABOUTME: Covers request transforms, response transforms, chaining, nil no-ops, and error handling.
package llm

import (
	"context"
	"errors"
	"testing"
)

func TestTransformMiddleware_Interface(t *testing.T) {
	// Compile-time check that TransformMiddleware satisfies the Middleware interface.
	var _ Middleware = (*TransformMiddleware)(nil)
}

func TestTransformMiddleware_ModifiesRequest(t *testing.T) {
	// Request transform appends a system message; verify it arrives in next().
	var captured *Request
	inner := func(ctx context.Context, req *Request) (*Response, error) {
		captured = req
		return &Response{ID: "ok", Message: AssistantMessage("hi")}, nil
	}

	mw := NewTransformMiddleware(func(req *Request) {
		req.Messages = append(req.Messages, SystemMessage("injected system prompt"))
	})
	handler := mw.WrapComplete(inner)

	original := &Request{
		Model:    "m",
		Messages: []Message{UserMessage("hello")},
	}
	resp, err := handler(context.Background(), original)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.ID != "ok" {
		t.Fatalf("expected response ID 'ok', got %q", resp.ID)
	}
	if captured == nil {
		t.Fatal("inner handler was not called")
	}
	if len(captured.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(captured.Messages))
	}
	if captured.Messages[1].Role != RoleSystem {
		t.Fatalf("expected second message role 'system', got %q", captured.Messages[1].Role)
	}
	if captured.Messages[1].Text() != "injected system prompt" {
		t.Fatalf("expected injected text, got %q", captured.Messages[1].Text())
	}
}

func TestTransformMiddleware_NoOp(t *testing.T) {
	// nil requestTransform passes through unchanged.
	var captured *Request
	inner := func(ctx context.Context, req *Request) (*Response, error) {
		captured = req
		return &Response{ID: "ok", Message: AssistantMessage("hi")}, nil
	}

	mw := NewTransformMiddleware(nil)
	handler := mw.WrapComplete(inner)

	original := &Request{
		Model:    "m",
		Messages: []Message{UserMessage("hello")},
	}
	resp, err := handler(context.Background(), original)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.ID != "ok" {
		t.Fatalf("expected response ID 'ok', got %q", resp.ID)
	}
	if len(captured.Messages) != 1 {
		t.Fatalf("expected 1 message (unchanged), got %d", len(captured.Messages))
	}
	if captured.Messages[0].Text() != "hello" {
		t.Fatalf("expected original message text, got %q", captured.Messages[0].Text())
	}
}

func TestTransformMiddleware_ResponseTransform(t *testing.T) {
	// WithResponseTransform modifies the response message.
	inner := func(ctx context.Context, req *Request) (*Response, error) {
		return &Response{ID: "ok", Message: AssistantMessage("original")}, nil
	}

	mw := NewTransformMiddleware(nil, WithResponseTransform(func(resp *Response) {
		resp.Message = AssistantMessage("transformed")
	}))
	handler := mw.WrapComplete(inner)

	resp, err := handler(context.Background(), &Request{Model: "m", Messages: []Message{UserMessage("hi")}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Text() != "transformed" {
		t.Fatalf("expected response text 'transformed', got %q", resp.Text())
	}
}

func TestTransformMiddleware_ChainMultiple(t *testing.T) {
	// Two TransformMiddleware composed, both apply.
	inner := func(ctx context.Context, req *Request) (*Response, error) {
		return &Response{ID: "ok", Message: AssistantMessage("done")}, nil
	}

	mw1 := NewTransformMiddleware(func(req *Request) {
		req.Messages = append(req.Messages, SystemMessage("first"))
	})
	mw2 := NewTransformMiddleware(func(req *Request) {
		req.Messages = append(req.Messages, SystemMessage("second"))
	})

	// Chain: mw2 wraps mw1 wraps inner
	// Execution order: mw2's transform runs first, then mw1's transform
	handler := mw2.WrapComplete(mw1.WrapComplete(inner))

	req := &Request{
		Model:    "m",
		Messages: []Message{UserMessage("hello")},
	}
	resp, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.ID != "ok" {
		t.Fatalf("expected response ID 'ok', got %q", resp.ID)
	}
	// Both transforms should have appended messages.
	if len(req.Messages) != 3 {
		t.Fatalf("expected 3 messages after both transforms, got %d", len(req.Messages))
	}
	if req.Messages[1].Text() != "second" {
		t.Fatalf("expected first appended message 'second' (outer runs first), got %q", req.Messages[1].Text())
	}
	if req.Messages[2].Text() != "first" {
		t.Fatalf("expected second appended message 'first' (inner runs second), got %q", req.Messages[2].Text())
	}
}

func TestTransformMiddleware_ErrorSkipsResponseTransform(t *testing.T) {
	// When next() returns error, response transform is not called.
	expectedErr := errors.New("downstream failure")
	inner := func(ctx context.Context, req *Request) (*Response, error) {
		return nil, expectedErr
	}

	responseTransformCalled := false
	mw := NewTransformMiddleware(nil, WithResponseTransform(func(resp *Response) {
		responseTransformCalled = true
	}))
	handler := mw.WrapComplete(inner)

	_, err := handler(context.Background(), &Request{Model: "m", Messages: []Message{UserMessage("hi")}})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected downstream error, got: %v", err)
	}
	if responseTransformCalled {
		t.Fatal("response transform should not be called when next() returns an error")
	}
}
