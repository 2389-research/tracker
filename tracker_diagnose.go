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
	// LogWriter receives non-fatal parse/read warnings — specifically
	// malformed status.json content (one warning per bad file) and
	// bufio.Scanner errors while reading activity.jsonl (e.g. lines
	// exceeding the 1 MB buffer limit, I/O failures). Nil is treated
	// as io.Discard so library callers do not see stray warnings on
	// os.Stderr. The tracker CLI sets this to io.Discard for user-
	// facing commands.
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
	// It is encoded as integer nanoseconds in JSON ("duration_ns"), not
	// as a duration string.
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
	// SuggestionToolOutputTruncated fires when a tool node's output stream
	// exceeded its per-stream cap. Surfaces actionable copy pointing at
	// output_limit and at the canonical authoring pattern. Issue #208.
	SuggestionToolOutputTruncated SuggestionKind = "tool_output_truncated"
	// SuggestionConditionalFallthrough fires when a node's conditional
	// routing edges all evaluated false and routing fell back to an
	// unconditional edge. Issue #208.
	SuggestionConditionalFallthrough SuggestionKind = "conditional_fallthrough"
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
	halt, anomalies, err := enrichFromActivity(ctx, runDir, failures, logW)
	if err != nil {
		return nil, err
	}
	report.BudgetHalt = halt
	report.Failures = sortedFailures(failures)
	report.Suggestions = buildSuggestions(report.Failures, report.BudgetHalt, anomalies)
	return report, nil
}

// runtimeAnomalies are non-failure events that nonetheless warrant a
// surfaced suggestion in the diagnose report. Today: tool stdout/stderr
// truncations (#208) and conditional-edge fallthroughs (#208 Tier 2).
type runtimeAnomalies struct {
	Truncations  []truncObservation
	Fallthroughs []fallthroughObservation
}

// Seq is a monotonically-increasing scan position shared across all
// runtime anomaly observation types, assigned in chronological order
// during the activity.jsonl scan. The suggestion builder uses it to
// merge truncations and fallthroughs into a single ordered stream so
// that loops/restarts don't mis-correlate a truncation on visit N with
// a fallthrough on visit M.
type truncObservation struct {
	Seq           int
	NodeID        string
	Stream        string
	Limit         int
	CapturedBytes int
	DroppedBytes  int
	TotalBytes    int
}

type fallthroughObservation struct {
	Seq             int
	NodeID          string
	EdgeTo          string
	ConditionsTried []pipeline.ConditionEval
}

// DiagnoseMostRecent finds the most recent run under workdir and diagnoses it.
func DiagnoseMostRecent(ctx context.Context, workdir string, opts ...DiagnoseConfig) (*DiagnoseReport, error) {
	cfg := firstDiagnoseConfig(opts)
	id, err := mostRecentRunID(workdir, logWriterOrDiscard(cfg.LogWriter))
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

	// Truncation event fields (#208).
	TruncStream   string `json:"trunc_stream"`
	TruncLimit    int    `json:"trunc_limit"`
	TruncCaptured int    `json:"trunc_captured_bytes"`
	TruncDropped  int    `json:"trunc_dropped_bytes"`
	TruncTotal    int    `json:"trunc_total_bytes"`

	// Conditional-fallthrough event fields (#208).
	EdgeTo          string                   `json:"edge_to"`
	ConditionsTried []pipeline.ConditionEval `json:"conditions_tried"`
}

// enrichFromActivity streams activity.jsonl, populating failures + detecting
// budget halt events and runtime anomalies (tool-output truncations,
// conditional fallthroughs). Returns (nil, runtimeAnomalies{}, nil) if
// activity.jsonl does not exist (runs that never started). Returns
// ctx.Err() if cancellation fires mid-parse, and scanner.Err() if the
// scanner aborts (buffer overflow at 1 MB, I/O error) — both surface
// truncation to the caller so partial analysis is never silently treated
// as authoritative.
func enrichFromActivity(ctx context.Context, runDir string, failures map[string]*NodeFailure, logW io.Writer) (*BudgetHalt, runtimeAnomalies, error) {
	path := filepath.Join(runDir, "activity.jsonl")
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, runtimeAnomalies{}, nil
		}
		return nil, runtimeAnomalies{}, fmt.Errorf("open activity log: %w", err)
	}
	defer f.Close()

	stageStarts := map[string]time.Time{}
	failSignatures := map[string][]string{}
	var halt *BudgetHalt
	var anomalies runtimeAnomalies
	// anomalySeq increments on every truncation or fallthrough so the
	// suggestion builder can merge the two slices into a single
	// chronologically-ordered stream — required to correctly pair the
	// truncation and fallthrough from the same node-visit when the
	// pipeline loops through that node multiple times.
	anomalySeq := 0

	scanner := bufio.NewScanner(f)
	// Match LoadActivityLog: allow 1 MB lines.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return nil, runtimeAnomalies{}, err
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var entry diagnoseEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		switch pipeline.PipelineEventType(entry.Type) {
		case pipeline.EventBudgetExceeded:
			halt = &BudgetHalt{
				TotalTokens:   entry.TotalTokens,
				TotalCostUSD:  entry.TotalCostUSD,
				WallElapsedMs: entry.WallElapsedMs,
				Message:       entry.Message,
			}
		case pipeline.EventToolOutputTruncated:
			anomalySeq++
			anomalies.Truncations = append(anomalies.Truncations, truncObservation{
				Seq:           anomalySeq,
				NodeID:        entry.NodeID,
				Stream:        entry.TruncStream,
				Limit:         entry.TruncLimit,
				CapturedBytes: entry.TruncCaptured,
				DroppedBytes:  entry.TruncDropped,
				TotalBytes:    entry.TruncTotal,
			})
		case pipeline.EventConditionalFallthrough:
			anomalySeq++
			anomalies.Fallthroughs = append(anomalies.Fallthroughs, fallthroughObservation{
				Seq:             anomalySeq,
				NodeID:          entry.NodeID,
				EdgeTo:          entry.EdgeTo,
				ConditionsTried: entry.ConditionsTried,
			})
		}
		enrichFromEntry(entry, failures, stageStarts, failSignatures)
	}
	if err := scanner.Err(); err != nil {
		fmt.Fprintf(logW, "warning: activity log scanner stopped at %s: %v\n", path, err)
		return nil, runtimeAnomalies{}, fmt.Errorf("scan activity log: %w", err)
	}
	applyRetryAnalysis(failures, failSignatures)
	return halt, anomalies, nil
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

func buildSuggestions(failures []NodeFailure, halt *BudgetHalt, anomalies runtimeAnomalies) []Suggestion {
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
	// Merge truncations and fallthroughs into a single chronological
	// stream using the Seq counter populated during the activity.jsonl
	// scan, then walk it with a per-node state machine to correlate
	// events from the same node-visit. The order within a single visit
	// is always truncation-then-fallthrough (engine_run.go emits the
	// truncation event right after tool execution; selectEdge emits
	// the fallthrough event during edge selection), so a pending
	// truncation pairs with the next fallthrough on the same node IF
	// no other truncation on that node falls in between. Anything
	// unpaired emits as its own standalone suggestion. This is the
	// minimum machinery needed to keep loops + restarts from
	// mis-correlating events across iterations (#208 follow-up).
	type pendingTrunc struct {
		ok bool
		tr truncObservation
	}
	pending := map[string]pendingTrunc{}
	type combined struct {
		seq int
		tr  *truncObservation // exactly one of tr/fb is non-nil
		fb  *fallthroughObservation
	}
	merged := make([]combined, 0, len(anomalies.Truncations)+len(anomalies.Fallthroughs))
	for i := range anomalies.Truncations {
		t := &anomalies.Truncations[i]
		merged = append(merged, combined{seq: t.Seq, tr: t})
	}
	for i := range anomalies.Fallthroughs {
		f := &anomalies.Fallthroughs[i]
		merged = append(merged, combined{seq: f.Seq, fb: f})
	}
	sort.Slice(merged, func(i, j int) bool { return merged[i].seq < merged[j].seq })

	emitTrunc := func(tr truncObservation, paired *fallthroughObservation) {
		msg := fmt.Sprintf("%s: %s truncated — captured last %d bytes of %d (dropped %d from head; limit %d). The tail-window capture is designed to preserve a routing marker emitted at end-of-output (as long as the marker fits within the limit). Raise the per-node `output_limit` attribute if you need more context retained or if the marker itself is larger than the cap.",
			tr.NodeID, tr.Stream, tr.CapturedBytes, tr.TotalBytes, tr.DroppedBytes, tr.Limit)
		if paired != nil {
			var tried []string
			for _, c := range paired.ConditionsTried {
				tried = append(tried, c.Condition)
			}
			msg += fmt.Sprintf(" Note: routing on this node also fell through to %q after %d conditional edge(s) evaluated false (%s) — verify the captured tail is what you expect.",
				paired.EdgeTo, len(paired.ConditionsTried), strings.Join(tried, "; "))
		}
		out = append(out, Suggestion{
			NodeID: tr.NodeID, Kind: SuggestionToolOutputTruncated, Message: msg,
		})
	}
	emitFt := func(fb fallthroughObservation) {
		var tried []string
		for _, c := range fb.ConditionsTried {
			tried = append(tried, c.Condition)
		}
		out = append(out, Suggestion{
			NodeID: fb.NodeID, Kind: SuggestionConditionalFallthrough,
			Message: fmt.Sprintf("%s: %d conditional edge(s) all evaluated false (%s); routing fell back to %q. If this was unintentional, check the routing context — `ctx.outcome`, `ctx.tool_stdout`, or whatever your conditions reference.",
				fb.NodeID, len(fb.ConditionsTried), strings.Join(tried, "; "), fb.EdgeTo),
		})
	}

	for _, ev := range merged {
		switch {
		case ev.tr != nil:
			// New truncation on this node — flush any prior unpaired
			// truncation on the same node (orphan from a previous visit)
			// before stashing this one as the new pending.
			if p, ok := pending[ev.tr.NodeID]; ok && p.ok {
				emitTrunc(p.tr, nil)
			}
			pending[ev.tr.NodeID] = pendingTrunc{ok: true, tr: *ev.tr}
		case ev.fb != nil:
			// Fallthrough on this node — pair with the pending truncation
			// if one exists; otherwise emit standalone (a fallthrough can
			// only pair with a *prior* truncation in the same visit).
			if p, ok := pending[ev.fb.NodeID]; ok && p.ok {
				emitTrunc(p.tr, ev.fb)
				delete(pending, ev.fb.NodeID)
			} else {
				emitFt(*ev.fb)
			}
		}
	}
	// Flush any pending truncations that never got a matching fallthrough.
	// Iterate the original slice (not the map) so output order is
	// deterministic across runs.
	for _, tr := range anomalies.Truncations {
		if p, ok := pending[tr.NodeID]; ok && p.ok && p.tr.Seq == tr.Seq {
			emitTrunc(p.tr, nil)
			delete(pending, tr.NodeID)
		}
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
