// ABOUTME: Checkpoint-generation scoping for the validation-override flip-point dedup (#279).
// ABOUTME: Restricts the (gate,label) idempotency check to the current run generation.
package pipeline

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
