// ABOUTME: Activity-log scanning helpers for Diagnose — line loop, per-line
// ABOUTME: decoding, anomaly recording, and per-node failure enrichment.
package tracker

import (
	"bufio"
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/2389-research/tracker/pipeline"
)

// scanActivityLoop drives the bufio scan of the activity log, delegating each
// line to processActivityLine. It returns early (without touching scanner.Err)
// when the context is cancelled, so the caller can distinguish cancellation
// from a scanner read error.
func scanActivityLoop(ctx context.Context, scanner *bufio.Scanner, secureUsed bool, anomalies *runtimeAnomalies, failures map[string]*NodeFailure, stageStarts map[string]time.Time, failSignatures map[string][]string) (*BudgetHalt, error) {
	var halt *BudgetHalt
	seq := 0
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if h := processActivityLine(scanner.Text(), secureUsed, &seq, anomalies, failures, stageStarts, failSignatures); h != nil {
			halt = h
		}
	}
	return halt, nil
}

// processActivityLine folds one raw activity-log line into the running
// diagnosis state. The injection counter is bumped BEFORE the blank-line skip
// so blank padding on the secure log is still counted — matching
// ScanActivityLog.consumeLine (#213, #517). Returns a non-nil BudgetHalt only
// for a budget_exceeded line.
func processActivityLine(raw string, secureUsed bool, seq *int, anomalies *runtimeAnomalies, failures map[string]*NodeFailure, stageStarts map[string]time.Time, failSignatures map[string][]string) *BudgetHalt {
	body, hasSentinel := stripActivitySentinel(raw)
	if secureUsed && !hasSentinel {
		anomalies.InjectedLines++
	}
	line := strings.TrimSpace(body)
	if line == "" {
		return nil
	}
	var entry diagnoseEntry
	if err := json.Unmarshal([]byte(line), &entry); err != nil {
		return nil
	}
	var halt *BudgetHalt
	if pipeline.PipelineEventType(entry.Type) == pipeline.EventBudgetExceeded {
		halt = &BudgetHalt{
			TotalTokens:   entry.TotalTokens,
			TotalCostUSD:  entry.TotalCostUSD,
			WallElapsedMs: entry.WallElapsedMs,
			Message:       entry.Message,
		}
	}
	recordAnomalyEvent(entry, seq, anomalies)
	enrichFromEntry(entry, failures, stageStarts, failSignatures)
	return halt
}

// recordAnomalyEvent appends the runtime anomaly (if any) carried by entry to
// anomalies, assigning each a monotonically increasing seq so the suggestion
// builder can merge the anomaly streams in chronological order.
func recordAnomalyEvent(entry diagnoseEntry, seq *int, anomalies *runtimeAnomalies) {
	switch pipeline.PipelineEventType(entry.Type) {
	case pipeline.EventToolOutputTruncated:
		*seq++
		anomalies.Truncations = append(anomalies.Truncations, truncObservation{
			Seq:           *seq,
			NodeID:        entry.NodeID,
			Stream:        entry.TruncStream,
			Limit:         entry.TruncLimit,
			CapturedBytes: entry.TruncCaptured,
			DroppedBytes:  entry.TruncDropped,
			TotalBytes:    entry.TruncTotal,
		})
	case pipeline.EventConditionalFallthrough:
		*seq++
		anomalies.Fallthroughs = append(anomalies.Fallthroughs, fallthroughObservation{
			Seq:             *seq,
			NodeID:          entry.NodeID,
			EdgeTo:          entry.EdgeTo,
			EdgePriority:    entry.EdgePriority,
			ConditionsTried: entry.ConditionsTried,
		})
	case pipeline.EventStageStarted:
		// Mark a visit boundary so the suggestion builder can flush
		// pending truncations from a prior visit before pairing
		// within the new visit.
		if entry.NodeID != "" {
			*seq++
			anomalies.VisitStarts = append(anomalies.VisitStarts, visitBoundary{
				Seq:    *seq,
				NodeID: entry.NodeID,
			})
		}
	case pipeline.EventToolMarkerMissing:
		*seq++
		anomalies.MarkerMissings = append(anomalies.MarkerMissings, markerMissingObservation{
			Seq:          *seq,
			NodeID:       entry.NodeID,
			Pattern:      entry.MarkerPattern,
			CapturedTail: entry.MarkerTail,
			Error:        entry.MarkerError,
		})
	case pipeline.EventToolRouteMissing:
		*seq++
		anomalies.RouteMissings = append(anomalies.RouteMissings, routeMissingObservation{
			Seq:          *seq,
			NodeID:       entry.NodeID,
			CapturedTail: entry.RouteTail,
		})
	default:
		recordNodeAnomalyEvent(entry, seq, anomalies)
	}
}

// recordNodeAnomalyEvent handles the node-level anomaly events (auto-status
// miss #346, tool timeout #644, fallback latch #642, jail degrade #648). Split from
// recordAnomalyEvent for the complexity gate; same seq stream.
func recordNodeAnomalyEvent(entry diagnoseEntry, seq *int, anomalies *runtimeAnomalies) {
	switch pipeline.PipelineEventType(entry.Type) {
	case pipeline.EventAutoStatusMissing:
		*seq++
		anomalies.StatusMissings = append(anomalies.StatusMissings, statusMissingObservation{
			Seq:          *seq,
			NodeID:       entry.NodeID,
			ResponseTail: entry.AutoStatusTail,
			FailClosed:   entry.AutoStatusFailClosed,
		})
	case pipeline.EventToolTimeout:
		*seq++
		anomalies.ToolTimeouts = append(anomalies.ToolTimeouts, toolTimeoutObservation{
			Seq:           *seq,
			NodeID:        entry.NodeID,
			Timeout:       time.Duration(entry.ToolTimeoutMs) * time.Millisecond,
			CapturedBytes: entry.ToolTimeoutCaptured,
		})
	case pipeline.EventFallbackLatched:
		*seq++
		anomalies.FallbackLatch = append(anomalies.FallbackLatch, fallbackLatchObservation{
			Seq:     *seq,
			NodeID:  entry.NodeID,
			Message: entry.Message,
		})
	case pipeline.EventRestartBudgetReset:
		anomalies.BudgetResets = append(anomalies.BudgetResets, budgetResetFromEntry(entry))
	case pipeline.EventJailDegraded:
		*seq++
		anomalies.JailDegrades = append(anomalies.JailDegrades, jailDegradedObservation{
			Seq:           *seq,
			NodeID:        entry.NodeID,
			Reason:        entry.JailReason,
			DeclaredGlobs: entry.JailDeclaredGlobs,
		})
	}
}

// budgetResetFromEntry projects a restart_budget_reset line (#643) onto the
// report's informational record. restart_count is a pointer on the wire so
// "absent" (latch-only reset) reads as 0.
func budgetResetFromEntry(entry diagnoseEntry) RestartBudgetReset {
	previous := 0
	if entry.RestartCount != nil {
		previous = *entry.RestartCount
	}
	return RestartBudgetReset{
		NodeID:               entry.NodeID,
		ResetBy:              entry.ResetBy,
		PreviousCount:        previous,
		FallbackLatchCleared: entry.FallbackLatchCleared,
	}
}

func enrichFromEntry(entry diagnoseEntry, failures map[string]*NodeFailure, stageStarts map[string]time.Time, failSignatures map[string][]string) {
	ts, _ := parseActivityTimestamp(entry.Timestamp)
	applyStageTiming(entry, failures, stageStarts, failSignatures, ts)
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

// withFallbackOrigins stamps NodeFailure.ReachedFrom / ReachedVia from the
// checkpoint's per-node fail-route provenance (#650, kind-aware per #654): a
// node the engine reached via a fallback (build_product's AbortRun terminal)
// or an authored `when fail` edge names the failure that actually routed
// there, so the operator fixes the cause rather than the terminal.
func withFallbackOrigins(cp *pipeline.Checkpoint, failures map[string]*NodeFailure) map[string]*NodeFailure {
	for id, f := range failures {
		origin, kind := cp.FailRouteOrigin(id)
		f.ReachedFrom, f.ReachedVia = origin, string(kind)
	}
	return failures
}

// applyStageTiming updates stage-timing bookkeeping (stageStarts) and, for
// failures, records the elapsed duration and the error signature used for
// retry analysis.
func applyStageTiming(entry diagnoseEntry, failures map[string]*NodeFailure, stageStarts map[string]time.Time, failSignatures map[string][]string, ts time.Time) {
	switch entry.Type {
	case "stage_started":
		// Always mark the attempt (a zero ts still opens one; timing checks
		// IsZero on read) so retry analysis is not skewed by a bad timestamp.
		stageStarts[entry.NodeID] = ts
	case "stage_failed":
		updateFailureTiming(failures[entry.NodeID], stageStarts, entry, ts)
		// One attempt = one stage_started. The engine emits a SECOND
		// stage_failed for the same attempt when it routes a strict failure to
		// a fallback or halts on one (#650), so only the first stage_failed
		// after a stage_started counts toward the retry signature.
		if _, started := stageStarts[entry.NodeID]; !started {
			return
		}
		delete(stageStarts, entry.NodeID)
		sig := entry.Error + "\x00" + entry.ToolErr
		failSignatures[entry.NodeID] = append(failSignatures[entry.NodeID], sig)
	case "stage_completed":
		updateFailureTiming(failures[entry.NodeID], stageStarts, entry, ts)
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

	// Conditional-fallthrough event fields (#208; edge_priority "else" = #649).
	EdgeTo          string                   `json:"edge_to"`
	EdgePriority    string                   `json:"edge_priority"`
	ConditionsTried []pipeline.ConditionEval `json:"conditions_tried"`

	// Tool-marker-missing event fields (#210).
	MarkerPattern string `json:"marker_pattern"`
	MarkerTail    string `json:"marker_tail"`
	MarkerError   string `json:"marker_error"`

	// Tool-route-missing event fields (#212).
	RouteTail string `json:"route_tail"`

	// Tool-timeout event fields (#644).
	ToolTimeoutMs       int64 `json:"tool_timeout_ms"`
	ToolTimeoutCaptured int   `json:"tool_timeout_captured_bytes"`

	// Auto-status-missing event fields (#346).
	AutoStatusTail       string `json:"auto_status_tail"`
	AutoStatusFailClosed bool   `json:"auto_status_fail_closed"`

	// Jail-degraded event fields (#648).
	JailReason        string   `json:"jail_reason"`
	JailDeclaredGlobs []string `json:"jail_declared_globs"`

	// Restart-budget-reset fields (#643).
	RestartCount         *int   `json:"restart_count"`
	ResetBy              string `json:"reset_by"`
	FallbackLatchCleared bool   `json:"fallback_latch_cleared"`
}
