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
		accumulateStatsEntry(&agg, entry.Stats, seenCreated, seenModified)
	}
	return agg
}

// accumulateStatsEntry merges one session stats entry into the aggregate.
func accumulateStatsEntry(agg *aggregatedStats, s *pipeline.SessionStats, seenCreated, seenModified map[string]bool) {
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
	appendUnique(&agg.FilesCreated, s.FilesCreated, seenCreated)
	appendUnique(&agg.FilesModified, s.FilesModified, seenModified)
}

// appendUnique appends items from src to dst, skipping duplicates tracked in seen.
func appendUnique(dst *[]string, src []string, seen map[string]bool) {
	for _, f := range src {
		if !seen[f] {
			seen[f] = true
			*dst = append(*dst, f)
		}
	}
}

// toolCount pairs a tool name with its call count for sorting.
type toolCount struct {
	name  string
	count int
}

// formatToolBreakdown returns a parenthesized breakdown like "(bash: 198, write: 67)".
func formatToolBreakdown(toolCalls map[string]int) string {
	if len(toolCalls) == 0 {
		return ""
	}
	sorted := sortedToolCounts(toolCalls)
	var parts []string
	for _, tc := range sorted {
		parts = append(parts, fmt.Sprintf("%s: %d", tc.name, tc.count))
	}
	return "(" + strings.Join(parts, ", ") + ")"
}

// sortedToolCounts converts a tool call map to a slice sorted by count desc, name asc.
func sortedToolCounts(toolCalls map[string]int) []toolCount {
	var sorted []toolCount
	for name, count := range toolCalls {
		sorted = append(sorted, toolCount{name, count})
	}
	slices.SortFunc(sorted, compareToolCounts)
	return sorted
}

// compareToolCounts sorts by count descending, then name ascending.
func compareToolCounts(a, b toolCount) int {
	if a.count != b.count {
		return b.count - a.count
	}
	if a.name < b.name {
		return -1
	}
	if a.name > b.name {
		return 1
	}
	return 0
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
	printParamOverrideSummary()
	printRunDuration(result)
	printRunTotals(result, tracker)
	printNodeTable(result)
	printTokensByProvider(tracker)
	printPipelineFlow(result)

	if result != nil && result.Status == pipeline.OutcomeBudgetExceeded {
		printBudgetHaltBanner(result, tracker)
	} else if pipelineErr != nil {
		fmt.Println()
		fmt.Printf("  ERROR: %v\n", pipelineErr)
	}

	printResumeHint(result, pipelineFile)

	fmt.Println("═══════════════════════════════════════════════════════════")
}

func printParamOverrideSummary() {
	if len(activeEffectiveRunParams) == 0 {
		return
	}
	fmt.Printf("  Params:    %s\n", formatParamOverridesForSummary(activeEffectiveRunParams))
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
	printTotalsBody(agg, tracker)
}

// printTotalsBody prints the body rows of the totals section.
func printTotalsBody(agg aggregatedStats, tracker *llm.TokenTracker) {
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
		fmt.Printf("  %s %s (%s)\n", nodeStatusIcon(entry.Status), entry.NodeID, formatElapsed(entry.Duration))
		printNodeConnector(entries, i)
	}
}

// nodeStatusIcon returns the ASCII icon for a node execution status.
func nodeStatusIcon(status string) string {
	switch status {
	case pipeline.OutcomeFail:
		return "✗"
	case pipeline.OutcomeRetry:
		return "↻"
	default:
		return "✓"
	}
}

// printNodeConnector draws the connector line between entries.
func printNodeConnector(entries []pipeline.TraceEntry, i int) {
	if i >= len(entries)-1 {
		return
	}
	if entries[i].EdgeTo != "" && entries[i].EdgeTo != entries[i+1].NodeID {
		fmt.Printf("  │ → %s\n", entries[i].EdgeTo)
	}
	fmt.Println("  │")
}

// printBudgetHaltBanner prints a prominent halt notice when a budget limit was exceeded.
func printBudgetHaltBanner(result *pipeline.EngineResult, tracker *llm.TokenTracker) {
	fmt.Println()
	fmt.Println("─── HALTED: budget exceeded ───────────────────────────────")
	if len(result.BudgetLimitsHit) > 0 {
		fmt.Printf("  reason: %s\n", strings.Join(result.BudgetLimitsHit, ", "))
	}
	if tracker != nil {
		total := tracker.TotalUsage()
		if total.InputTokens > 0 || total.OutputTokens > 0 {
			totalToks := total.InputTokens + total.OutputTokens
			fmt.Printf("  spent:  %s tokens", formatNumber(totalToks))
			if total.EstimatedCost > 0 {
				fmt.Printf(", $%.4f", total.EstimatedCost)
			}
			fmt.Println()
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
