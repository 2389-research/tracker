// ABOUTME: Tests for the start handler, verifying it implements Handler and returns success.
// ABOUTME: Covers Name() and Execute() for the no-op start node.
package handlers

import (
	"context"
	"testing"

	"github.com/2389-research/mammoth-lite/pipeline"
)

func TestStartHandlerName(t *testing.T) {
	h := NewStartHandler()
	if h.Name() != "start" {
		t.Errorf("expected name %q, got %q", "start", h.Name())
	}
}

func TestStartHandlerExecute(t *testing.T) {
	h := NewStartHandler()
	node := &pipeline.Node{ID: "s", Shape: "Mdiamond", Attrs: map[string]string{}}
	pctx := pipeline.NewPipelineContext()

	outcome, err := h.Execute(context.Background(), node, pctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Status != pipeline.OutcomeSuccess {
		t.Errorf("expected status %q, got %q", pipeline.OutcomeSuccess, outcome.Status)
	}
}

func TestStartHandlerImplementsHandler(t *testing.T) {
	var _ pipeline.Handler = NewStartHandler()
}
