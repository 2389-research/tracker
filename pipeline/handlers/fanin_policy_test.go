// ABOUTME: Tests for the configurable fan-in aggregation policy (issue #313).
// ABOUTME: Covers any/all/quorum semantics in both the parallel and fan-in handlers.
package handlers

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/2389-research/tracker/pipeline"
)

// mixedStubHandler returns fail for node IDs in failIDs, success otherwise.
func mixedStubHandler(name string, failIDs ...string) *stubHandler {
	failSet := make(map[string]bool, len(failIDs))
	for _, id := range failIDs {
		failSet[id] = true
	}
	return &stubHandler{
		name: name,
		execFunc: func(_ context.Context, node *pipeline.Node, _ *pipeline.PipelineContext) (pipeline.Outcome, error) {
			if failSet[node.ID] {
				return pipeline.Outcome{Status: string(pipeline.OutcomeFail)}, nil
			}
			return pipeline.Outcome{Status: string(pipeline.OutcomeSuccess)}, nil
		},
	}
}

// runParallelWithPolicy executes a parallel node over the given branches with
// fan_in_policy attrs applied, returning the outcome.
func runParallelWithPolicy(t *testing.T, branches []string, policyAttrs map[string]string, stub *stubHandler, eh pipeline.PipelineEventHandler) (pipeline.Outcome, error) {
	t.Helper()
	g := buildTestGraph(branches, stub.name)
	for k, v := range policyAttrs {
		g.Nodes["parallel_node"].Attrs[k] = v
	}
	registry := pipeline.NewHandlerRegistry()
	registry.Register(stub)
	h := NewParallelHandler(g, registry, eh)
	return h.Execute(context.Background(), g.Nodes["parallel_node"], pipeline.NewPipelineContext())
}

// --- parallel handler (aggregateStatus path) ---

// Default policy (unset) stays success-if-any — back-compat pin for #313.
func TestParallelHandlerDefaultPolicyIsAny(t *testing.T) {
	stub := mixedStubHandler("stub_pin_any", "branch_fail")
	outcome, err := runParallelWithPolicy(t, []string{"branch_ok", "branch_fail"}, nil, stub, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Status != string(pipeline.OutcomeSuccess) {
		t.Errorf("default policy must remain success-if-any, got %q", outcome.Status)
	}
}

func TestParallelHandlerPolicyAllFailsOnPartialFailure(t *testing.T) {
	stub := mixedStubHandler("stub_all_partial", "branch_fail")
	outcome, err := runParallelWithPolicy(t, []string{"branch_ok", "branch_fail"},
		map[string]string{"fan_in_policy": "all"}, stub, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Status != string(pipeline.OutcomeFail) {
		t.Errorf("policy=all with a failed branch should aggregate fail, got %q", outcome.Status)
	}
}

func TestParallelHandlerPolicyAllSucceedsWhenAllSucceed(t *testing.T) {
	stub := mixedStubHandler("stub_all_ok")
	outcome, err := runParallelWithPolicy(t, []string{"branch_a", "branch_b"},
		map[string]string{"fan_in_policy": "all"}, stub, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Status != string(pipeline.OutcomeSuccess) {
		t.Errorf("policy=all with all branches succeeding should be success, got %q", outcome.Status)
	}
}

func TestParallelHandlerPolicyQuorum(t *testing.T) {
	cases := []struct {
		name   string
		quorum string
		want   string
	}{
		{"met", "2", string(pipeline.OutcomeSuccess)},   // 2/3 succeed, quorum 2
		{"not_met", "3", string(pipeline.OutcomeFail)},  // 2/3 succeed, quorum 3
		{"unsatisfiable", "4", string(pipeline.OutcomeFail)}, // quorum > branch count
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stub := mixedStubHandler("stub_quorum_"+tc.name, "branch_fail")
			outcome, err := runParallelWithPolicy(t, []string{"branch_a", "branch_b", "branch_fail"},
				map[string]string{"fan_in_policy": "quorum", "quorum": tc.quorum}, stub, nil)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if outcome.Status != tc.want {
				t.Errorf("quorum=%s with 2/3 succeeding: got %q, want %q", tc.quorum, outcome.Status, tc.want)
			}
		})
	}
}

// Invalid policy config errors before any branch executes (fail fast).
func TestParallelHandlerPolicyInvalidConfig(t *testing.T) {
	cases := []struct {
		name  string
		attrs map[string]string
	}{
		{"unknown_policy", map[string]string{"fan_in_policy": "bogus"}},
		{"quorum_missing_n", map[string]string{"fan_in_policy": "quorum"}},
		{"quorum_non_positive", map[string]string{"fan_in_policy": "quorum", "quorum": "0"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stub := mixedStubHandler("stub_invalid_" + tc.name)
			_, err := runParallelWithPolicy(t, []string{"branch_a", "branch_b"}, tc.attrs, stub, nil)
			if err == nil {
				t.Fatal("expected error for invalid policy config")
			}
			if stub.called.Load() != 0 {
				t.Errorf("branches must not execute on invalid policy config, got %d calls", stub.called.Load())
			}
		})
	}
}

// A policy-caused failure must be visible in the EventParallelCompleted
// message (policy name + failed branch IDs) so the TUI / diagnose see it.
func TestParallelHandlerPolicyFailureSurfacesInEvent(t *testing.T) {
	stub := mixedStubHandler("stub_event_policy", "branch_fail")
	eh := &collectingEventHandler{}
	outcome, err := runParallelWithPolicy(t, []string{"branch_ok", "branch_fail"},
		map[string]string{"fan_in_policy": "all"}, stub, eh)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Status != string(pipeline.OutcomeFail) {
		t.Fatalf("expected fail, got %q", outcome.Status)
	}
	var completed *pipeline.PipelineEvent
	for i := range eh.events {
		if eh.events[i].Type == pipeline.EventParallelCompleted {
			completed = &eh.events[i]
		}
	}
	if completed == nil {
		t.Fatal("no EventParallelCompleted emitted")
	}
	if !strings.Contains(completed.Message, "all") || !strings.Contains(completed.Message, "branch_fail") {
		t.Errorf("EventParallelCompleted message should name the policy and failed branches, got %q", completed.Message)
	}
}

// --- fan-in handler (mergeSuccessfulBranches path) ---

func runFanInWithPolicy(t *testing.T, results []ParallelResult, policyAttrs map[string]string) (pipeline.Outcome, error) {
	t.Helper()
	node := &pipeline.Node{ID: "fan_in_node", Handler: "parallel.fan_in", Attrs: policyAttrs}
	pctx := pipeline.NewPipelineContext()
	data, err := json.Marshal(results)
	if err != nil {
		t.Fatalf("marshal results: %v", err)
	}
	pctx.Set("parallel.results", string(data))
	return NewFanInHandler().Execute(context.Background(), node, pctx)
}

func TestFanInHandlerPolicyAllFailsOnPartialFailure(t *testing.T) {
	results := []ParallelResult{
		{NodeID: "branch_ok", Status: string(pipeline.OutcomeSuccess), ContextUpdates: map[string]string{"from_ok": "yes"}},
		{NodeID: "branch_fail", Status: string(pipeline.OutcomeFail), Error: "boom"},
	}
	outcome, err := runFanInWithPolicy(t, results, map[string]string{"fan_in_policy": "all"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Status != string(pipeline.OutcomeFail) {
		t.Errorf("policy=all with a failed branch should be fail, got %q", outcome.Status)
	}
	// Successful-branch context still merges so downstream escalation gates
	// can reference partial output.
	if outcome.ContextUpdates["from_ok"] != "yes" {
		t.Errorf("successful branch context should still merge, got %v", outcome.ContextUpdates)
	}
}

func TestFanInHandlerPolicyQuorum(t *testing.T) {
	results := []ParallelResult{
		{NodeID: "a", Status: string(pipeline.OutcomeSuccess)},
		{NodeID: "b", Status: string(pipeline.OutcomeSuccess)},
		{NodeID: "c", Status: string(pipeline.OutcomeFail)},
	}
	met, err := runFanInWithPolicy(t, results, map[string]string{"fan_in_policy": "quorum", "quorum": "2"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if met.Status != string(pipeline.OutcomeSuccess) {
		t.Errorf("quorum=2 with 2/3 succeeding should be success, got %q", met.Status)
	}
	notMet, err := runFanInWithPolicy(t, results, map[string]string{"fan_in_policy": "quorum", "quorum": "3"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if notMet.Status != string(pipeline.OutcomeFail) {
		t.Errorf("quorum=3 with 2/3 succeeding should be fail, got %q", notMet.Status)
	}
}

func TestFanInHandlerPolicyInvalidConfig(t *testing.T) {
	results := []ParallelResult{{NodeID: "a", Status: string(pipeline.OutcomeSuccess)}}
	for name, attrs := range map[string]map[string]string{
		"unknown_policy":   {"fan_in_policy": "bogus"},
		"quorum_missing_n": {"fan_in_policy": "quorum"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := runFanInWithPolicy(t, results, attrs); err == nil {
				t.Fatal("expected error for invalid policy config")
			}
		})
	}
}

// Policy-caused fan-in failure records a human-readable detail in context so
// the audit trail / diagnose can explain why a partially-successful parallel
// block routed to fail.
func TestFanInHandlerPolicyFailureDetailInContext(t *testing.T) {
	results := []ParallelResult{
		{NodeID: "branch_ok", Status: string(pipeline.OutcomeSuccess)},
		{NodeID: "branch_fail", Status: string(pipeline.OutcomeFail), Error: "boom"},
	}
	outcome, err := runFanInWithPolicy(t, results, map[string]string{"fan_in_policy": "all"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	detail := outcome.ContextUpdates["fan_in.policy_detail"]
	if !strings.Contains(detail, "all") || !strings.Contains(detail, "branch_fail") {
		t.Errorf("fan_in.policy_detail should name the policy and failed branches, got %q", detail)
	}
}

// Default (unset) fan-in policy stays success-if-any — back-compat pin for #313.
func TestFanInHandlerDefaultPolicyIsAny(t *testing.T) {
	results := []ParallelResult{
		{NodeID: "branch_ok", Status: string(pipeline.OutcomeSuccess)},
		{NodeID: "branch_fail", Status: string(pipeline.OutcomeFail)},
	}
	outcome, err := runFanInWithPolicy(t, results, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Status != string(pipeline.OutcomeSuccess) {
		t.Errorf("default policy must remain success-if-any, got %q", outcome.Status)
	}
}
