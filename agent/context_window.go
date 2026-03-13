// ABOUTME: Tracks context window utilization using the latest turn's input token count against a limit.
// ABOUTME: Provides utilization calculation and one-shot warning when approaching the limit.
package agent

import "github.com/2389-research/tracker/llm"

// ContextWindowTracker monitors context window utilization against a configured limit.
// InputTokens from each LLM response already represents the full conversation context
// for that turn, so utilization reflects the latest input token count, not a cumulative sum.
type ContextWindowTracker struct {
	Limit            int
	WarningThreshold float64
	latestInputTokens int
	WarningEmitted   bool
}

// NewContextWindowTracker creates a tracker with the given token limit and warning threshold.
// The threshold is a fraction (e.g. 0.8 means warn at 80% utilization).
func NewContextWindowTracker(limit int, threshold float64) *ContextWindowTracker {
	return &ContextWindowTracker{
		Limit:            limit,
		WarningThreshold: threshold,
	}
}

// Update records the latest turn's input token count for utilization tracking.
// InputTokens from the LLM provider already includes the full conversation context.
func (t *ContextWindowTracker) Update(usage llm.Usage) {
	t.latestInputTokens = usage.InputTokens
}

// Utilization returns the fraction of the context window currently consumed,
// based on the latest turn's input token count.
func (t *ContextWindowTracker) Utilization() float64 {
	if t.Limit == 0 {
		return 0
	}
	return float64(t.latestInputTokens) / float64(t.Limit)
}

// ShouldWarn returns true if utilization meets or exceeds the warning threshold
// and a warning has not yet been emitted for this session.
func (t *ContextWindowTracker) ShouldWarn() bool {
	return !t.WarningEmitted && t.Utilization() >= t.WarningThreshold
}

// MarkWarned records that the warning has been emitted, preventing further warnings.
func (t *ContextWindowTracker) MarkWarned() {
	t.WarningEmitted = true
}
