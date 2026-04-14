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
	nodeID           string
	outcome          string
	stdout           string
	stderr           string
	errors           []string // errors from activity log
	duration         time.Duration
	handler          string
	retryCount       int  // how many stage_failed events for this node
	identicalRetries bool // true if all failures had the same error
}

// diagnoseMostRecent finds and diagnoses the most recent run.
func diagnoseMostRecent(workdir string) error {
	runsDir := filepath.Join(workdir, ".tracker", "runs")
	latestID, err := findMostRecentRunID(runsDir)
	if err != nil {
		return err
	}
	return runDiagnose(workdir, latestID)
}

// findMostRecentRunID scans the runs directory for the most recent checkpoint.
func findMostRecentRunID(runsDir string) (string, error) {
	entries, err := readRunsDir(runsDir)
	if err != nil {
		return "", err
	}
	latestID := pickLatestRunID(runsDir, entries)
	if latestID == "" {
		return "", fmt.Errorf("no runs found with valid checkpoints")
	}
	return latestID, nil
}

// readRunsDir reads the runs directory, mapping ENOENT to a user-friendly error.
func readRunsDir(runsDir string) ([]os.DirEntry, error) {
	entries, err := os.ReadDir(runsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("no runs found — run a pipeline first")
		}
		return nil, fmt.Errorf("cannot read runs directory: %w", err)
	}
	return entries, nil
}

// pickLatestRunID returns the directory name of the most recent valid checkpoint.
func pickLatestRunID(runsDir string, entries []os.DirEntry) string {
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
	return latestID
}

// budgetHalt holds information about a budget halt detected in the activity log.
type budgetHalt struct {
	TotalTokens   int
	TotalCostUSD  float64
	WallElapsedMs int64
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

	// Parse activity log for runtime errors, tool output, and budget halts.
	halt := enrichFromActivity(runDir, failures)

	printDiagnoseHeader(cp, halt, failures)
	return nil
}

// printDiagnoseHeader renders the diagnose banner, budget halt section (if any),
// and node failure details.
func printDiagnoseHeader(cp *pipeline.Checkpoint, halt *budgetHalt, failures map[string]*nodeFailure) {
	fmt.Println()
	fmt.Println(bannerStyle.Render("tracker diagnose"))
	fmt.Println()
	fmt.Printf("  Run ID:  %s\n", cp.RunID)
	fmt.Printf("  Nodes:   %d completed\n", len(cp.CompletedNodes))

	// Surface budget halt prominently before other sections.
	if halt != nil {
		printBudgetHalt(halt)
	}

	if len(failures) == 0 && halt == nil {
		fmt.Println()
		fmt.Println(lipgloss.NewStyle().Foreground(colorNeon).Render("  No failures found — this run completed cleanly."))
		fmt.Println()
		return
	}

	if len(failures) > 0 {
		printNodeFailures(failures, cp)
	}
}

// printNodeFailures prints the failure count, per-node diagnosis, and suggestions.
func printNodeFailures(failures map[string]*nodeFailure, cp *pipeline.Checkpoint) {
	fmt.Printf("  Failures: %d\n", len(failures))
	fmt.Println()

	// Sort failures by node ID for deterministic output.
	nodeIDs := make([]string, 0, len(failures))
	for id := range failures {
		nodeIDs = append(nodeIDs, id)
	}
	sort.Strings(nodeIDs)

	for _, nodeID := range nodeIDs {
		printNodeDiagnosis(failures[nodeID])
	}

	// Print suggestions.
	printDiagnoseSuggestions(failures, cp)
}

// printBudgetHalt prints a prominent budget halt section.
func printBudgetHalt(halt *budgetHalt) {
	w := os.Stdout
	fmt.Fprintln(w, "")
	fmt.Fprintln(w, "━━━ Budget halt detected ━━━")
	if halt.TotalTokens > 0 {
		fmt.Fprintf(w, "  tokens used:  %d\n", halt.TotalTokens)
	}
	if halt.TotalCostUSD > 0 {
		fmt.Fprintf(w, "  cost:         $%.4f\n", halt.TotalCostUSD)
	}
	if halt.WallElapsedMs > 0 {
		fmt.Fprintf(w, "  wall time:    %dms\n", halt.WallElapsedMs)
	}
	fmt.Fprintln(w, "  suggestion:   raise the relevant --max-tokens, --max-cost, or --max-wall-time flag,")
	fmt.Fprintln(w, "                or remove the Config.Budget value in your pipeline configuration")
	fmt.Fprintln(w, "")
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
		if f := loadNodeFailure(runDir, e.Name()); f != nil {
			failures[e.Name()] = f
		}
	}

	return failures
}

// loadNodeFailure reads and parses a node's status.json, returning a nodeFailure
// if the node failed, or nil otherwise.
func loadNodeFailure(runDir, nodeID string) *nodeFailure {
	statusPath := filepath.Join(runDir, nodeID, "status.json")
	data, err := os.ReadFile(statusPath)
	if err != nil {
		return nil
	}

	var status struct {
		Outcome        string            `json:"outcome"`
		ContextUpdates map[string]string `json:"context_updates"`
	}
	if err := json.Unmarshal(data, &status); err != nil {
		return nil
	}
	if status.Outcome != "fail" {
		return nil
	}

	f := &nodeFailure{nodeID: nodeID, outcome: status.Outcome}
	if status.ContextUpdates != nil {
		f.stdout = status.ContextUpdates["tool_stdout"]
		f.stderr = status.ContextUpdates["tool_stderr"]
	}
	return f
}

// diagnoseEntry is a parsed activity.jsonl line with fields needed for diagnosis.
type diagnoseEntry struct {
	Timestamp     string  `json:"ts"`
	Type          string  `json:"type"`
	NodeID        string  `json:"node_id"`
	Error         string  `json:"error"`
	ToolErr       string  `json:"tool_error"`
	Handler       string  `json:"handler"`
	TotalTokens   int     `json:"total_tokens"`
	TotalCostUSD  float64 `json:"total_cost_usd"`
	WallElapsedMs int64   `json:"wall_elapsed_ms"`
}

// enrichFromActivity adds error messages, timing, and retry analysis from activity.jsonl.
// Returns a non-nil *budgetHalt if a budget_exceeded event was found.
func enrichFromActivity(runDir string, failures map[string]*nodeFailure) *budgetHalt {
	path := filepath.Join(runDir, "activity.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	stageStarts := make(map[string]time.Time)
	failSignatures := make(map[string][]string)

	halt := parseActivityLines(string(data), failures, stageStarts, failSignatures)
	applyRetryAnalysis(failures, failSignatures)
	return halt
}

// parseActivityLines processes each JSONL line from the activity log.
// Returns a non-nil *budgetHalt if a budget_exceeded event was found.
func parseActivityLines(data string, failures map[string]*nodeFailure, stageStarts map[string]time.Time, failSignatures map[string][]string) *budgetHalt {
	var halt *budgetHalt
	for _, line := range strings.Split(data, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var entry diagnoseEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		if entry.Type == "budget_exceeded" {
			halt = &budgetHalt{
				TotalTokens:   entry.TotalTokens,
				TotalCostUSD:  entry.TotalCostUSD,
				WallElapsedMs: entry.WallElapsedMs,
			}
		}
		enrichFromEntry(entry, failures, stageStarts, failSignatures)
	}
	return halt
}

// applyRetryAnalysis updates failure records with retry count and pattern data.
func applyRetryAnalysis(failures map[string]*nodeFailure, failSignatures map[string][]string) {
	for nodeID, sigs := range failSignatures {
		f, ok := failures[nodeID]
		if !ok {
			continue
		}
		f.retryCount = len(sigs)
		if len(sigs) >= 2 {
			f.identicalRetries = allIdentical(sigs)
		}
	}
}

// allIdentical returns true if all strings in the slice are equal.
// Returns false for slices with fewer than 2 elements.
func allIdentical(ss []string) bool {
	if len(ss) < 2 {
		return false
	}
	for i := 1; i < len(ss); i++ {
		if ss[i] != ss[0] {
			return false
		}
	}
	return true
}

// enrichFromEntry processes a single activity log entry, updating failure
// records with timing, handler info, and error details.
func enrichFromEntry(entry diagnoseEntry, failures map[string]*nodeFailure, stageStarts map[string]time.Time, failSignatures map[string][]string) {
	ts, _ := time.Parse(time.RFC3339Nano, entry.Timestamp)
	processStageEvent(entry, failures, stageStarts, failSignatures, ts)
	enrichNodeFailure(entry, failures)
}

// processStageEvent updates timing/signature data from stage lifecycle events.
func processStageEvent(entry diagnoseEntry, failures map[string]*nodeFailure, stageStarts map[string]time.Time, failSignatures map[string][]string, ts time.Time) {
	switch entry.Type {
	case "stage_started":
		stageStarts[entry.NodeID] = ts
	case "stage_failed":
		updateFailureTiming(failures[entry.NodeID], stageStarts, entry, ts)
		sig := entry.Error + "\x00" + entry.ToolErr
		failSignatures[entry.NodeID] = append(failSignatures[entry.NodeID], sig)
	case "stage_completed":
		updateFailureTiming(failures[entry.NodeID], stageStarts, entry, ts)
	}
}

// enrichNodeFailure appends error and stderr details to the matching failure record.
func enrichNodeFailure(entry diagnoseEntry, failures map[string]*nodeFailure) {
	if entry.NodeID == "" {
		return
	}
	f, ok := failures[entry.NodeID]
	if !ok {
		return
	}
	if entry.Error != "" {
		f.errors = append(f.errors, entry.Error)
	}
	if entry.ToolErr != "" && f.stderr == "" {
		f.stderr = entry.ToolErr
	}
}

// updateFailureTiming sets duration and handler on a failure from a stage event.
func updateFailureTiming(f *nodeFailure, stageStarts map[string]time.Time, entry diagnoseEntry, ts time.Time) {
	if f == nil {
		return
	}
	if start, ok := stageStarts[entry.NodeID]; ok && !ts.IsZero() {
		f.duration = ts.Sub(start)
	}
	if entry.Handler != "" {
		f.handler = entry.Handler
	}
}

func printNodeDiagnosis(f *nodeFailure) {
	headerStyle := lipgloss.NewStyle().Bold(true).Foreground(colorHot)
	labelStyle := lipgloss.NewStyle().Foreground(colorSky).Bold(true)

	fmt.Println(headerStyle.Render(fmt.Sprintf("  ✗ %s", f.nodeID)))

	printNodeDiagnosisMeta(f, labelStyle)
	printIndentedBlock(labelStyle, "Output:", f.stdout)
	printIndentedBlock(labelStyle, "Stderr:", f.stderr)
	printNodeDiagnosisErrors(f, labelStyle)

	// If no useful info was found, say so.
	if f.stdout == "" && f.stderr == "" && len(f.errors) == 0 {
		fmt.Printf("    %s\n", mutedStyle.Render("No error details captured — node may have failed silently"))
	}

	fmt.Println()
}

// printNodeDiagnosisMeta prints handler, duration, and retry count for a node failure.
func printNodeDiagnosisMeta(f *nodeFailure, labelStyle lipgloss.Style) {
	if f.handler != "" {
		fmt.Printf("    %s %s\n", labelStyle.Render("Handler:"), f.handler)
	}
	if f.duration > 0 {
		durationLabel := "Duration:"
		if f.retryCount >= 2 {
			durationLabel = "Duration (last):"
		}
		fmt.Printf("    %s %s\n", labelStyle.Render(durationLabel), formatElapsed(f.duration))
	}
	if f.retryCount >= 2 {
		retryInfo := fmt.Sprintf("%d failures", f.retryCount)
		if f.identicalRetries {
			retryInfo += " (all identical — deterministic)"
		}
		fmt.Printf("    %s %s\n", labelStyle.Render("Attempts:"), retryInfo)
	}
}

// printNodeDiagnosisErrors prints deduplicated error messages for a node failure.
func printNodeDiagnosisErrors(f *nodeFailure, labelStyle lipgloss.Style) {
	if len(f.errors) == 0 {
		return
	}
	seen := make(map[string]bool)
	fmt.Printf("    %s\n", labelStyle.Render("Errors:"))
	for _, e := range f.errors {
		if !seen[e] {
			seen[e] = true
			fmt.Printf("      %s\n", e)
		}
	}
}

// printIndentedBlock prints a labeled multi-line block with 6-space indent.
func printIndentedBlock(labelStyle lipgloss.Style, label, content string) {
	if content == "" {
		return
	}
	fmt.Printf("    %s\n", labelStyle.Render(label))
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			fmt.Printf("      %s\n", line)
		}
	}
}

func printDiagnoseSuggestions(failures map[string]*nodeFailure, cp *pipeline.Checkpoint) {
	fmt.Println("─── Suggestions ───────────────────────────────────────────")

	var suggestions []string
	for _, f := range failures {
		suggestions = append(suggestions, suggestionsForFailure(f)...)
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

// suggestionsForFailure generates actionable suggestions for a single node failure.
func suggestionsForFailure(f *nodeFailure) []string {
	var out []string
	out = append(out, suggestRetryPattern(f)...)
	out = append(out, suggestOutputPatterns(f)...)
	return out
}

// suggestRetryPattern detects deterministic vs flaky retry failures.
func suggestRetryPattern(f *nodeFailure) []string {
	if f.identicalRetries && f.retryCount >= 2 {
		return []string{fmt.Sprintf("%s: Failed %d times with identical errors — this is a "+
			"deterministic bug in the command, not a transient failure. Retrying won't help. "+
			"Fix the tool command in the .dip file and re-run.", f.nodeID, f.retryCount)}
	}
	if f.retryCount >= 3 {
		return []string{fmt.Sprintf("%s: Failed %d times with varying errors — may be a flaky "+
			"command or environment issue.", f.nodeID, f.retryCount)}
	}
	return nil
}

// suggestOutputPatterns checks stdout/stderr for known failure signatures.
func suggestOutputPatterns(f *nodeFailure) []string {
	var out []string
	out = append(out, suggestEscalateLimitPattern(f)...)
	out = append(out, suggestNoOutputPattern(f)...)
	out = append(out, suggestShellCommandPattern(f)...)
	out = append(out, suggestGoTestPattern(f)...)
	out = append(out, suggestSuspiciouslyFastPattern(f)...)
	return out
}

// suggestEscalateLimitPattern detects the fix_attempts escalation sentinel in stdout.
func suggestEscalateLimitPattern(f *nodeFailure) []string {
	if strings.Contains(f.stdout, "ESCALATE") && strings.Contains(f.stdout, "fix attempts") {
		return []string{fmt.Sprintf("%s: Hit fix attempt limit. The fix_attempts counter persists "+
			"on disk across restarts — if you retry after escalation, the counter "+
			"is already maxed. Reset it with: rm .ai/milestones/fix_attempts", f.nodeID)}
	}
	return nil
}

// suggestNoOutputPattern detects failures that produced no diagnostics at all.
func suggestNoOutputPattern(f *nodeFailure) []string {
	if f.stdout == "" && f.stderr == "" && len(f.errors) == 0 {
		return []string{fmt.Sprintf("%s: No error details captured. Check the activity.jsonl "+
			"for this node's events: grep %q activity.jsonl | tail -20", f.nodeID, f.nodeID)}
	}
	return nil
}

// suggestShellCommandPattern detects missing command or file errors in stderr.
func suggestShellCommandPattern(f *nodeFailure) []string {
	if strings.Contains(f.stderr, "command not found") || strings.Contains(f.stderr, "No such file or directory") {
		return []string{fmt.Sprintf("%s: Shell command failed — check that the working directory "+
			"and required tools exist before running", f.nodeID)}
	}
	return nil
}

// suggestGoTestPattern detects Go test failures in stdout.
func suggestGoTestPattern(f *nodeFailure) []string {
	if strings.Contains(f.stdout, "FAIL") && strings.Contains(f.stdout, "go test") {
		return []string{fmt.Sprintf("%s: Go test failures — check if .ai/milestones/known_failures "+
			"should include these tests for this milestone", f.nodeID)}
	}
	return nil
}

// suggestSuspiciouslyFastPattern detects non-tool nodes that completed too quickly.
func suggestSuspiciouslyFastPattern(f *nodeFailure) []string {
	if f.duration > 0 && f.duration < 50*time.Millisecond && f.handler != "tool" {
		return []string{fmt.Sprintf("%s: Completed in %s — suspiciously fast. May indicate "+
			"a configuration issue or missing handler", f.nodeID, formatElapsed(f.duration))}
	}
	return nil
}
