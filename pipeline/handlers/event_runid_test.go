// ABOUTME: Tests that handler-originated pipeline events carry the run's ID (audit finding 3).
// ABOUTME: Guards codergen node-guard events and parallel/stage events against empty run_id.
package handlers

import (
	"context"
	"testing"

	"github.com/2389-research/tracker/llm"
	"github.com/2389-research/tracker/pipeline"
)

const testRunID = "run-finding3"

// TestCodergenNodeGuardEventsCarryRunID pins that the node-level cost/no-progress
// guard events are stamped with the active run ID from PipelineContext, matching
// the gate handler so a control plane keying by run_id sees consistent attribution.
func TestCodergenNodeGuardEventsCarryRunID(t *testing.T) {
	cases := []struct {
		name  string
		attrs map[string]string
		resp  *llm.Response
		want  pipeline.PipelineEventType
	}{
		{
			name:  "cost_limit",
			attrs: map[string]string{"prompt": "do something", "max_cost_usd": "0.01"},
			resp:  truncatedCostResponse(0.006),
			want:  pipeline.EventNodeCostLimitExceeded,
		},
		{
			name:  "no_progress",
			attrs: map[string]string{"prompt": "do something", "no_progress_turns": "2"},
			resp:  truncatedCostResponse(0.0001),
			want:  pipeline.EventNodeNoProgressDetected,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := &scriptedCompleter{responses: []*llm.Response{tc.resp, tc.resp}}
			emitter := &stubPipelineEmitter{}
			h := NewCodergenHandler(client, t.TempDir(), WithPipelineEmitter(emitter))
			node := &pipeline.Node{ID: "gen", Shape: "box", Handler: "codergen", Attrs: tc.attrs}
			pctx := pipeline.NewPipelineContext()
			pctx.SetInternal(pipeline.InternalKeyRunID, testRunID)
			if _, err := h.Execute(context.Background(), node, pctx); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			assertEventRunID(t, emitter.events, tc.want)
		})
	}
}

// TestParallelEventsCarryRunID pins that the fan-out/fan-in and per-branch stage
// events emitted by the parallel handler carry the active run ID.
func TestParallelEventsCarryRunID(t *testing.T) {
	g := buildTestGraph([]string{"branch_a", "branch_b"}, "stub_runid")
	registry := pipeline.NewHandlerRegistry()
	registry.Register(&stubHandler{
		name:    "stub_runid",
		outcome: pipeline.Outcome{Status: pipeline.OutcomeSuccess},
	})
	emitter := &collectingEventHandler{}
	h := NewParallelHandler(g, registry, emitter)
	pctx := pipeline.NewPipelineContext()
	pctx.SetInternal(pipeline.InternalKeyRunID, testRunID)

	if _, err := h.Execute(context.Background(), g.Nodes["parallel_node"], pctx); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, want := range []pipeline.PipelineEventType{
		pipeline.EventParallelStarted,
		pipeline.EventParallelCompleted,
		pipeline.EventStageStarted,
		pipeline.EventStageCompleted,
	} {
		assertEventRunID(t, emitter.events, want)
	}
}

// assertEventRunID fails unless at least one event of the given type is present
// and every event of that type carries RunID == testRunID (non-empty, correct).
func assertEventRunID(t *testing.T, events []pipeline.PipelineEvent, typ pipeline.PipelineEventType) {
	t.Helper()
	var seen bool
	for _, evt := range events {
		if evt.Type != typ {
			continue
		}
		seen = true
		if evt.RunID != testRunID {
			t.Errorf("%s: RunID = %q, want %q", typ, evt.RunID, testRunID)
		}
	}
	if !seen {
		t.Fatalf("no %s event emitted; got %d events", typ, len(events))
	}
}
