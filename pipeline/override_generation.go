// ABOUTME: Checkpoint-generation scoping for the validation-override flip-point dedup (#279).
// ABOUTME: Restricts the (gate,label) idempotency check to the current run generation.
package pipeline

import (
	"fmt"
	"slices"
	"time"
)

// applyChildOverrides records overrides propagated up from a child run
// (subgraph/manager_loop) into the parent's sticky + checkpoint lists, emits an
// audit event per override, then persists synchronously so a kill between here
// and the next selectEdge cannot lose the propagated state. This is the SECOND
// sticky-write site (the first is the own-graph flip-point in
// recordOverrideIfPresent). Child entries carry a non-empty SubgraphPath and
// never collide with own-graph entries, so the flip-point dedup does not apply —
// but this checkpoint saves BEFORE the subgraph node is marked completed, so a
// resume re-executes the node and re-propagates the same override;
// priorGenChildOverrideExists skips one already carried over from the resumed
// checkpoint without suppressing a genuinely new re-occurrence (#535).
func (e *Engine) applyChildOverrides(s *runState, currentNodeID string, overrides []OverrideDetail) {
	if len(overrides) == 0 {
		return
	}
	for i := range overrides {
		d := overrides[i]
		if s.priorGenChildOverrideExists(d) {
			continue
		}
		s.appendOverride(d)
		e.emit(PipelineEvent{
			Type:      EventValidationOverridden,
			Timestamp: time.Now(),
			RunID:     s.runID,
			NodeID:    currentNodeID,
			Message:   fmt.Sprintf("validation override propagated from subgraph child via %q", currentNodeID),
			Override:  &d,
		})
	}
	e.saveCheckpointWithTag(s.cp, s.pctx, s.runID, s, currentNodeID)
}

// appendOverride appends an OverrideDetail to BOTH the in-memory hot-path
// slice (s.validationOverrides) and the checkpoint slice (s.cp.ValidationOverrides).
// They MUST stay in sync — the hot-path slice serves the engine's terminal-status
// rule and event-emission; the checkpoint slice is the durable record for resume
// and audit-log fallback. Any code path that records a new override must use
// this helper.
func (s *runState) appendOverride(d OverrideDetail) {
	s.validationOverrides = append(s.validationOverrides, d)
	s.cp.ValidationOverrides = append(s.cp.ValidationOverrides, d)
}

// priorGenChildOverrideExists reports whether an identical child override (same
// gate, label, and subgraph path) was carried over from the resumed checkpoint
// — i.e. it sits in the seeded prior-generation window (index < baseline). A
// subgraph node's override checkpoint saves before the node is marked completed,
// so a resume re-executes the node and re-propagates the same child override;
// this guard skips that resume artifact without suppressing a genuinely new
// re-occurrence in the current generation (#535).
func (s *runState) priorGenChildOverrideExists(d OverrideDetail) bool {
	limit := s.overrideGenerationBaseline
	if limit > len(s.validationOverrides) {
		limit = len(s.validationOverrides)
	}
	for i := 0; i < limit; i++ {
		e := s.validationOverrides[i]
		if e.GateNodeID == d.GateNodeID && e.Label == d.Label && slices.Equal(e.SubgraphPath, d.SubgraphPath) {
			return true
		}
	}
	return false
}

// currentGenerationOverrides returns the sub-slice of validationOverrides
// recorded in THIS checkpoint generation (entries at index >=
// overrideGenerationBaseline). The flip-point dedup scans only this window so
// a re-occurrence of the same (gate, label) across a resume/restart is
// recorded rather than suppressed as a prior-generation duplicate (#279). The
// returned slice aliases the backing array and is read-only for the caller.
func (s *runState) currentGenerationOverrides() []OverrideDetail {
	if s.overrideGenerationBaseline >= len(s.validationOverrides) {
		return nil
	}
	return s.validationOverrides[s.overrideGenerationBaseline:]
}
