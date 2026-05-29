// ABOUTME: Library API for auditing a completed pipeline run.
// ABOUTME: Returns structured timeline, retries, errors, and recommendations.
package tracker

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/2389-research/tracker/pipeline"
)

// AuditConfig configures an Audit() or ListRuns() call.
type AuditConfig struct {
	// LogWriter receives non-fatal warnings (unreadable activity.jsonl
	// in a run directory, etc.). Nil is treated as io.Discard so
	// embedded library callers do not see warnings on os.Stderr. The
	// tracker CLI sets this to io.Discard for user-facing commands.
	LogWriter io.Writer
}

// AuditReport is the structured result of Audit().
type AuditReport struct {
	RunID string `json:"run_id"`
	// Status is one of: "success", "fail", "validation_overridden",
	// "budget_exceeded". See classifyStatus for the resolution algorithm.
	Status string `json:"status"`
	// TotalDuration is encoded as integer nanoseconds in JSON
	// ("total_duration_ns"), not as a duration string.
	TotalDuration   time.Duration   `json:"total_duration_ns"`
	Timeline        []TimelineEntry `json:"timeline"`
	Retries         []RetryRecord   `json:"retries,omitempty"`
	Errors          []ActivityError `json:"errors,omitempty"`
	Recommendations []string        `json:"recommendations,omitempty"`
	// CompletedNodes is the number of completed nodes recorded in checkpoint.json.
	CompletedNodes int `json:"completed_nodes"`
	// RestartCount is the checkpoint restart counter for the run.
	RestartCount int `json:"restart_count"`
	// CheckpointTimestamp is the last checkpoint write time.
	CheckpointTimestamp time.Time `json:"checkpoint_timestamp"`
	// BundleIdentity is the content-addressed identity ("sha256:<hex>") of
	// the .dipx bundle the run was executed against. Read from the run's
	// checkpoint. Empty for runs from a plain .dip file.
	BundleIdentity string `json:"bundle_identity,omitempty"`
}

// TimelineEntry is a single entry in the audit timeline.
type TimelineEntry struct {
	Timestamp time.Time `json:"ts"`
	Type      string    `json:"type"`
	NodeID    string    `json:"node_id,omitempty"`
	Message   string    `json:"message,omitempty"`
	// Duration is encoded as integer nanoseconds in JSON ("duration_ns"),
	// not as a duration string.
	Duration time.Duration `json:"duration_ns,omitempty"`
}

// RetryRecord records how many times a node was retried.
type RetryRecord struct {
	NodeID   string `json:"node_id"`
	Attempts int    `json:"attempts"`
}

// ActivityError is an error entry extracted from the activity log.
type ActivityError struct {
	Timestamp time.Time `json:"ts"`
	NodeID    string    `json:"node_id,omitempty"`
	Message   string    `json:"message"`
}

// RunSummary is a condensed view of a single pipeline run for listing.
type RunSummary struct {
	RunID string `json:"run_id"`
	// Status is one of: "success", "fail", "validation_overridden",
	// "budget_exceeded". See classifyStatus for the resolution algorithm.
	Status    string    `json:"status"`
	Nodes     int       `json:"nodes"`
	Retries   int       `json:"retries"`
	Restarts  int       `json:"restarts"`
	Timestamp time.Time `json:"timestamp"`
	// Duration is encoded as integer nanoseconds in JSON ("duration_ns"),
	// not as a duration string.
	Duration time.Duration `json:"duration_ns"`
	FailedAt string        `json:"failed_at,omitempty"`
	// BundleIdentity is the content-addressed identity ("sha256:<hex>") of
	// the .dipx bundle the run was executed against. Read from the run's
	// checkpoint at summary-build time. Empty for runs from a plain .dip file.
	BundleIdentity string `json:"bundle_identity,omitempty"`
}

// Audit reads checkpoint.json and activity.jsonl under runDir and returns a
// structured report.
//
// The runDir argument must be a trusted path — Audit reads checkpoint.json
// and activity.jsonl directly under it. For user-supplied input, resolve
// the path via ResolveRunDir or use MostRecentRunID first, which enforce
// the .tracker/runs/<runID> layout.
//
// ctx is checked at entry so a caller that passes an already-cancelled
// context gets an immediate error instead of silent work. Full
// cancellation mid-parse would require threading ctx through
// pipeline.LoadCheckpoint and LoadActivityLog, which is out of scope
// today (both are fast and bounded). Nil is coalesced to
// context.Background().
//
// Audit does not accept AuditConfig — it emits no warnings to suppress.
// Use ListRuns + AuditConfig{LogWriter} for bulk enumeration where the
// summary builder may skip unreadable activity logs.
func Audit(ctx context.Context, runDir string) (*AuditReport, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cp, err := pipeline.LoadCheckpoint(filepath.Join(runDir, "checkpoint.json"))
	if err != nil {
		return nil, fmt.Errorf("load checkpoint: %w", err)
	}
	activity, err := LoadActivityLog(runDir)
	if err != nil {
		return nil, fmt.Errorf("load activity log: %w", err)
	}
	SortActivityByTime(activity)

	status := classifyStatus(cp, activity)
	r := &AuditReport{
		RunID:               cp.RunID,
		Status:              status,
		Timeline:            buildTimeline(activity),
		Retries:             buildRetryRecords(cp),
		Errors:              buildActivityErrors(activity),
		CompletedNodes:      len(cp.CompletedNodes),
		RestartCount:        cp.RestartCount,
		CheckpointTimestamp: cp.Timestamp,
		BundleIdentity:      cp.BundleIdentity,
	}
	if len(activity) >= 2 {
		r.TotalDuration = activity[len(activity)-1].Timestamp.Sub(activity[0].Timestamp)
	}
	r.Recommendations = buildAuditRecommendations(cp, status, r.TotalDuration)
	return r, nil
}

// ListRuns returns all runs under workdir/.tracker/runs, sorted newest first.
// If the runs directory does not exist, ListRuns returns (nil, nil).
func ListRuns(workdir string, opts ...AuditConfig) ([]RunSummary, error) {
	cfg := firstAuditConfig(opts)
	logW := logWriterOrDiscard(cfg.LogWriter)
	runsDir := filepath.Join(workdir, ".tracker", "runs")
	entries, err := os.ReadDir(runsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("cannot read runs directory: %w", err)
	}
	var runs []RunSummary
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		rs, ok := buildRunSummary(runsDir, e.Name(), logW)
		if ok {
			runs = append(runs, rs)
		}
	}
	sort.SliceStable(runs, func(i, j int) bool { return runs[i].Timestamp.After(runs[j].Timestamp) })
	return runs, nil
}

func firstAuditConfig(opts []AuditConfig) AuditConfig {
	if len(opts) == 0 {
		return AuditConfig{}
	}
	return opts[0]
}

// classifyStatus collapses a run's activity log and checkpoint into a single
// status string for the audit/list surfaces. Algorithm per spec §6.4:
//
//  1. Reverse-scan activity entries. pipeline_failed / budget_exceeded
//     short-circuit (failure dominates). pipeline_completed and
//     validation_overridden are observed but the scan continues so a later
//     (i.e. earlier-in-scan) failure event can still override them.
//  2. If both pipeline_completed and validation_overridden were observed in
//     the scan, return "validation_overridden". A lone pipeline_completed
//     resolves to "success".
//  3. If no terminal activity event (completed/failed/budget) was observed,
//     fall back to checkpoint signals: a non-empty CurrentNode means the run
//     halted mid-graph → "fail"; a sticky ValidationOverrides on a finished
//     run (CurrentNode == "") → "validation_overridden"; otherwise "success".
//
// D12 fix (Gap 5.2): budget_exceeded no longer collapses to "fail" — it
// surfaces as its own status string. Scripts that previously filtered on
// status == "fail" will see budget-halted runs leave that bucket.
func classifyStatus(cp *pipeline.Checkpoint, activity []ActivityEntry) string {
	sawCompletion := false
	sawOverride := false
	for i := len(activity) - 1; i >= 0; i-- {
		switch activity[i].Type {
		case "pipeline_failed":
			return "fail"
		case "budget_exceeded":
			return "budget_exceeded"
		case "pipeline_completed":
			sawCompletion = true
		case "validation_overridden":
			sawOverride = true
		}
	}
	if sawCompletion {
		if sawOverride {
			return "validation_overridden"
		}
		return "success"
	}
	// No terminal event in activity — fall back to checkpoint signals. A lone
	// validation_overridden event in the log is not treated as terminal here;
	// it only contributes when paired with pipeline_completed above.
	if len(cp.ValidationOverrides) > 0 && cp.CurrentNode == "" {
		return "validation_overridden"
	}
	if cp.CurrentNode != "" {
		return "fail"
	}
	return "success"
}

func buildTimeline(activity []ActivityEntry) []TimelineEntry {
	out := make([]TimelineEntry, 0, len(activity))
	stageStarts := map[string]time.Time{}
	for _, entry := range activity {
		e := TimelineEntry{
			Timestamp: entry.Timestamp,
			Type:      entry.Type,
			NodeID:    entry.NodeID,
			Message:   entry.Message,
		}
		switch entry.Type {
		case "stage_started":
			stageStarts[entry.NodeID] = entry.Timestamp
		case "stage_completed", "stage_failed":
			if start, ok := stageStarts[entry.NodeID]; ok {
				e.Duration = entry.Timestamp.Sub(start)
				delete(stageStarts, entry.NodeID)
			}
		}
		out = append(out, e)
	}
	return out
}

func buildRetryRecords(cp *pipeline.Checkpoint) []RetryRecord {
	if len(cp.RetryCounts) == 0 {
		return nil
	}
	ids := make([]string, 0, len(cp.RetryCounts))
	for id := range cp.RetryCounts {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]RetryRecord, 0, len(ids))
	for _, id := range ids {
		out = append(out, RetryRecord{NodeID: id, Attempts: cp.RetryCounts[id]})
	}
	return out
}

func buildActivityErrors(activity []ActivityEntry) []ActivityError {
	var out []ActivityError
	for _, e := range activity {
		if e.Error == "" {
			continue
		}
		out = append(out, ActivityError{Timestamp: e.Timestamp, NodeID: e.NodeID, Message: e.Error})
	}
	return out
}

func buildAuditRecommendations(cp *pipeline.Checkpoint, status string, total time.Duration) []string {
	var recs []string
	for nodeID, count := range cp.RetryCounts {
		if count >= 2 {
			recs = append(recs, fmt.Sprintf("Consider adjusting retry_policy for %s (used %d retries)", nodeID, count))
		}
	}
	if cp.RestartCount > 0 {
		suffix := "time"
		if cp.RestartCount > 1 {
			suffix = "times"
		}
		recs = append(recs, fmt.Sprintf("Pipeline restarted %d %s — review loop conditions", cp.RestartCount, suffix))
	}
	if total > 30*time.Minute {
		recs = append(recs, "Long-running pipeline — consider fidelity=summary:medium for faster resumes")
	}
	if status == "fail" && cp.CurrentNode != "" {
		recs = append(recs, fmt.Sprintf("Pipeline failed at %s — check error details above", cp.CurrentNode))
	}
	sort.Strings(recs)
	return recs
}

func buildRunSummary(runsDir, name string, logW io.Writer) (RunSummary, bool) {
	runDir := filepath.Join(runsDir, name)
	cp, err := pipeline.LoadCheckpoint(filepath.Join(runDir, "checkpoint.json"))
	if err != nil {
		return RunSummary{}, false
	}
	activity, lerr := LoadActivityLog(runDir)
	if lerr != nil {
		fmt.Fprintf(logW, "warning: run %s: cannot read activity log: %v\n", name, lerr)
		activity = nil // continue with nil so the summary still builds
	}
	SortActivityByTime(activity)
	status := classifyStatus(cp, activity)
	totalRetries := 0
	for _, c := range cp.RetryCounts {
		totalRetries += c
	}
	var dur time.Duration
	if len(activity) >= 2 {
		dur = activity[len(activity)-1].Timestamp.Sub(activity[0].Timestamp)
	}
	rs := RunSummary{
		RunID:          name,
		Status:         status,
		Nodes:          len(cp.CompletedNodes),
		Retries:        totalRetries,
		Restarts:       cp.RestartCount,
		Timestamp:      cp.Timestamp,
		Duration:       dur,
		BundleIdentity: cp.BundleIdentity,
	}
	if status == "fail" {
		rs.FailedAt = cp.CurrentNode
	}
	return rs, true
}
