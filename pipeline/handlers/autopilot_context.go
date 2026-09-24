// ABOUTME: Pipeline-context plumbing shared by the API and claude-code autopilot interviewers.
// ABOUTME: Holds the run's context so autopilot LLM calls and subprocess spawns respect cancellation.
package handlers

import (
	"context"
	"sync"
)

// pipelineContextHolder stores the pipeline execution context an autopilot
// interviewer receives through ContextSetter. AutopilotInterviewer and
// ClaudeCodeAutopilotInterviewer embed it.
type pipelineContextHolder struct {
	mu          sync.RWMutex
	pipelineCtx context.Context // set by SetPipelineContext before each gate; nil = use Background
}

// SetPipelineContext stores the pipeline execution context so that LLM calls
// and claude subprocess spawns respect pipeline cancellation (ctrl-C, budget
// breach, etc.). Called by the human handler via the ContextSetter interface
// before any gate method is invoked.
func (h *pipelineContextHolder) SetPipelineContext(ctx context.Context) {
	h.mu.Lock()
	h.pipelineCtx = ctx
	h.mu.Unlock()
}

// parentContext returns the stored pipeline context, or context.Background() if none is set.
func (h *pipelineContextHolder) parentContext() context.Context {
	h.mu.RLock()
	ctx := h.pipelineCtx
	h.mu.RUnlock()
	if ctx == nil {
		return context.Background()
	}
	return ctx
}
