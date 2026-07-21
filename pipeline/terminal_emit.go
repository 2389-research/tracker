// ABOUTME: Terminal pipeline-event emission — guarantees every run exit carries a TerminalStatus.
// ABOUTME: Split from engine.go so those hot files stay under the complexity size ceiling.
package pipeline

import (
	"fmt"
	"runtime/debug"
	"time"
)

// newFailResult builds the canonical terminal-fail EngineResult snapshot shared
// by the engine's fail exits (retry-target error, panic recovery). Callers may
// set extra fields (WorkPreserveFailed, BudgetLimitsHit) on the returned value.
func (e *Engine) newFailResult(s *runState) *EngineResult {
	return &EngineResult{
		RunID:               s.runID,
		Status:              OutcomeFail,
		CompletedNodes:      s.cp.CompletedNodes,
		Context:             s.pctx.Snapshot(),
		Trace:               s.trace,
		Usage:               s.trace.AggregateUsage(),
		ValidationOverrides: append([]OverrideDetail(nil), s.validationOverrides...),
	}
}

// recoverPanic converts a panic on the run goroutine into a terminal fail
// result and event, so one panicking run never crashes the host process — a
// RunManager drives many runs from one process (Phase 0). The panic value and
// stack are surfaced in the returned error and the failed event, never
// swallowed.
func (e *Engine) recoverPanic(s *runState, r any) (*EngineResult, error) {
	err := fmt.Errorf("panic in pipeline run: %v\n%s", r, debug.Stack())
	if s == nil {
		return nil, err
	}
	s.trace.EndTime = time.Now()
	if !s.terminalEmitted {
		e.emitFailed(s, fmt.Sprintf("panic: %v", r), err)
	}
	// Return a nil result deliberately: after a panic mid-node the run state may
	// be inconsistent, and consumers must be able to tell a panic-crash apart
	// from a graceful fail Result they can route on (e.g. manager_loop treats a
	// child crash as an unroutable hard error, a graceful child fail as a
	// routable outcome). The terminal fail event was already emitted above, so a
	// stream-only subscriber still observes the run finishing.
	return nil, err
}

// emitFailed emits the terminal EventPipelineFailed event stamped with the fail
// terminal status, and marks the run's terminal event as emitted. Every
// non-budget failure exit that has a message routes through here so the
// authoritative status is set in exactly one place.
func (e *Engine) emitFailed(s *runState, msg string, err error) {
	e.emit(PipelineEvent{
		Type: EventPipelineFailed, Timestamp: time.Now(), RunID: s.runID,
		Message: msg, Err: err, TerminalStatus: string(OutcomeFail),
	})
	s.terminalEmitted = true
}

// emitTerminalBackstop guarantees the terminal-status contract: every terminal
// exit emits exactly one event carrying TerminalStatus. Per-path emits set
// s.terminalEmitted; this catches terminal exits that returned a result without
// emitting one (the strict-failure halt, invariant errors) and emits a final
// completed/failed event stamped with the result's status.
func (e *Engine) emitTerminalBackstop(s *runState, result *EngineResult) {
	if s.terminalEmitted {
		return
	}
	// A nil result means a terminal exit that returned (nil, err) — an invariant
	// error (node not found, no outgoing edges, unresolved edge condition) raised
	// after pipeline_started already fired. Emit a fail terminal event anyway so
	// a stream-only subscriber still sees the run finish (Phase 0); before this,
	// such exits emitted no TerminalStatus and a Slack thread would hang forever.
	status := string(OutcomeFail)
	evtType := EventPipelineFailed
	if result != nil {
		status = string(result.Status)
		if result.Status.IsSuccess() {
			evtType = EventPipelineCompleted
		}
	}
	e.emit(PipelineEvent{
		Type:           evtType,
		Timestamp:      time.Now(),
		RunID:          s.runID,
		Message:        "pipeline halted",
		TerminalStatus: status,
	})
	s.terminalEmitted = true
}

// haltForBudget produces the terminal loopResult emitted when a BudgetGuard
// trips. It saves the checkpoint (so restarts skip already-completed nodes),
// sets the trace end time, emits EventBudgetExceeded with the same combined
// usage snapshot the guard used to detect the breach (so diagnostics report
// the actual trigger value, not a child-local sub-total that sits below the
// ceiling), and packages an EngineResult with Status=OutcomeBudgetExceeded.
//
// EngineResult.Usage intentionally holds the child-local aggregate only,
// not the combined snapshot. The subgraph handler copies this onto
// Outcome.ChildUsage and the parent trace's AggregateUsage folds it back
// in; using the combined value here would double-count the parent's own
// spend once the parent aggregates a second time.
func (e *Engine) haltForBudget(s *runState, breach BudgetBreach) loopResult {
	e.saveCheckpoint(s.cp, s.pctx, s.runID)
	s.trace.EndTime = time.Now()
	combined := e.combinedUsageForBudget(s)
	var costSnap *CostSnapshot
	if combined != nil {
		costSnap = &CostSnapshot{
			TotalTokens:    combined.TotalTokens,
			TotalCostUSD:   combined.TotalCostUSD,
			ProviderTotals: combined.ProviderTotals,
			WallElapsed:    time.Since(s.trace.StartTime),
			Estimated:      combined.Estimated,
		}
	}
	e.emit(PipelineEvent{
		Type:           EventBudgetExceeded,
		Timestamp:      time.Now(),
		RunID:          s.runID,
		Message:        breach.Message,
		Cost:           costSnap,
		TerminalStatus: string(OutcomeBudgetExceeded),
	})
	s.terminalEmitted = true // budget_exceeded is the terminal event; don't let the Run backstop double-emit
	return loopResult{
		action: loopReturn,
		result: &EngineResult{
			RunID:               s.runID,
			Status:              OutcomeBudgetExceeded,
			CompletedNodes:      s.cp.CompletedNodes,
			Context:             s.pctx.Snapshot(),
			Trace:               s.trace,
			Usage:               s.trace.AggregateUsage(),
			BudgetLimitsHit:     []string{breach.Kind.String()},
			ValidationOverrides: append([]OverrideDetail(nil), s.validationOverrides...),
		},
	}
}
