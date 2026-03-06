// ABOUTME: Conditional handler for diamond-shaped decision nodes; a no-op that returns success.
// ABOUTME: The engine handles edge routing based on conditions; this handler just marks execution.
package handlers

import (
	"context"

	"github.com/2389-research/tracker/pipeline"
)

// ConditionalHandler handles pipeline conditional (diamond) nodes. It is a no-op
// because the engine is responsible for evaluating edge conditions and selecting
// the next node. The handler simply returns success to signal that execution
// should proceed to condition evaluation.
type ConditionalHandler struct{}

// NewConditionalHandler creates a new ConditionalHandler.
func NewConditionalHandler() *ConditionalHandler { return &ConditionalHandler{} }

// Name returns the handler name used for registry lookup.
func (h *ConditionalHandler) Name() string { return "conditional" }

// Execute is a no-op that returns a success outcome.
func (h *ConditionalHandler) Execute(ctx context.Context, node *pipeline.Node, pctx *pipeline.PipelineContext) (pipeline.Outcome, error) {
	return pipeline.Outcome{Status: pipeline.OutcomeSuccess}, nil
}
