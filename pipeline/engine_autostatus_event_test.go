// ABOUTME: Tests for EventAutoStatusMissing emission (#346) — the engine must
// ABOUTME: surface a handler-reported missing-STATUS anomaly as a typed audit event.
package pipeline

import (
	"context"
	"testing"
)

// TestEngineEmitsAutoStatusMissingEvent verifies the engine emits
// EventAutoStatusMissing when a handler outcome carries a MissingStatus
// detail — same pattern as EventToolMarkerMissing (#210): the handler decides
// the status, the engine provides the audit-trail companion.
func TestEngineEmitsAutoStatusMissingEvent(t *testing.T) {
	g := NewGraph("auto-status-missing")
	g.AddNode(&Node{ID: "start", Shape: "Mdiamond"})
	g.AddNode(&Node{ID: "verify", Shape: "box", Attrs: map[string]string{"goal_gate": "true"}})
	g.AddNode(&Node{ID: "done", Shape: "Msquare"})
	g.AddEdge(&Edge{From: "start", To: "verify"})
	g.AddEdge(&Edge{From: "verify", To: "done", Condition: "ctx.outcome = success"})
	g.AddEdge(&Edge{From: "verify", To: "done", Condition: "ctx.outcome = fail"})

	reg := newTestRegistry()
	reg.Register(&testHandler{
		name: "codergen",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			if node.ID == "verify" {
				return Outcome{
					Status:        OutcomeFail,
					MissingStatus: &AutoStatusDetail{ResponseTail: "no marker here", FailClosed: true},
				}, nil
			}
			return Outcome{Status: OutcomeSuccess}, nil
		},
	})

	var got *PipelineEvent
	handler := PipelineEventHandlerFunc(func(evt PipelineEvent) {
		if evt.Type == EventAutoStatusMissing {
			e := evt
			got = &e
		}
	})

	engine := NewEngine(g, reg, WithPipelineEventHandler(handler))
	_, _ = engine.Run(context.Background())

	if got == nil {
		t.Fatal("expected an EventAutoStatusMissing event, got none")
	}
	if got.NodeID != "verify" {
		t.Errorf("event NodeID = %q, want %q", got.NodeID, "verify")
	}
	if got.AutoStatus == nil || got.AutoStatus.ResponseTail != "no marker here" {
		t.Errorf("event AutoStatus payload = %+v, want ResponseTail %q", got.AutoStatus, "no marker here")
	}
	if !got.AutoStatus.FailClosed {
		t.Error("event AutoStatus.FailClosed = false, want true")
	}
}
