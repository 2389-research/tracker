// ABOUTME: Tests for the conditional handler, verifying it implements Handler and returns success.
// ABOUTME: Covers Name() and Execute() for the no-op conditional node (engine handles routing).
package handlers

import (
	"context"
	"testing"

	"github.com/2389-research/mammoth-lite/pipeline"
)

func TestConditionalHandlerName(t *testing.T) {
	h := NewConditionalHandler()
	if h.Name() != "conditional" {
		t.Errorf("expected name %q, got %q", "conditional", h.Name())
	}
}

func TestConditionalHandlerExecute(t *testing.T) {
	h := NewConditionalHandler()
	node := &pipeline.Node{ID: "c", Shape: "diamond", Attrs: map[string]string{}}
	pctx := pipeline.NewPipelineContext()

	outcome, err := h.Execute(context.Background(), node, pctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Status != pipeline.OutcomeSuccess {
		t.Errorf("expected status %q, got %q", pipeline.OutcomeSuccess, outcome.Status)
	}
}

func TestConditionalHandlerImplementsHandler(t *testing.T) {
	var _ pipeline.Handler = NewConditionalHandler()
}
