// ABOUTME: Audit subcommand — analyzes completed pipeline runs from on-disk artifacts.
// ABOUTME: Reads checkpoint, activity log, and node status files to produce structured reports.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/2389-research/tracker/pipeline"
)

// activityEntry represents a single parsed line from activity.jsonl.
type activityEntry struct {
	Timestamp time.Time
	Type      string
	RunID     string
	NodeID    string
	Message   string
	Error     string
}

// resolveRunDir finds the run directory for a given run ID using prefix matching.
// Returns the absolute path to the run directory.
func resolveRunDir(workdir, runID string) (string, error) {
	if runID == "" {
		return "", fmt.Errorf("run ID cannot be empty")
	}
	runsDir := filepath.Join(workdir, ".tracker", "runs")
	entries, err := os.ReadDir(runsDir)
	if err != nil {
		return "", fmt.Errorf("cannot read runs directory: %w", err)
	}

	var matches []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if strings.HasPrefix(e.Name(), runID) {
			matches = append(matches, e.Name())
		}
	}

	switch len(matches) {
	case 0:
		return "", fmt.Errorf("no run found matching %q in %s", runID, runsDir)
	case 1:
		// Unique match (exact or prefix)
	default:
		exact := false
		for _, m := range matches {
			if m == runID {
				matches = []string{m}
				exact = true
				break
			}
		}
		if !exact {
			return "", fmt.Errorf("ambiguous run ID %q matches %d runs: %s", runID, len(matches), strings.Join(matches, ", "))
		}
	}

	return filepath.Join(runsDir, matches[0]), nil
}

// loadActivityLog reads and parses activity.jsonl, skipping malformed lines.
func loadActivityLog(runDir string) ([]activityEntry, error) {
	path := filepath.Join(runDir, "activity.jsonl")
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("open activity log: %w", err)
	}
	defer f.Close()

	var entries []activityEntry
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		var raw struct {
			Timestamp string `json:"ts"`
			Type      string `json:"type"`
			RunID     string `json:"run_id"`
			NodeID    string `json:"node_id"`
			Message   string `json:"message"`
			Error     string `json:"error"`
		}
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			// Skip malformed lines.
			continue
		}

		ts, err := time.Parse(time.RFC3339Nano, raw.Timestamp)
		if err != nil {
			// Try alternate format.
			ts, err = time.Parse("2006-01-02T15:04:05.000Z07:00", raw.Timestamp)
			if err != nil {
				// Skip lines with unparseable timestamps.
				continue
			}
		}

		entries = append(entries, activityEntry{
			Timestamp: ts,
			Type:      raw.Type,
			RunID:     raw.RunID,
			NodeID:    raw.NodeID,
			Message:   raw.Message,
			Error:     raw.Error,
		})
	}

	return entries, scanner.Err()
}

// listRuns shows all available runs with their status and node count.
func listRuns(workdir string) error {
	runsDir := filepath.Join(workdir, ".tracker", "runs")
	entries, err := os.ReadDir(runsDir)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println("No runs found. Run a pipeline first.")
			return nil
		}
		return fmt.Errorf("cannot read runs directory: %w", err)
	}

	type runSummary struct {
		runID     string
		status    string
		nodes     int
		retries   int
		restarts  int
		timestamp time.Time
		duration  time.Duration
		failedAt  string
	}

	var runs []runSummary
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		runDir := filepath.Join(runsDir, e.Name())
		cpPath := filepath.Join(runDir, "checkpoint.json")

		cp, cpErr := pipeline.LoadCheckpoint(cpPath)
		if cpErr != nil {
			continue
		}

		activity, _ := loadActivityLog(runDir)
		sort.Slice(activity, func(i, j int) bool {
			return activity[i].Timestamp.Before(activity[j].Timestamp)
		})

		status := determinePipelineStatus(cp, activity)

		totalRetries := 0
		for _, count := range cp.RetryCounts {
			totalRetries += count
		}

		var dur time.Duration
		if len(activity) >= 2 {
			first := activity[0].Timestamp
			last := activity[len(activity)-1].Timestamp
			dur = last.Sub(first)
		}

		rs := runSummary{
			runID:     e.Name(),
			status:    status,
			nodes:     len(cp.CompletedNodes),
			retries:   totalRetries,
			restarts:  cp.RestartCount,
			timestamp: cp.Timestamp,
			duration:  dur,
		}
		if status == "fail" {
			rs.failedAt = cp.CurrentNode
		}
		runs = append(runs, rs)
	}

	if len(runs) == 0 {
		fmt.Println("No runs found. Run a pipeline first.")
		return nil
	}

	// Sort by timestamp, most recent first.
	sort.Slice(runs, func(i, j int) bool {
		return runs[i].timestamp.After(runs[j].timestamp)
	})

	fmt.Println()
	fmt.Printf("  %-14s  %-8s  %-6s  %-8s  %-10s  %s\n", "Run ID", "Status", "Nodes", "Retries", "Duration", "Failed At")
	fmt.Printf("  %-14s  %-8s  %-6s  %-8s  %-10s  %s\n", "──────", "──────", "─────", "───────", "────────", "─────────")

	for _, r := range runs {
		icon := "+"
		switch r.status {
		case "success":
			icon = "ok"
		case "fail":
			icon = "FAIL"
		}

		durStr := ""
		if r.duration > 0 {
			durStr = formatElapsed(r.duration)
		}

		failedAt := ""
		if r.failedAt != "" {
			failedAt = r.failedAt
		}

		fmt.Printf("  %-14s  %-8s  %-6d  %-8d  %-10s  %s\n",
			r.runID[:min(14, len(r.runID))], icon, r.nodes, r.retries, durStr, failedAt)
	}

	fmt.Printf("\n  %d runs total\n", len(runs))
	fmt.Printf("  Inspect a run: tracker audit <runID>\n\n")
	return nil
}

// runAudit loads run artifacts and prints a structured audit report.
func runAudit(workdir, runID string) error {
	runDir, err := resolveRunDir(workdir, runID)
	if err != nil {
		return err
	}

	// Load checkpoint.
	cpPath := filepath.Join(runDir, "checkpoint.json")
	cp, err := pipeline.LoadCheckpoint(cpPath)
	if err != nil {
		return fmt.Errorf("load checkpoint: %w", err)
	}

	// Load activity log and sort by timestamp to handle concurrent writes.
	activity, err := loadActivityLog(runDir)
	if err != nil {
		return fmt.Errorf("load activity log: %w", err)
	}
	sort.Slice(activity, func(i, j int) bool {
		return activity[i].Timestamp.Before(activity[j].Timestamp)
	})

	// Determine pipeline status.
	status := determinePipelineStatus(cp, activity)

	// Print report.
	printAuditHeader(cp, status)
	printTimeline(activity)
	printRetries(cp)
	printErrors(activity)
	printRecommendations(cp, status, activity)
	printAuditFooter()

	return nil
}

// determinePipelineStatus infers the final status from checkpoint and activity log.
func determinePipelineStatus(cp *pipeline.Checkpoint, activity []activityEntry) string {
	// Check activity log for explicit pipeline_completed or pipeline_failed.
	for i := len(activity) - 1; i >= 0; i-- {
		switch activity[i].Type {
		case "pipeline_completed":
			return "success"
		case "pipeline_failed":
			return "fail"
		}
	}

	// If checkpoint has a current_node set, the pipeline likely didn't finish.
	if cp.CurrentNode != "" {
		return "fail"
	}

	return "success"
}

func printAuditHeader(cp *pipeline.Checkpoint, status string) {
	fmt.Println()
	fmt.Println("\u2550\u2550\u2550 Audit Report \u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550")
	fmt.Printf("  Run ID:    %s\n", cp.RunID)
	fmt.Printf("  Status:    %s\n", status)
	fmt.Printf("  Nodes:     %d completed\n", len(cp.CompletedNodes))
	fmt.Printf("  Restarts:  %d\n", cp.RestartCount)
	fmt.Printf("  Timestamp: %s\n", cp.Timestamp.Format("2006-01-02 15:04:05 MST"))
}

func printTimeline(activity []activityEntry) {
	fmt.Println()
	fmt.Println("\u2500\u2500\u2500 Timeline \u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500")

	if len(activity) == 0 {
		fmt.Println("  (no activity recorded)")
		return
	}

	// Track stage start times for duration calculation.
	stageStarts := make(map[string]time.Time)

	for _, entry := range activity {
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
				elapsed := entry.Timestamp.Sub(start)
				dur = " (" + formatElapsed(elapsed) + ")"
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

	// Print total duration if we have start and end events.
	if len(activity) >= 2 {
		first := activity[0].Timestamp
		last := activity[len(activity)-1].Timestamp
		total := last.Sub(first)
		if total > 0 {
			fmt.Printf("  Total: %s\n", formatElapsed(total))
		}
	}
}

func printRetries(cp *pipeline.Checkpoint) {
	fmt.Println()
	fmt.Println("\u2500\u2500\u2500 Retries \u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500")

	if len(cp.RetryCounts) == 0 {
		fmt.Println("  (none)")
		return
	}

	// Sort node IDs for deterministic output.
	nodes := make([]string, 0, len(cp.RetryCounts))
	for nodeID := range cp.RetryCounts {
		nodes = append(nodes, nodeID)
	}
	sort.Strings(nodes)

	for _, nodeID := range nodes {
		count := cp.RetryCounts[nodeID]
		suffix := "retries"
		if count == 1 {
			suffix = "retry"
		}
		fmt.Printf("  %s:  %d %s\n", nodeID, count, suffix)
	}
}

func printErrors(activity []activityEntry) {
	fmt.Println()
	fmt.Println("\u2500\u2500\u2500 Errors \u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500")

	hasErrors := false
	for _, entry := range activity {
		if entry.Error != "" {
			timeStr := entry.Timestamp.Format("15:04:05")
			nodeLabel := entry.NodeID
			if nodeLabel == "" {
				nodeLabel = "pipeline"
			}
			fmt.Printf("  %s  [%s] %s\n", timeStr, nodeLabel, entry.Error)
			hasErrors = true
		}
	}

	if !hasErrors {
		fmt.Println("  (none)")
	}
}

func printRecommendations(cp *pipeline.Checkpoint, status string, activity []activityEntry) {
	fmt.Println()
	fmt.Println("\u2500\u2500\u2500 Recommendations \u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500\u2500")

	var recs []string

	// Retry recommendations.
	for nodeID, count := range cp.RetryCounts {
		if count >= 2 {
			recs = append(recs, fmt.Sprintf("Consider adjusting retry_policy for %s (used %d retries)", nodeID, count))
		}
	}

	// Restart recommendation.
	if cp.RestartCount > 0 {
		suffix := "time"
		if cp.RestartCount > 1 {
			suffix = "times"
		}
		recs = append(recs, fmt.Sprintf("Pipeline restarted %d %s — review loop conditions", cp.RestartCount, suffix))
	}

	// Long-running pipeline recommendation.
	if len(activity) >= 2 {
		first := activity[0].Timestamp
		last := activity[len(activity)-1].Timestamp
		total := last.Sub(first)
		if total > 30*time.Minute {
			recs = append(recs, "Long-running pipeline — consider fidelity=summary:medium for faster resumes")
		}
	}

	// Failed pipeline recommendation.
	if status == "fail" && cp.CurrentNode != "" {
		recs = append(recs, fmt.Sprintf("Pipeline failed at %s — check error details above", cp.CurrentNode))
	}

	if len(recs) == 0 {
		fmt.Println("  (none)")
	} else {
		// Sort for deterministic output.
		sort.Strings(recs)
		for _, rec := range recs {
			fmt.Printf("  \u2022 %s\n", rec)
		}
	}
}

func printAuditFooter() {
	fmt.Println("\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550\u2550")
}
