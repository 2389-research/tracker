// ABOUTME: Start handler for pipeline entry nodes; a no-op that returns success.
// ABOUTME: The start node marks the beginning of pipeline execution.
package handlers

import (
	"context"

	"github.com/2389-research/mammoth-lite/pipeline"
)

// StartHandler handles pipeline start nodes. It is a no-op that always
// returns success, since the start node exists only to mark the entry point.
type StartHandler struct{}

// NewStartHandler creates a new StartHandler.
func NewStartHandler() *StartHandler { return &StartHandler{} }

// Name returns the handler name used for registry lookup.
func (h *StartHandler) Name() string { return "start" }

// Execute is a no-op that returns a success outcome.
func (h *StartHandler) Execute(ctx context.Context, node *pipeline.Node, pctx *pipeline.PipelineContext) (pipeline.Outcome, error) {
	return pipeline.Outcome{Status: pipeline.OutcomeSuccess}, nil
}
