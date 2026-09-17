// ABOUTME: Post-execution node diagnostics — typed audit events for tool-output
// ABOUTME: truncation, marker/route/auto-status misses, and tool timeouts (#644).
package pipeline

import (
	"fmt"
	"time"
)

// emitNodeDiagnostics surfaces post-execution audit events (tool-output
// truncation #208, marker_grep no-match #210, missing route sentinel #212, and
// missing auto_status #346) as typed PipelineEvents. Extracted from executeNode
// for the complexity gate; behavior is unchanged.
func (e *Engine) emitNodeDiagnostics(s *runState, currentNodeID string, outcome *Outcome) {
	// One event per truncated stream — stdout and stderr can both fire if both
	// overflowed the per-stream cap.
	for i := range outcome.Tool.Truncations {
		td := &outcome.Tool.Truncations[i]
		e.emit(PipelineEvent{
			Type:       EventToolOutputTruncated,
			Timestamp:  time.Now(),
			RunID:      s.runID,
			NodeID:     currentNodeID,
			Message:    fmt.Sprintf("tool node %q: %s truncated — captured last %d bytes, dropped %d bytes from head (limit %d)", currentNodeID, td.Stream, td.CapturedBytes, td.DroppedBytes, td.Limit),
			Truncation: td,
		})
	}

	// Tool timeout (#644): the node already failed with the message in
	// ctx.tool_stderr; this typed event is what diagnose keys on.
	if td := outcome.Tool.Timeout; td != nil {
		e.emit(PipelineEvent{
			Type:        EventToolTimeout,
			Timestamp:   time.Now(),
			RunID:       s.runID,
			NodeID:      currentNodeID,
			Message:     fmt.Sprintf("tool node %q: command timed out after %v and was killed — captured %d bytes of output before the kill; the node fails (routable via `when ctx.outcome = fail` / fallback_target)", currentNodeID, td.Timeout, td.CapturedBytes),
			ToolTimeout: td,
		})
	}

	// marker_grep no-match: a populated Error means the regex failed to compile
	// (author error); an empty Error means it matched nothing in stdout.
	if outcome.Tool.MissingMarker != nil {
		e.emit(PipelineEvent{
			Type:      EventToolMarkerMissing,
			Timestamp: time.Now(),
			RunID:     s.runID,
			NodeID:    currentNodeID,
			Message:   missingMarkerMessage(currentNodeID, outcome.Tool.MissingMarker),
			Marker:    outcome.Tool.MissingMarker,
		})
	}

	// route_required: true but no _TRACKER_ROUTE= sentinel in captured stdout.
	if outcome.Tool.MissingRoute != nil {
		e.emit(PipelineEvent{
			Type:      EventToolRouteMissing,
			Timestamp: time.Now(),
			RunID:     s.runID,
			NodeID:    currentNodeID,
			Message: fmt.Sprintf("tool node %q: route_required is set but no _TRACKER_ROUTE= sentinel line was emitted to stdout — failing node to avoid silent fallback",
				currentNodeID),
			Route: outcome.Tool.MissingRoute,
		})
	}

	// auto_status set but no parseable STATUS line; the handler already chose
	// the status (fail-closed on goal gates, legacy success default otherwise).
	if outcome.MissingStatus != nil {
		e.emit(PipelineEvent{
			Type:       EventAutoStatusMissing,
			Timestamp:  time.Now(),
			RunID:      s.runID,
			NodeID:     currentNodeID,
			Message:    missingStatusMessage(currentNodeID, outcome.MissingStatus),
			AutoStatus: outcome.MissingStatus,
		})
	}
}

// missingMarkerMessage builds the marker_grep no-match diagnostic. A populated
// Error means the regex failed to compile; empty means it matched nothing.
func missingMarkerMessage(nodeID string, m *MarkerDetail) string {
	if m.Error != "" {
		return fmt.Sprintf("tool node %q: marker_grep regex %q failed to compile: %s — failing node to avoid silent fallback",
			nodeID, m.Pattern, m.Error)
	}
	return fmt.Sprintf("tool node %q: marker_grep %q matched nothing in captured stdout — failing node to avoid silent fallback",
		nodeID, m.Pattern)
}

// missingStatusMessage builds the auto_status no-verdict diagnostic, branching
// on whether the gate fails closed or defaults to legacy success.
func missingStatusMessage(nodeID string, ms *AutoStatusDetail) string {
	if ms.FailClosed {
		return fmt.Sprintf("node %q: auto_status is set but no parseable STATUS line was found — failing goal gate closed (an unparseable verdict on a gate is an anomaly, not a pass)",
			nodeID)
	}
	return fmt.Sprintf("node %q: auto_status is set but no parseable STATUS line was found — the STATUS verdict defaulted to success (legacy behavior; the node's final status may still differ, e.g. on a declared-writes failure; mark the node goal_gate: true to fail closed)",
		nodeID)
}
