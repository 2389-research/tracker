// ABOUTME: Logging event handler that prints pipeline lifecycle events to an io.Writer.
// ABOUTME: Provides human-readable output for CLI and debugging use cases.
package pipeline

import (
	"fmt"
	"io"
)

// LoggingEventHandler writes pipeline events to a Writer in a human-readable format.
type LoggingEventHandler struct {
	Writer io.Writer
}

// HandlePipelineEvent formats and writes a pipeline event to the configured Writer.
func (h *LoggingEventHandler) HandlePipelineEvent(evt PipelineEvent) {
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
