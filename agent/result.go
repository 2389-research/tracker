// ABOUTME: SessionResult captures the outcome of a completed agent session.
// ABOUTME: Tracks turns, tool calls, file changes, token usage, and provides pretty-print formatting.
package agent

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/2389-research/tracker/llm"
)

// SessionResult holds summary statistics and metadata from a completed session.
type SessionResult struct {
	SessionID          string
	Duration           time.Duration
	Turns              int
	MaxTurnsUsed       bool
	LoopDetected       bool
	ToolCalls          map[string]int
	FilesModified      []string
	FilesCreated       []string
	Usage              llm.Usage
	ContextUtilization float64
	ToolCacheHits      int
	ToolCacheMisses    int
	ToolTimings        map[string]time.Duration
	CompactionsApplied int
	LongestTurn        time.Duration
	Error              error
}

// TotalToolCalls returns the sum of all tool call counts.
func (r SessionResult) TotalToolCalls() int {
	total := 0
	for _, count := range r.ToolCalls {
		total += count
	}
	return total
}

// String returns a human-readable summary of the session result.
func (r SessionResult) String() string {
	var b strings.Builder

	status := "completed"
	if r.Error != nil {
		status = "failed"
	}
	fmt.Fprintf(&b, "Session %s %s in %s\n", r.SessionID, status, r.Duration.Round(time.Second))

	writeToolCallSummary(&b, r)
	writeFileSummary(&b, r)
	writeTokenSummary(&b, r)
	writeExtrasLine(&b, r)

	return b.String()
}

// writeToolCallSummary appends the turns and tool call breakdown line.
func writeToolCallSummary(b *strings.Builder, r SessionResult) {
	toolParts := sortedToolCallParts(r.ToolCalls)
	fmt.Fprintf(b, "Turns: %d | Tool calls: %d", r.Turns, r.TotalToolCalls())
	if len(toolParts) > 0 {
		fmt.Fprintf(b, " (%s)", strings.Join(toolParts, ", "))
	}
	b.WriteString("\n")
}

// sortedToolCallParts returns "name: count" parts sorted by name.
func sortedToolCallParts(calls map[string]int) []string {
	keys := make([]string, 0, len(calls))
	for k := range calls {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s: %d", k, calls[k]))
	}
	return parts
}

// writeFileSummary appends modified and created files lines.
func writeFileSummary(b *strings.Builder, r SessionResult) {
	if len(r.FilesModified) > 0 {
		fmt.Fprintf(b, "Files modified: %s\n", strings.Join(r.FilesModified, ", "))
	}
	if len(r.FilesCreated) > 0 {
		fmt.Fprintf(b, "Files created: %s\n", strings.Join(r.FilesCreated, ", "))
	}
}

// writeTokenSummary appends the token/cost line.
func writeTokenSummary(b *strings.Builder, r SessionResult) {
	fmt.Fprintf(b, "Tokens: %d (in: %d, out: %d)",
		r.Usage.TotalTokens, r.Usage.InputTokens, r.Usage.OutputTokens)
	if r.Usage.EstimatedCost > 0 {
		fmt.Fprintf(b, " | Cost: $%.2f", r.Usage.EstimatedCost)
	}
	b.WriteString("\n")
}

// writeExtrasLine appends compaction and longest-turn info if present.
func writeExtrasLine(b *strings.Builder, r SessionResult) {
	var extras []string
	if r.CompactionsApplied > 0 {
		extras = append(extras, fmt.Sprintf("Compactions: %d", r.CompactionsApplied))
	}
	if r.LongestTurn > 0 {
		extras = append(extras, fmt.Sprintf("Longest turn: %s", r.LongestTurn.Round(time.Second)))
	}
	if len(extras) > 0 {
		fmt.Fprintf(b, "%s\n", strings.Join(extras, " | "))
	}
}
