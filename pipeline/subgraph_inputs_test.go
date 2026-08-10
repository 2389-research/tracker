// ABOUTME: Tests for subgraph call-site input binding (#556) — the runtime half
// ABOUTME: of DIP160: parent subgraph_params validate + seed the child's inputs.*.
package pipeline

import (
	"context"
	"strings"
	"testing"
)

// TestSubgraphInputs_Dip2InputsKeywordRoundTrips pins tracker's keyword-agnostic
// adapter contract (tracker#565): dippin v0.63.0 renamed the dip-2 subgraph
// call-site binding from `params:` to `inputs:` (dippin#227), but both spellings
// parse to the same IR field (SubgraphConfig.Params), which extractSubgraphAttrs
// serializes to the `subgraph_params` attr the runtime already reads. So a dip-2
// `inputs:` binding must reach bindSubgraphInputs with no adapter change.
func TestSubgraphInputs_Dip2InputsKeywordRoundTrips(t *testing.T) {
	src := "dip 2\n\nworkflow P\n  start: S\n  exit: S\n  subgraph S\n    ref: child.dip\n    inputs:\n      topic: hi\n"
	g, _, err := LoadDippinWorkflow(src, "p.dip")
	if err != nil {
		t.Fatalf("load dip-2 subgraph inputs: %v", err)
	}
	var sg *Node
	for _, n := range g.Nodes {
		if n.Attrs["subgraph_ref"] != "" {
			sg = n
		}
	}
	if sg == nil {
		t.Fatalf("no subgraph node produced; nodes=%+v", g.Nodes)
	}
	if !strings.Contains(sg.Attrs["subgraph_params"], "topic=hi") {
		t.Fatalf("dip-2 inputs: did not reach subgraph_params: got %q", sg.Attrs["subgraph_params"])
	}
}

// childWithInput builds a subgraph whose middle node reads the child's
// inputs.topic straight from the child PipelineContext (where subgraph binding
// seeds it) and echoes it, so a test can assert the parent's params bound to the
// child's declared input.
func childWithInput(inputs []InputSpec) (*Graph, *HandlerRegistry) {
	g := NewGraph("child_in")
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond", Label: "S"})
	g.AddNode(&Node{ID: "cap", Shape: "box", Label: "Cap"})
	g.AddNode(&Node{ID: "e", Shape: "Msquare", Label: "E"})
	g.AddEdge(&Edge{From: "s", To: "cap"})
	g.AddEdge(&Edge{From: "cap", To: "e"})
	g.Inputs = inputs

	reg := newTestRegistry()
	reg.Register(&testHandler{
		name: "codergen",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			v, _ := pctx.Get(inputContextPrefix + "topic")
			return Outcome{Status: OutcomeSuccess, ContextUpdates: map[string]string{"seen_input": v}}, nil
		},
	})
	return g, reg
}

func TestSubgraphInputs_BindsAndSeeds(t *testing.T) {
	sub, reg := childWithInput([]InputSpec{{Name: "topic", Kind: InputText, Required: true}})
	h := NewSubgraphHandler(map[string]*Graph{"child": sub}, reg, nil, nil)

	node := &Node{ID: "sg", Shape: "tab", Attrs: map[string]string{
		"subgraph_ref":    "child",
		"subgraph_params": "topic=migrations",
	}}
	out, err := h.Execute(context.Background(), node, NewPipelineContext())
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if out.Status != OutcomeSuccess {
		t.Fatalf("status=%q, want success", out.Status)
	}
	if got := out.ContextUpdates["seen_input"]; got != "migrations" {
		t.Fatalf("child did not see the bound input: inputs.topic=%q, want migrations", got)
	}
}

func TestSubgraphInputs_MissingRequiredFailsClosed(t *testing.T) {
	sub, reg := childWithInput([]InputSpec{{Name: "topic", Kind: InputText, Required: true}})
	h := NewSubgraphHandler(map[string]*Graph{"child": sub}, reg, nil, nil)

	// No subgraph_params → required "topic" unsatisfied.
	node := &Node{ID: "sg", Shape: "tab", Attrs: map[string]string{"subgraph_ref": "child"}}
	out, err := h.Execute(context.Background(), node, NewPipelineContext())
	if err == nil || out.Status != OutcomeFail {
		t.Fatalf("expected fail-closed on missing required child input, got status=%q err=%v", out.Status, err)
	}
}

func TestSubgraphInputs_UnknownParamKeyIsNotAnError(t *testing.T) {
	// A params key that is a workflow var (not a declared input) must not fail.
	sub, reg := childWithInput([]InputSpec{{Name: "topic", Kind: InputText, Required: true}})
	h := NewSubgraphHandler(map[string]*Graph{"child": sub}, reg, nil, nil)

	node := &Node{ID: "sg", Shape: "tab", Attrs: map[string]string{
		"subgraph_ref":    "child",
		"subgraph_params": "topic=x,some_var=y",
	}}
	out, err := h.Execute(context.Background(), node, NewPipelineContext())
	if err != nil || out.Status != OutcomeSuccess {
		t.Fatalf("a non-input params key should not fail the subgraph: status=%q err=%v", out.Status, err)
	}
}

func TestSubgraphInputs_FileSecretNotBound(t *testing.T) {
	// A file/secret child input is filtered from subgraph binding: an unsupplied
	// file input must NOT trigger missing-required (it's not bindable here).
	sub, reg := childWithInput([]InputSpec{
		{Name: "spec", Kind: InputFile, Required: true},
		{Name: "topic", Kind: InputText, Required: true},
	})
	h := NewSubgraphHandler(map[string]*Graph{"child": sub}, reg, nil, nil)

	node := &Node{ID: "sg", Shape: "tab", Attrs: map[string]string{
		"subgraph_ref":    "child",
		"subgraph_params": "topic=x", // spec (file) not supplied — must be tolerated
	}}
	out, err := h.Execute(context.Background(), node, NewPipelineContext())
	if err != nil || out.Status != OutcomeSuccess {
		t.Fatalf("file input should be excluded from subgraph binding: status=%q err=%v", out.Status, err)
	}
}
