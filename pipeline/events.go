// ABOUTME: Event types emitted during pipeline execution for UI and logging.
// ABOUTME: Mirrors the Layer 2 EventHandler pattern with pipeline-specific event types.
package pipeline

import "time"

// PipelineEventType identifies the kind of lifecycle event emitted during pipeline execution.
type PipelineEventType string

const (
	EventPipelineStarted    PipelineEventType = "pipeline_started"
	EventPipelineCompleted  PipelineEventType = "pipeline_completed"
	EventPipelineFailed     PipelineEventType = "pipeline_failed"
	EventStageStarted       PipelineEventType = "stage_started"
	EventStageCompleted     PipelineEventType = "stage_completed"
	EventStageFailed        PipelineEventType = "stage_failed"
	EventStageRetrying      PipelineEventType = "stage_retrying"
	EventCheckpointSaved    PipelineEventType = "checkpoint_saved"
	EventCheckpointFailed   PipelineEventType = "checkpoint_failed"
	EventInterviewStarted   PipelineEventType = "interview_started"
	EventInterviewCompleted PipelineEventType = "interview_completed"
	EventParallelStarted    PipelineEventType = "parallel_started"
	EventParallelCompleted  PipelineEventType = "parallel_completed"
	EventLoopRestart        PipelineEventType = "loop_restart"
)

// PipelineEvent carries data about a single pipeline lifecycle occurrence.
type PipelineEvent struct {
	Type      PipelineEventType
	Timestamp time.Time
	RunID     string
	NodeID    string
	Message   string
	Err       error
}

// PipelineEventHandler receives pipeline events for observability purposes.
type PipelineEventHandler interface {
	HandlePipelineEvent(evt PipelineEvent)
}

// PipelineEventHandlerFunc is an adapter that lets ordinary functions serve as PipelineEventHandler.
type PipelineEventHandlerFunc func(evt PipelineEvent)

func (f PipelineEventHandlerFunc) HandlePipelineEvent(evt PipelineEvent) { f(evt) }

// pipelineNoopHandler silently discards all events.
type pipelineNoopHandler struct{}

func (pipelineNoopHandler) HandlePipelineEvent(PipelineEvent) {}

// PipelineNoopHandler is a handler that does nothing, useful as a default.
var PipelineNoopHandler PipelineEventHandler = pipelineNoopHandler{}

// NodeScopedPipelineHandler wraps a PipelineEventHandler and prefixes every
// event's NodeID with parentNodeID + "/". Child pipeline lifecycle events
// (started/completed/failed) are filtered out because the parent engine
// already tracks the subgraph node's lifecycle.
func NodeScopedPipelineHandler(parentNodeID string, inner PipelineEventHandler) PipelineEventHandler {
	if inner == nil {
		return PipelineNoopHandler
	}
	return PipelineEventHandlerFunc(func(evt PipelineEvent) {
		// Filter child pipeline lifecycle events — the parent tracks these.
		switch evt.Type {
		case EventPipelineStarted, EventPipelineCompleted, EventPipelineFailed:
			return
		}
		if evt.NodeID != "" {
			evt.NodeID = parentNodeID + "/" + evt.NodeID
		}
		inner.HandlePipelineEvent(evt)
	})
}

// PipelineMultiHandler fans out each event to every provided handler.
// Nil handlers in the list are safely skipped.
func PipelineMultiHandler(handlers ...PipelineEventHandler) PipelineEventHandler {
	cp := make([]PipelineEventHandler, len(handlers))
	copy(cp, handlers)
	return pipelineMultiHandler(cp)
}

type pipelineMultiHandler []PipelineEventHandler

func (m pipelineMultiHandler) HandlePipelineEvent(evt PipelineEvent) {
	for _, h := range m {
		if h != nil {
			h.HandlePipelineEvent(evt)
		}
	}
}
