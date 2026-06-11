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
	report.Suggestions = buildSuggestions(report.Failures, report.BudgetHalt, anomalies)
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
	var halt *BudgetHalt
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
		raw := scanner.Text()
		body, hasSentinel := stripActivitySentinel(raw)
		line := strings.TrimSpace(body)
		if line == "" {
			continue
		}
		if secureUsed && !hasSentinel {
			anomalies.InjectedLines++
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
		case pipeline.EventStageStarted:
			// Mark a visit boundary so the suggestion builder can flush
			// pending truncations from a prior visit before pairing
			// within the new visit.
			if entry.NodeID != "" {
				anomalySeq++
				anomalies.VisitStarts = append(anomalies.VisitStarts, visitBoundary{
					Seq:    anomalySeq,
					NodeID: entry.NodeID,
				})
			}
		case pipeline.EventToolMarkerMissing:
			anomalySeq++
			anomalies.MarkerMissings = append(anomalies.MarkerMissings, markerMissingObservation{
				Seq:          anomalySeq,
				NodeID:       entry.NodeID,
				Pattern:      entry.MarkerPattern,
				CapturedTail: entry.MarkerTail,
				Error:        entry.MarkerError,
			})
		case pipeline.EventToolRouteMissing:
			anomalySeq++
			anomalies.RouteMissings = append(anomalies.RouteMissings, routeMissingObservation{
				Seq:          anomalySeq,
				NodeID:       entry.NodeID,
				CapturedTail: entry.RouteTail,
			})
		case pipeline.EventAutoStatusMissing:
			anomalySeq++
			anomalies.StatusMissings = append(anomalies.StatusMissings, statusMissingObservation{
				Seq:          anomalySeq,
				NodeID:       entry.NodeID,
				ResponseTail: entry.AutoStatusTail,
				FailClosed:   entry.AutoStatusFailClosed,
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
	if anomalies.InjectedLines > 0 {
		plural := "line"
		if anomalies.InjectedLines > 1 {
			plural = "lines"
		}
		out = append(out, Suggestion{
			Kind: SuggestionAuditLogInjection,
			Message: fmt.Sprintf(
				"audit log integrity: %d %s in %s lacked the runtime sentinel prefix. Treat the audit trail as compromised — something other than the tracker runtime wrote to the secure log. The sentinel is detection-only (not cryptographic authentication), so a motivated attacker who knows about the scheme can forge it; investigate the run's tool subprocesses and any side processes that may have known the absolute path.",
				anomalies.InjectedLines, plural, anomalies.AuditLogPath),
		})
	}
	// Merge truncations, fallthroughs, and visit-start boundaries into a
	// single Seq-ordered stream and walk it with a per-node state
	// machine. Within one node-visit the order emitted by the engine
	// is: stage_started → (tool runs) → 0..N truncation events (one per
	// truncated stream — stdout and stderr can both fire if both
	// overflowed the cap) → 0..1 fallthrough event. So:
	//
	//   - stage_started flushes any pending truncations on that node
	//     (those were from a prior visit with no matching fallthrough).
	//   - A truncation appends to the per-node pending list — multiple
	//     truncations between visit boundaries are the same visit's
	//     multi-stream overflow.
	//   - A fallthrough pairs with ALL pending truncations on that node
	//     in one combined suggestion (covering every truncated stream
	//     for that visit), then clears the list.
	//   - At end-of-stream, any leftover pending truncations emit as
	//     standalone (no fallthrough was emitted on that visit).
	//
	// A fallthrough with no pending truncations emits standalone — a
	// fallthrough can only pair with a *prior* truncation in the same
	// visit (engine order guarantees this).
	pending := map[string][]truncObservation{}
	type combined struct {
		seq int
		tr  *truncObservation       // exactly one of tr / fb / vs is non-nil
		fb  *fallthroughObservation //
		vs  *visitBoundary          //
	}
	merged := make([]combined, 0, len(anomalies.Truncations)+len(anomalies.Fallthroughs)+len(anomalies.VisitStarts))
	for i := range anomalies.Truncations {
		t := &anomalies.Truncations[i]
		merged = append(merged, combined{seq: t.Seq, tr: t})
	}
	for i := range anomalies.Fallthroughs {
		f := &anomalies.Fallthroughs[i]
		merged = append(merged, combined{seq: f.Seq, fb: f})
	}
	for i := range anomalies.VisitStarts {
		v := &anomalies.VisitStarts[i]
		merged = append(merged, combined{seq: v.Seq, vs: v})
	}
	sort.Slice(merged, func(i, j int) bool { return merged[i].seq < merged[j].seq })

	emitTruncs := func(trs []truncObservation, paired *fallthroughObservation) {
		// Combine multi-stream truncations into one suggestion so the
		// operator sees a single "stdout + stderr both truncated" line
		// rather than two repetitive ones.
		nodeID := trs[0].NodeID
		var streamMsgs []string
		for _, tr := range trs {
			streamMsgs = append(streamMsgs,
				fmt.Sprintf("%s captured last %d bytes of %d (dropped %d from head; limit %d)",
					tr.Stream, tr.CapturedBytes, tr.TotalBytes, tr.DroppedBytes, tr.Limit))
		}
		msg := fmt.Sprintf("%s: tool output truncated — %s. The tail-window capture is designed to preserve a routing marker emitted at end-of-output (as long as the marker fits within the limit). Raise the per-node `output_limit` attribute if you need more context retained or if the marker itself is larger than the cap.",
			nodeID, strings.Join(streamMsgs, "; "))
		if paired != nil {
			var tried []string
			for _, c := range paired.ConditionsTried {
				tried = append(tried, c.Condition)
			}
			msg += fmt.Sprintf(" Note: routing on this node also fell through to %q after %d conditional edge(s) evaluated false (%s) — verify the captured tail is what you expect.",
				paired.EdgeTo, len(paired.ConditionsTried), strings.Join(tried, "; "))
		}
		out = append(out, Suggestion{
			NodeID: nodeID, Kind: SuggestionToolOutputTruncated, Message: msg,
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
		case ev.vs != nil:
			if trs := pending[ev.vs.NodeID]; len(trs) > 0 {
				emitTruncs(trs, nil)
				delete(pending, ev.vs.NodeID)
			}
		case ev.tr != nil:
			pending[ev.tr.NodeID] = append(pending[ev.tr.NodeID], *ev.tr)
		case ev.fb != nil:
			if trs, ok := pending[ev.fb.NodeID]; ok && len(trs) > 0 {
				emitTruncs(trs, ev.fb)
				delete(pending, ev.fb.NodeID)
			} else {
				emitFt(*ev.fb)
			}
		}
	}
	// Flush any leftover pending truncations as orphan suggestions.
	// Iterate the original Truncations slice for deterministic output
	// ordering (map iteration order is randomized in Go).
	emitted := map[string]bool{}
	for _, tr := range anomalies.Truncations {
		if emitted[tr.NodeID] {
			continue
		}
		if trs := pending[tr.NodeID]; len(trs) > 0 {
			emitTruncs(trs, nil)
			emitted[tr.NodeID] = true
		}
	}
	// Emit at most one suggestion per node for route sentinel failures.
	// Same de-dupe shape as marker_grep below — a node that fails on
	// retry/loop emits one tool_route_missing event per attempt but
	// gets one combined suggestion with an occurrence count.
	lastRouteByNode := map[string]routeMissingObservation{}
	routeCountByNode := map[string]int{}
	for _, rm := range anomalies.RouteMissings {
		routeCountByNode[rm.NodeID]++
		if prev, ok := lastRouteByNode[rm.NodeID]; !ok || rm.Seq > prev.Seq {
			lastRouteByNode[rm.NodeID] = rm
		}
	}
	emittedRoute := map[string]bool{}
	for _, rm := range anomalies.RouteMissings {
		if emittedRoute[rm.NodeID] {
			continue
		}
		emittedRoute[rm.NodeID] = true
		latest := lastRouteByNode[rm.NodeID]
		count := routeCountByNode[rm.NodeID]
		msg := fmt.Sprintf("%s: route_required is set but no _TRACKER_ROUTE= sentinel was emitted in captured stdout (tail: %q). Have the tool emit `printf '_TRACKER_ROUTE=<value>\\n'` once it knows the routing decision, then route via `when ctx.tool_route = <value>`.",
			latest.NodeID, latest.CapturedTail)
		if count > 1 {
			msg += fmt.Sprintf(" (%d occurrences across retries/loop; showing the most recent)", count)
		}
		out = append(out, Suggestion{
			NodeID: latest.NodeID, Kind: SuggestionToolRouteMissing, Message: msg,
		})
	}

	// Emit at most one suggestion per node for marker_grep failures.
	// In runs with retries / loops the same node can emit
	// tool_marker_missing multiple times; spamming the diagnose
	// output with the same suggestion N times makes the report
	// harder to scan. Keep the LAST observation per node (highest
	// Seq), which is the most recent failure shape the operator
	// would see — and include a "(N occurrences)" hint when there
	// were multiple so the retry signal is not lost.
	lastByNode := map[string]markerMissingObservation{}
	countByNode := map[string]int{}
	for _, mm := range anomalies.MarkerMissings {
		countByNode[mm.NodeID]++
		if prev, ok := lastByNode[mm.NodeID]; !ok || mm.Seq > prev.Seq {
			lastByNode[mm.NodeID] = mm
		}
	}
	// Iterate the original slice (not the map) for deterministic
	// suggestion order, emitting each node only once.
	emittedMarker := map[string]bool{}
	for _, mm := range anomalies.MarkerMissings {
		if emittedMarker[mm.NodeID] {
			continue
		}
		emittedMarker[mm.NodeID] = true
		latest := lastByNode[mm.NodeID]
		count := countByNode[mm.NodeID]
		var msg string
		switch {
		case latest.Error != "":
			msg = fmt.Sprintf("%s: marker_grep regex %q failed to compile: %s. Fix the regex on the node's marker_grep attribute.",
				latest.NodeID, latest.Pattern, latest.Error)
		case latest.CapturedTail != "":
			msg = fmt.Sprintf("%s: marker_grep %q matched nothing in captured stdout (tail: %q). Either the tool didn't emit the expected routing marker, or the regex is wrong. Tools should emit the marker at end-of-output via `printf '<marker>\\n'`.",
				latest.NodeID, latest.Pattern, latest.CapturedTail)
		default:
			msg = fmt.Sprintf("%s: marker_grep %q matched nothing in captured stdout. The tool produced no output, or the regex doesn't match what was emitted.",
				latest.NodeID, latest.Pattern)
		}
		if count > 1 {
			msg += fmt.Sprintf(" (%d occurrences across retries/loop; showing the most recent)", count)
		}
		out = append(out, Suggestion{
			NodeID: latest.NodeID, Kind: SuggestionToolMarkerMissing, Message: msg,
		})
	}

	// Emit at most one suggestion per node for missing auto_status
	// STATUS lines (#346). Same de-dupe shape as marker_grep above;
	// the copy distinguishes the fail-closed goal-gate flip from the
	// legacy success default so a silently-defaulted verdict is
	// visible post-run.
	lastStatusByNode := map[string]statusMissingObservation{}
	statusCountByNode := map[string]int{}
	for _, sm := range anomalies.StatusMissings {
		statusCountByNode[sm.NodeID]++
		if prev, ok := lastStatusByNode[sm.NodeID]; !ok || sm.Seq > prev.Seq {
			lastStatusByNode[sm.NodeID] = sm
		}
	}
	emittedStatus := map[string]bool{}
	for _, sm := range anomalies.StatusMissings {
		if emittedStatus[sm.NodeID] {
			continue
		}
		emittedStatus[sm.NodeID] = true
		latest := lastStatusByNode[sm.NodeID]
		count := statusCountByNode[sm.NodeID]
		var msg string
		if latest.FailClosed {
			msg = fmt.Sprintf("%s: auto_status is set but the agent emitted no parseable STATUS line, so the goal gate failed closed (response tail: %q). The agent likely phrased the verdict in a shape the parser rejects, or never emitted one — tighten the prompt's STATUS contract or inspect the node's response.md.",
				latest.NodeID, latest.ResponseTail)
		} else {
			msg = fmt.Sprintf("%s: auto_status is set but the agent emitted no parseable STATUS line, so the node defaulted to success (response tail: %q). If this node is a verification gate, mark it goal_gate: true so a missing verdict fails closed instead.",
				latest.NodeID, latest.ResponseTail)
		}
		if count > 1 {
			msg += fmt.Sprintf(" (%d occurrences across retries/loop; showing the most recent)", count)
		}
		out = append(out, Suggestion{
			NodeID: latest.NodeID, Kind: SuggestionAutoStatusMissing, Message: msg,
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
