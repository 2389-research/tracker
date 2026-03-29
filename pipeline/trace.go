// ABOUTME: Structured execution trace recording for pipeline runs.
// ABOUTME: Captures node execution timing, handler outcomes, edge selections, and errors.
package pipeline

import (
	"fmt"
	"strings"
	"time"
)

// SessionStats captures agent session metrics for a pipeline node.
// Only populated for codergen (LLM agent) nodes.
type SessionStats struct {
	Turns          int
	ToolCalls      map[string]int
	TotalToolCalls int
	FilesModified  []string
	FilesCreated   []string
	Compactions    int
	LongestTurn    time.Duration
	CacheHits      int
	CacheMisses    int
	InputTokens    int     `json:"input_tokens"`
	OutputTokens   int     `json:"output_tokens"`
	TotalTokens    int     `json:"total_tokens"`
	CostUSD        float64 `json:"cost_usd"`
}

// TraceEntry records the execution of a single pipeline node.
type TraceEntry struct {
	Timestamp   time.Time
	NodeID      string
	HandlerName string
	Status      string
	Duration    time.Duration
	EdgeTo      string
	Error       string
	Stats       *SessionStats // nil for non-agent nodes
}

// Trace captures the full execution history of a pipeline run.
type Trace struct {
	RunID     string
	Entries   []TraceEntry
	StartTime time.Time
	EndTime   time.Time
}

// AddEntry appends a trace entry to the trace log.
func (tr *Trace) AddEntry(entry TraceEntry) {
	tr.Entries = append(tr.Entries, entry)
}

// UsageSummary aggregates token usage and cost across all pipeline nodes.
type UsageSummary struct {
	TotalInputTokens  int     `json:"total_input_tokens"`
	TotalOutputTokens int     `json:"total_output_tokens"`
	TotalTokens       int     `json:"total_tokens"`
	TotalCostUSD      float64 `json:"total_cost_usd"`
	SessionCount      int     `json:"session_count"`
}

// AggregateUsage sums token usage and cost from all trace entries with session stats.
func (tr *Trace) AggregateUsage() *UsageSummary {
	if tr == nil {
		return nil
	}
	s := &UsageSummary{}
	for _, e := range tr.Entries {
		if e.Stats == nil {
			continue
		}
		s.TotalInputTokens += e.Stats.InputTokens
		s.TotalOutputTokens += e.Stats.OutputTokens
		s.TotalTokens += e.Stats.TotalTokens
		s.TotalCostUSD += e.Stats.CostUSD
		s.SessionCount++
	}
	if s.SessionCount == 0 {
		return nil
	}
	return s
}

// Summary returns a human-readable summary of the trace.
func (tr *Trace) Summary() string {
	var b strings.Builder

	totalDuration := tr.EndTime.Sub(tr.StartTime)
	fmt.Fprintf(&b, "Trace: run=%s entries=%d duration=%s\n", tr.RunID, len(tr.Entries), totalDuration)

	for i, e := range tr.Entries {
		line := fmt.Sprintf("  [%d] node=%s handler=%s status=%s duration=%s",
			i, e.NodeID, e.HandlerName, e.Status, e.Duration)
		if e.EdgeTo != "" {
			line += fmt.Sprintf(" -> %s", e.EdgeTo)
		}
		if e.Error != "" {
			line += fmt.Sprintf(" error=%q", e.Error)
		}
		fmt.Fprintln(&b, line)
	}

	return b.String()
}
