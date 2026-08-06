// ABOUTME: Pipeline-context plumbing for WebhookInterviewer — lets a run
// ABOUTME: cancellation unblock a gate parked waiting for a webhook callback.
package handlers

import "context"

// WebhookInterviewer implements ContextSetter so a run cancellation unblocks a
// gate parked here.
var _ ContextSetter = (*WebhookInterviewer)(nil)

// SetPipelineContext stores the pipeline execution context so a waiting gate is
// released when the run is canceled. Without this, RunManager.Cancel cancels the
// context but the gate stays parked on pending.ch / the timeout / w.canceled
// (closed only by Engine.Close, which is deferred behind the run returning — a
// deadlock), leaking the capacity slot and execute goroutine until the timeout.
func (w *WebhookInterviewer) SetPipelineContext(ctx context.Context) {
	w.mu.Lock()
	w.pctx = ctx
	w.mu.Unlock()
}

// pipelineDone returns the pipeline context's Done channel, or nil (which blocks
// forever in a select) when no context has been set.
func (w *WebhookInterviewer) pipelineDone() <-chan struct{} {
	w.mu.Lock()
	pctx := w.pctx
	w.mu.Unlock()
	if pctx == nil {
		return nil
	}
	return pctx.Done()
}
