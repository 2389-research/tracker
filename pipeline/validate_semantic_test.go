// ABOUTME: Tests for semantic validation of pipeline graphs.
// ABOUTME: Covers handler registration, condition syntax, and node attribute type checks.
package pipeline

import (
	"context"
	"strings"
	"testing"
)

// semanticStubHandler is a minimal Handler implementation for testing registry lookups.
type semanticStubHandler struct {
	name string
}

func (s *semanticStubHandler) Name() string { return s.name }
func (s *semanticStubHandler) Execute(_ context.Context, _ *Node, _ *PipelineContext) (Outcome, error) {
	return Outcome{Status: OutcomeSuccess}, nil
}

func TestHandlerRegistry_Has(t *testing.T) {
	reg := NewHandlerRegistry()
	reg.Register(&semanticStubHandler{name: "codergen"})

	if !reg.Has("codergen") {
		t.Error("expected Has(\"codergen\") to return true")
	}
	if reg.Has("nonexistent") {
		t.Error("expected Has(\"nonexistent\") to return false")
	}
}

func TestValidateSemantic_NilGraph(t *testing.T) {
	reg := NewHandlerRegistry()
	err := ValidateSemantic(nil, reg)
	if err == nil {
		t.Fatal("expected error for nil graph")
	}
	if !strings.Contains(err.Error(), "nil") {
		t.Errorf("error should mention 'nil', got: %v", err)
	}
}

func TestValidateSemantic_UnregisteredHandler(t *testing.T) {
	g := NewGraph("test")
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond"})
	g.AddNode(&Node{ID: "work", Shape: "box"})
	g.AddNode(&Node{ID: "e", Shape: "Msquare"})
	g.AddEdge(&Edge{From: "s", To: "work"})
	g.AddEdge(&Edge{From: "work", To: "e"})

	// Registry has start and exit but NOT codergen.
	reg := NewHandlerRegistry()
	reg.Register(&semanticStubHandler{name: "start"})
	reg.Register(&semanticStubHandler{name: "exit"})

	err := ValidateSemantic(g, reg)
	if err == nil {
		t.Fatal("expected error for unregistered handler")
	}
	if !strings.Contains(err.Error(), "unregistered handler") {
		t.Errorf("error should mention 'unregistered handler', got: %v", err)
	}
	if !strings.Contains(err.Error(), "codergen") {
		t.Errorf("error should mention 'codergen', got: %v", err)
	}
}

func TestValidateSemantic_StartExitSkipped(t *testing.T) {
	g := NewGraph("test")
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond"})
	g.AddNode(&Node{ID: "e", Shape: "Msquare"})
	g.AddEdge(&Edge{From: "s", To: "e"})

	// Registry has no handlers at all — start and exit should be skipped.
	reg := NewHandlerRegistry()

	err := ValidateSemantic(g, reg)
	if err != nil {
		t.Errorf("expected no error when only start/exit nodes present, got: %v", err)
	}
}

func TestValidateSemantic_InvalidConditionSyntax(t *testing.T) {
	g := NewGraph("test")
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond"})
	g.AddNode(&Node{ID: "check", Shape: "diamond"})
	g.AddNode(&Node{ID: "e", Shape: "Msquare"})
	g.AddEdge(&Edge{From: "s", To: "check"})
	g.AddEdge(&Edge{From: "check", To: "e", Condition: "== broken"})

	reg := NewHandlerRegistry()
	reg.Register(&semanticStubHandler{name: "conditional"})

	err := ValidateSemantic(g, reg)
	if err == nil {
		t.Fatal("expected error for invalid condition syntax")
	}
	if !strings.Contains(err.Error(), "condition") {
		t.Errorf("error should mention 'condition', got: %v", err)
	}
}

func TestValidateSemantic_InvalidMaxRetries(t *testing.T) {
	g := NewGraph("test")
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond"})
	g.AddNode(&Node{
		ID:    "work",
		Shape: "box",
		Attrs: map[string]string{"max_retries": "abc"},
	})
	g.AddNode(&Node{ID: "e", Shape: "Msquare"})
	g.AddEdge(&Edge{From: "s", To: "work"})
	g.AddEdge(&Edge{From: "work", To: "e"})

	reg := NewHandlerRegistry()
	reg.Register(&semanticStubHandler{name: "codergen"})

	err := ValidateSemantic(g, reg)
	if err == nil {
		t.Fatal("expected error for invalid max_retries")
	}
	if !strings.Contains(err.Error(), "max_retries") {
		t.Errorf("error should mention 'max_retries', got: %v", err)
	}
}

func TestValidateSemantic_AllValid(t *testing.T) {
	g := NewGraph("test")
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond"})
	g.AddNode(&Node{
		ID:    "work",
		Shape: "box",
		Attrs: map[string]string{"max_retries": "3"},
	})
	g.AddNode(&Node{ID: "check", Shape: "diamond"})
	g.AddNode(&Node{ID: "e", Shape: "Msquare"})
	g.AddEdge(&Edge{From: "s", To: "work"})
	g.AddEdge(&Edge{From: "work", To: "check"})
	g.AddEdge(&Edge{From: "check", To: "e", Condition: "outcome=success"})

	reg := NewHandlerRegistry()
	reg.Register(&semanticStubHandler{name: "codergen"})
	reg.Register(&semanticStubHandler{name: "conditional"})

	err := ValidateSemantic(g, reg)
	if err != nil {
		t.Errorf("expected no error for valid graph, got: %v", err)
	}
}

func TestValidateSemantic_MixedErrors(t *testing.T) {
	g := NewGraph("test")
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond"})
	g.AddNode(&Node{
		ID:    "work",
		Shape: "box",
		Attrs: map[string]string{"max_retries": "not-a-number"},
	})
	g.AddNode(&Node{ID: "e", Shape: "Msquare"})
	g.AddEdge(&Edge{From: "s", To: "work"})
	g.AddEdge(&Edge{From: "work", To: "e", Condition: "== broken"})

	// codergen not registered — so we get handler + condition + attribute errors.
	reg := NewHandlerRegistry()

	err := ValidateSemantic(g, reg)
	if err == nil {
		t.Fatal("expected multiple errors")
	}
	ve, ok := err.(*ValidationError)
	if !ok {
		t.Fatalf("expected *ValidationError, got %T", err)
	}
	if len(ve.Errors) < 3 {
		t.Errorf("expected at least 3 errors, got %d: %v", len(ve.Errors), ve.Errors)
	}
}
