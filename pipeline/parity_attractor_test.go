package pipeline

import (
	"context"
	"testing"
)

func TestParityNodeTypeOverridesShape(t *testing.T) {
	graph, err := ParseDOT(`
digraph g {
	start [shape=Mdiamond]
	work [shape=box, type="tool", tool_command="echo hi"]
	done [shape=Msquare]
	start -> work
	work -> done
}`)
	if err != nil {
		t.Fatalf("ParseDOT failed: %v", err)
	}

	if got := graph.Nodes["work"].Handler; got != "tool" {
		t.Fatalf("handler = %q, want %q", got, "tool")
	}
}

func TestParityShapeToHandlerIncludesManagerLoop(t *testing.T) {
	handler, ok := ShapeToHandler("house")
	if !ok {
		t.Fatal("expected house shape to be recognized")
	}
	if handler != "stack.manager_loop" {
		t.Fatalf("handler = %q, want %q", handler, "stack.manager_loop")
	}
}

func TestParityValidateRejectsExitWithOutgoingEdges(t *testing.T) {
	g := NewGraph("exit-outgoing")
	g.AddNode(&Node{ID: "start", Shape: "Mdiamond"})
	g.AddNode(&Node{ID: "done", Shape: "Msquare"})
	g.AddNode(&Node{ID: "after", Shape: "box"})
	g.AddEdge(&Edge{From: "start", To: "done"})
	g.AddEdge(&Edge{From: "done", To: "after"})

	if err := Validate(g); err == nil {
		t.Fatal("expected validation error for exit node with outgoing edges")
	}
}

func TestParityConditionResolvesContextPrefix(t *testing.T) {
	ctx := NewPipelineContext()
	ctx.Set("tests_passed", "true")

	ok, err := EvaluateCondition("context.tests_passed=true", ctx)
	if err != nil {
		t.Fatalf("EvaluateCondition failed: %v", err)
	}
	if !ok {
		t.Fatal("expected context-prefixed lookup to evaluate true")
	}
}

func TestParityStylesheetResolvesShapeSelectors(t *testing.T) {
	ss, err := ParseStylesheet(`box { llm_model: claude-opus-4-6; }`)
	if err != nil {
		t.Fatalf("ParseStylesheet failed: %v", err)
	}

	node := &Node{ID: "review", Shape: "box", Attrs: map[string]string{}}
	resolved := ss.Resolve(node)
	if got := resolved["llm_model"]; got != "claude-opus-4-6" {
		t.Fatalf("llm_model = %q, want %q", got, "claude-opus-4-6")
	}
}

func TestParityStylesheetUsesCommaSeparatedClasses(t *testing.T) {
	ss, err := ParseStylesheet(`
.code { llm_model: claude-opus-4-6; }
.fast { reasoning_effort: low; }
`)
	if err != nil {
		t.Fatalf("ParseStylesheet failed: %v", err)
	}

	node := &Node{
		ID:    "review",
		Shape: "box",
		Attrs: map[string]string{"class": "code, fast"},
	}
	resolved := ss.Resolve(node)
	if got := resolved["llm_model"]; got != "claude-opus-4-6" {
		t.Fatalf("llm_model = %q, want %q", got, "claude-opus-4-6")
	}
	if got := resolved["reasoning_effort"]; got != "low" {
		t.Fatalf("reasoning_effort = %q, want %q", got, "low")
	}
}

func TestParityGoalGateReroutesAtExitViaGraphRetryTarget(t *testing.T) {
	g := NewGraph("goal-gate")
	g.Attrs["retry_target"] = "repair"

	g.AddNode(&Node{ID: "start", Shape: "Mdiamond"})
	g.AddNode(&Node{ID: "work", Shape: "box", Attrs: map[string]string{"goal_gate": "true"}})
	g.AddNode(&Node{ID: "repair", Shape: "box"})
	g.AddNode(&Node{ID: "done", Shape: "Msquare"})

	g.AddEdge(&Edge{From: "start", To: "work"})
	g.AddEdge(&Edge{From: "work", To: "done"})
	g.AddEdge(&Edge{From: "repair", To: "work"})

	reg := newTestRegistry()
	attempts := 0
	reg.Register(&testHandler{
		name: "codergen",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			switch node.ID {
			case "work":
				attempts++
				if attempts == 1 {
					return Outcome{Status: OutcomeFail}, nil
				}
				return Outcome{Status: OutcomeSuccess}, nil
			case "repair":
				return Outcome{Status: OutcomeSuccess}, nil
			default:
				return Outcome{Status: OutcomeSuccess}, nil
			}
		},
	})

	engine := NewEngine(g, reg)
	result, err := engine.Run(context.Background())
	if err != nil {
		t.Fatalf("engine.Run failed: %v", err)
	}
	if result.Status != OutcomeSuccess {
		t.Fatalf("status = %q, want %q", result.Status, OutcomeSuccess)
	}

	completed := make(map[string]bool, len(result.CompletedNodes))
	for _, nodeID := range result.CompletedNodes {
		completed[nodeID] = true
	}
	if !completed["repair"] {
		t.Fatal("expected repair node to run before exit succeeds")
	}
	if attempts < 2 {
		t.Fatalf("expected work node to run twice, got %d attempt(s)", attempts)
	}
}
