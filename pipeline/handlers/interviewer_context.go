// ABOUTME: Context-aware gate interfaces (#599) — each gate call carries its own
// ABOUTME: context so a per-gate timeout cancels only that gate, not the run.
package handlers

import "context"

// The context-aware interviewer interfaces below split gate-scoped cancellation
// from run-wide teardown (#599). Historically a single gate timeout reached the
// interviewer through Cancel(), which run-wide implementations (Slack thread,
// webhook) satisfy by closing their one shared channel — permanently killing
// every later and concurrent sibling gate. These variants let the human handler
// carry a per-gate context into each call and cancel ONLY that call on timeout.
//
// An interviewer opts in by implementing whichever variant(s) match the modes it
// serves; the handler prefers a context variant when present and otherwise falls
// back to the legacy method plus the coarse Cancel()-on-timeout behaviour. With
// a context variant in play, Cancel()/Close() are reserved for run-wide teardown
// only (Engine.Close), so a local timeout no longer corrupts the rest of a run.

// ChoiceContextInterviewer is the context-aware form of Interviewer.Ask, covering
// choice and yes/no gates.
type ChoiceContextInterviewer interface {
	AskContext(ctx context.Context, prompt string, choices []string, defaultChoice string) (string, error)
}

// FreeformContextInterviewer is the context-aware form of
// FreeformInterviewer.AskFreeform.
type FreeformContextInterviewer interface {
	AskFreeformContext(ctx context.Context, prompt string) (string, error)
}

// LabeledFreeformContextInterviewer is the context-aware form of
// LabeledFreeformInterviewer.AskFreeformWithLabels.
type LabeledFreeformContextInterviewer interface {
	AskFreeformWithLabelsContext(ctx context.Context, prompt string, labels []string, defaultLabel string) (string, error)
}

// InterviewContextInterviewer is the context-aware form of
// InterviewInterviewer.AskInterview.
type InterviewContextInterviewer interface {
	AskInterviewContext(ctx context.Context, questions []Question, previousAnswers *InterviewResult) (*InterviewResult, error)
}
