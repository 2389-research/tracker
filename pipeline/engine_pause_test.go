// ABOUTME: Tests the recoverable PAUSE terminal (#487): a PauseError from a handler
// ABOUTME: yields a paused_billing terminal that is resumable (node not completed).
package pipeline

import (
	"context"
	"errors"
	"testing"
)

func TestEngine_PauseError_YieldsResumablePausedTerminal(t *testing.T) {
	g := NewGraph("pause_test")
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond", Label: "Start"})
	g.AddNode(&Node{ID: "work", Shape: "box", Label: "Work"})
	g.AddNode(&Node{ID: "end", Shape: "Msquare", Label: "End"})
	g.AddEdge(&Edge{From: "s", To: "work"})
	g.AddEdge(&Edge{From: "work", To: "end"})

	reg := newTestRegistry()
	reg.Register(&testHandler{
		name: "codergen",
		executeFn: func(_ context.Context, node *Node, _ *PipelineContext) (Outcome, error) {
			if node.ID == "work" {
				return Outcome{}, NewPauseError(OutcomePausedBilling,
					errors.New("node \"work\": anthropic: credit balance is too low"))
			}
			return Outcome{Status: OutcomeSuccess}, nil
		},
	})

	var pausedEvents int
	handler := PipelineEventHandlerFunc(func(evt PipelineEvent) {
		if evt.Type == EventBillingPaused {
			pausedEvents++
			if evt.TerminalStatus != string(OutcomePausedBilling) {
				t.Errorf("paused event TerminalStatus = %q", evt.TerminalStatus)
			}
		}
	})

	engine := NewEngine(g, reg, WithPipelineEventHandler(handler))
	result, err := engine.Run(context.Background())

	if result == nil {
		t.Fatalf("expected a result, got nil (err=%v)", err)
	}
	if result.Status != OutcomePausedBilling {
		t.Errorf("status = %q, want paused_billing", result.Status)
	}
	if result.Status.IsSuccess() {
		t.Error("paused_billing must not be a success status")
	}
	// Resumable: the paused node must NOT be marked completed, so a resume re-runs it.
	for _, n := range result.CompletedNodes {
		if n == "work" {
			t.Error("paused node must not be in CompletedNodes (else resume skips it)")
		}
	}
	// The billing error is surfaced (for the CLI/summary to classify).
	if err == nil {
		t.Error("expected the billing error to be returned alongside the paused result")
	}
	if pausedEvents != 1 {
		t.Errorf("expected exactly one billing_paused terminal event, got %d", pausedEvents)
	}
}

func TestPauseError_UnwrapAndMessage(t *testing.T) {
	inner := errors.New("credit balance is too low")
	pe := NewPauseError(OutcomePausedBilling, inner)
	if !errors.Is(pe, inner) {
		t.Error("PauseError must unwrap to its inner error")
	}
	if pe.Error() != inner.Error() {
		t.Errorf("Error() = %q, want %q", pe.Error(), inner.Error())
	}
	if got, ok := asPauseError(pe); !ok || got != pe {
		t.Error("asPauseError should extract the PauseError")
	}
	if _, ok := asPauseError(errors.New("plain")); ok {
		t.Error("asPauseError should not match a plain error")
	}
}
