// ABOUTME: Pipeline events the parallel handler emits — fan-out start, fan-in
// ABOUTME: completion, and per-branch completion — split from parallel.go.
package handlers

import (
	"fmt"
	"time"

	"github.com/2389-research/tracker/pipeline"
)

// emitParallelStarted emits the fan-out EventParallelStarted event naming
// every dispatched branch target.
func (h *ParallelHandler) emitParallelStarted(nodeID string, edges []*pipeline.Edge, pctx *pipeline.PipelineContext) {
	branchIDs := make([]string, len(edges))
	for i, edge := range edges {
		branchIDs[i] = edge.To
	}
	h.eventHandler.HandlePipelineEvent(stampRunID(pipeline.PipelineEvent{
		Type:      pipeline.EventParallelStarted,
		Timestamp: time.Now(),
		NodeID:    nodeID,
		Message:   fmt.Sprintf("fan-out to %d branches: %v", len(edges), branchIDs),
	}, pctx))
}

// emitParallelCompleted emits the fan-in EventParallelCompleted event,
// surfacing the policy evaluation (incl. failed branch IDs) for non-default
// policies so the TUI and `tracker diagnose` can explain a policy-caused
// failure (#313).
func (h *ParallelHandler) emitParallelCompleted(nodeID string, status pipeline.TerminalStatus, policy fanInPolicy, policyDetail string, pctx *pipeline.PipelineContext) {
	msg := fmt.Sprintf("fan-in complete, aggregate status: %s", status)
	if policy.name != "any" {
		msg += " (" + policyDetail + ")"
	}
	h.eventHandler.HandlePipelineEvent(stampRunID(pipeline.PipelineEvent{
		Type:      pipeline.EventParallelCompleted,
		Timestamp: time.Now(),
		NodeID:    nodeID,
		Message:   msg,
	}, pctx))
}

// emitBranchComplete emits the appropriate pipeline event for a branch result.
// A paused branch is waiting, not failed, so it gets no event.
func (h *ParallelHandler) emitBranchComplete(nodeID string, pr ParallelResult, paused bool, pctx *pipeline.PipelineContext) {
	if paused {
		return
	}
	if pr.Status == string(pipeline.OutcomeFail) {
		h.eventHandler.HandlePipelineEvent(stampRunID(pipeline.PipelineEvent{
			Type: pipeline.EventStageFailed, Timestamp: time.Now(), NodeID: nodeID, Message: pr.Error,
		}, pctx))
	} else {
		h.eventHandler.HandlePipelineEvent(stampRunID(pipeline.PipelineEvent{
			Type: pipeline.EventStageCompleted, Timestamp: time.Now(), NodeID: nodeID,
		}, pctx))
	}
}
