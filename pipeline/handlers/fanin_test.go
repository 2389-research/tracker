// ABOUTME: Tests for the fan-in handler that merges parallel branch results back into the parent context.
// ABOUTME: Validates JSON parsing, context merging, and success/failure determination from branch outcomes.
package handlers

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/2389-research/tracker/pipeline"
)

func TestFanInHandlerName(t *testing.T) {
	h := NewFanInHandler()
	if h.Name() != "parallel.fan_in" {
		t.Errorf("expected 'parallel.fan_in', got %q", h.Name())
	}
}

func TestFanInHandlerMissingResults(t *testing.T) {
	h := NewFanInHandler()
	node := &pipeline.Node{ID: "fan_in_node", Handler: "parallel.fan_in"}
	pctx := pipeline.NewPipelineContext()

	_, err := h.Execute(context.Background(), node, pctx)
	if err == nil {
		t.Fatal("expected error when parallel.results is missing from context")
	}
}

func TestFanInHandlerAllSuccess(t *testing.T) {
	h := NewFanInHandler()
	node := &pipeline.Node{ID: "fan_in_node", Handler: "parallel.fan_in"}
	pctx := pipeline.NewPipelineContext()

	results := []ParallelResult{
		{
			NodeID:         "branch_a",
			Status:         pipeline.OutcomeSuccess,
			ContextUpdates: map[string]string{"key_a": "val_a", "shared": "from_a"},
		},
		{
			NodeID:         "branch_b",
			Status:         pipeline.OutcomeSuccess,
			ContextUpdates: map[string]string{"key_b": "val_b", "shared": "from_b"},
		},
	}
	data, _ := json.Marshal(results)
	pctx.Set("parallel.results", string(data))

	outcome, err := h.Execute(context.Background(), node, pctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Status != pipeline.OutcomeSuccess {
		t.Errorf("expected 'success', got %q", outcome.Status)
	}

	// Both branches' context updates should be merged.
	if outcome.ContextUpdates["key_a"] != "val_a" {
		t.Errorf("expected key_a=val_a, got %q", outcome.ContextUpdates["key_a"])
	}
	if outcome.ContextUpdates["key_b"] != "val_b" {
		t.Errorf("expected key_b=val_b, got %q", outcome.ContextUpdates["key_b"])
	}
	// Later branch overwrites earlier for same key.
	if outcome.ContextUpdates["shared"] != "from_b" {
		t.Errorf("expected shared=from_b (later branch wins), got %q", outcome.ContextUpdates["shared"])
	}
}

func TestFanInHandlerAllFail(t *testing.T) {
	h := NewFanInHandler()
	node := &pipeline.Node{ID: "fan_in_node", Handler: "parallel.fan_in"}
	pctx := pipeline.NewPipelineContext()

	results := []ParallelResult{
		{NodeID: "branch_a", Status: pipeline.OutcomeFail, Error: "oops a"},
		{NodeID: "branch_b", Status: pipeline.OutcomeFail, Error: "oops b"},
	}
	data, _ := json.Marshal(results)
	pctx.Set("parallel.results", string(data))

	outcome, err := h.Execute(context.Background(), node, pctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Status != pipeline.OutcomeFail {
		t.Errorf("expected 'fail', got %q", outcome.Status)
	}
	if len(outcome.ContextUpdates) != 0 {
		t.Errorf("expected no context updates from all-fail, got %v", outcome.ContextUpdates)
	}
}

func TestFanInHandlerPartialFailure(t *testing.T) {
	h := NewFanInHandler()
	node := &pipeline.Node{ID: "fan_in_node", Handler: "parallel.fan_in"}
	pctx := pipeline.NewPipelineContext()

	results := []ParallelResult{
		{
			NodeID:         "branch_ok",
			Status:         pipeline.OutcomeSuccess,
			ContextUpdates: map[string]string{"from_ok": "yes"},
		},
		{
			NodeID:         "branch_fail",
			Status:         pipeline.OutcomeFail,
			Error:          "branch failed",
			ContextUpdates: map[string]string{"from_fail": "should_not_appear"},
		},
	}
	data, _ := json.Marshal(results)
	pctx.Set("parallel.results", string(data))

	outcome, err := h.Execute(context.Background(), node, pctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Status != pipeline.OutcomeSuccess {
		t.Errorf("expected 'success' (partial failure), got %q", outcome.Status)
	}
	// Only successful branch context should be merged.
	if outcome.ContextUpdates["from_ok"] != "yes" {
		t.Errorf("expected from_ok=yes, got %q", outcome.ContextUpdates["from_ok"])
	}
	if _, exists := outcome.ContextUpdates["from_fail"]; exists {
		t.Error("failed branch context should not be merged")
	}
}

func TestFanInHandlerEmptyResults(t *testing.T) {
	h := NewFanInHandler()
	node := &pipeline.Node{ID: "fan_in_node", Handler: "parallel.fan_in"}
	pctx := pipeline.NewPipelineContext()

	data, _ := json.Marshal([]ParallelResult{})
	pctx.Set("parallel.results", string(data))

	outcome, err := h.Execute(context.Background(), node, pctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Status != pipeline.OutcomeFail {
		t.Errorf("expected 'fail' for empty results, got %q", outcome.Status)
	}
}

func TestFanInHandlerInvalidJSON(t *testing.T) {
	h := NewFanInHandler()
	node := &pipeline.Node{ID: "fan_in_node", Handler: "parallel.fan_in"}
	pctx := pipeline.NewPipelineContext()

	pctx.Set("parallel.results", "not valid json{{{")

	_, err := h.Execute(context.Background(), node, pctx)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}
