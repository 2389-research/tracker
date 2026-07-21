// ABOUTME: PauseError lets a handler request a recoverable terminal PAUSE (e.g. a
// ABOUTME: billing/quota halt) instead of a fatal failure — resumable, not a crash.
package pipeline

import (
	"errors"
	"time"
)

// OutcomePausedBilling signals that the run stopped on a provider billing/quota
// exhaustion — a RECOVERABLE condition ("add credit and resume"), not a code
// failure. The checkpoint is saved and in-flight work preserved, so the run is
// resumable from the paused node once credits are topped up. Distinct from
// OutcomeFail so tooling can offer resume instead of a from-scratch redo (#487).
const OutcomePausedBilling TerminalStatus = "paused_billing"

// PauseError wraps a handler error to signal that the run should stop in a
// recoverable PAUSED terminal state (checkpointed + resumable) rather than
// terminal-fail. The handler layer — which can classify provider errors —
// constructs it; the engine core dispatches on it without needing to know how
// the classification was made. Status is the paused terminal to emit (e.g.
// OutcomePausedBilling).
type PauseError struct {
	Status TerminalStatus
	Err    error
}

func (e *PauseError) Error() string {
	if e.Err == nil {
		return string(e.Status)
	}
	return e.Err.Error()
}

func (e *PauseError) Unwrap() error { return e.Err }

// NewPauseError wraps err as a recoverable pause with the given terminal status.
func NewPauseError(status TerminalStatus, err error) *PauseError {
	return &PauseError{Status: status, Err: err}
}

// asPauseError extracts a *PauseError from anywhere in err's chain.
func asPauseError(err error) (*PauseError, bool) {
	var pe *PauseError
	if errors.As(err, &pe) {
		return pe, true
	}
	return nil, false
}

// haltForPause produces the terminal loopResult for a recoverable pause. It
// mirrors haltForBudget: the checkpoint was already saved by handleNodeError
// (with the paused node NOT marked completed, so a resume re-runs it) and
// in-flight work preserved; this emits the paused terminal event and packages an
// EngineResult carrying the paused status. The billing error is returned on the
// loopResult so the CLI/summary can classify and surface it (💳 + remediation);
// it is a graceful terminal, not a crash.
func (e *Engine) haltForPause(s *runState, nodeID string, pe *PauseError, workPreserveFailed bool) loopResult {
	e.emit(PipelineEvent{
		Type:           EventBillingPaused,
		Timestamp:      time.Now(),
		RunID:          s.runID,
		NodeID:         nodeID,
		Message:        pe.Error(),
		Err:            pe.Err,
		TerminalStatus: string(pe.Status),
	})
	s.terminalEmitted = true // the paused event is terminal; don't let the backstop double-emit
	result := &EngineResult{
		RunID:               s.runID,
		Status:              pe.Status,
		CompletedNodes:      s.cp.CompletedNodes,
		Context:             s.pctx.Snapshot(),
		Trace:               s.trace,
		Usage:               s.trace.AggregateUsage(),
		WorkPreserveFailed:  workPreserveFailed,
		ValidationOverrides: append([]OverrideDetail(nil), s.validationOverrides...),
	}
	return loopResult{action: loopReturn, result: result, err: pe.Err}
}
