// ABOUTME: Exit handler for pipeline termination nodes; a no-op that returns success.
// ABOUTME: The exit node marks the end of pipeline execution.
package handlers

import (
	"context"

	"github.com/2389-research/mammoth-lite/pipeline"
)

// ExitHandler handles pipeline exit nodes. It is a no-op that always
// returns success, since the exit node exists only to mark the termination point.
type ExitHandler struct{}

// NewExitHandler creates a new ExitHandler.
func NewExitHandler() *ExitHandler { return &ExitHandler{} }

// Name returns the handler name used for registry lookup.
func (h *ExitHandler) Name() string { return "exit" }

// Execute is a no-op that returns a success outcome.
func (h *ExitHandler) Execute(ctx context.Context, node *pipeline.Node, pctx *pipeline.PipelineContext) (pipeline.Outcome, error) {
	return pipeline.Outcome{Status: pipeline.OutcomeSuccess}, nil
}
