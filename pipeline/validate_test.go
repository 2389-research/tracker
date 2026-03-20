// ABOUTME: Tests for pipeline graph validation rules.
// ABOUTME: Validates start/exit node requirements, cycle detection, shape recognition, and reachability.
package pipeline

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateSimpleGraph(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "simple.dot"))
	if err != nil {
		t.Fatalf("failed to read DOT file: %v", err)
	}
	g, err := ParseDOT(string(data))
	if err != nil {
		t.Fatalf("ParseDOT failed: %v", err)
	}
	if err := Validate(g); err != nil {
		t.Errorf("expected simple graph to be valid, got: %v", err)
	}
}

func TestValidateDiamondGraph(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "diamond.dot"))
	if err != nil {
		t.Fatalf("failed to read DOT file: %v", err)
	}
	g, err := ParseDOT(string(data))
	if err != nil {
		t.Fatalf("ParseDOT failed: %v", err)
	}
	if err := Validate(g); err != nil {
		t.Errorf("expected diamond graph to be valid, got: %v", err)
	}
}

func TestValidateNoStartNode(t *testing.T) {
	g := NewGraph("no-start")
	g.AddNode(&Node{ID: "a", Shape: "box"})
	g.AddNode(&Node{ID: "b", Shape: "Msquare"})
	g.AddEdge(&Edge{From: "a", To: "b"})

	err := Validate(g)
	if err == nil {
		t.Fatal("expected error for missing start node")
	}
	if !strings.Contains(err.Error(), "start") {
		t.Errorf("error should mention 'start', got: %v", err)
	}
}

func TestValidateNoExitNode(t *testing.T) {
	g := NewGraph("no-exit")
	g.AddNode(&Node{ID: "a", Shape: "Mdiamond"})
	g.AddNode(&Node{ID: "b", Shape: "box"})
	g.AddEdge(&Edge{From: "a", To: "b"})

	err := Validate(g)
	if err == nil {
		t.Fatal("expected error for missing exit node")
	}
	if !strings.Contains(err.Error(), "exit") {
		t.Errorf("error should mention 'exit', got: %v", err)
	}
}

func TestValidateMultipleStartNodes(t *testing.T) {
	g := NewGraph("multi-start")
	g.AddNode(&Node{ID: "s1", Shape: "Mdiamond"})
	g.AddNode(&Node{ID: "s2", Shape: "Mdiamond"})
	g.AddNode(&Node{ID: "end", Shape: "Msquare"})
	g.AddEdge(&Edge{From: "s1", To: "end"})
	g.AddEdge(&Edge{From: "s2", To: "end"})

	err := Validate(g)
	if err == nil {
		t.Fatal("expected error for multiple start nodes")
	}
	if !strings.Contains(err.Error(), "start") {
		t.Errorf("error should mention 'start', got: %v", err)
	}
}

func TestValidateCycleDetection(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "cycle.dot"))
	if err != nil {
		t.Fatalf("failed to read DOT file: %v", err)
	}
	g, err := ParseDOT(string(data))
	if err != nil {
		t.Fatalf("ParseDOT failed: %v", err)
	}

	err = Validate(g)
	if err == nil {
		t.Fatal("expected error for graph with cycle")
	}
	if !strings.Contains(err.Error(), "cycle") {
		t.Errorf("error should mention 'cycle', got: %v", err)
	}
}

func TestValidateUnrecognizedShape(t *testing.T) {
	g := NewGraph("bad-shape")
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond"})
	g.AddNode(&Node{ID: "x", Shape: "trapezium"})
	g.AddNode(&Node{ID: "e", Shape: "Msquare"})
	g.AddEdge(&Edge{From: "s", To: "x"})
	g.AddEdge(&Edge{From: "x", To: "e"})

	err := Validate(g)
	if err == nil {
		t.Fatal("expected error for unrecognized shape")
	}
	if !strings.Contains(err.Error(), "trapezium") {
		t.Errorf("error should mention 'trapezium', got: %v", err)
	}
}

func TestValidateUnreachableNode(t *testing.T) {
	g := NewGraph("unreachable")
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond"})
	g.AddNode(&Node{ID: "a", Shape: "box"})
	g.AddNode(&Node{ID: "orphan", Shape: "box"})
	g.AddNode(&Node{ID: "e", Shape: "Msquare"})
	g.AddEdge(&Edge{From: "s", To: "a"})
	g.AddEdge(&Edge{From: "a", To: "e"})

	err := Validate(g)
	if err == nil {
		t.Fatal("expected error for unreachable node")
	}
	if !strings.Contains(err.Error(), "unreachable") {
		t.Errorf("error should mention 'unreachable', got: %v", err)
	}
}

func TestValidateNilGraph(t *testing.T) {
	err := Validate(nil)
	if err == nil {
		t.Fatal("expected error for nil graph")
	}
}

func TestValidateEdgeToUndeclaredNode(t *testing.T) {
	g := NewGraph("bad-edge")
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond"})
	g.AddNode(&Node{ID: "e", Shape: "Msquare"})
	g.AddEdge(&Edge{From: "s", To: "ghost"})
	g.AddEdge(&Edge{From: "ghost", To: "e"})

	err := Validate(g)
	if err == nil {
		t.Fatal("expected error for edge referencing undeclared node")
	}
	if !strings.Contains(err.Error(), "ghost") {
		t.Errorf("error should mention 'ghost', got: %v", err)
	}
}

func TestValidateConditionalCycleAllowed(t *testing.T) {
	g := NewGraph("retry-loop")
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond"})
	g.AddNode(&Node{ID: "work", Shape: "box"})
	g.AddNode(&Node{ID: "check", Shape: "diamond"})
	g.AddNode(&Node{ID: "e", Shape: "Msquare"})
	g.AddEdge(&Edge{From: "s", To: "work"})
	g.AddEdge(&Edge{From: "work", To: "check"})
	g.AddEdge(&Edge{From: "check", To: "e", Condition: "outcome=success"})
	g.AddEdge(&Edge{From: "check", To: "work", Condition: "outcome=fail"})

	if err := Validate(g); err != nil {
		t.Errorf("conditional retry loop should be valid, got: %v", err)
	}
}

func TestValidateEmptyGraph(t *testing.T) {
	g := NewGraph("empty")
	err := Validate(g)
	if err == nil {
		t.Fatal("expected error for empty graph")
	}
}

func TestValidateDuplicateEdges(t *testing.T) {
	g := NewGraph("dup-edges")
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond"})
	g.AddNode(&Node{ID: "a", Shape: "box"})
	g.AddNode(&Node{ID: "e", Shape: "Msquare"})
	g.AddEdge(&Edge{From: "s", To: "a"})
	g.AddEdge(&Edge{From: "s", To: "a"})
	g.AddEdge(&Edge{From: "a", To: "e"})

	err := Validate(g)
	if err == nil {
		t.Fatal("expected error for duplicate edges")
	}
	if !strings.Contains(err.Error(), "duplicate") {
		t.Errorf("error should mention 'duplicate', got: %v", err)
	}
}

func TestValidateConditionalMissingFailEdge(t *testing.T) {
	g := NewGraph("no-fail-edge")
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond"})
	g.AddNode(&Node{ID: "check", Shape: "diamond"})
	g.AddNode(&Node{ID: "e", Shape: "Msquare"})
	g.AddEdge(&Edge{From: "s", To: "check"})
	g.AddEdge(&Edge{From: "check", To: "e", Condition: "outcome=success", Label: "success"})

	ve := ValidateAll(g)
	if ve == nil {
		t.Fatal("expected warning for conditional node missing fail edge")
	}
	if !ve.hasWarnings() {
		t.Fatal("expected warnings to be present")
	}
	found := false
	for _, w := range ve.Warnings {
		if strings.Contains(w, "check") && strings.Contains(w, "fail") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected warning about missing fail edge on 'check', got warnings: %v", ve.Warnings)
	}
}

func TestValidateConditionalWithFailEdge(t *testing.T) {
	g := NewGraph("has-fail-edge")
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond"})
	g.AddNode(&Node{ID: "check", Shape: "diamond"})
	g.AddNode(&Node{ID: "a", Shape: "box"})
	g.AddNode(&Node{ID: "e", Shape: "Msquare"})
	g.AddEdge(&Edge{From: "s", To: "check"})
	g.AddEdge(&Edge{From: "check", To: "e", Condition: "outcome=success", Label: "success"})
	g.AddEdge(&Edge{From: "check", To: "a", Condition: "outcome=fail", Label: "fail"})
	g.AddEdge(&Edge{From: "a", To: "e"})

	err := Validate(g)
	if err != nil {
		ve, ok := err.(*ValidationError)
		if ok && ve.hasErrors() {
			t.Errorf("unexpected errors: %v", ve.Errors)
		}
		// Warnings about missing fail edge should NOT be present
		if ok {
			for _, w := range ve.Warnings {
				if strings.Contains(w, "check") && strings.Contains(w, "fail") {
					t.Errorf("should not warn about missing fail edge on 'check', got: %s", w)
				}
			}
		}
	}
}

func TestValidateEdgeLabelConsistency(t *testing.T) {
	g := NewGraph("mixed-labels")
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond"})
	g.AddNode(&Node{ID: "check", Shape: "diamond"})
	g.AddNode(&Node{ID: "a", Shape: "box"})
	g.AddNode(&Node{ID: "b", Shape: "box"})
	g.AddNode(&Node{ID: "e", Shape: "Msquare"})
	g.AddEdge(&Edge{From: "s", To: "check"})
	g.AddEdge(&Edge{From: "check", To: "a", Label: "yes"})
	g.AddEdge(&Edge{From: "check", To: "b"}) // no label
	g.AddEdge(&Edge{From: "a", To: "e"})
	g.AddEdge(&Edge{From: "b", To: "e"})

	ve := ValidateAll(g)
	if ve == nil {
		t.Fatal("expected warning for inconsistent edge labels")
	}
	found := false
	for _, w := range ve.Warnings {
		if strings.Contains(w, "check") && strings.Contains(w, "label") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected warning about inconsistent labels on 'check', got warnings: %v", ve.Warnings)
	}
}

func TestAutoFixMissingFailEdge(t *testing.T) {
	g := NewGraph("autofix-fail")
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond"})
	g.AddNode(&Node{ID: "check", Shape: "diamond"})
	g.AddNode(&Node{ID: "e", Shape: "Msquare"})
	g.AddEdge(&Edge{From: "s", To: "check"})
	g.AddEdge(&Edge{From: "check", To: "e", Condition: "outcome=success", Label: "success"})

	fixes := AutoFix(g)
	if len(fixes) == 0 {
		t.Fatal("expected AutoFix to apply at least one fix")
	}
	found := false
	for _, f := range fixes {
		if strings.Contains(f, "check") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected fix description to mention 'check', got: %v", fixes)
	}

	// Verify the fail edge was actually added
	outgoing := g.OutgoingEdges("check")
	hasFail := false
	for _, e := range outgoing {
		if strings.Contains(e.Condition, "fail") {
			hasFail = true
			break
		}
	}
	if !hasFail {
		t.Error("expected AutoFix to add a fail edge to 'check'")
	}
}

func TestAutoFixNoChanges(t *testing.T) {
	g := NewGraph("no-fix-needed")
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond"})
	g.AddNode(&Node{ID: "check", Shape: "diamond"})
	g.AddNode(&Node{ID: "a", Shape: "box"})
	g.AddNode(&Node{ID: "e", Shape: "Msquare"})
	g.AddEdge(&Edge{From: "s", To: "check"})
	g.AddEdge(&Edge{From: "check", To: "e", Condition: "outcome=success", Label: "success"})
	g.AddEdge(&Edge{From: "check", To: "a", Condition: "outcome=fail", Label: "fail"})
	g.AddEdge(&Edge{From: "a", To: "e"})

	fixes := AutoFix(g)
	if len(fixes) != 0 {
		t.Errorf("expected no fixes on correct graph, got: %v", fixes)
	}
}

func TestValidateDippinValidatedSkipsStructural(t *testing.T) {
	// A graph with DippinValidated=true should skip structural checks
	// (start/exit, edge endpoints, reachability, cycles, exit outgoing edges).
	// This graph is intentionally missing an exit node, which would normally
	// fail validation — but passes because Dippin already checked it.
	g := NewGraph("dippin-validated")
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond"})
	g.AddNode(&Node{ID: "work", Shape: "box"})
	g.AddNode(&Node{ID: "e", Shape: "Msquare"})
	g.AddEdge(&Edge{From: "s", To: "work"})
	g.AddEdge(&Edge{From: "work", To: "e"})
	g.DippinValidated = true

	if err := Validate(g); err != nil {
		t.Errorf("expected DippinValidated graph to pass, got: %v", err)
	}
}

func TestValidationErrorWithWarnings(t *testing.T) {
	ve := &ValidationError{
		Errors:   []string{"bad node"},
		Warnings: []string{"missing fail edge"},
	}
	msg := ve.Error()
	if !strings.Contains(msg, "errors: bad node") {
		t.Errorf("expected errors section, got: %s", msg)
	}
	if !strings.Contains(msg, "warnings: missing fail edge") {
		t.Errorf("expected warnings section, got: %s", msg)
	}
	if !strings.Contains(msg, " | ") {
		t.Errorf("expected ' | ' separator between errors and warnings, got: %s", msg)
	}
}
