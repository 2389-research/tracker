// ABOUTME: Trace + event bookkeeping for a node whose handler returned an error —
// ABOUTME: a failure gets stage_failed; a pause is traced as paused with no event.
package pipeline

import (
	"fmt"
	"time"
)

// recordHandlerError traces a handler error and emits stage_failed for it. A
// pause is waiting, not failed: it is traced as "paused" and gets no
// stage_failed, since its paused terminal event is the only end-of-run signal.
func (e *Engine) recordHandlerError(s *runState, nodeID string, traceEntry *TraceEntry, outcome Outcome, err error) {
	traceEntry.Status = "error"
	traceEntry.Error = err.Error()
	// Preserve ChildUsage even on handler error so that cancelled child
	// runs (e.g. manager_loop ctx-cancellation) still contribute their
	// accumulated spend to the parent trace's AggregateUsage and
	// BudgetGuard rollup. Without this, any handler that returns both a
	// non-nil ChildUsage and a non-nil error (e.g. cancellation path)
	// would silently drop the child's token/cost data from the parent.
	traceEntry.ChildUsage = outcome.ChildUsage
	if _, paused := asPauseError(err); paused {
		traceEntry.Status = "paused"
		s.trace.AddEntry(*traceEntry)
		return
	}
	s.trace.AddEntry(*traceEntry)
	e.emit(PipelineEvent{
		Type:      EventStageFailed,
		Timestamp: time.Now(),
		RunID:     s.runID,
		NodeID:    nodeID,
		Message:   fmt.Sprintf("handler error at node %q", nodeID),
		Err:       err,
	})
}
