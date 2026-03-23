// ABOUTME: Tests for LoggingEventHandler including resume event batching.
// ABOUTME: Verifies that resumed-node events are condensed into a single summary line.
package pipeline

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestLoggingEventHandlerResumedBatching(t *testing.T) {
	var buf bytes.Buffer
	h := &LoggingEventHandler{Writer: &buf}

	now := time.Now()

	// Simulate 3 resumed nodes.
	for _, id := range []string{"node_a", "node_b", "node_c"} {
		h.HandlePipelineEvent(PipelineEvent{
			Type:      EventStageCompleted,
			Timestamp: now,
			NodeID:    id,
			Message:   "previously completed (resumed)",
		})
	}

	// No output yet — events are batched.
	if buf.Len() != 0 {
		t.Errorf("expected no output during batching, got: %q", buf.String())
	}

	// Next real event should flush the batch.
	h.HandlePipelineEvent(PipelineEvent{
		Type:      EventStageStarted,
		Timestamp: now,
		NodeID:    "node_d",
		Message:   "starting",
	})

	output := buf.String()
	if !strings.Contains(output, "resumed 3 completed nodes") {
		t.Errorf("expected batched resume summary, got: %q", output)
	}
	if !strings.Contains(output, "node_a, node_b, node_c") {
		t.Errorf("expected node IDs in summary, got: %q", output)
	}
	if !strings.Contains(output, "stage_started") {
		t.Errorf("expected the real event after the summary, got: %q", output)
	}
}

func TestLoggingEventHandlerNormalEvents(t *testing.T) {
	var buf bytes.Buffer
	h := &LoggingEventHandler{Writer: &buf}

	now := time.Now()
	h.HandlePipelineEvent(PipelineEvent{
		Type:      EventStageStarted,
		Timestamp: now,
		NodeID:    "node_a",
		Message:   "starting",
	})

	output := buf.String()
	if !strings.Contains(output, "stage_started") {
		t.Errorf("expected stage_started in output, got: %q", output)
	}
	if !strings.Contains(output, "node_a") {
		t.Errorf("expected node_a in output, got: %q", output)
	}
}
