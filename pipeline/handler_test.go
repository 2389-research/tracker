// ABOUTME: Tests for the pipeline handler interface and registry.
// ABOUTME: Validates handler registration, lookup, execution dispatch, and unknown handler errors.
package pipeline

import (
	"context"
	"testing"
)

type stubHandler struct {
	name    string
	outcome Outcome
	err     error
}

func (s *stubHandler) Name() string { return s.name }
func (s *stubHandler) Execute(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
	return s.outcome, s.err
}

func TestOutcomeStatuses(t *testing.T) {
	if OutcomeSuccess != "success" {
		t.Errorf("expected 'success', got %q", OutcomeSuccess)
	}
	if OutcomeRetry != "retry" {
		t.Errorf("expected 'retry', got %q", OutcomeRetry)
	}
	if OutcomeFail != "fail" {
		t.Errorf("expected 'fail', got %q", OutcomeFail)
	}
}

func TestHandlerRegistryRegisterAndGet(t *testing.T) {
	reg := NewHandlerRegistry()
	h := &stubHandler{name: "test-handler", outcome: Outcome{Status: OutcomeSuccess}}
	reg.Register(h)
	got := reg.Get("test-handler")
	if got == nil {
		t.Fatal("expected handler to be found")
	}
	if got.Name() != "test-handler" {
		t.Errorf("expected 'test-handler', got %q", got.Name())
	}
}

func TestHandlerRegistryGetMissing(t *testing.T) {
	reg := NewHandlerRegistry()
	if reg.Get("nonexistent") != nil {
		t.Error("expected nil for missing handler")
	}
}

func TestHandlerRegistryExecute(t *testing.T) {
	reg := NewHandlerRegistry()
	h := &stubHandler{name: "my-handler", outcome: Outcome{Status: OutcomeSuccess, ContextUpdates: map[string]string{"key": "value"}}}
	reg.Register(h)
	node := &Node{ID: "n1", Handler: "my-handler"}
	outcome, err := reg.Execute(context.Background(), node, NewPipelineContext())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Status != OutcomeSuccess {
		t.Errorf("expected 'success', got %q", outcome.Status)
	}
	if outcome.ContextUpdates["key"] != "value" {
		t.Errorf("expected key=value")
	}
}

func TestHandlerRegistryExecuteUnknown(t *testing.T) {
	reg := NewHandlerRegistry()
	node := &Node{ID: "n1", Handler: "unknown"}
	_, err := reg.Execute(context.Background(), node, NewPipelineContext())
	if err == nil {
		t.Fatal("expected error for unknown handler")
	}
}

func TestHandlerRegistryExecuteError(t *testing.T) {
	reg := NewHandlerRegistry()
	h := &stubHandler{name: "fail-handler", err: context.DeadlineExceeded}
	reg.Register(h)
	node := &Node{ID: "n1", Handler: "fail-handler"}
	_, err := reg.Execute(context.Background(), node, NewPipelineContext())
	if err == nil {
		t.Fatal("expected error from handler")
	}
}

func TestHandlerRegistryOverwrite(t *testing.T) {
	reg := NewHandlerRegistry()
	h1 := &stubHandler{name: "dup", outcome: Outcome{Status: OutcomeFail}}
	h2 := &stubHandler{name: "dup", outcome: Outcome{Status: OutcomeSuccess}}
	reg.Register(h1)
	reg.Register(h2)
	node := &Node{ID: "n1", Handler: "dup"}
	outcome, err := reg.Execute(context.Background(), node, NewPipelineContext())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Status != OutcomeSuccess {
		t.Error("expected second registration to overwrite")
	}
}
