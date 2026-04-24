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
	// Estimated is true when the token/cost numbers come from a heuristic
	// rather than metered usage (e.g. the ACP backend's rune-count
	// estimator). Downstream consumers — CLI summary, TUI header, tracker
	// diagnose — use this to mark provider rollups with an "(estimated)"
	// suffix so operators don't confuse approximate spend with metered
	// spend. EstimateSource names the heuristic (e.g. "acp-chars-heuristic")
	// and is reserved for future per-source reporting. Derived in
	// buildSessionStats from llm.Usage.Raw.
	Estimated      bool   `json:"estimated,omitempty"`
	EstimateSource string `json:"estimate_source,omitempty"`
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
	// ChildUsage is the aggregated usage of a child run that executed under
	// this node (subgraph, manager_loop). Populated when the handler's
	// Outcome carries child-run totals so AggregateUsage can include them
	// in the parent's rollup. Omitted from JSON when nil.
	ChildUsage *UsageSummary `json:"child_usage,omitempty"`
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
// Estimated is true when at least one contributing session reported
// heuristic-derived usage (e.g. the ACP rune-count estimator). A mixed
// bucket — some sessions metered, some estimated — still flags Estimated
// so the operator knows the total is not fully trustworthy.
type ProviderUsage struct {
	InputTokens      int     `json:"input_tokens"`
	OutputTokens     int     `json:"output_tokens"`
	TotalTokens      int     `json:"total_tokens"`
	CostUSD          float64 `json:"cost_usd"`
	ReasoningTokens  int     `json:"reasoning_tokens"`
	CacheReadTokens  int     `json:"cache_read_tokens"`
	CacheWriteTokens int     `json:"cache_write_tokens"`
	SessionCount     int     `json:"session_count"`
	Estimated        bool    `json:"estimated,omitempty"`
}

const unknownProvider = "unknown"

// UsageSummary aggregates token usage and cost across all pipeline nodes.
// Estimated is true when any contributing session was heuristic-derived.
// CLI / TUI / diagnose surfaces render the run total with an "~" or
// "(estimated)" marker when this is set, so operators don't interpret
// mixed metered+estimated totals as fully metered.
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
	Estimated             bool                     `json:"estimated,omitempty"`
}

// AggregateUsage sums token usage and cost from all trace entries with
// session stats and any child-run rollups. Child usage is folded in so that
// subgraph/manager_loop spend is visible in the parent's budget snapshots
// and CLI summaries rather than disappearing into the child engine's own
// trace. Without this fold, BudgetGuard evaluating on the parent's trace
// could never see child spend and --max-tokens / --max-cost would be
// silently non-binding for any node that runs a child pipeline.
func (tr *Trace) AggregateUsage() *UsageSummary {
	if tr == nil {
		return nil
	}
	s := &UsageSummary{ProviderTotals: make(map[string]ProviderUsage)}
	for _, e := range tr.Entries {
		if e.Stats != nil {
			foldStatsIntoSummary(s, e.Stats)
		}
		if e.ChildUsage != nil {
			foldChildUsageIntoSummary(s, e.ChildUsage)
		}
	}
	if s.SessionCount == 0 {
		return nil
	}
	return s
}

// foldStatsIntoSummary adds a single node's SessionStats to the running
// totals and the matching per-provider bucket. Estimated propagates with
// OR semantics so any heuristic session "taints" both the provider bucket
// and the summary-level flag, letting downstream surfaces show an
// "(estimated)" marker on mixed metered+estimated runs.
func foldStatsIntoSummary(s *UsageSummary, stats *SessionStats) {
	s.TotalInputTokens += stats.InputTokens
	s.TotalOutputTokens += stats.OutputTokens
	s.TotalTokens += stats.TotalTokens
	s.TotalCostUSD += stats.CostUSD
	s.TotalReasoningTokens += stats.ReasoningTokens
	s.TotalCacheReadTokens += stats.CacheReadTokens
	s.TotalCacheWriteTokens += stats.CacheWriteTokens
	s.SessionCount++
	if stats.Estimated {
		s.Estimated = true
	}

	provider := strings.TrimSpace(stats.Provider)
	if provider == "" {
		provider = unknownProvider
	}
	pt := s.ProviderTotals[provider]
	pt.InputTokens += stats.InputTokens
	pt.OutputTokens += stats.OutputTokens
	pt.TotalTokens += stats.TotalTokens
	pt.CostUSD += stats.CostUSD
	pt.ReasoningTokens += stats.ReasoningTokens
	pt.CacheReadTokens += stats.CacheReadTokens
	pt.CacheWriteTokens += stats.CacheWriteTokens
	pt.SessionCount++
	if stats.Estimated {
		pt.Estimated = true
	}
	s.ProviderTotals[provider] = pt
}

// foldChildUsageIntoSummary adds a child run's pre-aggregated UsageSummary
// to the running totals, preserving per-provider attribution. SessionCount
// from the child contributes to the parent's SessionCount so rollups
// accurately reflect how many billable sessions participated. Estimated
// propagates with OR semantics (same rule as foldStatsIntoSummary) so a
// subgraph or manager_loop nesting estimated sessions taints the parent.
func foldChildUsageIntoSummary(s *UsageSummary, child *UsageSummary) {
	s.TotalInputTokens += child.TotalInputTokens
	s.TotalOutputTokens += child.TotalOutputTokens
	s.TotalTokens += child.TotalTokens
	s.TotalCostUSD += child.TotalCostUSD
	s.TotalReasoningTokens += child.TotalReasoningTokens
	s.TotalCacheReadTokens += child.TotalCacheReadTokens
	s.TotalCacheWriteTokens += child.TotalCacheWriteTokens
	s.SessionCount += child.SessionCount
	if child.Estimated {
		s.Estimated = true
	}

	for provider, cpu := range child.ProviderTotals {
		pt := s.ProviderTotals[provider]
		pt.InputTokens += cpu.InputTokens
		pt.OutputTokens += cpu.OutputTokens
		pt.TotalTokens += cpu.TotalTokens
		pt.CostUSD += cpu.CostUSD
		pt.ReasoningTokens += cpu.ReasoningTokens
		pt.CacheReadTokens += cpu.CacheReadTokens
		pt.CacheWriteTokens += cpu.CacheWriteTokens
		pt.SessionCount += cpu.SessionCount
		if cpu.Estimated {
			pt.Estimated = true
		}
		s.ProviderTotals[provider] = pt
	}
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
