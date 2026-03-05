// ABOUTME: Tests for pipeline event types and handler interface.
// ABOUTME: Validates event type uniqueness, handler func adapter, multi-handler fan-out, and noop handler.
package pipeline

import (
	"testing"
)

func TestPipelineEventTypesUnique(t *testing.T) {
	types := []PipelineEventType{
		EventPipelineStarted, EventPipelineCompleted, EventPipelineFailed,
		EventStageStarted, EventStageCompleted, EventStageFailed, EventStageRetrying,
		EventCheckpointSaved, EventInterviewStarted, EventInterviewCompleted,
		EventParallelStarted, EventParallelCompleted,
	}
	seen := make(map[PipelineEventType]bool)
	for _, et := range types {
		if seen[et] {
			t.Errorf("duplicate event type: %s", et)
		}
		seen[et] = true
	}
}

func TestPipelineEventHandlerFunc(t *testing.T) {
	var received []PipelineEvent
	handler := PipelineEventHandlerFunc(func(evt PipelineEvent) {
		received = append(received, evt)
	})
	handler.HandlePipelineEvent(PipelineEvent{Type: EventStageStarted, NodeID: "test-node"})
	if len(received) != 1 {
		t.Fatalf("expected 1 event, got %d", len(received))
	}
	if received[0].NodeID != "test-node" {
		t.Errorf("expected node ID 'test-node', got %q", received[0].NodeID)
	}
}

func TestPipelineMultiHandler(t *testing.T) {
	count := 0
	handler := PipelineEventHandlerFunc(func(evt PipelineEvent) { count++ })
	multi := PipelineMultiHandler(handler, handler, handler)
	multi.HandlePipelineEvent(PipelineEvent{Type: EventPipelineStarted})
	if count != 3 {
		t.Errorf("expected 3 handler calls, got %d", count)
	}
}

func TestPipelineNoopHandler(t *testing.T) {
	PipelineNoopHandler.HandlePipelineEvent(PipelineEvent{Type: EventPipelineFailed})
}

func TestPipelineMultiHandlerNilSafe(t *testing.T) {
	handler := PipelineEventHandlerFunc(func(evt PipelineEvent) {})
	multi := PipelineMultiHandler(handler, nil, handler)
	multi.HandlePipelineEvent(PipelineEvent{Type: EventStageCompleted})
}

func TestPipelineEventFields(t *testing.T) {
	evt := PipelineEvent{Type: EventStageCompleted, RunID: "run-123", NodeID: "generate", Message: "code generated successfully"}
	if evt.RunID != "run-123" {
		t.Errorf("expected RunID 'run-123', got %q", evt.RunID)
	}
	if evt.NodeID != "generate" {
		t.Errorf("expected NodeID 'generate', got %q", evt.NodeID)
	}
}
