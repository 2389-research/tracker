// ABOUTME: Run summary display — prints the end-of-pipeline report with stats,
// ABOUTME: per-node breakdown, token usage, pipeline graph, and resume hints.
package main

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/2389-research/tracker/llm"
	"github.com/2389-research/tracker/pipeline"
	"github.com/charmbracelet/lipgloss"
)

// aggregatedStats holds totals computed from all trace entries with SessionStats.
type aggregatedStats struct {
	TotalTurns        int
	TotalToolCalls    int
	ToolCallsByName   map[string]int
	FilesCreated      []string
	FilesModified     []string
	Compactions       int
	TotalInputTokens  int
	TotalOutputTokens int
	TotalTokens       int
	TotalCostUSD      float64
}

// aggregateSessionStats walks trace entries and sums up agent session metrics.
func aggregateSessionStats(entries []pipeline.TraceEntry) aggregatedStats {
	agg := aggregatedStats{
		ToolCallsByName: make(map[string]int),
	}
	seenCreated := make(map[string]bool)
	seenModified := make(map[string]bool)

	for _, entry := range entries {
		if entry.Stats == nil {
			continue
		}
		s := entry.Stats
		agg.TotalTurns += s.Turns
		agg.TotalToolCalls += s.TotalToolCalls
		agg.Compactions += s.Compactions
		agg.TotalInputTokens += s.InputTokens
		agg.TotalOutputTokens += s.OutputTokens
		agg.TotalTokens += s.TotalTokens
		agg.TotalCostUSD += s.CostUSD
		for name, count := range s.ToolCalls {
			agg.ToolCallsByName[name] += count
		}
		for _, f := range s.FilesCreated {
			if !seenCreated[f] {
				seenCreated[f] = true
				agg.FilesCreated = append(agg.FilesCreated, f)
			}
		}
		for _, f := range s.FilesModified {
			if !seenModified[f] {
				seenModified[f] = true
				agg.FilesModified = append(agg.FilesModified, f)
			}
		}
	}
	return agg
}

// formatToolBreakdown returns a parenthesized breakdown like "(bash: 198, write: 67)".
func formatToolBreakdown(toolCalls map[string]int) string {
	if len(toolCalls) == 0 {
		return ""
	}
	// Sort by count descending, then name ascending for stability.
	type toolCount struct {
		name  string
		count int
	}
	var sorted []toolCount
	for name, count := range toolCalls {
		sorted = append(sorted, toolCount{name, count})
	}
	slices.SortFunc(sorted, func(a, b toolCount) int {
		if a.count != b.count {
			return b.count - a.count // descending by count
		}
		if a.name < b.name {
			return -1
		}
		if a.name > b.name {
			return 1
		}
		return 0
	})
	var parts []string
	for _, tc := range sorted {
		parts = append(parts, fmt.Sprintf("%s: %d", tc.name, tc.count))
	}
	return "(" + strings.Join(parts, ", ") + ")"
}

// formatNumber adds comma separators to integers for readability.
func formatNumber(n int) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	s := fmt.Sprintf("%d", n)
	var result []byte
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			result = append(result, ',')
		}
		result = append(result, byte(c))
	}
	return string(result)
}

// formatElapsed formats a duration for the summary display.
func formatElapsed(d time.Duration) string {
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	if d < time.Minute {
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	minutes := int(d.Minutes())
	seconds := int(d.Seconds()) % 60
	return fmt.Sprintf("%dm%02ds", minutes, seconds)
}

// printRunSummary outputs a comprehensive run summary with the logo, aggregated
// session stats, per-node breakdown, token usage, and ASCII pipeline graph.
func printRunSummary(result *pipeline.EngineResult, pipelineErr error, tracker *llm.TokenTracker, pipelineFile string) {
	fmt.Println()

	// Logo
	fmt.Println(bannerStyle.Render(logo()))
	fmt.Println()

	// Header bar
	fmt.Println("═══ Run Complete ══════════════════════════════════════════")

	printRunHeader(result)
	printRunDuration(result)
	printRunTotals(result, tracker)
	printNodeTable(result)
	printTokensByProvider(tracker)
	printPipelineFlow(result)

	if pipelineErr != nil {
		fmt.Println()
		fmt.Printf("  ERROR: %v\n", pipelineErr)
	}

	printResumeHint(result, pipelineFile)

	fmt.Println("═══════════════════════════════════════════════════════════")
}

// printRunHeader prints the run ID and status lines.
func printRunHeader(result *pipeline.EngineResult) {
	if result == nil {
		return
	}
	fmt.Printf("  Run ID:    %s\n", result.RunID)

	statusIcon := "●"
	var statusText string
	switch result.Status {
	case pipeline.OutcomeSuccess:
		statusText = selectedStyle.Render(statusIcon + " success")
	case pipeline.OutcomeFail:
		statusText = lipgloss.NewStyle().Foreground(colorHot).Render(statusIcon + " fail")
	default:
		statusText = mutedStyle.Render(statusIcon + " " + result.Status)
	}
	fmt.Printf("  Status:    %s\n", statusText)
}

// printRunDuration prints the total elapsed time from trace.
func printRunDuration(result *pipeline.EngineResult) {
	if result == nil || result.Trace == nil {
		return
	}
	if result.Trace.StartTime.IsZero() || result.Trace.EndTime.IsZero() {
		return
	}
	elapsed := result.Trace.EndTime.Sub(result.Trace.StartTime)
	fmt.Printf("  Duration:  %s\n", formatElapsed(elapsed))
}

// printRunTotals prints the aggregated totals section (turns, tool calls, files, tokens).
func printRunTotals(result *pipeline.EngineResult, tracker *llm.TokenTracker) {
	if result == nil || result.Trace == nil || len(result.Trace.Entries) == 0 {
		return
	}
	agg := aggregateSessionStats(result.Trace.Entries)

	if agg.TotalTurns == 0 && agg.TotalToolCalls == 0 {
		return
	}
	fmt.Println()
	fmt.Println("─── Totals ────────────────────────────────────────────────")
	fmt.Printf("  LLM Turns:    %s\n", formatNumber(agg.TotalTurns))

	toolLine := fmt.Sprintf("  Tool Calls:   %s", formatNumber(agg.TotalToolCalls))
	if breakdown := formatToolBreakdown(agg.ToolCallsByName); breakdown != "" {
		toolLine += "  " + breakdown
	}
	fmt.Println(toolLine)

	if len(agg.FilesCreated) > 0 || len(agg.FilesModified) > 0 {
		fmt.Printf("  Files:        %d created, %d modified\n",
			len(agg.FilesCreated), len(agg.FilesModified))
	}
	if agg.Compactions > 0 {
		fmt.Printf("  Compactions:  %d\n", agg.Compactions)
	}

	printTotalTokens(tracker)
}

// printTotalTokens prints the inline token totals within the Totals section.
func printTotalTokens(tracker *llm.TokenTracker) {
	if tracker == nil {
		return
	}
	total := tracker.TotalUsage()
	if total.InputTokens == 0 && total.OutputTokens == 0 {
		return
	}
	tokenLine := fmt.Sprintf("  Tokens:       %s in / %s out",
		formatNumber(total.InputTokens), formatNumber(total.OutputTokens))
	if total.EstimatedCost > 0 {
		// When all usage is from claude-code (Max subscription), label as
		// usage estimate since Max is flat-rate, not pay-per-token.
		providers := tracker.Providers()
		if len(providers) == 1 && providers[0] == "claude-code" {
			tokenLine += fmt.Sprintf("  (~$%.2f usage)", total.EstimatedCost)
		} else {
			tokenLine += fmt.Sprintf("  ($%.2f)", total.EstimatedCost)
		}
	}
	fmt.Println(tokenLine)
}

// printNodeTable prints the per-node timing table with turns and tools columns.
func printNodeTable(result *pipeline.EngineResult) {
	if result == nil || result.Trace == nil || len(result.Trace.Entries) == 0 {
		return
	}
	fmt.Println()
	fmt.Println("─── Node Execution ────────────────────────────────────────")
	fmt.Printf("  %-22s  %-10s  %-10s  %5s  %5s  %s\n", "Node", "Status", "Time", "Turns", "Tools", "Handler")
	fmt.Printf("  %-22s  %-10s  %-10s  %5s  %5s  %s\n", "────", "──────", "────", "─────", "─────", "───────")
	for _, entry := range result.Trace.Entries {
		printNodeTableRow(entry)
	}
}

// printNodeTableRow prints a single row in the node execution table.
func printNodeTableRow(entry pipeline.TraceEntry) {
	icon := "✓"
	switch entry.Status {
	case pipeline.OutcomeFail:
		icon = "✗"
	case pipeline.OutcomeRetry:
		icon = "↻"
	}
	nodeID := entry.NodeID
	if len(nodeID) > 22 {
		nodeID = nodeID[:19] + "..."
	}

	turns := "-"
	tools := "-"
	if entry.Stats != nil {
		turns = fmt.Sprintf("%d", entry.Stats.Turns)
		tools = fmt.Sprintf("%d", entry.Stats.TotalToolCalls)
	}

	fmt.Printf("  %-22s  %s %-8s  %-10s  %5s  %5s  %s\n",
		nodeID, icon, entry.Status, formatElapsed(entry.Duration), turns, tools, entry.HandlerName)
}

// printTokensByProvider prints per-provider token usage breakdown.
func printTokensByProvider(tracker *llm.TokenTracker) {
	if tracker == nil {
		return
	}
	providers := tracker.Providers()
	if len(providers) == 0 {
		return
	}
	fmt.Println()
	fmt.Println("─── Tokens by Provider ────────────────────────────────────")
	fmt.Printf("  %-12s  %10s  %10s\n", "Provider", "Input", "Output")
	fmt.Printf("  %-12s  %10s  %10s\n", "────────", "─────", "──────")
	for _, p := range providers {
		u := tracker.ProviderUsage(p)
		fmt.Printf("  %-12s  %10s  %10s\n", p, formatNumber(u.InputTokens), formatNumber(u.OutputTokens))
	}
	total := tracker.TotalUsage()
	fmt.Printf("  %-12s  %10s  %10s\n", "TOTAL", formatNumber(total.InputTokens), formatNumber(total.OutputTokens))
	if total.EstimatedCost > 0 {
		if len(providers) == 1 && providers[0] == "claude-code" {
			fmt.Printf("  Est. usage: ~$%.4f (Max subscription — no actual charge)\n", total.EstimatedCost)
		} else {
			fmt.Printf("  Cost: $%.4f\n", total.EstimatedCost)
		}
	}
}

// printPipelineFlow prints the simple ASCII node graph from trace.
func printPipelineFlow(result *pipeline.EngineResult) {
	if result == nil || result.Trace == nil || len(result.Trace.Entries) == 0 {
		return
	}
	fmt.Println()
	fmt.Println("─── Pipeline ──────────────────────────────────────────────")
	printNodeGraph(result.Trace.Entries)
}

// printNodeGraph renders a simple vertical ASCII graph of the executed nodes.
func printNodeGraph(entries []pipeline.TraceEntry) {
	for i, entry := range entries {
		icon := "✓"
		switch entry.Status {
		case pipeline.OutcomeFail:
			icon = "✗"
		case pipeline.OutcomeRetry:
			icon = "↻"
		}

		label := entry.NodeID
		timing := formatElapsed(entry.Duration)

		fmt.Printf("  %s %s (%s)\n", icon, label, timing)

		// Draw connector to next node
		if i < len(entries)-1 {
			if entry.EdgeTo != "" && entry.EdgeTo != entries[i+1].NodeID {
				// Show branching
				fmt.Printf("  │ → %s\n", entry.EdgeTo)
			}
			fmt.Println("  │")
		}
	}
}

// printResumeHint shows the resume command when the pipeline didn't complete successfully.
func printResumeHint(result *pipeline.EngineResult, pipelineFile string) {
	if result == nil || result.Status == pipeline.OutcomeSuccess || result.RunID == "" {
		return
	}
	pipelineArg := pipelineFile
	if pipelineArg == "" {
		pipelineArg = "<pipeline.dip>"
	}
	fmt.Println()
	fmt.Println("─── Resume ────────────────────────────────────────────────")
	fmt.Printf("  tracker -r %s %s\n", result.RunID, pipelineArg)
}
