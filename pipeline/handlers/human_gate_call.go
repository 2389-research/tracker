// ABOUTME: Per-gate timeout runner and mode dispatch that prefer a context-aware
// ABOUTME: interviewer, scoping a gate timeout to that gate alone (#599).
package handlers

import (
	"context"
	"time"
)

// runGate runs a single gate call under the node's gate timeout, deriving a
// per-gate context from ctx. On timeout it cancels ONLY that gate's context —
// unblocking a context-aware interviewer's blocked call — and returns
// errHumanTimeout; sibling and later gates on the same interviewer are untouched.
// A zero timeout runs fn(ctx) directly.
//
// legacyFallback, when non-nil, is invoked on timeout to unblock an interviewer
// that ignores the gate context (a legacy Cancel()-based interviewer); it is nil
// for a context-aware interviewer, whose fn observes the canceled gate context.
// This preserves the pre-#599 leak-prevention (#446) for legacy interviewers
// while keeping Cancel()/Close() reserved for run-wide teardown when a context
// variant is available.
func runGate[T any](ctx context.Context, timeout time.Duration, legacyFallback func(), fn func(context.Context) (T, error)) (T, error) {
	if timeout <= 0 {
		return fn(ctx)
	}
	gctx, cancel := context.WithCancel(ctx)
	defer cancel()

	type result struct {
		val T
		err error
	}
	ch := make(chan result, 1)
	go func() {
		v, e := fn(gctx)
		ch <- result{v, e}
	}()

	select {
	case r := <-ch:
		return r.val, r.err
	case <-time.After(timeout):
		cancel()
		if legacyFallback != nil {
			legacyFallback()
		}
		var zero T
		return zero, errHumanTimeout
	}
}

// legacyCancel returns a timeout fallback that tears the interviewer down via its
// optional Cancel(), or nil when i is context-aware for gates (the handler then
// relies on the per-gate context instead of run-wide teardown).
func legacyCancel(i Interviewer) func() {
	return func() { cancelInterviewer(i) }
}

// askChoice runs a choice / yes-no gate, preferring the context-aware method.
func (h *HumanHandler) askChoice(ctx context.Context, timeout time.Duration, prompt string, choices []string, defaultChoice string) (string, error) {
	if ci, ok := h.interviewer.(ChoiceContextInterviewer); ok {
		return runGate(ctx, timeout, nil, func(gctx context.Context) (string, error) {
			return ci.AskContext(gctx, prompt, choices, defaultChoice)
		})
	}
	i := h.interviewer
	return runGate(ctx, timeout, legacyCancel(i), func(context.Context) (string, error) {
		return i.Ask(prompt, choices, defaultChoice)
	})
}

// askFreeform runs a freeform gate — labeled when the interviewer supports labels
// and labels are present, else plain — preferring the context-aware method.
func (h *HumanHandler) askFreeform(ctx context.Context, timeout time.Duration, fi FreeformInterviewer, prompt string, labels []string, defaultLabel string) (string, error) {
	if lfi, ok := fi.(LabeledFreeformInterviewer); ok && len(labels) > 0 {
		return runFreeformLabeled(ctx, timeout, lfi, prompt, labels, defaultLabel)
	}
	if fci, ok := fi.(FreeformContextInterviewer); ok {
		return runGate(ctx, timeout, nil, func(gctx context.Context) (string, error) {
			return fci.AskFreeformContext(gctx, prompt)
		})
	}
	return runGate(ctx, timeout, legacyCancel(fi), func(context.Context) (string, error) {
		return fi.AskFreeform(prompt)
	})
}

// runFreeformLabeled runs a labeled freeform gate, preferring the context-aware
// method when the interviewer implements it.
func runFreeformLabeled(ctx context.Context, timeout time.Duration, lfi LabeledFreeformInterviewer, prompt string, labels []string, defaultLabel string) (string, error) {
	if lci, ok := lfi.(LabeledFreeformContextInterviewer); ok {
		return runGate(ctx, timeout, nil, func(gctx context.Context) (string, error) {
			return lci.AskFreeformWithLabelsContext(gctx, prompt, labels, defaultLabel)
		})
	}
	return runGate(ctx, timeout, legacyCancel(lfi), func(context.Context) (string, error) {
		return lfi.AskFreeformWithLabels(prompt, labels, defaultLabel)
	})
}

// askInterview runs an interview gate, preferring the context-aware method.
func (h *HumanHandler) askInterview(ctx context.Context, timeout time.Duration, ii InterviewInterviewer, questions []Question, previous *InterviewResult) (*InterviewResult, error) {
	if ic, ok := h.interviewer.(InterviewContextInterviewer); ok {
		return runGate(ctx, timeout, nil, func(gctx context.Context) (*InterviewResult, error) {
			return ic.AskInterviewContext(gctx, questions, previous)
		})
	}
	i := h.interviewer
	return runGate(ctx, timeout, legacyCancel(i), func(context.Context) (*InterviewResult, error) {
		return ii.AskInterview(questions, previous)
	})
}
