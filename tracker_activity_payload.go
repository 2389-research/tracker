// ABOUTME: Decode struct and populate helpers behind ParseActivityLine's structured payloads.
// ABOUTME: JSON names mirror pipeline's jsonlLogEntry and Go names mirror StreamEvent (E2).
package tracker

import (
	"time"

	"github.com/2389-research/tracker/pipeline"
)

// activityRawLine is the wire-shaped decode target for one activity.jsonl
// line. It is the read-side mirror of pipeline's private jsonlLogEntry: every
// JSON name here is that writer's name, and every Go name is StreamEvent's Go
// name, so the three schemas cannot drift apart silently
// (tracker_activity_parity_test.go enforces both halves mechanically).
//
// Unknown keys are ignored by encoding/json, which is the documented
// forward-compatibility contract: a newer runtime's extra field makes an older
// reader lose that datum, never fail the line.
type activityRawLine struct {
	Timestamp      string `json:"ts"`
	Source         string `json:"source"`
	Type           string `json:"type"`
	RunID          string `json:"run_id"`
	NodeID         string `json:"node_id"`
	Message        string `json:"message"`
	Error          string `json:"error"`
	Provider       string `json:"provider"`
	Model          string `json:"model"`
	ToolName       string `json:"tool_name"`
	Content        string `json:"content"`
	BundleIdentity string `json:"bundle_identity"`

	// Decision audit trail fields.
	EdgeFrom        string                   `json:"edge_from"`
	EdgeTo          string                   `json:"edge_to"`
	EdgeCondition   string                   `json:"edge_condition"`
	EdgePriority    string                   `json:"edge_priority"`
	ConditionMatch  *bool                    `json:"condition_match"`
	OutcomeStatus   string                   `json:"outcome_status"`
	ContextSnapshot map[string]string        `json:"context_snapshot"`
	ContextUpdates  map[string]string        `json:"context_updates"`
	RestartCount    *int                     `json:"restart_count"`
	ClearedNodes    []string                 `json:"cleared_nodes"`
	ConditionsTried []pipeline.ConditionEval `json:"conditions_tried"`
	TokenInput      int                      `json:"token_input"`
	TokenOutput     int                      `json:"token_output"`

	// Cost snapshot fields.
	TotalTokens    int                               `json:"total_tokens"`
	TotalCostUSD   float64                           `json:"total_cost_usd"`
	ProviderTotals map[string]pipeline.ProviderUsage `json:"provider_totals"`
	WallElapsedMs  int64                             `json:"wall_elapsed_ms"`
	Estimated      bool                              `json:"estimated"`

	// Tool-node diagnostic fields.
	TruncStream   string `json:"trunc_stream"`
	TruncLimit    int    `json:"trunc_limit"`
	TruncCaptured int    `json:"trunc_captured_bytes"`
	TruncDropped  int    `json:"trunc_dropped_bytes"`
	TruncTotal    int    `json:"trunc_total_bytes"`
	MarkerPattern string `json:"marker_pattern"`
	MarkerTail    string `json:"marker_tail"`
	MarkerError   string `json:"marker_error"`
	RouteTail     string `json:"route_tail"`

	// Auto-status fields.
	AutoStatusTail       string `json:"auto_status_tail"`
	AutoStatusFailClosed bool   `json:"auto_status_fail_closed"`

	// Override fields.
	OverrideGate         string         `json:"override_gate"`
	OverrideLabel        string         `json:"override_label"`
	OverrideActor        pipeline.Actor `json:"override_actor"`
	OverrideSubgraphPath []string       `json:"override_subgraph_path"`

	// Gate lifecycle fields.
	GateID        string                  `json:"gate_id"`
	GateMode      string                  `json:"gate_mode"`
	GateLabel     string                  `json:"gate_label"`
	GatePrompt    string                  `json:"gate_prompt"`
	GateChoices   []string                `json:"gate_choices"`
	GateQuestions []pipeline.GateQuestion `json:"gate_questions"`
	GateResponse  string                  `json:"gate_response"`
	GateOutcome   string                  `json:"gate_outcome"`
	GateActor     pipeline.Actor          `json:"gate_actor"`
	GateTimedOut  bool                    `json:"gate_timed_out"`

	// Run-reconstruction / capture fields (#519) — session/turn/call identity,
	// node kind, attempt number, untruncated tool input, per-turn economics,
	// finish reason, and the run's terminal status. Mirror pipeline's
	// jsonlLogEntry so the supported reader stays lossless (E2).
	SessionID          string  `json:"session_id"`
	ParentSessionID    string  `json:"parent_session_id"`
	TurnNo             int     `json:"turn_no"`
	AttemptNo          int     `json:"attempt_no"`
	NodeKind           string  `json:"node_kind"`
	ToolInput          string  `json:"tool_input"`
	ToolDurationMs     int64   `json:"tool_duration_ms"`
	CacheReadTokens    int     `json:"cache_read_tokens"`
	CacheWriteTokens   int     `json:"cache_write_tokens"`
	ReasoningTokens    int     `json:"reasoning_tokens"`
	EstimatedCost      float64 `json:"estimated_cost"`
	ContextUtilization float64 `json:"context_utilization"`
	ToolCacheHits      int     `json:"tool_cache_hits"`
	ToolCacheMisses    int     `json:"tool_cache_misses"`
	TurnDurationMs     int64   `json:"turn_duration_ms"`
	CallID             string  `json:"call_id"`
	FinishReason       string  `json:"finish_reason"`
	TerminalStatus     string  `json:"terminal_status"`
}

// toEntry builds the exported ActivityEntry from a decoded line. ts is passed
// in already parsed because the timestamp needs the dual-format handling that
// keeps ActivityEntry off the JSON wire (see its doc comment).
//
// Aliasing: each ParseActivityLine call unmarshals into its own local
// activityRawLine, so every map and slice below was freshly allocated by
// encoding/json for this line only — no two entries can share backing storage,
// and nothing in tracker retains r after this returns.
func (r *activityRawLine) toEntry(ts time.Time) ActivityEntry {
	entry := ActivityEntry{
		Timestamp:      ts,
		Source:         r.Source,
		Type:           r.Type,
		RunID:          r.RunID,
		NodeID:         r.NodeID,
		Message:        r.Message,
		Error:          r.Error,
		Provider:       r.Provider,
		Model:          r.Model,
		ToolName:       r.ToolName,
		Content:        r.Content,
		BundleIdentity: r.BundleIdentity,
	}
	r.applyDecision(&entry)
	r.applyCost(&entry)
	r.applyToolSignals(&entry)
	r.applyNodeSignals(&entry)
	r.applyCapture(&entry)
	return entry
}

// applyCapture copies the run-reconstruction / capture group (#519):
// session/turn/call identity, node kind, attempt, tool input, per-turn
// economics, finish reason, and terminal status.
func (r *activityRawLine) applyCapture(entry *ActivityEntry) {
	entry.SessionID = r.SessionID
	entry.ParentSessionID = r.ParentSessionID
	entry.TurnNo = r.TurnNo
	entry.AttemptNo = r.AttemptNo
	entry.NodeKind = r.NodeKind
	entry.ToolInput = r.ToolInput
	entry.ToolDurationMs = r.ToolDurationMs
	entry.CacheReadTokens = r.CacheReadTokens
	entry.CacheWriteTokens = r.CacheWriteTokens
	entry.ReasoningTokens = r.ReasoningTokens
	entry.EstimatedCost = r.EstimatedCost
	entry.ContextUtilization = r.ContextUtilization
	entry.ToolCacheHits = r.ToolCacheHits
	entry.ToolCacheMisses = r.ToolCacheMisses
	entry.TurnDurationMs = r.TurnDurationMs
	entry.CallID = r.CallID
	entry.FinishReason = r.FinishReason
	entry.TerminalStatus = r.TerminalStatus
}

// applyDecision copies the edge-decision group (decision_* and
// conditional_fallthrough lines).
func (r *activityRawLine) applyDecision(entry *ActivityEntry) {
	entry.EdgeFrom = r.EdgeFrom
	entry.EdgeTo = r.EdgeTo
	entry.EdgeCondition = r.EdgeCondition
	entry.EdgePriority = r.EdgePriority
	entry.ConditionMatch = r.ConditionMatch
	entry.OutcomeStatus = r.OutcomeStatus
	entry.ContextSnapshot = r.ContextSnapshot
	entry.ContextUpdates = r.ContextUpdates
	entry.RestartCount = r.RestartCount
	entry.ClearedNodes = r.ClearedNodes
	entry.ConditionsTried = r.ConditionsTried
	entry.TokenInput = r.TokenInput
	entry.TokenOutput = r.TokenOutput
}

// applyCost copies the run-cumulative cost snapshot (cost_updated,
// budget_exceeded lines).
func (r *activityRawLine) applyCost(entry *ActivityEntry) {
	entry.TotalTokens = r.TotalTokens
	entry.TotalCostUSD = r.TotalCostUSD
	entry.ProviderTotals = r.ProviderTotals
	entry.WallElapsedMs = r.WallElapsedMs
	entry.Estimated = r.Estimated
}

// applyToolSignals copies the tool-node diagnostic groups (truncation, marker,
// route).
func (r *activityRawLine) applyToolSignals(entry *ActivityEntry) {
	entry.TruncStream = r.TruncStream
	entry.TruncLimit = r.TruncLimit
	entry.TruncCaptured = r.TruncCaptured
	entry.TruncDropped = r.TruncDropped
	entry.TruncTotal = r.TruncTotal
	entry.MarkerPattern = r.MarkerPattern
	entry.MarkerTail = r.MarkerTail
	entry.MarkerError = r.MarkerError
	entry.RouteTail = r.RouteTail
}

// applyNodeSignals copies the node-level groups (auto-status, override, gate
// lifecycle).
func (r *activityRawLine) applyNodeSignals(entry *ActivityEntry) {
	entry.AutoStatusTail = r.AutoStatusTail
	entry.AutoStatusFailClosed = r.AutoStatusFailClosed
	entry.OverrideGate = r.OverrideGate
	entry.OverrideLabel = r.OverrideLabel
	entry.OverrideActor = r.OverrideActor
	if len(r.OverrideSubgraphPath) > 0 {
		// Pre-existing defensive copy, kept verbatim so this change stays
		// behavior-neutral. Redundant given the per-call decode above, but
		// harmless — and it keeps the guarantee if r ever becomes reused.
		entry.OverrideSubgraphPath = append([]string(nil), r.OverrideSubgraphPath...)
	}
	entry.GateID = r.GateID
	entry.GateMode = r.GateMode
	entry.GateLabel = r.GateLabel
	entry.GatePrompt = r.GatePrompt
	entry.GateChoices = r.GateChoices
	entry.GateQuestions = r.GateQuestions
	entry.GateResponse = r.GateResponse
	entry.GateOutcome = r.GateOutcome
	entry.GateActor = r.GateActor
	entry.GateTimedOut = r.GateTimedOut
}
