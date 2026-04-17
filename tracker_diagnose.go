// ABOUTME: Library API for diagnosing pipeline run failures.
// ABOUTME: Reads checkpoint + status.json + activity.jsonl and returns a structured report.
package tracker

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/2389-research/tracker/pipeline"
)

// DiagnoseConfig configures a Diagnose() run.
type DiagnoseConfig struct {
	// LogWriter receives non-fatal parse warnings (malformed status.json
	// files, unreadable artifact directories). Nil is treated as
	// io.Discard so library callers do not see stray warnings on
	// os.Stderr. The tracker CLI sets this to io.Discard for user-facing
	// commands.
	LogWriter io.Writer
}

// DiagnoseReport is the structured output of Diagnose / DiagnoseMostRecent.
type DiagnoseReport struct {
	RunID          string        `json:"run_id"`
	CompletedNodes int           `json:"completed_nodes"`
	BudgetHalt     *BudgetHalt   `json:"budget_halt,omitempty"`
	Failures       []NodeFailure `json:"failures"`
	Suggestions    []Suggestion  `json:"suggestions"`
}

// NodeFailure captures everything known about a failed node.
type NodeFailure struct {
	NodeID  string `json:"node_id"`
	Outcome string `json:"outcome"`
	Handler string `json:"handler,omitempty"`
	// Duration is the elapsed time for the most recent attempt of the node.
	Duration time.Duration `json:"duration_ns,omitempty"`
	// RetryCount is the number of stage_failed events observed for this node
	// — i.e., the total failure count, not "retries beyond the first attempt."
	// A node that failed once (no retry) has RetryCount == 1.
	RetryCount int `json:"retry_count,omitempty"`
	// IdenticalRetries is true when every stage_failed event had the same
	// error/tool_error signature — a deterministic bug, not a flaky one.
	IdenticalRetries bool     `json:"identical_retries,omitempty"`
	Stdout           string   `json:"stdout,omitempty"`
	Stderr           string   `json:"stderr,omitempty"`
	Errors           []string `json:"errors,omitempty"`
}

// BudgetHalt holds information about a budget halt detected in the activity log.
type BudgetHalt struct {
	TotalTokens   int     `json:"total_tokens"`
	TotalCostUSD  float64 `json:"total_cost_usd"`
	WallElapsedMs int64   `json:"wall_elapsed_ms"`
	Message       string  `json:"message"`
}

// SuggestionKind is the typed string identifying which template produced a
// Suggestion. The underlying string values are stable; new kinds may be
// added additively.
type SuggestionKind string

// Suggestion is an actionable recommendation produced by Diagnose.
type Suggestion struct {
	NodeID  string         `json:"node_id,omitempty"`
	Kind    SuggestionKind `json:"kind"`
	Message string         `json:"message"`
}

// Suggestion kinds (stable; new ones may be added additively).
const (
	SuggestionRetryPattern     SuggestionKind = "retry_pattern"
	SuggestionEscalateLimit    SuggestionKind = "escalate_limit"
	SuggestionNoOutput         SuggestionKind = "no_output"
	SuggestionShellCommand     SuggestionKind = "shell_command"
	SuggestionGoTest           SuggestionKind = "go_test"
	SuggestionSuspiciousTiming SuggestionKind = "suspicious_timing"
	SuggestionBudget           SuggestionKind = "budget"
)

// Diagnose analyzes a run directory and returns a structured report.
//
// The runDir argument must be a trusted path — Diagnose reads
// checkpoint.json, activity.jsonl, and every <nodeID>/status.json
// under it. For user-supplied input, resolve the path via
// ResolveRunDir or DiagnoseMostRecent first, which enforce the
// .tracker/runs/<runID> layout.
//
// If ctx is cancelled mid-parse, Diagnose returns ctx.Err() — a partial
// report is never returned as a success, so callers using deadlines can
// distinguish complete from truncated analysis. A nil ctx is treated as
// context.Background() (no cancellation possible).
func Diagnose(ctx context.Context, runDir string, opts ...DiagnoseConfig) (*DiagnoseReport, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	cfg := firstDiagnoseConfig(opts)
	logW := logWriterOrDiscard(cfg.LogWriter)

	cpPath := filepath.Join(runDir, "checkpoint.json")
	cp, err := pipeline.LoadCheckpoint(cpPath)
	if err != nil {
		return nil, fmt.Errorf("load checkpoint: %w", err)
	}
	report := &DiagnoseReport{
		RunID:          cp.RunID,
		CompletedNodes: len(cp.CompletedNodes),
	}
	failures := collectNodeFailures(runDir, logW)
	halt, err := enrichFromActivity(ctx, runDir, failures, logW)
	if err != nil {
		return nil, err
	}
	report.BudgetHalt = halt
	report.Failures = sortedFailures(failures)
	report.Suggestions = buildSuggestions(report.Failures, report.BudgetHalt)
	return report, nil
}

// DiagnoseMostRecent finds the most recent run under workdir and diagnoses it.
func DiagnoseMostRecent(ctx context.Context, workdir string, opts ...DiagnoseConfig) (*DiagnoseReport, error) {
	id, err := MostRecentRunID(workdir)
	if err != nil {
		return nil, err
	}
	return Diagnose(ctx, filepath.Join(workdir, ".tracker", "runs", id), opts...)
}

func firstDiagnoseConfig(opts []DiagnoseConfig) DiagnoseConfig {
	if len(opts) == 0 {
		return DiagnoseConfig{}
	}
	return opts[0]
}

func logWriterOrDiscard(w io.Writer) io.Writer {
	if w == nil {
		return io.Discard
	}
	return w
}

// ----- internals -----

func collectNodeFailures(runDir string, logW io.Writer) map[string]*NodeFailure {
	failures := make(map[string]*NodeFailure)
	entries, err := os.ReadDir(runDir)
	if err != nil {
		return failures
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if f := loadNodeFailure(runDir, e.Name(), logW); f != nil {
			failures[e.Name()] = f
		}
	}
	return failures
}

func loadNodeFailure(runDir, nodeID string, logW io.Writer) *NodeFailure {
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
		fmt.Fprintf(logW, "warning: cannot parse %s: %v\n", statusPath, err)
		return nil
	}
	if status.Outcome != "fail" {
		return nil
	}
	f := &NodeFailure{NodeID: nodeID, Outcome: status.Outcome}
	if status.ContextUpdates != nil {
		f.Stdout = status.ContextUpdates["tool_stdout"]
		f.Stderr = status.ContextUpdates["tool_stderr"]
	}
	return f
}

// diagnoseEntry is a parsed activity.jsonl line with fields needed for diagnosis.
type diagnoseEntry struct {
	Timestamp     string  `json:"ts"`
	Type          string  `json:"type"`
	NodeID        string  `json:"node_id"`
	Message       string  `json:"message"`
	Error         string  `json:"error"`
	ToolErr       string  `json:"tool_error"`
	Handler       string  `json:"handler"`
	TotalTokens   int     `json:"total_tokens"`
	TotalCostUSD  float64 `json:"total_cost_usd"`
	WallElapsedMs int64   `json:"wall_elapsed_ms"`
}

// enrichFromActivity streams activity.jsonl, populating failures + detecting
// budget halt events. Returns (nil, nil) if activity.jsonl does not exist
// (runs that never started). Returns ctx.Err() if cancellation fires
// mid-parse, and scanner.Err() if the scanner aborts (buffer overflow at
// 1 MB, I/O error) — both surface truncation to the caller so partial
// analysis is never silently treated as authoritative.
func enrichFromActivity(ctx context.Context, runDir string, failures map[string]*NodeFailure, logW io.Writer) (*BudgetHalt, error) {
	path := filepath.Join(runDir, "activity.jsonl")
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("open activity log: %w", err)
	}
	defer f.Close()

	stageStarts := map[string]time.Time{}
	failSignatures := map[string][]string{}
	var halt *BudgetHalt

	scanner := bufio.NewScanner(f)
	// Match LoadActivityLog: allow 1 MB lines.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var entry diagnoseEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		if entry.Type == "budget_exceeded" {
			halt = &BudgetHalt{
				TotalTokens:   entry.TotalTokens,
				TotalCostUSD:  entry.TotalCostUSD,
				WallElapsedMs: entry.WallElapsedMs,
				Message:       entry.Message,
			}
		}
		enrichFromEntry(entry, failures, stageStarts, failSignatures)
	}
	if err := scanner.Err(); err != nil {
		fmt.Fprintf(logW, "warning: activity log scanner stopped at %s: %v\n", path, err)
		return nil, fmt.Errorf("scan activity log: %w", err)
	}
	applyRetryAnalysis(failures, failSignatures)
	return halt, nil
}

func enrichFromEntry(entry diagnoseEntry, failures map[string]*NodeFailure, stageStarts map[string]time.Time, failSignatures map[string][]string) {
	ts, _ := parseActivityTimestamp(entry.Timestamp)
	switch entry.Type {
	case "stage_started":
		if !ts.IsZero() {
			stageStarts[entry.NodeID] = ts
		}
	case "stage_failed":
		updateFailureTiming(failures[entry.NodeID], stageStarts, entry, ts)
		sig := entry.Error + "\x00" + entry.ToolErr
		failSignatures[entry.NodeID] = append(failSignatures[entry.NodeID], sig)
	case "stage_completed":
		updateFailureTiming(failures[entry.NodeID], stageStarts, entry, ts)
	}
	if entry.NodeID == "" {
		return
	}
	f, ok := failures[entry.NodeID]
	if !ok {
		return
	}
	if entry.Error != "" {
		f.Errors = append(f.Errors, entry.Error)
	}
	if entry.ToolErr != "" && f.Stderr == "" {
		f.Stderr = entry.ToolErr
	}
}

func updateFailureTiming(f *NodeFailure, stageStarts map[string]time.Time, entry diagnoseEntry, ts time.Time) {
	if f == nil {
		return
	}
	if start, ok := stageStarts[entry.NodeID]; ok && !start.IsZero() && !ts.IsZero() {
		f.Duration = ts.Sub(start)
	}
	if entry.Handler != "" {
		f.Handler = entry.Handler
	}
}

func applyRetryAnalysis(failures map[string]*NodeFailure, failSignatures map[string][]string) {
	for nodeID, sigs := range failSignatures {
		f, ok := failures[nodeID]
		if !ok {
			continue
		}
		f.RetryCount = len(sigs)
		if len(sigs) >= 2 {
			f.IdenticalRetries = allIdenticalStrings(sigs)
		}
	}
}

func allIdenticalStrings(ss []string) bool {
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

func sortedFailures(m map[string]*NodeFailure) []NodeFailure {
	ids := make([]string, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]NodeFailure, 0, len(ids))
	for _, id := range ids {
		out = append(out, *m[id])
	}
	return out
}

func buildSuggestions(failures []NodeFailure, halt *BudgetHalt) []Suggestion {
	var out []Suggestion
	for _, f := range failures {
		out = append(out, suggestionsForNodeFailure(f)...)
	}
	if halt != nil {
		out = append(out, Suggestion{
			Kind:    SuggestionBudget,
			Message: "Raise the relevant --max-tokens, --max-cost, or --max-wall-time flag, or remove the Config.Budget value",
		})
	}
	return out
}

func suggestionsForNodeFailure(f NodeFailure) []Suggestion {
	var out []Suggestion
	if f.IdenticalRetries && f.RetryCount >= 2 {
		out = append(out, Suggestion{
			NodeID: f.NodeID, Kind: SuggestionRetryPattern,
			Message: fmt.Sprintf("%s: Failed %d times with identical errors — this is a deterministic bug in the command, not a transient failure. Retrying won't help. Fix the tool command in the .dip file and re-run.", f.NodeID, f.RetryCount),
		})
	} else if f.RetryCount >= 3 {
		out = append(out, Suggestion{
			NodeID: f.NodeID, Kind: SuggestionRetryPattern,
			Message: fmt.Sprintf("%s: Failed %d times with varying errors — may be a flaky command or environment issue.", f.NodeID, f.RetryCount),
		})
	}
	if strings.Contains(f.Stdout, "ESCALATE") && strings.Contains(f.Stdout, "fix attempts") {
		out = append(out, Suggestion{
			NodeID: f.NodeID, Kind: SuggestionEscalateLimit,
			Message: fmt.Sprintf("%s: Hit fix attempt limit. The fix_attempts counter persists on disk across restarts — if you retry after escalation, the counter is already maxed. Reset it with: rm .ai/milestones/fix_attempts", f.NodeID),
		})
	}
	if f.Stdout == "" && f.Stderr == "" && len(f.Errors) == 0 {
		out = append(out, Suggestion{
			NodeID: f.NodeID, Kind: SuggestionNoOutput,
			Message: fmt.Sprintf("%s: No error details captured. Check the activity.jsonl for this node's events: grep %q activity.jsonl | tail -20", f.NodeID, f.NodeID),
		})
	}
	if strings.Contains(f.Stderr, "command not found") || strings.Contains(f.Stderr, "No such file or directory") {
		out = append(out, Suggestion{
			NodeID: f.NodeID, Kind: SuggestionShellCommand,
			Message: fmt.Sprintf("%s: Shell command failed — check that the working directory and required tools exist before running", f.NodeID),
		})
	}
	if strings.Contains(f.Stdout, "FAIL") && strings.Contains(f.Stdout, "go test") {
		out = append(out, Suggestion{
			NodeID: f.NodeID, Kind: SuggestionGoTest,
			Message: fmt.Sprintf("%s: Go test failures — check if .ai/milestones/known_failures should include these tests for this milestone", f.NodeID),
		})
	}
	if f.Duration > 0 && f.Duration < 50*time.Millisecond && f.Handler != "tool" {
		out = append(out, Suggestion{
			NodeID: f.NodeID, Kind: SuggestionSuspiciousTiming,
			Message: fmt.Sprintf("%s: Completed in %s — suspiciously fast. May indicate a configuration issue or missing handler", f.NodeID, f.Duration),
		})
	}
	return out
}
