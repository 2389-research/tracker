package handlers

import (
	"context"

	"github.com/2389-research/tracker/pipeline"
)

type ManagerLoopHandler struct{}

func NewManagerLoopHandler() *ManagerLoopHandler { return &ManagerLoopHandler{} }

func (h *ManagerLoopHandler) Name() string { return "stack.manager_loop" }

func (h *ManagerLoopHandler) Execute(ctx context.Context, node *pipeline.Node, pctx *pipeline.PipelineContext) (pipeline.Outcome, error) {
	return pipeline.Outcome{Status: pipeline.OutcomeSuccess}, nil
}
