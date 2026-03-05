// ABOUTME: Tests for the codergen handler that invokes Layer 2 agent sessions.
// ABOUTME: Uses a mock Completer to verify session creation, prompt passing, and result capture.
package handlers

import (
	"context"
	"testing"

	"github.com/2389-research/mammoth-lite/llm"
	"github.com/2389-research/mammoth-lite/pipeline"
)

type fakeCompleter struct {
	responseText string
	err          error
}

func (f *fakeCompleter) Complete(ctx context.Context, req *llm.Request) (*llm.Response, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &llm.Response{
		Message:      llm.AssistantMessage(f.responseText),
		FinishReason: llm.FinishReason{Reason: "stop"},
		Usage:        llm.Usage{InputTokens: 10, OutputTokens: 20, TotalTokens: 30},
	}, nil
}

func TestCodergenHandlerName(t *testing.T) {
	h := NewCodergenHandler(nil, "")
	if h.Name() != "codergen" {
		t.Errorf("expected 'codergen', got %q", h.Name())
	}
}

func TestCodergenHandlerMissingPrompt(t *testing.T) {
	client := &fakeCompleter{responseText: "done"}
	h := NewCodergenHandler(client, t.TempDir())
	node := &pipeline.Node{ID: "gen", Shape: "box", Handler: "codergen", Attrs: map[string]string{}}
	pctx := pipeline.NewPipelineContext()
	_, err := h.Execute(context.Background(), node, pctx)
	if err == nil {
		t.Fatal("expected error for missing prompt")
	}
}

func TestCodergenHandlerSuccess(t *testing.T) {
	client := &fakeCompleter{responseText: "Hello, World!"}
	h := NewCodergenHandler(client, t.TempDir())
	node := &pipeline.Node{ID: "gen", Shape: "box", Handler: "codergen", Attrs: map[string]string{"prompt": "Write hello world"}}
	pctx := pipeline.NewPipelineContext()
	outcome, err := h.Execute(context.Background(), node, pctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Status != pipeline.OutcomeSuccess {
		t.Errorf("expected 'success', got %q", outcome.Status)
	}
	lastResp, ok := pctx.Get(pipeline.ContextKeyLastResponse)
	if !ok {
		t.Fatal("expected last_response in context")
	}
	if lastResp != "Hello, World!" {
		t.Errorf("expected 'Hello, World!', got %q", lastResp)
	}
}

func TestCodergenHandlerCapturesOutcomeInContext(t *testing.T) {
	client := &fakeCompleter{responseText: "completed task"}
	h := NewCodergenHandler(client, t.TempDir())
	node := &pipeline.Node{ID: "gen", Shape: "box", Handler: "codergen", Attrs: map[string]string{"prompt": "do something"}}
	pctx := pipeline.NewPipelineContext()
	outcome, err := h.Execute(context.Background(), node, pctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.ContextUpdates[pipeline.ContextKeyLastResponse] != "completed task" {
		t.Errorf("expected context update for last_response")
	}
}

func TestCodergenHandlerLLMError(t *testing.T) {
	client := &fakeCompleter{err: context.DeadlineExceeded}
	h := NewCodergenHandler(client, t.TempDir())
	node := &pipeline.Node{ID: "gen", Shape: "box", Handler: "codergen", Attrs: map[string]string{"prompt": "do something"}}
	pctx := pipeline.NewPipelineContext()
	outcome, err := h.Execute(context.Background(), node, pctx)
	if err != nil {
		t.Fatalf("handler should not return error on LLM failure, got: %v", err)
	}
	if outcome.Status != pipeline.OutcomeFail {
		t.Errorf("expected 'fail', got %q", outcome.Status)
	}
}

func TestCodergenHandlerAutoStatusSuccess(t *testing.T) {
	client := &fakeCompleter{responseText: "STATUS:success\nAll tests pass."}
	h := NewCodergenHandler(client, t.TempDir())
	node := &pipeline.Node{ID: "gen", Shape: "box", Handler: "codergen", Attrs: map[string]string{"prompt": "run tests", "auto_status": "true"}}
	pctx := pipeline.NewPipelineContext()
	outcome, err := h.Execute(context.Background(), node, pctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Status != pipeline.OutcomeSuccess {
		t.Errorf("expected 'success', got %q", outcome.Status)
	}
}

func TestCodergenHandlerAutoStatusFail(t *testing.T) {
	client := &fakeCompleter{responseText: "STATUS:fail\nTests failed."}
	h := NewCodergenHandler(client, t.TempDir())
	node := &pipeline.Node{ID: "gen", Shape: "box", Handler: "codergen", Attrs: map[string]string{"prompt": "run tests", "auto_status": "true"}}
	pctx := pipeline.NewPipelineContext()
	outcome, err := h.Execute(context.Background(), node, pctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Status != pipeline.OutcomeFail {
		t.Errorf("expected 'fail', got %q", outcome.Status)
	}
}

func TestCodergenHandlerAutoStatusRetry(t *testing.T) {
	client := &fakeCompleter{responseText: "STATUS:retry\nNeed more context."}
	h := NewCodergenHandler(client, t.TempDir())
	node := &pipeline.Node{ID: "gen", Shape: "box", Handler: "codergen", Attrs: map[string]string{"prompt": "analyze code", "auto_status": "true"}}
	pctx := pipeline.NewPipelineContext()
	outcome, err := h.Execute(context.Background(), node, pctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Status != pipeline.OutcomeRetry {
		t.Errorf("expected 'retry', got %q", outcome.Status)
	}
}

func TestCodergenHandlerSystemPrompt(t *testing.T) {
	client := &fakeCompleter{responseText: "done"}
	h := NewCodergenHandler(client, t.TempDir())
	node := &pipeline.Node{ID: "gen", Shape: "box", Handler: "codergen", Attrs: map[string]string{"prompt": "do work", "system_prompt": "You are helpful."}}
	pctx := pipeline.NewPipelineContext()
	outcome, err := h.Execute(context.Background(), node, pctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Status != pipeline.OutcomeSuccess {
		t.Errorf("expected 'success', got %q", outcome.Status)
	}
}
