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
	case pipeline.EventAutoStatusMissing:
		*seq++
		anomalies.StatusMissings = append(anomalies.StatusMissings, statusMissingObservation{
			Seq:          *seq,
			NodeID:       entry.NodeID,
			ResponseTail: entry.AutoStatusTail,
			FailClosed:   entry.AutoStatusFailClosed,
		})
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

// applyStageTiming updates stage-timing bookkeeping (stageStarts) and, for
// failures, records the elapsed duration and the error signature used for
// retry analysis.
func applyStageTiming(entry diagnoseEntry, failures map[string]*NodeFailure, stageStarts map[string]time.Time, failSignatures map[string][]string, ts time.Time) {
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
