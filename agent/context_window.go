// ABOUTME: Tracks cumulative token usage within an agent session against a context window limit.
// ABOUTME: Provides utilization calculation and one-shot warning when approaching the limit.
package agent

import "github.com/2389-research/mammoth-lite/llm"

// ContextWindowTracker monitors cumulative token usage against a configured limit.
// Token counts accumulate monotonically (never decrease) over the session lifetime.
type ContextWindowTracker struct {
	Limit            int
	WarningThreshold float64
	CurrentTokens    int
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

// Update adds the input and output tokens from a single LLM response to the running total.
func (t *ContextWindowTracker) Update(usage llm.Usage) {
	t.CurrentTokens += usage.InputTokens + usage.OutputTokens
}

// Utilization returns the fraction of the context window currently consumed.
func (t *ContextWindowTracker) Utilization() float64 {
	if t.Limit == 0 {
		return 0
	}
	return float64(t.CurrentTokens) / float64(t.Limit)
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
