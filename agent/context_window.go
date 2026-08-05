// ABOUTME: Tracks context window utilization using the latest turn's input token count against a limit.
// ABOUTME: Provides utilization calculation and one-shot warning when approaching the limit.
package agent

import "github.com/2389-research/tracker/llm"

// ContextWindowTracker monitors context window utilization against a configured limit.
// Each LLM response's PROCESSED context for that turn is InputTokens plus the
// cache buckets (cache-read + cache-write), which llm.Usage keeps additive and
// separate from InputTokens; utilization reflects that latest total, not a
// cumulative sum across turns.
type ContextWindowTracker struct {
	Limit             int
	WarningThreshold  float64
	latestInputTokens int
	WarningEmitted    bool
}

// NewContextWindowTracker creates a tracker with the given token limit and warning threshold.
// The threshold is a fraction (e.g. 0.8 means warn at 80% utilization).
func NewContextWindowTracker(limit int, threshold float64) *ContextWindowTracker {
	return &ContextWindowTracker{
		Limit:            limit,
		WarningThreshold: threshold,
	}
}

// Update records the latest turn's PROCESSED context size for utilization
// tracking. This is InputTokens PLUS the cache buckets: llm.Usage keeps
// cache-read and cache-write tokens additive and separate from InputTokens, so
// with prompt caching on (the default for Anthropic) a nearly-full window can
// report a tiny InputTokens while the cached prefix lands in CacheReadTokens.
// Counting only InputTokens made utilization read ~0 and silently disabled
// compaction and the context-window warning (#539).
func (t *ContextWindowTracker) Update(usage llm.Usage) {
	t.latestInputTokens = usage.InputTokens + intVal(usage.CacheReadTokens) + intVal(usage.CacheWriteTokens)
}

// intVal dereferences an optional token count, treating nil as zero.
func intVal(p *int) int {
	if p == nil {
		return 0
	}
	return *p
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
