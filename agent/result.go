// ABOUTME: SessionResult captures the outcome of a completed agent session.
// ABOUTME: Tracks turns, tool calls, file changes, token usage, and provides pretty-print formatting.
package agent

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/2389-research/mammoth-lite/llm"
)

// SessionResult holds summary statistics and metadata from a completed session.
type SessionResult struct {
	SessionID     string
	Duration      time.Duration
	Turns         int
	ToolCalls     map[string]int
	FilesModified []string
	FilesCreated  []string
	Usage         llm.Usage
	Error         error
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

	var toolParts []string
	keys := make([]string, 0, len(r.ToolCalls))
	for k := range r.ToolCalls {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		toolParts = append(toolParts, fmt.Sprintf("%s: %d", k, r.ToolCalls[k]))
	}
	fmt.Fprintf(&b, "Turns: %d | Tool calls: %d", r.Turns, r.TotalToolCalls())
	if len(toolParts) > 0 {
		fmt.Fprintf(&b, " (%s)", strings.Join(toolParts, ", "))
	}
	b.WriteString("\n")

	if len(r.FilesModified) > 0 {
		fmt.Fprintf(&b, "Files modified: %s\n", strings.Join(r.FilesModified, ", "))
	}
	if len(r.FilesCreated) > 0 {
		fmt.Fprintf(&b, "Files created: %s\n", strings.Join(r.FilesCreated, ", "))
	}

	fmt.Fprintf(&b, "Tokens: %d (in: %d, out: %d)",
		r.Usage.TotalTokens, r.Usage.InputTokens, r.Usage.OutputTokens)
	if r.Usage.EstimatedCost > 0 {
		fmt.Fprintf(&b, " | Cost: $%.2f", r.Usage.EstimatedCost)
	}
	b.WriteString("\n")

	return b.String()
}
