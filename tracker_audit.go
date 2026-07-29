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
	// Status is one of:
	//   - "success"
	//   - "fail"
	//   - "budget_exceeded"
	//   - "validation_overridden"
	//   - "paused_billing"
	// The enum is open — future minor releases may add new values. Consumers
	// should use StatusClass for stable {succeeded|failed|paused} bucketing.
	// See classifyStatus for the resolution algorithm.
	Status string `json:"status"`
	// StatusClass is one of "succeeded", "failed", or "paused" — stable
	// companion to Status for downstream consumers that need bucket
	// categorization that survives future enum extensions. "paused" flags the
	// recoverable, resumable paused_billing terminal (#514); everything else is
	// classified via pipeline.TerminalStatus(Status).IsSuccess().
	StatusClass string `json:"status_class"`
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
	// ValidationOverrides is populated when one or more override edges were
	// traversed during the run. Sourced from activity-log
	// EventValidationOverridden entries (chronological order); falls back to
	// Checkpoint.ValidationOverrides when the activity log carries no
	// override entries. Empty for runs with no override edges.
	ValidationOverrides []pipeline.OverrideDetail `json:"validation_overrides,omitempty"`
	// OverrideCount is len(ValidationOverrides). Kept as its own field so
	// thin consumers can read it without unmarshaling the slice.
	OverrideCount int `json:"override_count,omitempty"`
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
	// Status is one of: "success", "fail", "budget_exceeded",
	// "validation_overridden", "paused_billing". Open enum; prefer StatusClass
	// for stable {succeeded|failed|paused} bucketing. See classifyStatus for the
	// resolution algorithm.
	Status string `json:"status"`
	// StatusClass is one of "succeeded", "failed", or "paused" — stable
	// companion to Status. "paused" flags paused_billing (#514); everything else
	// is classified via pipeline.TerminalStatus(Status).IsSuccess().
	StatusClass string    `json:"status_class"`
	Nodes       int       `json:"nodes"`
	Retries     int       `json:"retries"`
	Restarts    int       `json:"restarts"`
	Timestamp   time.Time `json:"timestamp"`
	// Duration is encoded as integer nanoseconds in JSON ("duration_ns"),
	// not as a duration string.
	Duration time.Duration `json:"duration_ns"`
	// FailedAt is the node ID where the run halted, populated for both
	// "fail" and "budget_exceeded" runs (Gap 5.2: budget-halted runs are
	// no longer classified as "fail", so the gate also fires on
	// "budget_exceeded").
	FailedAt string `json:"failed_at,omitempty"`
	// BundleIdentity is the content-addressed identity ("sha256:<hex>") of
	// the .dipx bundle the run was executed against. Read from the run's
	// checkpoint at summary-build time. Empty for runs from a plain .dip file.
	BundleIdentity string `json:"bundle_identity,omitempty"`
	// OverrideCount is the number of override edges traversed in this run.
	// Sourced from activity log when present, else
	// len(Checkpoint.ValidationOverrides). RunSummary stays thin — for the
	// full OverrideDetail slice see AuditReport.ValidationOverrides.
	OverrideCount int `json:"override_count,omitempty"`
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
		StatusClass:         statusClassFor(status),
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
	// Source ValidationOverrides from activity events first; fall back to the
	// sticky checkpoint slice when the activity log carries no override entries
	// (legacy runs, archived activity logs, etc.).
	overrides := extractOverridesFromActivity(activity)
	if len(overrides) == 0 {
		overrides = cp.ValidationOverrides
	}
	r.ValidationOverrides = overrides
	r.OverrideCount = len(overrides)
	r.Recommendations = buildAuditRecommendations(cp, status, r.TotalDuration, overrides)
	return r, nil
}

// extractOverridesFromActivity returns the OverrideDetail entries from
// validation_overridden activity entries, in the order they appear in
// activity. SortActivityByTime is the caller's responsibility — Audit()
// and buildRunSummary both call it before this helper, so the result is
// in chronological order.
func extractOverridesFromActivity(activity []ActivityEntry) []pipeline.OverrideDetail {
	var out []pipeline.OverrideDetail
	for _, e := range activity {
		if e.Type != "validation_overridden" {
			continue
		}
		det := pipeline.OverrideDetail{
			GateNodeID: e.OverrideGate,
			Label:      e.OverrideLabel,
			Actor:      e.OverrideActor,
			Timestamp:  e.Timestamp,
		}
		if len(e.OverrideSubgraphPath) > 0 {
			// Defensive copy: callers may mutate the slice on the returned
			// OverrideDetail without leaking back into the source
			// ActivityEntry.
			det.SubgraphPath = append([]string(nil), e.OverrideSubgraphPath...)
		}
		out = append(out, det)
	}
	return out
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
		StatusClass:    statusClassFor(status),
		Nodes:          len(cp.CompletedNodes),
		Retries:        totalRetries,
		Restarts:       cp.RestartCount,
		Timestamp:      cp.Timestamp,
		Duration:       dur,
		BundleIdentity: cp.BundleIdentity,
	}
	// Gap 5.2: budget_exceeded is no longer collapsed into "fail" (D12 fix),
	// so this gate must include both statuses to keep populating FailedAt for
	// budget-halted runs.
	if status == "fail" || status == "budget_exceeded" {
		rs.FailedAt = cp.CurrentNode
	}
	// Count overrides from activity when present, else fall back to the sticky
	// checkpoint slice. RunSummary stays thin (count only) — AuditReport
	// carries the full slice.
	overrides := extractOverridesFromActivity(activity)
	if len(overrides) > 0 {
		rs.OverrideCount = len(overrides)
	} else {
		rs.OverrideCount = len(cp.ValidationOverrides)
	}
	return rs, true
}
