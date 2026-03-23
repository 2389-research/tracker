// ABOUTME: Logging event handler that prints pipeline lifecycle events to an io.Writer.
// ABOUTME: Provides human-readable output for CLI and debugging use cases.
package pipeline

import (
	"fmt"
	"io"
	"strings"
)

// LoggingEventHandler writes pipeline events to a Writer in a human-readable format.
// Batches rapid-fire "previously completed" events from checkpoint resume into a
// single summary line so the console isn't flooded on resume.
type LoggingEventHandler struct {
	Writer       io.Writer
	resumedNodes []string
}

// HandlePipelineEvent formats and writes a pipeline event to the configured Writer.
func (h *LoggingEventHandler) HandlePipelineEvent(evt PipelineEvent) {
	// Batch resumed-node events into a single summary line.
	if evt.Type == EventStageCompleted && strings.Contains(evt.Message, "previously completed") {
		h.resumedNodes = append(h.resumedNodes, evt.NodeID)
		return
	}

	// Flush any batched resume events before printing the next real event.
	h.flushResumed()

	ts := evt.Timestamp.Format("15:04:05")
	if evt.NodeID != "" {
		if evt.Err != nil {
			fmt.Fprintf(h.Writer, "[%s] %-22s node=%-20s %s (err: %v)\n", ts, evt.Type, evt.NodeID, evt.Message, evt.Err)
		} else {
			fmt.Fprintf(h.Writer, "[%s] %-22s node=%-20s %s\n", ts, evt.Type, evt.NodeID, evt.Message)
		}
	} else {
		if evt.Err != nil {
			fmt.Fprintf(h.Writer, "[%s] %-22s %s (err: %v)\n", ts, evt.Type, evt.Message, evt.Err)
		} else {
			fmt.Fprintf(h.Writer, "[%s] %-22s %s\n", ts, evt.Type, evt.Message)
		}
	}
}

// flushResumed prints a single summary line for batched resumed-node events.
func (h *LoggingEventHandler) flushResumed() {
	if len(h.resumedNodes) == 0 {
		return
	}
	fmt.Fprintf(h.Writer, "  ✓ resumed %d completed nodes: %s\n",
		len(h.resumedNodes), strings.Join(h.resumedNodes, ", "))
	h.resumedNodes = nil
}
