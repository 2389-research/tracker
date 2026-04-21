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
	Turns            int            `json:"turns"`
	ToolCalls        map[string]int `json:"tool_calls,omitempty"`
	TotalToolCalls   int            `json:"total_tool_calls"`
	FilesModified    []string       `json:"files_modified,omitempty"`
	FilesCreated     []string       `json:"files_created,omitempty"`
	Compactions      int            `json:"compactions"`
	LongestTurn      time.Duration  `json:"longest_turn"`
	CacheHits        int            `json:"cache_hits"`
	CacheMisses      int            `json:"cache_misses"`
	InputTokens      int            `json:"input_tokens"`
	OutputTokens     int            `json:"output_tokens"`
	TotalTokens      int            `json:"total_tokens"`
	CostUSD          float64        `json:"cost_usd"`
	ReasoningTokens  int            `json:"reasoning_tokens"`
	CacheReadTokens  int            `json:"cache_read_tokens"`
	CacheWriteTokens int            `json:"cache_write_tokens"`
	Provider         string         `json:"provider,omitempty"`
}

// TraceEntry records the execution of a single pipeline node.
type TraceEntry struct {
	Timestamp   time.Time     `json:"timestamp"`
	NodeID      string        `json:"node_id"`
	HandlerName string        `json:"handler_name"`
	Status      string        `json:"status"`
	Duration    time.Duration `json:"duration"`
	EdgeTo      string        `json:"edge_to,omitempty"`
	Error       string        `json:"error,omitempty"`
	Stats       *SessionStats `json:"stats,omitempty"`
}

// Trace captures the full execution history of a pipeline run.
type Trace struct {
	RunID     string       `json:"run_id"`
	Entries   []TraceEntry `json:"entries"`
	StartTime time.Time    `json:"start_time"`
	EndTime   time.Time    `json:"end_time"`
}

// AddEntry appends a trace entry to the trace log.
func (tr *Trace) AddEntry(entry TraceEntry) {
	tr.Entries = append(tr.Entries, entry)
}

// ProviderUsage is the per-provider rollup embedded in UsageSummary.
type ProviderUsage struct {
	InputTokens      int     `json:"input_tokens"`
	OutputTokens     int     `json:"output_tokens"`
	TotalTokens      int     `json:"total_tokens"`
	CostUSD          float64 `json:"cost_usd"`
	ReasoningTokens  int     `json:"reasoning_tokens"`
	CacheReadTokens  int     `json:"cache_read_tokens"`
	CacheWriteTokens int     `json:"cache_write_tokens"`
	SessionCount     int     `json:"session_count"`
}

const unknownProvider = "unknown"

// UsageSummary aggregates token usage and cost across all pipeline nodes.
type UsageSummary struct {
	TotalInputTokens      int                      `json:"total_input_tokens"`
	TotalOutputTokens     int                      `json:"total_output_tokens"`
	TotalTokens           int                      `json:"total_tokens"`
	TotalCostUSD          float64                  `json:"total_cost_usd"`
	TotalReasoningTokens  int                      `json:"total_reasoning_tokens"`
	TotalCacheReadTokens  int                      `json:"total_cache_read_tokens"`
	TotalCacheWriteTokens int                      `json:"total_cache_write_tokens"`
	SessionCount          int                      `json:"session_count"`
	ProviderTotals        map[string]ProviderUsage `json:"provider_totals,omitempty"`
}

// AggregateUsage sums token usage and cost from all trace entries with session stats.
func (tr *Trace) AggregateUsage() *UsageSummary {
	if tr == nil {
		return nil
	}
	s := &UsageSummary{ProviderTotals: make(map[string]ProviderUsage)}
	for _, e := range tr.Entries {
		if e.Stats == nil {
			continue
		}
		s.TotalInputTokens += e.Stats.InputTokens
		s.TotalOutputTokens += e.Stats.OutputTokens
		s.TotalTokens += e.Stats.TotalTokens
		s.TotalCostUSD += e.Stats.CostUSD
		s.TotalReasoningTokens += e.Stats.ReasoningTokens
		s.TotalCacheReadTokens += e.Stats.CacheReadTokens
		s.TotalCacheWriteTokens += e.Stats.CacheWriteTokens
		s.SessionCount++

		provider := strings.TrimSpace(e.Stats.Provider)
		if provider == "" {
			provider = unknownProvider
		}
		pt := s.ProviderTotals[provider]
		pt.InputTokens += e.Stats.InputTokens
		pt.OutputTokens += e.Stats.OutputTokens
		pt.TotalTokens += e.Stats.TotalTokens
		pt.CostUSD += e.Stats.CostUSD
		pt.ReasoningTokens += e.Stats.ReasoningTokens
		pt.CacheReadTokens += e.Stats.CacheReadTokens
		pt.CacheWriteTokens += e.Stats.CacheWriteTokens
		pt.SessionCount++
		s.ProviderTotals[provider] = pt
	}
	if s.SessionCount == 0 {
		return nil
	}
	return s
}

// AggregateToolCalls sums tool call counts from all trace entries with session stats.
func (t *Trace) AggregateToolCalls() map[string]int {
	if t == nil {
		return nil
	}
	calls := make(map[string]int)
	for _, entry := range t.Entries {
		if entry.Stats == nil {
			continue
		}
		for name, count := range entry.Stats.ToolCalls {
			calls[name] += count
		}
	}
	return calls
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
