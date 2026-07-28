// ABOUTME: Tests that ParseActivityLine decodes every structured payload activity.jsonl carries.
// ABOUTME: Synthetic lines stand in for real runs so the reader's losslessness is asserted directly.
package tracker

import (
	"reflect"
	"testing"

	"github.com/2389-research/tracker/pipeline"
)

// mustParse decodes line and fails the test if the line is rejected.
func mustParse(t *testing.T, line string) ActivityEntry {
	t.Helper()
	entry, ok := ParseActivityLine(line)
	if !ok {
		t.Fatalf("ParseActivityLine rejected line: %s", line)
	}
	return entry
}

func TestParseActivityLine_CostUpdated(t *testing.T) {
	line := `{"ts":"2026-07-28T10:00:00.000Z","source":"pipeline","type":"cost_updated",` +
		`"run_id":"run-1","total_tokens":4200,"total_cost_usd":0.1234,"wall_elapsed_ms":98765,` +
		`"estimated":true,"provider_totals":{"anthropic":{"input_tokens":1000,"output_tokens":200,` +
		`"total_tokens":1200,"cost_usd":0.05,"session_count":2}}}`
	entry := mustParse(t, line)

	if entry.Source != "pipeline" {
		t.Errorf("Source = %q, want pipeline", entry.Source)
	}
	if entry.TotalTokens != 4200 {
		t.Errorf("TotalTokens = %d, want 4200", entry.TotalTokens)
	}
	if entry.TotalCostUSD != 0.1234 {
		t.Errorf("TotalCostUSD = %v, want 0.1234", entry.TotalCostUSD)
	}
	if entry.WallElapsedMs != 98765 {
		t.Errorf("WallElapsedMs = %d, want 98765", entry.WallElapsedMs)
	}
	if !entry.Estimated {
		t.Error("Estimated = false, want true")
	}
	pu, ok := entry.ProviderTotals["anthropic"]
	if !ok {
		t.Fatalf("ProviderTotals missing anthropic key: %#v", entry.ProviderTotals)
	}
	if pu.TotalTokens != 1200 || pu.CostUSD != 0.05 || pu.SessionCount != 2 {
		t.Errorf("ProviderTotals[anthropic] = %#v, want tokens 1200 / cost 0.05 / sessions 2", pu)
	}
}

func TestParseActivityLine_DecisionEdge(t *testing.T) {
	line := `{"ts":"2026-07-28T10:00:01.000Z","source":"pipeline","type":"decision_edge",` +
		`"run_id":"run-1","node_id":"Build","edge_from":"Build","edge_to":"Review",` +
		`"edge_condition":"ctx.outcome = success","edge_priority":"conditional",` +
		`"condition_match":true,"outcome_status":"success","restart_count":3,` +
		`"cleared_nodes":["FixX","FixY"],"token_input":11,"token_output":22,` +
		`"context_snapshot":{"outcome":"success"},"context_updates":{"tool_stdout":"ok"},` +
		`"conditions_tried":[{"edge_to":"FixX","condition":"ctx.outcome = fail"}]}`
	entry := mustParse(t, line)

	if entry.EdgeFrom != "Build" || entry.EdgeTo != "Review" {
		t.Errorf("edge = %q -> %q, want Build -> Review", entry.EdgeFrom, entry.EdgeTo)
	}
	if entry.EdgeCondition != "ctx.outcome = success" {
		t.Errorf("EdgeCondition = %q", entry.EdgeCondition)
	}
	if entry.EdgePriority != "conditional" {
		t.Errorf("EdgePriority = %q, want conditional", entry.EdgePriority)
	}
	if entry.ConditionMatch == nil || !*entry.ConditionMatch {
		t.Errorf("ConditionMatch = %v, want pointer to true", entry.ConditionMatch)
	}
	if entry.OutcomeStatus != "success" {
		t.Errorf("OutcomeStatus = %q, want success", entry.OutcomeStatus)
	}
	if entry.RestartCount == nil || *entry.RestartCount != 3 {
		t.Errorf("RestartCount = %v, want pointer to 3", entry.RestartCount)
	}
	if got := entry.ClearedNodes; len(got) != 2 || got[0] != "FixX" || got[1] != "FixY" {
		t.Errorf("ClearedNodes = %v", got)
	}
	if entry.TokenInput != 11 || entry.TokenOutput != 22 {
		t.Errorf("tokens = %d/%d, want 11/22", entry.TokenInput, entry.TokenOutput)
	}
	if entry.ContextSnapshot["outcome"] != "success" {
		t.Errorf("ContextSnapshot = %v", entry.ContextSnapshot)
	}
	if entry.ContextUpdates["tool_stdout"] != "ok" {
		t.Errorf("ContextUpdates = %v", entry.ContextUpdates)
	}
	want := []pipeline.ConditionEval{{EdgeTo: "FixX", Condition: "ctx.outcome = fail"}}
	if len(entry.ConditionsTried) != 1 || entry.ConditionsTried[0] != want[0] {
		t.Errorf("ConditionsTried = %#v, want %#v", entry.ConditionsTried, want)
	}
}

// TestParseActivityLine_ConditionMatchFalseIsCarried guards the pointer
// semantics: a condition that evaluated false must be distinguishable from a
// line that carries no condition_match at all.
func TestParseActivityLine_ConditionMatchFalseIsCarried(t *testing.T) {
	entry := mustParse(t, `{"ts":"2026-07-28T10:00:01.000Z","type":"decision_condition","condition_match":false}`)
	if entry.ConditionMatch == nil {
		t.Fatal("ConditionMatch = nil, want pointer to false")
	}
	if *entry.ConditionMatch {
		t.Error("*ConditionMatch = true, want false")
	}
}

func TestParseActivityLine_GatePair(t *testing.T) {
	opened := `{"ts":"2026-07-28T10:00:02.000Z","source":"pipeline","type":"gate_opened",` +
		`"run_id":"run-1","node_id":"Approve","gate_id":"gate-7","gate_mode":"yes_no",` +
		`"gate_label":"Ship it?","gate_prompt":"Review the diff","gate_choices":["Yes","No"],` +
		`"gate_questions":[{"id":"q1","text":"Ready?","options":["a","b"],"is_yes_no":false}]}`
	resolved := `{"ts":"2026-07-28T10:00:03.000Z","source":"pipeline","type":"gate_resolved",` +
		`"run_id":"run-1","node_id":"Approve","gate_id":"gate-7","gate_mode":"yes_no",` +
		`"gate_response":"Yes","gate_outcome":"success","gate_actor":"human","gate_timed_out":true}`

	open := mustParse(t, opened)
	res := mustParse(t, resolved)

	if open.GateID == "" || open.GateID != res.GateID {
		t.Errorf("gate_id correlation broken: opened %q, resolved %q", open.GateID, res.GateID)
	}
	if open.GateMode != "yes_no" || res.GateMode != "yes_no" {
		t.Errorf("GateMode = %q / %q, want yes_no on both", open.GateMode, res.GateMode)
	}
	if open.GateLabel != "Ship it?" || open.GatePrompt != "Review the diff" {
		t.Errorf("label/prompt = %q / %q", open.GateLabel, open.GatePrompt)
	}
	if got := open.GateChoices; len(got) != 2 || got[0] != "Yes" || got[1] != "No" {
		t.Errorf("GateChoices = %v", got)
	}
	if len(open.GateQuestions) != 1 || open.GateQuestions[0].ID != "q1" ||
		len(open.GateQuestions[0].Options) != 2 {
		t.Errorf("GateQuestions = %#v", open.GateQuestions)
	}
	if res.GateResponse != "Yes" || res.GateOutcome != "success" {
		t.Errorf("response/outcome = %q / %q", res.GateResponse, res.GateOutcome)
	}
	if res.GateActor != pipeline.Actor("human") {
		t.Errorf("GateActor = %q, want human", res.GateActor)
	}
	if !res.GateTimedOut {
		t.Error("GateTimedOut = false, want true")
	}
}

func TestParseActivityLine_ToolOutputTruncated(t *testing.T) {
	line := `{"ts":"2026-07-28T10:00:04.000Z","source":"pipeline","type":"tool_output_truncated",` +
		`"node_id":"Build","trunc_stream":"stdout","trunc_limit":65536,` +
		`"trunc_captured_bytes":65536,"trunc_dropped_bytes":1024,"trunc_total_bytes":66560}`
	entry := mustParse(t, line)

	if entry.TruncStream != "stdout" {
		t.Errorf("TruncStream = %q, want stdout", entry.TruncStream)
	}
	if entry.TruncLimit != 65536 || entry.TruncCaptured != 65536 {
		t.Errorf("limit/captured = %d/%d", entry.TruncLimit, entry.TruncCaptured)
	}
	if entry.TruncDropped != 1024 || entry.TruncTotal != 66560 {
		t.Errorf("dropped/total = %d/%d", entry.TruncDropped, entry.TruncTotal)
	}
}

// TestParseActivityLine_ToolSignals covers the remaining diagnostic groups that
// the reader used to drop: marker, route, auto-status, and the agent/llm line
// identity fields (provider / model / tool_name / content / bundle_identity).
func TestParseActivityLine_ToolSignals(t *testing.T) {
	line := `{"ts":"2026-07-28T10:00:05.000Z","source":"agent","type":"tool_call_end",` +
		`"provider":"anthropic","model":"claude-x","tool_name":"Bash","content":"hello",` +
		`"bundle_identity":"sha256:abc","marker_pattern":"^READY","marker_tail":"tail-bytes",` +
		`"marker_error":"bad regex","route_tail":"route-bytes","auto_status_tail":"status-tail",` +
		`"auto_status_fail_closed":true}`
	entry := mustParse(t, line)

	checks := []struct {
		name string
		got  string
		want string
	}{
		{"Source", entry.Source, "agent"},
		{"Provider", entry.Provider, "anthropic"},
		{"Model", entry.Model, "claude-x"},
		{"ToolName", entry.ToolName, "Bash"},
		{"Content", entry.Content, "hello"},
		{"BundleIdentity", entry.BundleIdentity, "sha256:abc"},
		{"MarkerPattern", entry.MarkerPattern, "^READY"},
		{"MarkerTail", entry.MarkerTail, "tail-bytes"},
		{"MarkerError", entry.MarkerError, "bad regex"},
		{"RouteTail", entry.RouteTail, "route-bytes"},
		{"AutoStatusTail", entry.AutoStatusTail, "status-tail"},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", c.name, c.got, c.want)
		}
	}
	if !entry.AutoStatusFailClosed {
		t.Error("AutoStatusFailClosed = false, want true")
	}
}

// TestParseActivityLine_OverrideFieldsUnchanged pins the pre-existing override
// decode so the additive change cannot regress it.
func TestParseActivityLine_OverrideFieldsUnchanged(t *testing.T) {
	line := `{"ts":"2026-07-28T10:00:06.000Z","type":"validation_overridden","override_gate":"Gate",` +
		`"override_label":"override","override_actor":"human","override_subgraph_path":["parent","child"]}`
	entry := mustParse(t, line)
	if entry.OverrideGate != "Gate" || entry.OverrideLabel != "override" {
		t.Errorf("gate/label = %q / %q", entry.OverrideGate, entry.OverrideLabel)
	}
	if entry.OverrideActor != pipeline.Actor("human") {
		t.Errorf("OverrideActor = %q", entry.OverrideActor)
	}
	if got := entry.OverrideSubgraphPath; len(got) != 2 || got[0] != "parent" || got[1] != "child" {
		t.Errorf("OverrideSubgraphPath = %v", got)
	}
}

// TestParseActivityLine_MinimalAndUnknownKeys asserts the two robustness
// properties of the reader: a line carrying only ts/type parses with every
// optional field zero-valued, and an unknown key does not break the parse.
func TestParseActivityLine_MinimalAndUnknownKeys(t *testing.T) {
	entry := mustParse(t, `{"ts":"2026-07-28T10:00:07.000Z","type":"stage_started"}`)
	if entry.Type != "stage_started" {
		t.Errorf("Type = %q", entry.Type)
	}
	if entry.Timestamp.IsZero() {
		t.Error("Timestamp is zero, want parsed")
	}
	assertActivityPayloadZero(t, entry)

	withExtra := mustParse(t, `{"ts":"2026-07-28T10:00:07.000Z","type":"stage_started",`+
		`"future_field":{"nested":[1,2,3]},"another":"x"}`)
	if withExtra.Type != "stage_started" {
		t.Errorf("unknown-key line: Type = %q", withExtra.Type)
	}
	assertActivityPayloadZero(t, withExtra)
}

// assertActivityPayloadZero fails if any optional payload field is set. It
// checks pointers and containers explicitly (they are the fields where a
// decode bug shows up as a non-nil empty value) and covers the scalars by
// comparing against a zero entry with only the core fields copied over.
func assertActivityPayloadZero(t *testing.T, entry ActivityEntry) {
	t.Helper()
	if entry.ConditionMatch != nil {
		t.Errorf("ConditionMatch = %v, want nil", entry.ConditionMatch)
	}
	if entry.RestartCount != nil {
		t.Errorf("RestartCount = %v, want nil", entry.RestartCount)
	}
	if entry.ContextSnapshot != nil || entry.ContextUpdates != nil || entry.ProviderTotals != nil {
		t.Errorf("maps not nil: %v %v %v", entry.ContextSnapshot, entry.ContextUpdates, entry.ProviderTotals)
	}
	if entry.ClearedNodes != nil || entry.ConditionsTried != nil ||
		entry.GateChoices != nil || entry.GateQuestions != nil || entry.OverrideSubgraphPath != nil {
		t.Errorf("slices not nil: %v %v %v %v %v", entry.ClearedNodes, entry.ConditionsTried,
			entry.GateChoices, entry.GateQuestions, entry.OverrideSubgraphPath)
	}
	// ActivityEntry holds maps and slices, so it is not comparable with ==.
	bare := ActivityEntry{Timestamp: entry.Timestamp, Type: entry.Type}
	if !reflect.DeepEqual(entry, bare) {
		t.Errorf("entry carries non-zero payload fields:\n got %#v\nwant %#v", entry, bare)
	}
}
