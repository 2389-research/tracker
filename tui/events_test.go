// ABOUTME: Tests for TUIEventHandler - verifies pipeline event forwarding.
package tui

import (
	"testing"
	"time"

	"github.com/2389-research/tracker/pipeline"
)

func TestNewTUIEventHandlerCreatesHandler(t *testing.T) {
	called := false
	handler := NewTUIEventHandler(func(evt pipeline.PipelineEvent) {
		called = true
	})
	if handler == nil {
		t.Fatal("expected non-nil handler")
	}
	handler.HandlePipelineEvent(pipeline.PipelineEvent{Type: pipeline.EventPipelineStarted})
	if !called {
		t.Error("expected send function to be called")
	}
}

func TestHandlePipelineEventForwardsEvent(t *testing.T) {
	var received pipeline.PipelineEvent
	handler := NewTUIEventHandler(func(evt pipeline.PipelineEvent) {
		received = evt
	})

	evt := pipeline.PipelineEvent{
		Type:      pipeline.EventStageCompleted,
		NodeID:    "node-1",
		Message:   "completed successfully",
		Timestamp: time.Now(),
	}
	handler.HandlePipelineEvent(evt)

	if received.Type != pipeline.EventStageCompleted {
		t.Errorf("expected EventStageCompleted, got %v", received.Type)
	}
	if received.NodeID != "node-1" {
		t.Errorf("expected NodeID='node-1', got %q", received.NodeID)
	}
	if received.Message != "completed successfully" {
		t.Errorf("expected message 'completed successfully', got %q", received.Message)
	}
}

func TestHandlePipelineEventNilSendIsNoop(t *testing.T) {
	// Nil send should not panic
	handler := NewTUIEventHandler(nil)
	// Should not panic
	handler.HandlePipelineEvent(pipeline.PipelineEvent{Type: pipeline.EventPipelineStarted})
}

func TestHandlePipelineEventForwardsAllEventTypes(t *testing.T) {
	eventTypes := []pipeline.PipelineEventType{
		pipeline.EventPipelineStarted,
		pipeline.EventPipelineCompleted,
		pipeline.EventPipelineFailed,
		pipeline.EventStageStarted,
		pipeline.EventStageCompleted,
		pipeline.EventStageFailed,
		pipeline.EventInterviewStarted,
		pipeline.EventInterviewCompleted,
		pipeline.EventParallelStarted,
		pipeline.EventParallelCompleted,
		pipeline.EventCheckpointSaved,
	}

	for _, evtType := range eventTypes {
		t.Run(string(evtType), func(t *testing.T) {
			var received pipeline.PipelineEventType
			handler := NewTUIEventHandler(func(evt pipeline.PipelineEvent) {
				received = evt.Type
			})
			handler.HandlePipelineEvent(pipeline.PipelineEvent{Type: evtType})
			if received != evtType {
				t.Errorf("expected %v, got %v", evtType, received)
			}
		})
	}
}

func TestTUIEventHandlerImplementsInterface(t *testing.T) {
	// Compile-time interface assertion
	var _ pipeline.PipelineEventHandler = (*TUIEventHandler)(nil)
}

func TestHandlePipelineEventConcurrentlySafe(t *testing.T) {
	count := 0
	mu := make(chan struct{}, 1)
	mu <- struct{}{}

	handler := NewTUIEventHandler(func(evt pipeline.PipelineEvent) {
		<-mu
		count++
		mu <- struct{}{}
	})

	done := make(chan struct{})
	for i := 0; i < 50; i++ {
		go func() {
			handler.HandlePipelineEvent(pipeline.PipelineEvent{Type: pipeline.EventStageCompleted})
			done <- struct{}{}
		}()
	}
	for i := 0; i < 50; i++ {
		<-done
	}
	if count != 50 {
		t.Errorf("expected 50 events forwarded, got %d", count)
	}
}
