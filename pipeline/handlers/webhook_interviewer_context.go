// ABOUTME: Pipeline-context plumbing for WebhookInterviewer — lets a run
// ABOUTME: cancellation unblock a gate parked waiting for a webhook callback.
package handlers

import (
	"context"

	"github.com/2389-research/tracker/internal/diag"
)

// WebhookInterviewer implements ContextSetter so a run cancellation unblocks a
// gate parked here.
var _ ContextSetter = (*WebhookInterviewer)(nil)

// AskContext is the context-aware Ask (#599): canceling ctx abandons this gate
// only, so a per-gate timeout no longer tears the interviewer down run-wide.
func (w *WebhookInterviewer) AskContext(ctx context.Context, prompt string, choices []string, defaultChoice string) (string, error) {
	gateChoices := make([]WebhookGateChoice, len(choices))
	for i, c := range choices {
		gateChoices[i] = WebhookGateChoice{Label: c, Value: c}
	}

	resp, timedOut, err := w.ask(ctx, prompt, "", gateChoices)
	if err != nil {
		return "", err
	}

	choice := resolveWebhookChoice(resp.Choice, choices, defaultChoice)
	if timedOut {
		diag.Warnf("[webhook] gate timed out (action=%s), returning %q", w.DefaultAction, choice)
	}
	return choice, nil
}

// AskFreeformContext is the context-aware AskFreeform (#599).
func (w *WebhookInterviewer) AskFreeformContext(ctx context.Context, prompt string) (string, error) {
	resp, timedOut, err := w.ask(ctx, prompt, "", nil)
	if err != nil {
		return "", err
	}
	if timedOut {
		diag.Warnf("[webhook] freeform gate timed out (action=%s)", w.DefaultAction)
		return resp.Freeform, nil
	}
	if resp.Freeform != "" {
		return resp.Freeform, nil
	}
	return resp.Choice, nil
}

// AskFreeformWithLabelsContext is the context-aware AskFreeformWithLabels (#599).
func (w *WebhookInterviewer) AskFreeformWithLabelsContext(ctx context.Context, prompt string, labels []string, defaultLabel string) (string, error) {
	gateChoices := make([]WebhookGateChoice, len(labels))
	for i, l := range labels {
		gateChoices[i] = WebhookGateChoice{Label: l, Value: l}
	}

	resp, timedOut, err := w.ask(ctx, prompt, "", gateChoices)
	if err != nil {
		return "", err
	}

	if timedOut {
		// Route the timeout action through the same label resolver a real response
		// uses. This maps "fail"/"success" to an actual label when possible, or
		// falls back to defaultLabel so the pipeline always gets a valid edge label.
		resolved := resolveWebhookChoice(resp.Choice, labels, defaultLabel)
		diag.Warnf("[webhook] labeled freeform gate timed out (action=%s), returning %q", w.DefaultAction, resolved)
		return resolved, nil
	}

	// Prefer Freeform when the responder typed custom text.
	if resp.Freeform != "" {
		return resp.Freeform, nil
	}
	return resolveWebhookChoice(resp.Choice, labels, defaultLabel), nil
}

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
