// ABOUTME: Wire-stability guard: the NDJSON StreamEvent envelope is the contract
// ABOUTME: every transport/subscriber parses, so its field set must not drift silently.
package tracker

import (
	"reflect"
	"sort"
	"strings"
	"testing"
)

// TestStreamEvent_WireEnvelopeStable pins the NDJSON envelope's JSON field names.
// A new or renamed field breaks this deliberately, forcing a conscious wire-
// contract change (update `want` when the format intentionally evolves) rather
// than silently breaking a subscriber compiled against the old shape.
func TestStreamEvent_WireEnvelopeStable(t *testing.T) {
	want := []string{
		// The pre-E1 envelope. Every one of these is unchanged in name and type.
		"content", "error", "gate_id", "message", "model", "node_id", "provider",
		"run_id", "source", "terminal_status", "tool_name", "ts", "type",

		// E1: payload parity with activity.jsonl. Purely additive and every
		// field is omitempty, so a pre-E1 subscriber sees the same keys on the
		// same events. Names below mirror pipeline's jsonlLogEntry tags exactly
		// (verified mechanically by TestStreamEvent_MirrorsActivityLogFieldNames)
		// EXCEPT the snapshot_* group and
		// token_cache_read / token_cache_write / turn_cost_usd, which have no
		// activity.jsonl counterpart.
		"auto_status_fail_closed", "auto_status_tail", "bundle_identity",
		"cleared_nodes", "condition_match", "conditions_tried",
		"context_snapshot", "context_updates", "edge_condition", "edge_from",
		"edge_priority", "edge_to", "estimated", "gate_actor", "gate_choices",
		"gate_label", "gate_mode", "gate_outcome", "gate_prompt",
		"gate_questions", "gate_response", "gate_timed_out", "marker_error",
		"marker_pattern", "marker_tail", "outcome_status", "override_actor",
		"override_gate", "override_label", "override_subgraph_path",
		"provider_totals", "restart_count", "route_tail",
		"snapshot_completed_nodes", "snapshot_current_node", "snapshot_exit_node",
		"snapshot_nodes", "snapshot_start_node", "token_cache_read",
		"token_cache_write", "token_input", "token_output", "total_cost_usd",
		"total_tokens", "trunc_captured_bytes", "trunc_dropped_bytes",
		"trunc_limit", "trunc_stream", "trunc_total_bytes", "turn_cost_usd",
		"wall_elapsed_ms",

		// #519 run-capture / run-reconstruction group. Additive and omitempty;
		// mirrors pipeline's jsonlLogEntry so one struct decodes both schemas
		// (TestStreamEvent_MirrorsActivityLogFieldNames). Unpopulated on the
		// live wire — capture is activity.jsonl-only — but present so a
		// consumer can decode the richer audit log with StreamEvent.
		"attempt_no", "cache_read_tokens", "cache_write_tokens", "call_id",
		"context_utilization", "estimated_cost", "finish_reason", "node_kind",
		"parent_session_id", "reasoning_tokens", "session_id", "tool_cache_hits",
		"tool_cache_misses", "tool_duration_ms", "tool_input", "turn_duration_ms",
		"turn_no",
	}
	sort.Strings(want)
	got := jsonTagNames(reflect.TypeOf(StreamEvent{}))
	sort.Strings(got)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("StreamEvent wire envelope changed.\n got: %v\nwant: %v\n"+
			"If this is an intended wire-format change, update `want` — this is the\n"+
			"contract every NDJSON subscriber parses.", got, want)
	}
}

// jsonTagNames returns the JSON field names (tag before the first comma,
// skipping "-") of a struct type.
func jsonTagNames(tp reflect.Type) []string {
	var names []string
	for i := 0; i < tp.NumField(); i++ {
		tag := tp.Field(i).Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		if name, _, _ := strings.Cut(tag, ","); name != "" {
			names = append(names, name)
		}
	}
	return names
}
