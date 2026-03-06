// ABOUTME: Bridges pipeline lifecycle events into the bubbletea TUI message loop.
// ABOUTME: Implements pipeline.PipelineEventHandler by forwarding events via a callback.
package tui

import (
	"github.com/2389-research/tracker/pipeline"
)

// TUIEventHandler implements pipeline.PipelineEventHandler and forwards events
// to the bubbletea program via a send function.
// In the full TUI (mode 2), the send function calls tea.Program.Send().
type TUIEventHandler struct {
	send func(pipeline.PipelineEvent)
}

// NewTUIEventHandler creates an event handler that forwards pipeline events
// via the provided send function.
func NewTUIEventHandler(send func(pipeline.PipelineEvent)) *TUIEventHandler {
	return &TUIEventHandler{send: send}
}

// HandlePipelineEvent implements pipeline.PipelineEventHandler.
// It forwards the event to the bubbletea program synchronously.
func (h *TUIEventHandler) HandlePipelineEvent(evt pipeline.PipelineEvent) {
	if h.send != nil {
		h.send(evt)
	}
}
