// ABOUTME: Audit subcommand — analyzes completed pipeline runs from on-disk artifacts.
// ABOUTME: Reads checkpoint, activity log, and node status files to produce structured reports.
package main

import (
	"fmt"
	"time"

	tracker "github.com/2389-research/tracker"
)

// listRuns shows all available runs with their status and node count.
func listRuns(workdir string) error {
	runs, err := tracker.ListRuns(workdir)
	if err != nil {
		return err
	}
	if len(runs) == 0 {
		fmt.Println("No runs found. Run a pipeline first.")
		return nil
	}
	printRunList(runs)
	return nil
}

// printRunList prints the formatted run listing table.
func printRunList(runs []tracker.RunSummary) {
	fmt.Println()
	fmt.Printf("  %-14s  %-8s  %-6s  %-8s  %-10s  %s\n", "Run ID", "Status", "Nodes", "Retries", "Duration", "Failed At")
	fmt.Printf("  %-14s  %-8s  %-6s  %-8s  %-10s  %s\n", "──────", "──────", "─────", "───────", "────────", "─────────")

	for _, r := range runs {
		icon := "+"
		switch r.Status {
		case "success":
			icon = "ok"
		case "fail":
			icon = "FAIL"
		}
		durStr := ""
		if r.Duration > 0 {
			durStr = formatElapsed(r.Duration)
		}
		fmt.Printf("  %-14s  %-8s  %-6d  %-8d  %-10s  %s\n",
			r.RunID[:min(14, len(r.RunID))], icon, r.Nodes, r.Retries, durStr, r.FailedAt)
	}

	fmt.Printf("\n  %d runs total\n", len(runs))
	fmt.Printf("  Inspect a run: tracker audit <runID>\n\n")
}

// runAudit loads run artifacts and prints a structured audit report.
func runAudit(workdir, runID string) error {
	runDir, err := tracker.ResolveRunDir(workdir, runID)
	if err != nil {
		return err
	}
	report, err := tracker.Audit(runDir)
	if err != nil {
		return err
	}
	printAuditReport(report)
	return nil
}

// printAuditReport is the top-level printer for an AuditReport.
func printAuditReport(r *tracker.AuditReport) {
	printAuditHeader(r)
	printTimeline(r.Timeline)
	printRetries(r.Retries)
	printErrors(r.Errors)
	printRecommendations(r.Recommendations)
	printAuditFooter()
}

func printAuditHeader(r *tracker.AuditReport) {
	fmt.Println()
	fmt.Println("\u2550\u2550\u2550 Audit Report \u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550")
	fmt.Printf("  Run ID:    %s\n", r.RunID)
	fmt.Printf("  Status:    %s\n", r.Status)
	fmt.Printf("  Nodes:     %d completed\n", r.CompletedNodes)
	fmt.Printf("  Restarts:  %d\n", r.RestartCount)
	fmt.Printf("  Timestamp: %s\n", r.CheckpointTimestamp.Format("2006-01-02 15:04:05 MST"))
}

func printTimeline(timeline []tracker.TimelineEntry) {
	fmt.Println()
	fmt.Println("\u2500\u2500\u2500 Timeline \u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500")

	if len(timeline) == 0 {
		fmt.Println("  (no activity recorded)")
		return
	}

	// Track stage starts for inline duration computation during printing.
	stageStarts := make(map[string]time.Time)
	// Re-read timestamps from original entries; TimelineEntry carries Duration
	// for completed stages but we need start times for the printTimelineEntry format.
	// Rebuild from the timeline slice itself.
	for _, entry := range timeline {
		printTimelineEntryFromLib(entry, stageStarts)
	}

	printTimelineTotalDurationFromLib(timeline)
}

// printTimelineEntryFromLib prints one TimelineEntry to stdout, matching the
// original printTimelineEntry format exactly.
func printTimelineEntryFromLib(entry tracker.TimelineEntry, stageStarts map[string]time.Time) {
	timeStr := entry.Timestamp.Format("15:04:05")

	switch entry.Type {
	case "pipeline_started", "pipeline_completed", "pipeline_failed", "loop_restart":
		fmt.Printf("  %s  \u25b6 %s\n", timeStr, entry.Type)
	case "stage_started":
		stageStarts[entry.NodeID] = entry.Timestamp
		fmt.Printf("  %s  \u25b8 %s \u2014 %s\n", timeStr, entry.NodeID, entry.Type)
	case "stage_completed", "stage_failed":
		dur := ""
		if start, ok := stageStarts[entry.NodeID]; ok {
			dur = " (" + formatElapsed(entry.Timestamp.Sub(start)) + ")"
			delete(stageStarts, entry.NodeID)
		}
		fmt.Printf("  %s  \u25b8 %s \u2014 %s%s\n", timeStr, entry.NodeID, entry.Type, dur)
	case "stage_retrying":
		fmt.Printf("  %s  \u25b8 %s \u2014 %s\n", timeStr, entry.NodeID, entry.Type)
	default:
		if entry.NodeID != "" {
			fmt.Printf("  %s  \u25b8 %s \u2014 %s\n", timeStr, entry.NodeID, entry.Type)
		} else {
			fmt.Printf("  %s  \u25b6 %s\n", timeStr, entry.Type)
		}
	}
}

// printTimelineTotalDurationFromLib prints total elapsed time from the timeline.
func printTimelineTotalDurationFromLib(timeline []tracker.TimelineEntry) {
	if len(timeline) < 2 {
		return
	}
	total := timeline[len(timeline)-1].Timestamp.Sub(timeline[0].Timestamp)
	if total > 0 {
		fmt.Printf("  Total: %s\n", formatElapsed(total))
	}
}

func printRetries(retries []tracker.RetryRecord) {
	fmt.Println()
	fmt.Println("\u2500\u2500\u2500 Retries \u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500")

	if len(retries) == 0 {
		fmt.Println("  (none)")
		return
	}

	// Retries are already sorted by node ID from the library.
	for _, rec := range retries {
		suffix := "retries"
		if rec.Attempts == 1 {
			suffix = "retry"
		}
		fmt.Printf("  %s:  %d %s\n", rec.NodeID, rec.Attempts, suffix)
	}
}

func printErrors(errors []tracker.ActivityError) {
	fmt.Println()
	fmt.Println("\u2500\u2500\u2500 Errors \u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500")

	if len(errors) == 0 {
		fmt.Println("  (none)")
		return
	}

	for _, e := range errors {
		timeStr := e.Timestamp.Format("15:04:05")
		nodeLabel := e.NodeID
		if nodeLabel == "" {
			nodeLabel = "pipeline"
		}
		fmt.Printf("  %s  [%s] %s\n", timeStr, nodeLabel, e.Message)
	}
}

func printRecommendations(recs []string) {
	fmt.Println()
	fmt.Println("\u2500\u2500\u2500 Recommendations \u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500")

	if len(recs) == 0 {
		fmt.Println("  (none)")
		return
	}

	for _, rec := range recs {
		fmt.Printf("  \u2022 %s\n", rec)
	}
}

func printAuditFooter() {
	fmt.Println("\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550")
}
