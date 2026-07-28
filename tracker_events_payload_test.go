// ABOUTME: Round-trip tests for the structured StreamEvent payload fields (E1).
// ABOUTME: Asserts activity.jsonl payload parity on the public NDJSON wire, and the omitempty guarantee.
package tracker

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/2389-research/tracker/agent"
	"github.com/2389-research/tracker/llm"
	"github.com/2389-research/tracker/pipeline"
)

// writePipelineEvent feeds evt through a fresh NDJSONWriter's PipelineHandler and
// returns the single emitted line, both raw and decoded.
func writePipelineEvent(t *testing.T, evt pipeline.PipelineEvent) (string, StreamEvent) {
	t.Helper()
	var buf bytes.Buffer
	NewNDJSONWriter(&buf).PipelineHandler().HandlePipelineEvent(evt)
	line := strings.TrimSuffix(buf.String(), "\n")
	if line == "" {
		t.Fatal("no NDJSON line written")
	}
	var got StreamEvent
	if err := json.Unmarshal([]byte(line), &got); err != nil {
		t.Fatalf("unmarshal %q: %v", line, err)
	}
	return line, got
}

func TestStreamEvent_CostSnapshotRoundTrip(t *testing.T) {
	_, got := writePipelineEvent(t, pipeline.PipelineEvent{
		Type:      pipeline.EventCostUpdated,
		Timestamp: time.Now(),
		RunID:     "run1",
		NodeID:    "Build",
		Cost: &pipeline.CostSnapshot{
			TotalTokens:  1234,
			TotalCostUSD: 0.4275,
			ProviderTotals: map[string]pipeline.ProviderUsage{
				"anthropic": {InputTokens: 1000, OutputTokens: 234, TotalTokens: 1234, CostUSD: 0.4275, SessionCount: 2},
			},
			WallElapsed: 1500 * time.Millisecond,
			Estimated:   true,
		},
	})
	if got.TotalTokens != 1234 || got.TotalCostUSD != 0.4275 {
		t.Errorf("cost totals lost: %+v", got)
	}
	if got.WallElapsedMs != 1500 {
		t.Errorf("wall_elapsed_ms = %d, want 1500", got.WallElapsedMs)
	}
	if !got.Estimated {
		t.Error("estimated flag lost")
	}
	pu, ok := got.ProviderTotals["anthropic"]
	if !ok {
		t.Fatalf("provider_totals lost: %+v", got.ProviderTotals)
	}
	if pu.TotalTokens != 1234 || pu.CostUSD != 0.4275 || pu.SessionCount != 2 {
		t.Errorf("provider usage detail lost: %+v", pu)
	}
}

func TestStreamEvent_RunSnapshotRoundTrip(t *testing.T) {
	_, got := writePipelineEvent(t, pipeline.PipelineEvent{
		Type:      pipeline.EventPipelineStarted,
		Timestamp: time.Now(),
		RunID:     "run1",
		Snapshot: &pipeline.RunSnapshot{
			Nodes: []pipeline.SnapshotNode{
				{ID: "Build", Label: "Build it", Handler: "codergen"},
				{ID: "Review", Label: "Review it", Handler: "wait.human"},
			},
			StartNode:      "Build",
			ExitNode:       "Done",
			CurrentNode:    "Review",
			CompletedNodes: []string{"Build"},
		},
	})
	if len(got.SnapshotNodes) != 2 {
		t.Fatalf("snapshot_nodes = %+v, want 2 entries", got.SnapshotNodes)
	}
	if got.SnapshotNodes[0].ID != "Build" || got.SnapshotNodes[0].Handler != "codergen" {
		t.Errorf("snapshot node detail lost: %+v", got.SnapshotNodes[0])
	}
	if got.SnapshotNodes[1].Label != "Review it" {
		t.Errorf("snapshot node label lost: %+v", got.SnapshotNodes[1])
	}
	if got.SnapshotStartNode != "Build" || got.SnapshotExitNode != "Done" {
		t.Errorf("snapshot start/exit lost: %+v", got)
	}
	if got.SnapshotCurrentNode != "Review" || len(got.SnapshotCompletedNodes) != 1 {
		t.Errorf("snapshot resume state lost: %+v", got)
	}
}

func TestStreamEvent_DecisionEdgeRoundTrip(t *testing.T) {
	_, got := writePipelineEvent(t, pipeline.PipelineEvent{
		Type:      pipeline.EventDecisionEdge,
		Timestamp: time.Now(),
		RunID:     "run1",
		NodeID:    "Build",
		Decision: &pipeline.DecisionDetail{
			EdgeFrom:        "Build",
			EdgeTo:          "Review",
			EdgePriority:    "condition",
			EdgeCondition:   "ctx.outcome = success",
			ConditionMatch:  true,
			OutcomeStatus:   "success",
			ContextUpdates:  map[string]string{"last_response": "ok"},
			ContextSnapshot: map[string]string{"outcome": "success"},
			RestartCount:    2,
			ClearedNodes:    []string{"Fix"},
			TokenInput:      100,
			TokenOutput:     20,
			ConditionsTried: []pipeline.ConditionEval{{EdgeTo: "Fix", Condition: "ctx.outcome = fail"}},
		},
	})
	if got.EdgeFrom != "Build" || got.EdgeTo != "Review" || got.EdgePriority != "condition" {
		t.Errorf("edge fields lost: %+v", got)
	}
	if got.EdgeCondition != "ctx.outcome = success" {
		t.Errorf("edge_condition lost: %+v", got)
	}
	if got.ConditionMatch == nil || !*got.ConditionMatch {
		t.Errorf("condition_match lost: %+v", got.ConditionMatch)
	}
	if got.OutcomeStatus != "success" {
		t.Errorf("outcome_status lost: %+v", got)
	}
	if got.ContextUpdates["last_response"] != "ok" || got.ContextSnapshot["outcome"] != "success" {
		t.Errorf("context maps lost: %+v %+v", got.ContextUpdates, got.ContextSnapshot)
	}
	if got.RestartCount == nil || *got.RestartCount != 2 {
		t.Errorf("restart_count lost: %+v", got.RestartCount)
	}
	if len(got.ClearedNodes) != 1 || got.ClearedNodes[0] != "Fix" {
		t.Errorf("cleared_nodes lost: %+v", got.ClearedNodes)
	}
	if got.TokenInput != 100 || got.TokenOutput != 20 {
		t.Errorf("token counts lost: %+v", got)
	}
	if len(got.ConditionsTried) != 1 || got.ConditionsTried[0].EdgeTo != "Fix" {
		t.Errorf("conditions_tried lost: %+v", got.ConditionsTried)
	}
}

func TestStreamEvent_GateOpenedRoundTrip(t *testing.T) {
	_, got := writePipelineEvent(t, pipeline.PipelineEvent{
		Type:      pipeline.EventGateOpened,
		Timestamp: time.Now(),
		RunID:     "run1",
		NodeID:    "Review",
		Gate: &pipeline.GateDetail{
			GateID:  "gate-1",
			Mode:    "choice",
			Label:   "Ship it?",
			Prompt:  "Ship it?\n\nthe diff looks fine",
			Choices: []string{"approve", "reject"},
		},
	})
	if got.GateID != "gate-1" || got.GateMode != "choice" || got.GateLabel != "Ship it?" {
		t.Errorf("gate identity fields lost: %+v", got)
	}
	if !strings.Contains(got.GatePrompt, "the diff looks fine") {
		t.Errorf("gate_prompt lost: %q", got.GatePrompt)
	}
	if len(got.GateChoices) != 2 || got.GateChoices[0] != "approve" {
		t.Errorf("gate_choices lost: %+v", got.GateChoices)
	}
}

func TestStreamEvent_GateResolvedRoundTrip(t *testing.T) {
	_, got := writePipelineEvent(t, pipeline.PipelineEvent{
		Type:      pipeline.EventGateResolved,
		Timestamp: time.Now(),
		RunID:     "run1",
		NodeID:    "Interview",
		Gate: &pipeline.GateDetail{
			GateID:   "gate-2",
			Mode:     "interview",
			Question: []pipeline.GateQuestion{{ID: "q0", Text: "Which DB?", Options: []string{"pg", "sqlite"}}},
			Response: "q0: pg",
			Outcome:  "success",
			Actor:    pipeline.ActorHuman,
			TimedOut: true,
		},
	})
	if got.GateID != "gate-2" || got.GateResponse != "q0: pg" || got.GateOutcome != "success" {
		t.Errorf("gate resolution fields lost: %+v", got)
	}
	if got.GateActor != pipeline.ActorHuman || !got.GateTimedOut {
		t.Errorf("gate actor/timeout lost: %+v", got)
	}
	if len(got.GateQuestions) != 1 || got.GateQuestions[0].ID != "q0" {
		t.Errorf("gate_questions lost: %+v", got.GateQuestions)
	}
}

func TestStreamEvent_ToolAndNodeSignalRoundTrip(t *testing.T) {
	_, trunc := writePipelineEvent(t, pipeline.PipelineEvent{
		Type:       pipeline.EventToolOutputTruncated,
		Timestamp:  time.Now(),
		RunID:      "run1",
		NodeID:     "Build",
		Truncation: &pipeline.TruncationDetail{Stream: "stdout", Limit: 64, CapturedBytes: 64, DroppedBytes: 36, TotalBytes: 100},
	})
	if trunc.TruncStream != "stdout" || trunc.TruncLimit != 64 {
		t.Errorf("truncation stream/limit lost: %+v", trunc)
	}
	if trunc.TruncCaptured != 64 || trunc.TruncDropped != 36 || trunc.TruncTotal != 100 {
		t.Errorf("truncation byte accounting lost: %+v", trunc)
	}

	_, marker := writePipelineEvent(t, pipeline.PipelineEvent{
		Type:      pipeline.EventToolMarkerMissing,
		Timestamp: time.Now(),
		RunID:     "run1",
		Marker:    &pipeline.MarkerDetail{Pattern: "^OK$", CapturedTail: "nope", Error: "bad regex"},
	})
	if marker.MarkerPattern != "^OK$" || marker.MarkerTail != "nope" || marker.MarkerError != "bad regex" {
		t.Errorf("marker fields lost: %+v", marker)
	}

	_, route := writePipelineEvent(t, pipeline.PipelineEvent{
		Type:      pipeline.EventToolRouteMissing,
		Timestamp: time.Now(),
		RunID:     "run1",
		Route:     &pipeline.RouteDetail{CapturedTail: "no sentinel"},
	})
	if route.RouteTail != "no sentinel" {
		t.Errorf("route_tail lost: %+v", route)
	}

	_, auto := writePipelineEvent(t, pipeline.PipelineEvent{
		Type:       pipeline.EventAutoStatusMissing,
		Timestamp:  time.Now(),
		RunID:      "run1",
		AutoStatus: &pipeline.AutoStatusDetail{ResponseTail: "no STATUS", FailClosed: true},
	})
	if auto.AutoStatusTail != "no STATUS" || !auto.AutoStatusFailClosed {
		t.Errorf("auto-status fields lost: %+v", auto)
	}
}

func TestStreamEvent_OverrideAndBundleRoundTrip(t *testing.T) {
	_, got := writePipelineEvent(t, pipeline.PipelineEvent{
		Type:           pipeline.EventValidationOverridden,
		Timestamp:      time.Now(),
		RunID:          "run1",
		NodeID:         "Review",
		BundleIdentity: "sha256:abc",
		Override: &pipeline.OverrideDetail{
			GateNodeID:   "Review",
			Label:        "accept",
			Actor:        pipeline.ActorAutopilot,
			SubgraphPath: []string{"Child"},
		},
	})
	if got.OverrideGate != "Review" || got.OverrideLabel != "accept" {
		t.Errorf("override gate/label lost: %+v", got)
	}
	if got.OverrideActor != pipeline.ActorAutopilot {
		t.Errorf("override_actor lost: %+v", got)
	}
	if len(got.OverrideSubgraphPath) != 1 || got.OverrideSubgraphPath[0] != "Child" {
		t.Errorf("override_subgraph_path lost: %+v", got.OverrideSubgraphPath)
	}
	if got.BundleIdentity != "sha256:abc" {
		t.Errorf("bundle_identity lost: %+v", got)
	}
}

// TestStreamEvent_OverrideSubgraphPathIsCopied guards against the wire event
// aliasing the emitter's slice.
func TestStreamEvent_OverrideSubgraphPathIsCopied(t *testing.T) {
	path := []string{"Child"}
	var buf bytes.Buffer
	h := NewNDJSONWriter(&buf).PipelineHandler()
	h.HandlePipelineEvent(pipeline.PipelineEvent{
		Type:      pipeline.EventValidationOverridden,
		Timestamp: time.Now(),
		RunID:     "run1",
		Override:  &pipeline.OverrideDetail{GateNodeID: "Review", SubgraphPath: path},
	})
	if !strings.Contains(buf.String(), `"override_subgraph_path":["Child"]`) {
		t.Fatalf("subgraph path not serialized: %s", buf.String())
	}
}

func TestStreamEvent_TurnMetricsUsageRoundTrip(t *testing.T) {
	cacheRead, cacheWrite := 40, 10
	var buf bytes.Buffer
	NewNDJSONWriter(&buf).AgentHandler().HandleEvent(agent.Event{
		Type:      agent.EventTurnMetrics,
		Timestamp: time.Now(),
		NodeID:    "Build",
		Provider:  "anthropic",
		Model:     "claude-opus-4",
		Usage: llm.Usage{
			InputTokens:      500,
			OutputTokens:     120,
			TotalTokens:      620,
			CacheReadTokens:  &cacheRead,
			CacheWriteTokens: &cacheWrite,
		},
		Metrics: &agent.TurnMetrics{InputTokens: 500, OutputTokens: 120, EstimatedCost: 0.0031},
	})
	var got StreamEvent
	if err := json.Unmarshal([]byte(strings.TrimSuffix(buf.String(), "\n")), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Type != "turn_metrics" || got.Provider != "anthropic" || got.Model != "claude-opus-4" {
		t.Errorf("turn_metrics attribution lost: %+v", got)
	}
	if got.TokenInput != 500 || got.TokenOutput != 120 {
		t.Errorf("turn token counts lost: %+v", got)
	}
	if got.TokenCacheRead != 40 || got.TokenCacheWrite != 10 {
		t.Errorf("turn cache token counts lost: %+v", got)
	}
	if got.TurnCostUSD != 0.0031 {
		t.Errorf("turn_cost_usd = %v, want 0.0031", got.TurnCostUSD)
	}
}

// TestStreamEvent_PlainStageStartedHasNoNewKeys is the omitempty guarantee: an
// event that carries no structured payload must serialize exactly the keys a
// pre-E1 subscriber already saw.
func TestStreamEvent_PlainStageStartedHasNoNewKeys(t *testing.T) {
	line, _ := writePipelineEvent(t, pipeline.PipelineEvent{
		Type:      pipeline.EventStageStarted,
		Timestamp: time.Now(),
		RunID:     "run1",
		NodeID:    "Build",
		Message:   "starting",
	})
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(line), &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	allowed := map[string]bool{"ts": true, "source": true, "type": true, "run_id": true, "node_id": true, "message": true}
	for k := range raw {
		if !allowed[k] {
			t.Errorf("stage_started serialized unexpected key %q (omitempty guarantee broken): %s", k, line)
		}
	}
}

// TestStreamEvent_PlainAgentEventHasNoNewKeys is the same guarantee on the agent
// path: a tool_call_end with no usage must not sprout token fields.
func TestStreamEvent_PlainAgentEventHasNoNewKeys(t *testing.T) {
	var buf bytes.Buffer
	NewNDJSONWriter(&buf).AgentHandler().HandleEvent(agent.Event{
		Type:       agent.EventToolCallEnd,
		Timestamp:  time.Now(),
		NodeID:     "Build",
		ToolName:   "Read",
		ToolOutput: "contents",
	})
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(strings.TrimSuffix(buf.String(), "\n")), &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	allowed := map[string]bool{"ts": true, "source": true, "type": true, "node_id": true, "tool_name": true, "content": true}
	for k := range raw {
		if !allowed[k] {
			t.Errorf("tool_call_end serialized unexpected key %q: %s", k, buf.String())
		}
	}
}
