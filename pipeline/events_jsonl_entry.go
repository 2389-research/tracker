// ABOUTME: On-disk schema for one activity-log line, plus the field-copy helpers
// ABOUTME: that populate it from agent events. Split from events_jsonl.go, which owns the writer.
package pipeline

import (
	"github.com/2389-research/tracker/agent"
	"github.com/2389-research/tracker/llm"
)

// jsonlLogEntry is the on-disk format for one activity log line.
type jsonlLogEntry struct {
	Timestamp      string `json:"ts"`
	Source         string `json:"source"` // "pipeline" (engine emissions) | "agent" (LLM session) | "llm" (raw provider events) | "cli" (CLI-level audit, e.g. bundle_mismatch_forced)
	Type           string `json:"type"`
	RunID          string `json:"run_id,omitempty"`
	NodeID         string `json:"node_id,omitempty"`
	Message        string `json:"message,omitempty"`
	Error          string `json:"error,omitempty"`
	Provider       string `json:"provider,omitempty"`
	Model          string `json:"model,omitempty"`
	ToolName       string `json:"tool_name,omitempty"`
	Content        string `json:"content,omitempty"`
	BundleIdentity string `json:"bundle_identity,omitempty"`

	// Decision audit trail fields.
	EdgeFrom        string            `json:"edge_from,omitempty"`
	EdgeTo          string            `json:"edge_to,omitempty"`
	EdgeCondition   string            `json:"edge_condition,omitempty"`
	EdgePriority    string            `json:"edge_priority,omitempty"`
	ConditionMatch  *bool             `json:"condition_match,omitempty"`
	OutcomeStatus   string            `json:"outcome_status,omitempty"`
	ContextSnapshot map[string]string `json:"context_snapshot,omitempty"`
	ContextUpdates  map[string]string `json:"context_updates,omitempty"`
	RestartCount    *int              `json:"restart_count,omitempty"`
	ClearedNodes    []string          `json:"cleared_nodes,omitempty"`
	TokenInput      int               `json:"token_input,omitempty"`
	TokenOutput     int               `json:"token_output,omitempty"`

	// Cost snapshot fields — non-zero for cost_updated and budget_exceeded events.
	TotalTokens    int                      `json:"total_tokens,omitempty"`
	TotalCostUSD   float64                  `json:"total_cost_usd,omitempty"`
	ProviderTotals map[string]ProviderUsage `json:"provider_totals,omitempty"`
	WallElapsedMs  int64                    `json:"wall_elapsed_ms,omitempty"`
	// Estimated is true when any session contributing to this cost snapshot
	// was heuristic-derived (currently: ACP rune-count estimator). External
	// NDJSON consumers read this to distinguish metered from estimated
	// spend — see cmd/tracker/summary.go for the equivalent CLI surface.
	Estimated bool `json:"estimated,omitempty"`

	// Truncation fields — populated for tool_output_truncated events.
	// Stream is "stdout" or "stderr"; CapturedBytes / DroppedBytes /
	// TotalBytes record the per-stream byte accounting at the time of
	// truncation. Issue #208.
	TruncStream   string `json:"trunc_stream,omitempty"`
	TruncLimit    int    `json:"trunc_limit,omitempty"`
	TruncCaptured int    `json:"trunc_captured_bytes,omitempty"`
	TruncDropped  int    `json:"trunc_dropped_bytes,omitempty"`
	TruncTotal    int    `json:"trunc_total_bytes,omitempty"`

	// Conditional-fallthrough fields — populated for
	// conditional_fallthrough events. Lists routing intents that
	// evaluated false on the way to a fallback selection.
	ConditionsTried []ConditionEval `json:"conditions_tried,omitempty"`

	// Marker fields — populated for tool_marker_missing events
	// (issue #210). Pattern is the configured marker_grep regex;
	// MarkerTail is up to 256 bytes from end of captured stdout for
	// diagnosis; MarkerError is the regex-compile error when the
	// failure was a bad regex rather than a missing match.
	MarkerPattern string `json:"marker_pattern,omitempty"`
	MarkerTail    string `json:"marker_tail,omitempty"`
	MarkerError   string `json:"marker_error,omitempty"`

	// Route fields — populated for tool_route_missing events (#212).
	// The matcher is built-in so there is no Pattern field; just the
	// captured stdout tail for diagnosis.
	RouteTail string `json:"route_tail,omitempty"`

	// Auto-status fields — populated for auto_status_missing events
	// (#346). Tail is up to 256 bytes from the end of the agent's
	// response text (where the STATUS line was expected); FailClosed is
	// true when the missing line failed a goal gate closed.
	AutoStatusTail       string `json:"auto_status_tail,omitempty"`
	AutoStatusFailClosed bool   `json:"auto_status_fail_closed,omitempty"`

	// Override fields — populated for validation_overridden events.
	// Identify the gate that produced the override, the edge label that
	// selected it, who acted, and the subgraph_path when the override
	// was propagated up from a child run.
	OverrideGate         string   `json:"override_gate,omitempty"`
	OverrideLabel        string   `json:"override_label,omitempty"`
	OverrideActor        Actor    `json:"override_actor,omitempty"`
	OverrideSubgraphPath []string `json:"override_subgraph_path,omitempty"`

	// Gate lifecycle fields — populated for gate_opened / gate_resolved
	// (#509). GateID correlates the pair; node_id above identifies the gate
	// node. A gate that failed to collect an answer carries the reason in
	// the shared Error field.
	GateID        string         `json:"gate_id,omitempty"`
	GateMode      string         `json:"gate_mode,omitempty"`
	GateLabel     string         `json:"gate_label,omitempty"`
	GatePrompt    string         `json:"gate_prompt,omitempty"`
	GateChoices   []string       `json:"gate_choices,omitempty"`
	GateQuestions []GateQuestion `json:"gate_questions,omitempty"`
	GateResponse  string         `json:"gate_response,omitempty"`
	GateOutcome   string         `json:"gate_outcome,omitempty"`
	GateActor     Actor          `json:"gate_actor,omitempty"`
	GateTimedOut  bool           `json:"gate_timed_out,omitempty"`

	// --- Run-reconstruction fields ---
	//
	// The agent package emits a nested span tree (session → turn → tool
	// call) but the log used to flatten it: a post-hoc reader could see
	// that `bash` ran and what it returned, never which turn issued it or
	// what command was asked for. These fields carry that structure onto
	// disk so a run can be rebuilt as a tree rather than a flat sequence.
	//
	// SessionID is set on every agent-source line. TurnNo is set on
	// turn-scoped lines, 1-indexed, or -1 for a repair turn (which runs
	// outside the MaxTurns budget and so has no ordinal). Without both,
	// events from concurrently-executing `parallel` branches — which
	// interleave into one file — cannot be separated.
	//
	// ParentSessionID is unset by stock tracker: the only thing that
	// creates a child session is the spawn_agent tool, and its
	// tools.SessionRunner has no in-tree implementation — it is an
	// injection seam for embedders (agent.WithSessionRunner), and the tool
	// is registered only when one is supplied. The field is here so an
	// embedder that does spawn children can attribute their events; nothing
	// in this repo populates it.
	SessionID       string `json:"session_id,omitempty"`
	ParentSessionID string `json:"parent_session_id,omitempty"`
	TurnNo          int    `json:"turn_no,omitempty"`
	// AttemptNo separates retry attempts of the same node. RetryCounts in
	// the checkpoint gives a final tally; this says which attempt a given
	// event belongs to.
	AttemptNo int `json:"attempt_no,omitempty"`
	// NodeKind ("agent" | "tool" | "human" | …) comes from engine state.
	// It is deliberately not inferred from which artifact files exist:
	// directories are sparse in large runs (a 39-node run leaves only a
	// handful), so file-based inference loses kind for most nodes.
	NodeKind string `json:"node_kind,omitempty"`

	// ToolInput is the tool call's arguments as the model produced them.
	// Content holds the tool's *output*; before this field existed the log
	// recorded that a tool ran and what came back, but never what was
	// asked — which makes repeated re-acquisition of the same state
	// invisible.
	ToolInput      string `json:"tool_input,omitempty"`
	ToolDurationMs int64  `json:"tool_duration_ms,omitempty"`

	// Per-turn economics. The engine already computes all of this per turn
	// (agent.TurnMetrics); run-level cost snapshots can't attribute spend
	// to a step. TokenInput/TokenOutput above carry the per-turn counts.
	CacheReadTokens    int     `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens   int     `json:"cache_write_tokens,omitempty"`
	ReasoningTokens    int     `json:"reasoning_tokens,omitempty"`
	EstimatedCost      float64 `json:"estimated_cost,omitempty"`
	ContextUtilization float64 `json:"context_utilization,omitempty"`
	ToolCacheHits      int     `json:"tool_cache_hits,omitempty"`
	ToolCacheMisses    int     `json:"tool_cache_misses,omitempty"`
	TurnDurationMs     int64   `json:"turn_duration_ms,omitempty"`

	// CallID groups every line belonging to one LLM call. Two log paths
	// write LLM activity — the agent session re-emits trace events, and
	// the client-level writer catches calls the session never sees (the
	// autopilot interviewer, for one) — so the same call can legitimately
	// appear twice. Both paths are kept because their coverage differs;
	// this field is what lets a reader collapse the overlap instead, so
	// aggregating usage doesn't double-count.
	CallID string `json:"call_id,omitempty"`
	// FinishReason distinguishes a completed turn from one that hit max
	// turns or stopped on a tool — different failure classes that were
	// previously indistinguishable.
	FinishReason string `json:"finish_reason,omitempty"`

	// TerminalStatus is the run's authoritative outcome, set on exactly one
	// event per run ("success", "validation_overridden", "fail",
	// "budget_exceeded"). The engine already guarantees the contract — see
	// Engine.emitTerminalBackstop, which fires even on a panic or an invariant
	// error — but the log dropped the field, so the one value a post-hoc
	// reader needs in order to say how a run ended was reconstructible only
	// by inferring from event types.
	TerminalStatus string `json:"terminal_status,omitempty"`
}

// applyAgentEventFields copies the run-reconstruction fields off an agent
// event. Kept out of WriteAgentEvent so that adding a field here doesn't
// raise that function's complexity.
func applyAgentEventFields(entry *jsonlLogEntry, evt agent.Event) {
	entry.SessionID = evt.SessionID
	entry.TurnNo = evt.Turn
	entry.ToolInput = evt.ToolInput
	entry.FinishReason = evt.FinishReason
	entry.ToolDurationMs = evt.ToolDuration.Milliseconds()
	entry.ContextUtilization = evt.ContextUtilization
	entry.CallID = evt.CallID
	// The request body is the highest-fidelity content this event carries, so
	// it wins over the display preview the generic content path would use.
	if len(evt.RequestRaw) > 0 {
		entry.Content = string(evt.RequestRaw)
	}
	applyUsageFields(entry, evt.Usage)
	applyTurnMetricsFields(entry, evt.Metrics)
}

// llmTraceContent picks the highest-fidelity payload the trace event carries.
// The Raw* fields are untruncated; Preview is clipped to 80 chars for display
// and is only the best available form for text and reasoning deltas, which
// carry no raw counterpart because they are never clipped in the first place.
func llmTraceContent(evt llm.TraceEvent) string {
	switch {
	case len(evt.RequestRaw) > 0:
		return string(evt.RequestRaw)
	case len(evt.ToolArguments) > 0:
		return string(evt.ToolArguments)
	case len(evt.ProviderRaw) > 0:
		return string(evt.ProviderRaw)
	case evt.Preview != "":
		return evt.Preview
	default:
		return evt.RawPreview
	}
}

// joinAgentErrors merges the two error channels on an agent event. A tool
// can fail (ToolError) and the session can also error (Err) on the same
// event, so both are preserved rather than one overwriting the other.
func joinAgentErrors(evt agent.Event) string {
	msg := evt.ToolError
	if evt.Err == nil {
		return msg
	}
	if msg == "" {
		return evt.Err.Error()
	}
	return msg + ": " + evt.Err.Error()
}

// applyUsageFields copies provider-reported token usage. The optional
// pointers are nil for providers that don't report that dimension, so a
// zero in the log means "not reported" rather than "reported as zero".
func applyUsageFields(entry *jsonlLogEntry, usage llm.Usage) {
	entry.TokenInput = usage.InputTokens
	entry.TokenOutput = usage.OutputTokens
	entry.EstimatedCost = usage.EstimatedCost
	if usage.ReasoningTokens != nil {
		entry.ReasoningTokens = *usage.ReasoningTokens
	}
	if usage.CacheReadTokens != nil {
		entry.CacheReadTokens = *usage.CacheReadTokens
	}
	if usage.CacheWriteTokens != nil {
		entry.CacheWriteTokens = *usage.CacheWriteTokens
	}
}

// applyTurnMetricsFields copies the per-turn metrics block. Metrics is nil
// on every event except turn_metrics.
func applyTurnMetricsFields(entry *jsonlLogEntry, m *agent.TurnMetrics) {
	if m == nil {
		return
	}
	entry.TokenInput = m.InputTokens
	entry.TokenOutput = m.OutputTokens
	entry.CacheReadTokens = m.CacheReadTokens
	entry.CacheWriteTokens = m.CacheWriteTokens
	entry.ContextUtilization = m.ContextUtilization
	entry.ToolCacheHits = m.ToolCacheHits
	entry.ToolCacheMisses = m.ToolCacheMisses
	entry.TurnDurationMs = m.TurnDuration.Milliseconds()
	entry.EstimatedCost = m.EstimatedCost
}
