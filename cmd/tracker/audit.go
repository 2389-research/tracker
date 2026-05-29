// ABOUTME: Audit subcommand — analyzes completed pipeline runs from on-disk artifacts.
// ABOUTME: Reads checkpoint, activity log, and node status files to produce structured reports.
package main

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	tracker "github.com/2389-research/tracker"
	"github.com/2389-research/tracker/pipeline"
)

// listRuns shows all available runs with their status and node count.
func listRuns(workdir string) error {
	runs, err := tracker.ListRuns(workdir, tracker.AuditConfig{LogWriter: io.Discard})
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
	fmt.Printf("  %-14s  %-10s  %-6s  %-8s  %-10s  %-26s  %s\n", "Run ID", "Status", "Nodes", "Retries", "Duration", "Bundle", "Failed At")
	fmt.Printf("  %-14s  %-10s  %-6s  %-8s  %-10s  %-26s  %s\n", "──────", "──────", "─────", "───────", "────────", "──────", "─────────")

	for _, r := range runs {
		icon := "+"
		switch r.Status {
		case "success":
			icon = "ok"
		case "validation_overridden":
			icon = "override"
		case "budget_exceeded":
			icon = "budget"
		case "fail":
			icon = "FAIL"
		default:
			icon = r.Status // fall back to raw value for unknown statuses
		}
		durStr := ""
		if r.Duration > 0 {
			durStr = formatElapsed(r.Duration)
		}
		fmt.Printf("  %-14s  %-10s  %-6d  %-8d  %-10s  %-26s  %s\n",
			r.RunID[:min(14, len(r.RunID))], icon, r.Nodes, r.Retries, durStr, truncateBundleIdentity(r.BundleIdentity), r.FailedAt)
	}

	fmt.Printf("\n  %d runs total\n", len(runs))
	fmt.Printf("  Inspect a run: tracker audit <runID>\n\n")
}

// truncateBundleIdentity shortens a "sha256:<hex>" identity for display.
// Returns empty string for empty input; full identity for short inputs;
// "sha256:<16-hex>..." for normal-length identities.
func truncateBundleIdentity(id string) string {
	if id == "" {
		return ""
	}
	const prefix = "sha256:"
	if !strings.HasPrefix(id, prefix) {
		return id
	}
	hex := id[len(prefix):]
	if len(hex) <= 16 {
		return id
	}
	return prefix + hex[:16] + "..."
}

// runAudit loads run artifacts and prints a structured audit report.
func runAudit(workdir, runID string) error {
	runDir, err := tracker.ResolveRunDir(workdir, runID)
	if err != nil {
		return err
	}
	report, err := tracker.Audit(context.Background(), runDir)
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
	if r.BundleIdentity != "" {
		fmt.Printf("  Bundle:    %s\n", r.BundleIdentity)
	}
	// Status line: append override headline inline when overrides exist. Per
	// spec D5a the headline is the LATEST entry (the one that produced the
	// terminal validation_overridden status).
	fmt.Printf("  Status:    %s", r.Status)
	if len(r.ValidationOverrides) > 0 {
		head := r.ValidationOverrides[len(r.ValidationOverrides)-1]
		fmt.Printf(" (label %q at %s by %s)", head.Label, formatOverrideGate(head), head.Actor)
	}
	fmt.Println()
	fmt.Printf("  Nodes:     %d completed\n", r.CompletedNodes)
	fmt.Printf("  Restarts:  %d\n", r.RestartCount)
	fmt.Printf("  Timestamp: %s\n", r.CheckpointTimestamp.Format("2006-01-02 15:04:05 MST"))
	// Per-override Override: chain lines (chronological order, all entries).
	// SubgraphPath is joined outermost-to-innermost with the leaf gate appended.
	for _, d := range r.ValidationOverrides {
		fmt.Printf("  Override:  %s \u2192 %s\n", formatOverrideGate(d), d.Label)
	}
}

// formatOverrideGate joins the SubgraphPath as `outer/inner/.../gate` with the
// GateNodeID appended as the leaf segment. Returns just GateNodeID when no
// subgraph path is set. Copies into a fresh slice so the caller's
// SubgraphPath is never mutated by the trailing append.
func formatOverrideGate(d pipeline.OverrideDetail) string {
	if len(d.SubgraphPath) == 0 {
		return d.GateNodeID
	}
	parts := make([]string, 0, len(d.SubgraphPath)+1)
	parts = append(parts, d.SubgraphPath...)
	parts = append(parts, d.GateNodeID)
	return strings.Join(parts, "/")
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
