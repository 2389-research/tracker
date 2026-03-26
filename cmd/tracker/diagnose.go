// ABOUTME: Diagnose subcommand — deep analysis of pipeline run failures.
// ABOUTME: Reads activity.jsonl and node status files to surface errors, tool output, and suggestions.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/2389-research/tracker/pipeline"
	"github.com/charmbracelet/lipgloss"
)

// nodeFailure captures everything known about a failed node.
type nodeFailure struct {
	nodeID   string
	outcome  string
	stdout   string
	stderr   string
	errors   []string // errors from activity log
	duration time.Duration
	handler  string
}

// diagnoseMostRecent finds and diagnoses the most recent run.
func diagnoseMostRecent(workdir string) error {
	runsDir := filepath.Join(workdir, ".tracker", "runs")
	entries, err := os.ReadDir(runsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("no runs found — run a pipeline first")
		}
		return fmt.Errorf("cannot read runs directory: %w", err)
	}

	// Find the most recent run by checkpoint timestamp.
	var latestID string
	var latestTime time.Time
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		cpPath := filepath.Join(runsDir, e.Name(), "checkpoint.json")
		cp, err := pipeline.LoadCheckpoint(cpPath)
		if err != nil {
			continue
		}
		if cp.Timestamp.After(latestTime) {
			latestTime = cp.Timestamp
			latestID = e.Name()
		}
	}
	if latestID == "" {
		return fmt.Errorf("no runs found with valid checkpoints")
	}
	return runDiagnose(workdir, latestID)
}

// runDiagnose performs deep failure analysis on a pipeline run.
func runDiagnose(workdir, runID string) error {
	runDir, err := resolveRunDir(workdir, runID)
	if err != nil {
		return err
	}

	// Load checkpoint for context.
	cpPath := filepath.Join(runDir, "checkpoint.json")
	cp, err := pipeline.LoadCheckpoint(cpPath)
	if err != nil {
		return fmt.Errorf("load checkpoint: %w", err)
	}

	// Collect node failures from status.json files.
	failures := collectNodeFailures(runDir)

	// Parse activity log for runtime errors and tool output.
	enrichFromActivity(runDir, failures)

	// Print diagnosis.
	fmt.Println()
	fmt.Println(bannerStyle.Render("tracker diagnose"))
	fmt.Println()
	fmt.Printf("  Run ID:  %s\n", cp.RunID)
	fmt.Printf("  Nodes:   %d completed\n", len(cp.CompletedNodes))

	if len(failures) == 0 {
		fmt.Println()
		fmt.Println(lipgloss.NewStyle().Foreground(colorNeon).Render("  No failures found — this run completed cleanly."))
		fmt.Println()
		return nil
	}

	fmt.Printf("  Failures: %d\n", len(failures))
	fmt.Println()

	// Sort failures by node ID for deterministic output.
	nodeIDs := make([]string, 0, len(failures))
	for id := range failures {
		nodeIDs = append(nodeIDs, id)
	}
	sort.Strings(nodeIDs)

	for _, nodeID := range nodeIDs {
		f := failures[nodeID]
		printNodeDiagnosis(f)
	}

	// Print suggestions.
	printDiagnoseSuggestions(failures, cp)

	return nil
}

// collectNodeFailures reads status.json files from each node directory.
func collectNodeFailures(runDir string) map[string]*nodeFailure {
	failures := make(map[string]*nodeFailure)

	entries, err := os.ReadDir(runDir)
	if err != nil {
		return failures
	}

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		statusPath := filepath.Join(runDir, e.Name(), "status.json")
		data, err := os.ReadFile(statusPath)
		if err != nil {
			continue
		}

		var status struct {
			Outcome        string            `json:"outcome"`
			ContextUpdates map[string]string `json:"context_updates"`
		}
		if err := json.Unmarshal(data, &status); err != nil {
			continue
		}

		if status.Outcome != "fail" {
			continue
		}

		f := &nodeFailure{
			nodeID:  e.Name(),
			outcome: status.Outcome,
		}
		if status.ContextUpdates != nil {
			f.stdout = status.ContextUpdates["tool_stdout"]
			f.stderr = status.ContextUpdates["tool_stderr"]
		}
		failures[e.Name()] = f
	}

	return failures
}

// enrichFromActivity adds error messages and timing from activity.jsonl.
func enrichFromActivity(runDir string, failures map[string]*nodeFailure) {
	path := filepath.Join(runDir, "activity.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}

	stageStarts := make(map[string]time.Time)

	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		var raw struct {
			Timestamp string `json:"ts"`
			Type      string `json:"type"`
			NodeID    string `json:"node_id"`
			Message   string `json:"message"`
			Error     string `json:"error"`
			ToolOut   string `json:"tool_output"`
			ToolErr   string `json:"tool_error"`
			Handler   string `json:"handler"`
		}
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			continue
		}

		ts, _ := time.Parse(time.RFC3339Nano, raw.Timestamp)

		switch raw.Type {
		case "stage_started":
			stageStarts[raw.NodeID] = ts
		case "stage_failed", "stage_completed":
			if f, ok := failures[raw.NodeID]; ok {
				if start, ok := stageStarts[raw.NodeID]; ok && !ts.IsZero() {
					f.duration = ts.Sub(start)
				}
				if raw.Handler != "" {
					f.handler = raw.Handler
				}
			}
		}

		// Collect errors for failed nodes.
		if raw.NodeID != "" {
			if f, ok := failures[raw.NodeID]; ok {
				if raw.Error != "" {
					f.errors = append(f.errors, raw.Error)
				}
				// Tool output can contain the real error info.
				if raw.ToolErr != "" && f.stderr == "" {
					f.stderr = raw.ToolErr
				}
			}
		}
	}
}

func printNodeDiagnosis(f *nodeFailure) {
	headerStyle := lipgloss.NewStyle().Bold(true).Foreground(colorHot)
	labelStyle := lipgloss.NewStyle().Foreground(colorSky).Bold(true)

	fmt.Println(headerStyle.Render(fmt.Sprintf("  ✗ %s", f.nodeID)))

	if f.handler != "" {
		fmt.Printf("    %s %s\n", labelStyle.Render("Handler:"), f.handler)
	}
	if f.duration > 0 {
		fmt.Printf("    %s %s\n", labelStyle.Render("Duration:"), formatElapsed(f.duration))
	}

	// Show stdout (often contains the actual failure reason).
	if f.stdout != "" {
		fmt.Printf("    %s\n", labelStyle.Render("Output:"))
		for _, line := range strings.Split(f.stdout, "\n") {
			line = strings.TrimSpace(line)
			if line != "" {
				fmt.Printf("      %s\n", line)
			}
		}
	}

	// Show stderr.
	if f.stderr != "" {
		fmt.Printf("    %s\n", labelStyle.Render("Stderr:"))
		for _, line := range strings.Split(f.stderr, "\n") {
			line = strings.TrimSpace(line)
			if line != "" {
				fmt.Printf("      %s\n", line)
			}
		}
	}

	// Show collected errors from activity log.
	if len(f.errors) > 0 {
		// Deduplicate.
		seen := make(map[string]bool)
		fmt.Printf("    %s\n", labelStyle.Render("Errors:"))
		for _, e := range f.errors {
			if !seen[e] {
				seen[e] = true
				fmt.Printf("      %s\n", e)
			}
		}
	}

	// If no useful info was found, say so.
	if f.stdout == "" && f.stderr == "" && len(f.errors) == 0 {
		fmt.Printf("    %s\n", mutedStyle.Render("No error details captured — node may have failed silently"))
	}

	fmt.Println()
}

func printDiagnoseSuggestions(failures map[string]*nodeFailure, cp *pipeline.Checkpoint) {
	fmt.Println("─── Suggestions ───────────────────────────────────────────")

	var suggestions []string

	for _, f := range failures {
		// Detect circuit breaker escalation.
		if strings.Contains(f.stdout, "ESCALATE") && strings.Contains(f.stdout, "fix attempts") {
			suggestions = append(suggestions,
				fmt.Sprintf("%s: Hit fix attempt limit. The fix_attempts counter persists "+
					"on disk across restarts — if you retry after escalation, the counter "+
					"is already maxed. Reset it with: rm .ai/milestones/fix_attempts", f.nodeID))
		}

		// Detect empty output (silent failures).
		if f.stdout == "" && f.stderr == "" && len(f.errors) == 0 {
			suggestions = append(suggestions,
				fmt.Sprintf("%s: No error details captured. Check the activity.jsonl "+
					"for this node's events: grep %q activity.jsonl | tail -20", f.nodeID, f.nodeID))
		}

		// Detect command execution errors.
		if strings.Contains(f.stderr, "command not found") ||
			strings.Contains(f.stderr, "No such file or directory") {
			suggestions = append(suggestions,
				fmt.Sprintf("%s: Shell command failed — check that the working directory "+
					"and required tools exist before running", f.nodeID))
		}

		// Detect build/test failures.
		if strings.Contains(f.stdout, "FAIL") && strings.Contains(f.stdout, "go test") {
			suggestions = append(suggestions,
				fmt.Sprintf("%s: Go test failures — check if .ai/milestones/known_failures "+
					"should include these tests for this milestone", f.nodeID))
		}

		// Detect instant failures (< 50ms usually means the node didn't really run).
		if f.duration > 0 && f.duration < 50*time.Millisecond && f.handler != "tool" {
			suggestions = append(suggestions,
				fmt.Sprintf("%s: Completed in %s — suspiciously fast. May indicate "+
					"a configuration issue or missing handler", f.nodeID, formatElapsed(f.duration)))
		}
	}

	if len(suggestions) == 0 {
		fmt.Println("  (none)")
	} else {
		for _, s := range suggestions {
			fmt.Printf("  %s %s\n", lipgloss.NewStyle().Foreground(colorWarm).Render("→"), s)
		}
	}
	fmt.Println()
}
