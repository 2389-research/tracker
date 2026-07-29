// ABOUTME: Suggestion-building helpers for Diagnose — turns node failures and
// ABOUTME: runtime anomalies into actionable Suggestion entries.
package tracker

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

func buildSuggestions(failures []NodeFailure, halt *BudgetHalt, anomalies runtimeAnomalies) []Suggestion {
	var out []Suggestion
	out = append(out, failureSuggestions(failures)...)
	out = append(out, budgetSuggestion(halt)...)
	out = append(out, injectionSuggestion(anomalies)...)
	out = append(out, truncationFallthroughSuggestions(anomalies)...)
	out = append(out, routeMissingSuggestions(anomalies)...)
	out = append(out, markerMissingSuggestions(anomalies)...)
	out = append(out, statusMissingSuggestions(anomalies)...)
	return out
}

func failureSuggestions(failures []NodeFailure) []Suggestion {
	var out []Suggestion
	for _, f := range failures {
		out = append(out, suggestionsForNodeFailure(f)...)
	}
	return out
}

func budgetSuggestion(halt *BudgetHalt) []Suggestion {
	if halt == nil {
		return nil
	}
	return []Suggestion{{
		Kind:    SuggestionBudget,
		Message: "Raise the relevant --max-tokens, --max-cost, or --max-wall-time flag, or remove the Config.Budget value",
	}}
}

func injectionSuggestion(anomalies runtimeAnomalies) []Suggestion {
	if anomalies.InjectedLines == 0 {
		return nil
	}
	plural := "line"
	if anomalies.InjectedLines > 1 {
		plural = "lines"
	}
	return []Suggestion{{
		Kind: SuggestionAuditLogInjection,
		Message: fmt.Sprintf(
			"audit log integrity: %d %s in %s lacked the runtime sentinel prefix. Treat the audit trail as compromised — something other than the tracker runtime wrote to the secure log. The sentinel is detection-only (not cryptographic authentication), so a motivated attacker who knows about the scheme can forge it; investigate the run's tool subprocesses and any side processes that may have known the absolute path.",
			anomalies.InjectedLines, plural, anomalies.AuditLogPath),
	}}
}

// mergedAnomaly is one entry in the Seq-ordered merge of truncation,
// fallthrough, and visit-start events. Exactly one of tr / fb / vs is non-nil.
type mergedAnomaly struct {
	seq int
	tr  *truncObservation
	fb  *fallthroughObservation
	vs  *visitBoundary
}

// truncationFallthroughSuggestions merges truncations, fallthroughs, and
// visit-start boundaries into a single Seq-ordered stream and walks it with a
// per-node state machine. Within one node-visit the engine emits:
// stage_started → 0..N truncation events (one per truncated stream) → 0..1
// fallthrough event. So a visit boundary flushes any pending truncations from
// a prior visit, each truncation appends to the per-node pending list, and a
// fallthrough pairs with ALL pending truncations on that node. Leftover
// pending truncations at end-of-stream emit standalone.
func truncationFallthroughSuggestions(anomalies runtimeAnomalies) []Suggestion {
	merged := buildMergedAnomalies(anomalies)
	pending := map[string][]truncObservation{}
	out := drainMergedAnomalies(merged, pending)
	out = append(out, flushPendingTruncations(anomalies.Truncations, pending)...)
	return out
}

func buildMergedAnomalies(anomalies runtimeAnomalies) []mergedAnomaly {
	merged := make([]mergedAnomaly, 0, len(anomalies.Truncations)+len(anomalies.Fallthroughs)+len(anomalies.VisitStarts))
	for i := range anomalies.Truncations {
		t := &anomalies.Truncations[i]
		merged = append(merged, mergedAnomaly{seq: t.Seq, tr: t})
	}
	for i := range anomalies.Fallthroughs {
		f := &anomalies.Fallthroughs[i]
		merged = append(merged, mergedAnomaly{seq: f.Seq, fb: f})
	}
	for i := range anomalies.VisitStarts {
		v := &anomalies.VisitStarts[i]
		merged = append(merged, mergedAnomaly{seq: v.Seq, vs: v})
	}
	sort.Slice(merged, func(i, j int) bool { return merged[i].seq < merged[j].seq })
	return merged
}

func drainMergedAnomalies(merged []mergedAnomaly, pending map[string][]truncObservation) []Suggestion {
	var out []Suggestion
	for _, ev := range merged {
		switch {
		case ev.vs != nil:
			if trs := pending[ev.vs.NodeID]; len(trs) > 0 {
				out = append(out, truncationSuggestion(trs, nil))
				delete(pending, ev.vs.NodeID)
			}
		case ev.tr != nil:
			pending[ev.tr.NodeID] = append(pending[ev.tr.NodeID], *ev.tr)
		case ev.fb != nil:
			out = append(out, pairFallthrough(pending, ev.fb)...)
		}
	}
	return out
}

// pairFallthrough pairs a fallthrough with ALL pending truncations on its node
// (one combined suggestion), or emits it standalone when none are pending — a
// fallthrough can only pair with a prior truncation in the same visit.
func pairFallthrough(pending map[string][]truncObservation, fb *fallthroughObservation) []Suggestion {
	if trs, ok := pending[fb.NodeID]; ok && len(trs) > 0 {
		delete(pending, fb.NodeID)
		return []Suggestion{truncationSuggestion(trs, fb)}
	}
	return []Suggestion{fallthroughSuggestion(*fb)}
}

// flushPendingTruncations emits leftover pending truncations as orphan
// suggestions. It iterates the original Truncations slice for deterministic
// output ordering (map iteration order is randomized in Go).
func flushPendingTruncations(truncations []truncObservation, pending map[string][]truncObservation) []Suggestion {
	var out []Suggestion
	emitted := map[string]bool{}
	for _, tr := range truncations {
		if emitted[tr.NodeID] {
			continue
		}
		if trs := pending[tr.NodeID]; len(trs) > 0 {
			out = append(out, truncationSuggestion(trs, nil))
			emitted[tr.NodeID] = true
		}
	}
	return out
}

// truncationSuggestion combines multi-stream truncations for one node into a
// single suggestion, optionally noting a paired routing fallthrough.
func truncationSuggestion(trs []truncObservation, paired *fallthroughObservation) Suggestion {
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
	return Suggestion{NodeID: nodeID, Kind: SuggestionToolOutputTruncated, Message: msg}
}

func fallthroughSuggestion(fb fallthroughObservation) Suggestion {
	var tried []string
	for _, c := range fb.ConditionsTried {
		tried = append(tried, c.Condition)
	}
	return Suggestion{
		NodeID: fb.NodeID, Kind: SuggestionConditionalFallthrough,
		Message: fmt.Sprintf("%s: %d conditional edge(s) all evaluated false (%s); routing fell back to %q. If this was unintentional, check the routing context — `ctx.outcome`, `ctx.tool_stdout`, or whatever your conditions reference.",
			fb.NodeID, len(fb.ConditionsTried), strings.Join(tried, "; "), fb.EdgeTo),
	}
}

// latestBySeq tallies per-node occurrence counts and keeps the highest-Seq
// observation per node. Shared by the route/marker/status dedup emitters,
// which all keep the most recent observation and annotate repeat counts.
func latestBySeq[T any](items []T, nodeID func(T) string, seq func(T) int) (map[string]T, map[string]int) {
	last := map[string]T{}
	count := map[string]int{}
	for _, it := range items {
		id := nodeID(it)
		count[id]++
		if prev, ok := last[id]; !ok || seq(it) > seq(prev) {
			last[id] = it
		}
	}
	return last, count
}

// routeMissingSuggestions emits at most one suggestion per node for route
// sentinel failures — a node that fails on retry/loop emits one event per
// attempt but gets one combined suggestion with an occurrence count.
func routeMissingSuggestions(anomalies runtimeAnomalies) []Suggestion {
	last, count := latestBySeq(anomalies.RouteMissings,
		func(r routeMissingObservation) string { return r.NodeID },
		func(r routeMissingObservation) int { return r.Seq })
	emitted := map[string]bool{}
	var out []Suggestion
	for _, rm := range anomalies.RouteMissings {
		if emitted[rm.NodeID] {
			continue
		}
		emitted[rm.NodeID] = true
		latest := last[rm.NodeID]
		msg := fmt.Sprintf("%s: route_required is set but no _TRACKER_ROUTE= sentinel was emitted in captured stdout (tail: %q). Have the tool emit `printf '_TRACKER_ROUTE=<value>\\n'` once it knows the routing decision, then route via `when ctx.tool_route = <value>`.",
			latest.NodeID, latest.CapturedTail)
		if count[rm.NodeID] > 1 {
			msg += fmt.Sprintf(" (%d occurrences across retries/loop; showing the most recent)", count[rm.NodeID])
		}
		out = append(out, Suggestion{NodeID: latest.NodeID, Kind: SuggestionToolRouteMissing, Message: msg})
	}
	return out
}

// markerMissingSuggestions emits at most one suggestion per node for
// marker_grep failures, keeping the most recent observation and noting the
// occurrence count when a node failed repeatedly across retries/loops.
func markerMissingSuggestions(anomalies runtimeAnomalies) []Suggestion {
	last, count := latestBySeq(anomalies.MarkerMissings,
		func(m markerMissingObservation) string { return m.NodeID },
		func(m markerMissingObservation) int { return m.Seq })
	emitted := map[string]bool{}
	var out []Suggestion
	for _, mm := range anomalies.MarkerMissings {
		if emitted[mm.NodeID] {
			continue
		}
		emitted[mm.NodeID] = true
		latest := last[mm.NodeID]
		msg := markerMissingMessage(latest)
		if count[mm.NodeID] > 1 {
			msg += fmt.Sprintf(" (%d occurrences across retries/loop; showing the most recent)", count[mm.NodeID])
		}
		out = append(out, Suggestion{NodeID: latest.NodeID, Kind: SuggestionToolMarkerMissing, Message: msg})
	}
	return out
}

func markerMissingMessage(latest markerMissingObservation) string {
	switch {
	case latest.Error != "":
		return fmt.Sprintf("%s: marker_grep regex %q failed to compile: %s. Fix the regex on the node's marker_grep attribute.",
			latest.NodeID, latest.Pattern, latest.Error)
	case latest.CapturedTail != "":
		return fmt.Sprintf("%s: marker_grep %q matched nothing in captured stdout (tail: %q). Either the tool didn't emit the expected routing marker, or the regex is wrong. Tools should emit the marker at end-of-output via `printf '<marker>\\n'`.",
			latest.NodeID, latest.Pattern, latest.CapturedTail)
	default:
		return fmt.Sprintf("%s: marker_grep %q matched nothing in captured stdout. The tool produced no output, or the regex doesn't match what was emitted.",
			latest.NodeID, latest.Pattern)
	}
}

// statusMissingSuggestions emits at most one suggestion per node for missing
// auto_status STATUS lines (#346), distinguishing the fail-closed goal-gate
// flip from the legacy success default.
func statusMissingSuggestions(anomalies runtimeAnomalies) []Suggestion {
	last, count := latestBySeq(anomalies.StatusMissings,
		func(s statusMissingObservation) string { return s.NodeID },
		func(s statusMissingObservation) int { return s.Seq })
	emitted := map[string]bool{}
	var out []Suggestion
	for _, sm := range anomalies.StatusMissings {
		if emitted[sm.NodeID] {
			continue
		}
		emitted[sm.NodeID] = true
		latest := last[sm.NodeID]
		msg := statusMissingMessage(latest)
		if count[sm.NodeID] > 1 {
			msg += fmt.Sprintf(" (%d occurrences across retries/loop; showing the most recent)", count[sm.NodeID])
		}
		out = append(out, Suggestion{NodeID: latest.NodeID, Kind: SuggestionAutoStatusMissing, Message: msg})
	}
	return out
}

func statusMissingMessage(latest statusMissingObservation) string {
	if latest.FailClosed {
		return fmt.Sprintf("%s: auto_status is set but the agent emitted no parseable STATUS line, so the goal gate failed closed (response tail: %q). The agent likely phrased the verdict in a shape the parser rejects, or never emitted one — tighten the prompt's STATUS contract or inspect the node's response.md.",
			latest.NodeID, latest.ResponseTail)
	}
	return fmt.Sprintf("%s: auto_status is set but the agent emitted no parseable STATUS line, so the STATUS verdict defaulted to success (response tail: %q). If this node is a verification gate, mark it goal_gate: true so a missing verdict fails closed instead.",
		latest.NodeID, latest.ResponseTail)
}

func suggestionsForNodeFailure(f NodeFailure) []Suggestion {
	var out []Suggestion
	out = append(out, retryPatternSuggestions(f)...)
	out = append(out, escalateLimitSuggestions(f)...)
	out = append(out, noOutputSuggestions(f)...)
	out = append(out, shellCommandSuggestions(f)...)
	out = append(out, goTestSuggestions(f)...)
	out = append(out, suspiciousTimingSuggestions(f)...)
	return out
}

func retryPatternSuggestions(f NodeFailure) []Suggestion {
	if f.IdenticalRetries && f.RetryCount >= 2 {
		return []Suggestion{{
			NodeID: f.NodeID, Kind: SuggestionRetryPattern,
			Message: fmt.Sprintf("%s: Failed %d times with identical errors — this is a deterministic bug in the command, not a transient failure. Retrying won't help. Fix the tool command in the .dip file and re-run.", f.NodeID, f.RetryCount),
		}}
	}
	if f.RetryCount >= 3 {
		return []Suggestion{{
			NodeID: f.NodeID, Kind: SuggestionRetryPattern,
			Message: fmt.Sprintf("%s: Failed %d times with varying errors — may be a flaky command or environment issue.", f.NodeID, f.RetryCount),
		}}
	}
	return nil
}

func escalateLimitSuggestions(f NodeFailure) []Suggestion {
	if strings.Contains(f.Stdout, "ESCALATE") && strings.Contains(f.Stdout, "fix attempts") {
		return []Suggestion{{
			NodeID: f.NodeID, Kind: SuggestionEscalateLimit,
			Message: fmt.Sprintf("%s: Hit fix attempt limit. The fix_attempts counter persists on disk across restarts — if you retry after escalation, the counter is already maxed. Reset it with: rm .ai/milestones/fix_attempts", f.NodeID),
		}}
	}
	return nil
}

func noOutputSuggestions(f NodeFailure) []Suggestion {
	if f.Stdout == "" && f.Stderr == "" && len(f.Errors) == 0 {
		return []Suggestion{{
			NodeID: f.NodeID, Kind: SuggestionNoOutput,
			Message: fmt.Sprintf("%s: No error details captured. Check the activity.jsonl for this node's events: grep %q activity.jsonl | tail -20", f.NodeID, f.NodeID),
		}}
	}
	return nil
}

func shellCommandSuggestions(f NodeFailure) []Suggestion {
	if strings.Contains(f.Stderr, "command not found") || strings.Contains(f.Stderr, "No such file or directory") {
		return []Suggestion{{
			NodeID: f.NodeID, Kind: SuggestionShellCommand,
			Message: fmt.Sprintf("%s: Shell command failed — check that the working directory and required tools exist before running", f.NodeID),
		}}
	}
	return nil
}

func goTestSuggestions(f NodeFailure) []Suggestion {
	if strings.Contains(f.Stdout, "FAIL") && strings.Contains(f.Stdout, "go test") {
		return []Suggestion{{
			NodeID: f.NodeID, Kind: SuggestionGoTest,
			Message: fmt.Sprintf("%s: Go test failures — check if .ai/milestones/known_failures should include these tests for this milestone", f.NodeID),
		}}
	}
	return nil
}

func suspiciousTimingSuggestions(f NodeFailure) []Suggestion {
	if f.Duration > 0 && f.Duration < 50*time.Millisecond && f.Handler != "tool" {
		return []Suggestion{{
			NodeID: f.NodeID, Kind: SuggestionSuspiciousTiming,
			Message: fmt.Sprintf("%s: Completed in %s — suspiciously fast. May indicate a configuration issue or missing handler", f.NodeID, f.Duration),
		}}
	}
	return nil
}
