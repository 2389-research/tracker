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
	RunID          string      `json:"run_id"`
	CompletedNodes int         `json:"completed_nodes"`
	BudgetHalt     *BudgetHalt `json:"budget_halt,omitempty"`
	// ValidationOverrides is the list of override edges traversed during the run.
	// Sourced from activity log first, with checkpoint fallback for runs whose
	// activity log is missing (legacy / archived). Empty for runs with no
	// override edges. Populated for every terminal status (success, fail,
	// budget_exceeded, validation_overridden) so override forensics survive
	// failure-after-override scenarios. Per spec §9.4 the override section in
	// `tracker diagnose` is informational only — override is NOT a failure and
	// does NOT raise a Suggestion of its own.
	ValidationOverrides []pipeline.OverrideDetail `json:"validation_overrides,omitempty"`
	// OverrideCount is len(ValidationOverrides). Kept as its own field so
	// JSON consumers can branch without iterating the slice.
	OverrideCount int           `json:"override_count,omitempty"`
	Failures      []NodeFailure `json:"failures"`
	Suggestions   []Suggestion  `json:"suggestions"`
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
	// SuggestionToolMarkerMissing fires when a tool node declared
	// marker_grep but the regex matched nothing (or failed to compile).
	// Surfaces the configured pattern, the captured stdout tail, and the
	// recommended fix. Issue #210.
	SuggestionToolMarkerMissing SuggestionKind = "tool_marker_missing"
	// SuggestionToolRouteMissing fires when a tool node had
	// route_required: true but no _TRACKER_ROUTE= sentinel line was
	// emitted to stdout. Surfaces the captured stdout tail and the
	// recommended author pattern. Issue #212.
	SuggestionToolRouteMissing SuggestionKind = "tool_route_missing"
	// SuggestionAutoStatusMissing fires when an auto_status agent node
	// completed normally but its response contained no parseable STATUS
	// line (#346). Goal-gate nodes fail closed; plain auto_status nodes
	// keep the legacy success default — the suggestion copy distinguishes
	// the two so a silently-defaulted verdict is visible post-run.
	SuggestionAutoStatusMissing SuggestionKind = "auto_status_missing"
	// SuggestionAuditLogInjection fires when the integrity-protected
	// activity log has one or more lines missing the runtime sentinel
	// prefix (#213). Detection-only — the suggestion text is explicit
	// that the sentinel is not authentication; a motivated forger who
	// reads tracker's source can emit the bytes. Surfaces the count of
	// suspect lines and the audit-log path so operators can
	// investigate.
	SuggestionAuditLogInjection SuggestionKind = "audit_log_injection"
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
	// Source ValidationOverrides from activity log first; fall back to the sticky
	// checkpoint slice when the activity log carries no override entries (legacy
	// runs, archived activity logs, etc.). Mirrors the Audit() pattern in
	// tracker_audit.go. Per spec §9.4: override is NOT a failure, so the
	// suggestion builder is intentionally not informed — the diagnose render
	// surfaces overrides as a separate informational section.
	if activity, err := LoadActivityLog(runDir); err == nil {
		SortActivityByTime(activity)
		overrides := extractOverridesFromActivity(activity)
		if len(overrides) == 0 {
			overrides = cp.ValidationOverrides
		}
		report.ValidationOverrides = overrides
		report.OverrideCount = len(overrides)
	} else {
		// Activity log unreadable: fall back to checkpoint sticky slice.
		report.ValidationOverrides = cp.ValidationOverrides
		report.OverrideCount = len(cp.ValidationOverrides)
	}
	report.Suggestions = append(buildSuggestions(report.Failures, report.BudgetHalt, anomalies), costAsymmetrySuggestions(scanCostDomination(ctx, runDir))...)
	return report, nil
}

// runtimeAnomalies collects runtime events that warrant a surfaced
// suggestion in the diagnose report — separate from the per-node
// failures list so the suggestion builder can reason about them as
// their own typed signals. Today: tool stdout/stderr truncations
// (#208), conditional-edge fallthroughs (#208 Tier 2),
// tool_marker_missing events (#210), and tool_route_missing events
// (#212).
//
// Routing-flow semantics differ by event type:
//
//   - Truncations & fallthroughs are non-failure events on their own
//     — the node may still have succeeded, and the suggestion explains
//     why routing picked the fallback edge.
//   - Marker misses and route misses are failures by construction:
//     the tool handler sets OutcomeFail to prevent the silent-
//     fallback foot-gun that marker_grep / route_required exist to
//     remove. There is no fallback edge to explain; the suggestion
//     explains why the node failed and what the operator needs to
//     fix (different mechanisms, same shape: marker_grep is the
//     attribute-declared regex channel, route sentinel is the
//     convention-based stdout-line channel).
type runtimeAnomalies struct {
	Truncations    []truncObservation
	Fallthroughs   []fallthroughObservation
	MarkerMissings []markerMissingObservation
	RouteMissings  []routeMissingObservation
	StatusMissings []statusMissingObservation
	// VisitStarts records per-node stage_started events so the
	// suggestion builder can flush stale pending truncations from a
	// prior visit as orphans before pairing within the new visit.
	VisitStarts []visitBoundary
	// InjectedLines counts non-sentinel lines in the integrity-
	// protected activity log (#213). Non-zero implies something other
	// than the tracker runtime wrote to the secure file — either an
	// injected forgery from a tool subprocess that discovered the
	// absolute path, or a runtime bug. Always 0 when the legacy
	// fallback path was the source: legacy/snapshot files don't carry
	// the sentinel and absence isn't a signal there.
	InjectedLines int
	// AuditLogPath is the on-disk path the scan read from. Surfaced in
	// the SuggestionAuditLogInjection message so operators know which
	// file to inspect. Empty when the activity log didn't exist.
	AuditLogPath string
}

type markerMissingObservation struct {
	Seq          int
	NodeID       string
	Pattern      string
	CapturedTail string
	Error        string
}

type routeMissingObservation struct {
	Seq          int
	NodeID       string
	CapturedTail string
}

type statusMissingObservation struct {
	Seq          int
	NodeID       string
	ResponseTail string
	FailClosed   bool
}

// Seq is a monotonically-increasing scan position shared across all
// runtime anomaly observation types, assigned in chronological order
// during the activity.jsonl scan. The suggestion builder uses it to
// merge truncations, fallthroughs, and visit-boundary markers into a
// single ordered stream so that loops/restarts don't mis-correlate a
// truncation on visit N with a fallthrough on visit M.
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

// visitBoundary marks a stage_started event for a node. The suggestion
// builder uses these to flush any pending per-node truncations from a
// prior visit as orphans before the new visit's events arrive — so two
// back-to-back same-node truncations separated by a re-entry get
// treated as two visits (one orphan + one new), while two back-to-back
// truncations within the same visit (stdout + stderr both overflowed)
// accumulate together and pair as a group with the visit's fallthrough.
type visitBoundary struct {
	Seq    int
	NodeID string
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

	// Tool-marker-missing event fields (#210).
	MarkerPattern string `json:"marker_pattern"`
	MarkerTail    string `json:"marker_tail"`
	MarkerError   string `json:"marker_error"`

	// Tool-route-missing event fields (#212).
	RouteTail string `json:"route_tail"`

	// Auto-status-missing event fields (#346).
	AutoStatusTail       string `json:"auto_status_tail"`
	AutoStatusFailClosed bool   `json:"auto_status_fail_closed"`
}

// enrichFromActivity streams the activity log (preferring the secure
// path; see ResolveActivityLogPath), populating failures + detecting
// budget halt events and runtime anomalies (tool-output truncations,
// conditional fallthroughs). Returns (nil, runtimeAnomalies{}, nil) if
// the activity log does not exist (runs that never started). Returns
// ctx.Err() if cancellation fires mid-parse, and scanner.Err() if the
// scanner aborts (buffer overflow at 1 MB, I/O error) — both surface
// truncation to the caller so partial analysis is never silently treated
// as authoritative.
//
// When the secure path is the source, lines lacking the runtime
// sentinel prefix are counted in anomalies.InjectedLines so the
// suggestion builder can fire SuggestionAuditLogInjection. Sentinel
// stripping happens here, before JSON unmarshaling.
func enrichFromActivity(ctx context.Context, runDir string, failures map[string]*NodeFailure, logW io.Writer) (*BudgetHalt, runtimeAnomalies, error) {
	path, secureUsed := ResolveActivityLogPath(runDir)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, runtimeAnomalies{}, nil
		}
		return nil, runtimeAnomalies{}, fmt.Errorf("open activity log: %w", err)
	}
	defer f.Close()
	anomalies := runtimeAnomalies{AuditLogPath: path}
	stageStarts := map[string]time.Time{}
	failSignatures := map[string][]string{}

	scanner := bufio.NewScanner(f)
	// Match LoadActivityLog: allow 1 MB lines.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	// scanActivityLoop returns early (without touching scanner.Err) on
	// context cancellation, so a cancelled diagnose is distinguishable from
	// a scanner read error and does not emit a spurious warning.
	halt, err := scanActivityLoop(ctx, scanner, secureUsed, &anomalies, failures, stageStarts, failSignatures)
	if err != nil {
		return nil, runtimeAnomalies{}, err
	}
	if err := scanner.Err(); err != nil {
		fmt.Fprintf(logW, "warning: activity log scanner stopped at %s: %v\n", path, err)
		return nil, runtimeAnomalies{}, fmt.Errorf("scan activity log: %w", err)
	}
	applyRetryAnalysis(failures, failSignatures)
	return halt, anomalies, nil
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
