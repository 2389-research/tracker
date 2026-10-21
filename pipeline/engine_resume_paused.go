// ABOUTME: Resume preparation for a run paused at a human gate: its context is
// ABOUTME: restored as-is (no compaction) and its budget continues across answers.
package pipeline

// prepareResumeContext readies the merged checkpoint context for the resumed
// segment. A run waiting at a gate is restored without compaction — nothing
// failed, and a degraded context would re-render a different prompt, so the
// answer would never match the question — and its budget baseline is seeded
// from the usage recorded at the pause, so max_cost / token caps bound the
// whole run rather than each answer. Every other resume compacts as before.
func (e *Engine) prepareResumeContext(cp *Checkpoint, pctx *PipelineContext, runID string) {
	if len(cp.PausedGates) == 0 {
		e.compactResumeContext(cp, pctx, runID)
		return
	}
	if cp.PriorUsage != nil && e.baselineUsage == nil {
		e.baselineUsage = cloneUsageSummary(cp.PriorUsage)
	}
}
