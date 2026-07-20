// ABOUTME: Terminal pipeline-event emission — guarantees every run exit carries a TerminalStatus.
// ABOUTME: Split from engine.go so those hot files stay under the complexity size ceiling.
package pipeline

import "time"

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
	if result == nil || s.terminalEmitted {
		return
	}
	evtType := EventPipelineFailed
	if result.Status.IsSuccess() {
		evtType = EventPipelineCompleted
	}
	e.emit(PipelineEvent{
		Type:           evtType,
		Timestamp:      time.Now(),
		RunID:          s.runID,
		Message:        "pipeline halted",
		TerminalStatus: string(result.Status),
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
