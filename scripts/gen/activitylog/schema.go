// ABOUTME: The single field authority for the activity-log reader schema.
// ABOUTME: One entry per activity.jsonl field; the generator derives the three
// ABOUTME: reader-side artifacts (activityRawLine, ActivityEntry, toEntry) from it.
package main

// field is one activity.jsonl datum, declared exactly once. Every reader-side
// artifact the generator emits is derived from this list, so a new field is
// added here (and nowhere else) and the three shapes cannot drift apart.
//
//   - Go is the Go field name, shared by activityRawLine and ActivityEntry so a
//     caller moving between the live NDJSON stream and the replayed audit log
//     writes the same field accesses (the Go-name half of E1 parity).
//   - JSON is the on-disk key. It mirrors pipeline's jsonlLogEntry writer name,
//     so one decoder serves both the audit log and the --json wire.
//   - Type is the ActivityEntry field type. The raw decode struct uses the same
//     type, except Timestamp (FromTS) which is a string on the wire and a
//     time.Time once parsed.
//   - Doc, when set, is the per-field doc comment on the exported ActivityEntry.
//   - FromTS marks the timestamp field: raw type string, entry type time.Time,
//     copied from the pre-parsed ts argument rather than off the raw struct.
//   - Defensive marks a slice that toEntry copies into fresh backing storage.
type field struct {
	Go        string
	JSON      string
	Type      string
	Doc       string
	FromTS    bool
	Defensive bool
}

// group is a documented cluster of fields; its Comment heads the cluster in the
// generated structs.
type group struct {
	Comment string
	Fields  []field
}

// schema is the complete, ordered field authority for the activity-log reader.
// Order here is the field order in the generated structs; it is cosmetic (the
// raw struct is decode-only and ActivityEntry is never marshalled) but must be
// deterministic for the freshness gate.
var schema = []group{
	{
		Comment: "Core identity and payload — present on most lines.",
		Fields: []field{
			{Go: "Timestamp", JSON: "ts", Type: "time.Time", FromTS: true, Doc: "Timestamp is always set — a line whose ts does not parse is rejected\noutright."},
			{Go: "Source", JSON: "source", Type: "string", Doc: "Source is the emitting subsystem: \"pipeline\" (engine), \"agent\" (LLM\nsession), \"llm\" (raw provider events), or \"cli\" (CLI-level audit)."},
			{Go: "Type", JSON: "type", Type: "string"},
			{Go: "RunID", JSON: "run_id", Type: "string"},
			{Go: "NodeID", JSON: "node_id", Type: "string"},
			{Go: "Message", JSON: "message", Type: "string"},
			{Go: "Error", JSON: "error", Type: "string"},
			{Go: "Provider", JSON: "provider", Type: "string", Doc: "Identity of the emitting LLM call / tool, and its payload text\n(tool output or response preview). Set on agent and llm lines."},
			{Go: "Model", JSON: "model", Type: "string"},
			{Go: "ToolName", JSON: "tool_name", Type: "string"},
			{Go: "Content", JSON: "content", Type: "string"},
			{Go: "BundleIdentity", JSON: "bundle_identity", Type: "string", Doc: "BundleIdentity is the content-addressed identity of the .dipx bundle\nthe run executed against (\"sha256:<hex>\"); empty for a plain .dip run."},
		},
	},
	{
		Comment: "Decision fields — populated for decision_edge / decision_condition /\ndecision_outcome / decision_restart / conditional_fallthrough entries.\nConditionMatch and RestartCount are pointers precisely so a consumer can\ntell false/0 from absent.",
		Fields: []field{
			{Go: "EdgeFrom", JSON: "edge_from", Type: "string"},
			{Go: "EdgeTo", JSON: "edge_to", Type: "string"},
			{Go: "EdgeCondition", JSON: "edge_condition", Type: "string"},
			{Go: "EdgePriority", JSON: "edge_priority", Type: "string"},
			{Go: "ConditionMatch", JSON: "condition_match", Type: "*bool"},
			{Go: "OutcomeStatus", JSON: "outcome_status", Type: "string"},
			{Go: "ContextSnapshot", JSON: "context_snapshot", Type: "map[string]string"},
			{Go: "ContextUpdates", JSON: "context_updates", Type: "map[string]string"},
			{Go: "RestartCount", JSON: "restart_count", Type: "*int"},
			{Go: "ClearedNodes", JSON: "cleared_nodes", Type: "[]string"},
			{Go: "ConditionsTried", JSON: "conditions_tried", Type: "[]pipeline.ConditionEval"},
			{Go: "TokenInput", JSON: "token_input", Type: "int", Doc: "TokenInput / TokenOutput are the node's session token counts on a\ndecision entry — never run-cumulative (that is TotalTokens). Per-turn\ncache-token counts and cost ride on the capture group below."},
			{Go: "TokenOutput", JSON: "token_output", Type: "int"},
		},
	},
	{
		Comment: "Cost snapshot fields — populated for cost_updated and budget_exceeded\nentries. Run-cumulative, not per-node. Estimated is true when any\ncontributing session was heuristic-derived.",
		Fields: []field{
			{Go: "TotalTokens", JSON: "total_tokens", Type: "int"},
			{Go: "TotalCostUSD", JSON: "total_cost_usd", Type: "float64"},
			{Go: "ProviderTotals", JSON: "provider_totals", Type: "map[string]pipeline.ProviderUsage"},
			{Go: "WallElapsedMs", JSON: "wall_elapsed_ms", Type: "int64"},
			{Go: "Estimated", JSON: "estimated", Type: "bool"},
		},
	},
	{
		Comment: "Truncation fields — populated for tool_output_truncated entries (#208).",
		Fields: []field{
			{Go: "TruncStream", JSON: "trunc_stream", Type: "string"},
			{Go: "TruncLimit", JSON: "trunc_limit", Type: "int"},
			{Go: "TruncCaptured", JSON: "trunc_captured_bytes", Type: "int"},
			{Go: "TruncDropped", JSON: "trunc_dropped_bytes", Type: "int"},
			{Go: "TruncTotal", JSON: "trunc_total_bytes", Type: "int"},
		},
	},
	{
		Comment: "Marker fields — populated for tool_marker_missing entries (#210).",
		Fields: []field{
			{Go: "MarkerPattern", JSON: "marker_pattern", Type: "string"},
			{Go: "MarkerTail", JSON: "marker_tail", Type: "string"},
			{Go: "MarkerError", JSON: "marker_error", Type: "string"},
		},
	},
	{
		Comment: "RouteTail is populated for tool_route_missing entries (#212).",
		Fields: []field{
			{Go: "RouteTail", JSON: "route_tail", Type: "string"},
		},
	},
	{
		Comment: "Auto-status fields — populated for auto_status_missing entries (#346).",
		Fields: []field{
			{Go: "AutoStatusTail", JSON: "auto_status_tail", Type: "string"},
			{Go: "AutoStatusFailClosed", JSON: "auto_status_fail_closed", Type: "bool"},
		},
	},
	{
		Comment: "Override fields — populated for \"validation_overridden\" entries. Mirror\nthe wire-format fields written by the runtime's jsonlLogEntry: the gate\nthat produced the override, the label that selected the override edge, who\nacted, and the subgraph_path when propagated up from a child run.",
		Fields: []field{
			{Go: "OverrideGate", JSON: "override_gate", Type: "string"},
			{Go: "OverrideLabel", JSON: "override_label", Type: "string"},
			{Go: "OverrideActor", JSON: "override_actor", Type: "pipeline.Actor"},
			{Go: "OverrideSubgraphPath", JSON: "override_subgraph_path", Type: "[]string", Defensive: true},
		},
	},
	{
		Comment: "Gate lifecycle fields — populated for gate_opened / gate_resolved entries\n(#509). GateID correlates the pair; NodeID identifies the gate node on\nboth. Open-time: GateMode, GateLabel, GatePrompt, GateChoices,\nGateQuestions. Resolve-time: GateResponse, GateOutcome, GateActor,\nGateTimedOut (plus Error when the gate failed to collect an answer).",
		Fields: []field{
			{Go: "GateID", JSON: "gate_id", Type: "string"},
			{Go: "GateMode", JSON: "gate_mode", Type: "string"},
			{Go: "GateLabel", JSON: "gate_label", Type: "string"},
			{Go: "GatePrompt", JSON: "gate_prompt", Type: "string"},
			{Go: "GateChoices", JSON: "gate_choices", Type: "[]string"},
			{Go: "GateQuestions", JSON: "gate_questions", Type: "[]pipeline.GateQuestion"},
			{Go: "GateResponse", JSON: "gate_response", Type: "string"},
			{Go: "GateOutcome", JSON: "gate_outcome", Type: "string"},
			{Go: "GateActor", JSON: "gate_actor", Type: "pipeline.Actor"},
			{Go: "GateTimedOut", JSON: "gate_timed_out", Type: "bool"},
		},
	},
	{
		Comment: "Run-reconstruction / capture fields (#519). Mirror the same-named keys on\npipeline's jsonlLogEntry so the supported reader is lossless (E2).\nSessionID/TurnNo/CallID identify the emitting session/turn/LLM call;\nNodeKind and AttemptNo come from engine state; ToolInput is the\nuntruncated tool arguments; the token/cost/duration fields carry per-turn\neconomics; FinishReason classifies a turn's end; TerminalStatus is the\nrun's authoritative outcome, set on exactly one entry per run.",
		Fields: []field{
			{Go: "SessionID", JSON: "session_id", Type: "string"},
			{Go: "ParentSessionID", JSON: "parent_session_id", Type: "string"},
			{Go: "TurnNo", JSON: "turn_no", Type: "int"},
			{Go: "AttemptNo", JSON: "attempt_no", Type: "int"},
			{Go: "NodeKind", JSON: "node_kind", Type: "string"},
			{Go: "ToolInput", JSON: "tool_input", Type: "string"},
			{Go: "ToolDurationMs", JSON: "tool_duration_ms", Type: "int64"},
			{Go: "CacheReadTokens", JSON: "cache_read_tokens", Type: "int"},
			{Go: "CacheWriteTokens", JSON: "cache_write_tokens", Type: "int"},
			{Go: "ReasoningTokens", JSON: "reasoning_tokens", Type: "int"},
			{Go: "EstimatedCost", JSON: "estimated_cost", Type: "float64"},
			{Go: "ContextUtilization", JSON: "context_utilization", Type: "float64"},
			{Go: "ToolCacheHits", JSON: "tool_cache_hits", Type: "int"},
			{Go: "ToolCacheMisses", JSON: "tool_cache_misses", Type: "int"},
			{Go: "TurnDurationMs", JSON: "turn_duration_ms", Type: "int64"},
			{Go: "CallID", JSON: "call_id", Type: "string"},
			{Go: "FinishReason", JSON: "finish_reason", Type: "string"},
			{Go: "TerminalStatus", JSON: "terminal_status", Type: "string"},
			{Go: "ResumeAfter", JSON: "resume_after", Type: "string", Doc: "ResumeAfter is the provider's rate/usage reset time (RFC3339 string), set\nonly on a billing_paused line whose pause carried it (#591). Kept as the\non-disk string — like TerminalStatus — rather than a typed time.Time so\nthe reader stays a pure decode with no reparse."},
		},
	},
}
