// ABOUTME: Pretty-print formatting for Response and Usage types via fmt.Stringer.
// ABOUTME: Produces concise, human-readable output for CLI and TUI display.
package llm

import "fmt"

// String formats a Response as a concise, human-readable summary.
func (r Response) String() string {
	cacheInfo := ""
	if r.Usage.CacheReadTokens != nil && *r.Usage.CacheReadTokens > 0 {
		cacheInfo = fmt.Sprintf(" (cache: %d read", *r.Usage.CacheReadTokens)
		if r.Usage.CacheWriteTokens != nil && *r.Usage.CacheWriteTokens > 0 {
			cacheInfo += fmt.Sprintf(", %d write", *r.Usage.CacheWriteTokens)
		}
		cacheInfo += ")"
	}

	provider := r.Provider
	if provider == "" {
		provider = "unknown"
	}

	line1 := fmt.Sprintf("[%s/%s] %d tokens in, %d out%s",
		provider, r.Model,
		r.Usage.InputTokens, r.Usage.OutputTokens,
		cacheInfo)

	line2 := fmt.Sprintf("Cost: $%.3f | Latency: %s | Finish: %s",
		r.Usage.EstimatedCost,
		formatLatency(r.Latency),
		r.FinishReason.Reason)

	return line1 + "\n" + line2
}

// String formats Usage as a concise token summary.
func (u Usage) String() string {
	s := fmt.Sprintf("%d in, %d out", u.InputTokens, u.OutputTokens)

	if u.CacheReadTokens != nil && *u.CacheReadTokens > 0 {
		s += fmt.Sprintf(" (cache: %d read)", *u.CacheReadTokens)
	}

	if u.EstimatedCost > 0 {
		s += fmt.Sprintf(" | $%.3f", u.EstimatedCost)
	}

	return s
}

// formatLatency formats a duration for display, picking the right unit.
func formatLatency(d interface{ Seconds() float64 }) string {
	sec := d.Seconds()
	if sec < 1 {
		return fmt.Sprintf("%.0fms", sec*1000)
	}
	return fmt.Sprintf("%.1fs", sec)
}
