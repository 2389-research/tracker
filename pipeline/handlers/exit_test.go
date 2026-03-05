// ABOUTME: Tests for the exit handler, verifying it implements Handler and returns success.
// ABOUTME: Covers Name() and Execute() for the no-op exit node.
package handlers

import (
	"context"
	"testing"

	"github.com/2389-research/mammoth-lite/pipeline"
)

func TestExitHandlerName(t *testing.T) {
	h := NewExitHandler()
	if h.Name() != "exit" {
		t.Errorf("expected name %q, got %q", "exit", h.Name())
	}
}

func TestExitHandlerExecute(t *testing.T) {
	h := NewExitHandler()
	node := &pipeline.Node{ID: "e", Shape: "Msquare", Attrs: map[string]string{}}
	pctx := pipeline.NewPipelineContext()

	outcome, err := h.Execute(context.Background(), node, pctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Status != pipeline.OutcomeSuccess {
		t.Errorf("expected status %q, got %q", pipeline.OutcomeSuccess, outcome.Status)
	}
}

func TestExitHandlerImplementsHandler(t *testing.T) {
	var _ pipeline.Handler = NewExitHandler()
}
