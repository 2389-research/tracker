// ABOUTME: Tests for the dashboard agent log component — scrolling viewport of events.
package dashboard

import (
	"strings"
	"testing"
	"time"

	"github.com/2389-research/tracker/pipeline"
)

func TestAgentLogModelCreation(t *testing.T) {
	log := NewAgentLogModel(80, 20)
	if log.width != 80 {
		t.Errorf("expected width=80, got %d", log.width)
	}
	if log.height != 20 {
		t.Errorf("expected height=20, got %d", log.height)
	}
}

func TestAgentLogModelInitiallyEmpty(t *testing.T) {
	log := NewAgentLogModel(80, 20)
	if log.Len() != 0 {
		t.Errorf("expected 0 entries, got %d", log.Len())
	}
}

func TestAgentLogAppendLine(t *testing.T) {
	log := NewAgentLogModel(80, 20)
	log.AppendLine("hello log line")
	if log.Len() != 1 {
		t.Errorf("expected 1 entry, got %d", log.Len())
	}
}

func TestAgentLogAppendMultipleLines(t *testing.T) {
	log := NewAgentLogModel(80, 20)
	log.AppendLine("line 1")
	log.AppendLine("line 2")
	log.AppendLine("line 3")
	if log.Len() != 3 {
		t.Errorf("expected 3 entries, got %d", log.Len())
	}
}

func TestAgentLogAppendEvent(t *testing.T) {
	log := NewAgentLogModel(80, 20)
	evt := pipeline.PipelineEvent{
		Type:      pipeline.EventStageCompleted,
		NodeID:    "mynode",
		Message:   "completed",
		Timestamp: time.Now(),
	}
	log.AppendEvent(evt)
	if log.Len() != 1 {
		t.Errorf("expected 1 entry after AppendEvent, got %d", log.Len())
	}
}

func TestAgentLogAppendEventWithError(t *testing.T) {
	log := NewAgentLogModel(80, 20)
	err := &errImpl{msg: "something went wrong"}
	evt := pipeline.PipelineEvent{
		Type:      pipeline.EventStageFailed,
		NodeID:    "failnode",
		Message:   "stage failed",
		Timestamp: time.Now(),
		Err:       err,
	}
	log.AppendEvent(evt)
	if log.Len() != 1 {
		t.Errorf("expected 1 entry after AppendEvent with error, got %d", log.Len())
	}
	entry := log.entries[0]
	if !entry.IsError {
		t.Error("expected IsError=true for event with non-nil Err")
	}
	if !strings.Contains(entry.Message, "something went wrong") {
		t.Errorf("expected error text in message, got: %q", entry.Message)
	}
}

func TestAgentLogViewContainsTitleWhenReady(t *testing.T) {
	log := NewAgentLogModel(80, 20)
	view := log.View()
	if !strings.Contains(view, "Agent Log") {
		t.Errorf("expected 'Agent Log' title in view, got: %q", view)
	}
}

func TestAgentLogViewContainsInitializingWhenNotReady(t *testing.T) {
	log := NewAgentLogModel(0, 0)
	view := log.View()
	if !strings.Contains(view, "initializ") {
		t.Errorf("expected initializing message in view, got: %q", view)
	}
}

func TestAgentLogSetSize(t *testing.T) {
	log := NewAgentLogModel(40, 10)
	log.SetSize(100, 30)
	if log.width != 100 {
		t.Errorf("expected width=100, got %d", log.width)
	}
	if log.height != 30 {
		t.Errorf("expected height=30, got %d", log.height)
	}
}

func TestAgentLogSetSizeMakesReady(t *testing.T) {
	log := NewAgentLogModel(0, 0)
	if log.ready {
		t.Error("expected not ready with 0 dimensions")
	}
	log.SetSize(80, 20)
	if !log.ready {
		t.Error("expected ready after SetSize with positive dimensions")
	}
}

func TestAgentLogInitReturnsNilCmd(t *testing.T) {
	log := NewAgentLogModel(80, 20)
	cmd := log.Init()
	if cmd != nil {
		t.Error("expected nil Cmd from Init")
	}
}

func TestAgentLogFormatIncludesTimestamp(t *testing.T) {
	entry := LogEntry{
		Time:      time.Date(2026, 3, 6, 12, 30, 45, 0, time.UTC),
		EventType: "stage_started",
		NodeID:    "mynode",
		Message:   "test message",
	}
	result := formatLogEntry(entry)
	if !strings.Contains(result, "12:30:45") {
		t.Errorf("expected timestamp '12:30:45' in formatted entry, got: %q", result)
	}
}

func TestAgentLogFormatIncludesEventType(t *testing.T) {
	entry := LogEntry{
		Time:      time.Now(),
		EventType: "stage_completed",
		Message:   "done",
	}
	result := formatLogEntry(entry)
	if !strings.Contains(result, "stage_completed") {
		t.Errorf("expected event type in formatted entry, got: %q", result)
	}
}

func TestAgentLogFormatIncludesNodeID(t *testing.T) {
	entry := LogEntry{
		Time:   time.Now(),
		NodeID: "node-xyz",
	}
	result := formatLogEntry(entry)
	if !strings.Contains(result, "node-xyz") {
		t.Errorf("expected node ID in formatted entry, got: %q", result)
	}
}

func TestAgentLogFormatIncludesMessage(t *testing.T) {
	entry := LogEntry{
		Time:    time.Now(),
		Message: "pipeline is running",
	}
	result := formatLogEntry(entry)
	if !strings.Contains(result, "pipeline is running") {
		t.Errorf("expected message in formatted entry, got: %q", result)
	}
}

func TestAgentLogFormatEmptyEventTypeOmitsTypeField(t *testing.T) {
	entry := LogEntry{
		Time:    time.Now(),
		Message: "bare message",
	}
	result := formatLogEntry(entry)
	if strings.Contains(result, "[]") {
		t.Errorf("expected no empty brackets when EventType is empty, got: %q", result)
	}
}

func TestAgentLogAppendEventAllTypes(t *testing.T) {
	eventTypes := []pipeline.PipelineEventType{
		pipeline.EventPipelineStarted,
		pipeline.EventPipelineCompleted,
		pipeline.EventPipelineFailed,
		pipeline.EventStageStarted,
		pipeline.EventStageCompleted,
		pipeline.EventStageFailed,
		pipeline.EventInterviewStarted,
		pipeline.EventInterviewCompleted,
	}
	log := NewAgentLogModel(80, 20)
	for _, evtType := range eventTypes {
		log.AppendEvent(pipeline.PipelineEvent{
			Type:      evtType,
			Timestamp: time.Now(),
		})
	}
	if log.Len() != len(eventTypes) {
		t.Errorf("expected %d entries, got %d", len(eventTypes), log.Len())
	}
}

// errImpl is a minimal error implementation for test use.
type errImpl struct{ msg string }

func (e *errImpl) Error() string { return e.msg }
