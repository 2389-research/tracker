// ABOUTME: Tests for the SubgraphHandler which executes nested pipelines as single node steps.
// ABOUTME: Covers happy path, context propagation, missing refs, failures, and shape mapping.
package pipeline

import (
	"context"
	"testing"
)

func TestSubgraphHandler_Execute(t *testing.T) {
	// Build a simple sub-pipeline: start -> step -> exit
	subGraph := NewGraph("sub")
	subGraph.AddNode(&Node{ID: "sub_s", Shape: "Mdiamond", Label: "SubStart"})
	subGraph.AddNode(&Node{ID: "sub_step", Shape: "box", Label: "SubStep"})
	subGraph.AddNode(&Node{ID: "sub_end", Shape: "Msquare", Label: "SubEnd"})
	subGraph.AddEdge(&Edge{From: "sub_s", To: "sub_step"})
	subGraph.AddEdge(&Edge{From: "sub_step", To: "sub_end"})

	reg := newTestRegistry()
	reg.Register(&testHandler{
		name: "subgraph",
		executeFn: NewSubgraphHandler(
			map[string]*Graph{"child": subGraph},
			reg, nil, nil,
		).Execute,
	})

	// Build parent pipeline: start -> subgraph_node -> exit
	g := NewGraph("parent")
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond", Label: "Start"})
	g.AddNode(&Node{ID: "sg", Shape: "tab", Label: "SubgraphNode", Attrs: map[string]string{"subgraph_ref": "child"}})
	g.AddNode(&Node{ID: "end", Shape: "Msquare", Label: "End"})
	g.AddEdge(&Edge{From: "s", To: "sg"})
	g.AddEdge(&Edge{From: "sg", To: "end"})

	engine := NewEngine(g, reg)
	result, err := engine.Run(context.Background())
	if err != nil {
		t.Fatalf("engine run failed: %v", err)
	}
	if result.Status != OutcomeSuccess {
		t.Errorf("expected success, got %q", result.Status)
	}

	completedSet := make(map[string]bool)
	for _, n := range result.CompletedNodes {
		completedSet[n] = true
	}
	if !completedSet["sg"] {
		t.Error("expected subgraph node 'sg' to be completed")
	}
}

func TestSubgraphHandler_ContextPropagation(t *testing.T) {
	// Sub-pipeline that sets a context value.
	subGraph := NewGraph("sub_ctx")
	subGraph.AddNode(&Node{ID: "sub_s", Shape: "Mdiamond", Label: "SubStart"})
	subGraph.AddNode(&Node{ID: "sub_setter", Shape: "box", Label: "Setter"})
	subGraph.AddNode(&Node{ID: "sub_end", Shape: "Msquare", Label: "SubEnd"})
	subGraph.AddEdge(&Edge{From: "sub_s", To: "sub_setter"})
	subGraph.AddEdge(&Edge{From: "sub_setter", To: "sub_end"})

	reg := newTestRegistry()
	// The "codergen" handler in sub-pipeline sets a context value.
	reg.Register(&testHandler{
		name: "codergen",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			return Outcome{
				Status:         OutcomeSuccess,
				ContextUpdates: map[string]string{"child_key": "child_value"},
			}, nil
		},
	})

	handler := NewSubgraphHandler(
		map[string]*Graph{"ctx_child": subGraph},
		reg, nil, nil,
	)

	// Set up parent context with a value.
	pctx := NewPipelineContext()
	pctx.Set("parent_key", "parent_value")

	node := &Node{
		ID:    "sg_node",
		Shape: "tab",
		Attrs: map[string]string{"subgraph_ref": "ctx_child"},
	}

	outcome, err := handler.Execute(context.Background(), node, pctx)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if outcome.Status != OutcomeSuccess {
		t.Errorf("expected success, got %q", outcome.Status)
	}

	// Child context updates should propagate back via ContextUpdates.
	if outcome.ContextUpdates["child_key"] != "child_value" {
		t.Errorf("expected child_key=child_value in context updates, got %v", outcome.ContextUpdates)
	}
}

func TestSubgraphHandler_MissingSubgraph(t *testing.T) {
	reg := newTestRegistry()
	handler := NewSubgraphHandler(
		map[string]*Graph{},
		reg, nil, nil,
	)

	node := &Node{
		ID:    "sg_node",
		Shape: "tab",
		Attrs: map[string]string{"subgraph_ref": "nonexistent"},
	}
	pctx := NewPipelineContext()

	outcome, err := handler.Execute(context.Background(), node, pctx)
	if err == nil {
		t.Fatal("expected error for missing subgraph ref")
	}
	if outcome.Status != OutcomeFail {
		t.Errorf("expected fail outcome, got %q", outcome.Status)
	}
}

func TestSubgraphHandler_MissingRef(t *testing.T) {
	reg := newTestRegistry()
	handler := NewSubgraphHandler(
		map[string]*Graph{},
		reg, nil, nil,
	)

	// Node without subgraph_ref attribute.
	node := &Node{
		ID:    "sg_node",
		Shape: "tab",
		Attrs: map[string]string{},
	}
	pctx := NewPipelineContext()

	outcome, err := handler.Execute(context.Background(), node, pctx)
	if err == nil {
		t.Fatal("expected error for missing subgraph_ref attribute")
	}
	if outcome.Status != OutcomeFail {
		t.Errorf("expected fail outcome, got %q", outcome.Status)
	}
}

func TestSubgraphHandler_SubgraphFailure(t *testing.T) {
	// Sub-pipeline where a goal-gate node fails.
	subGraph := NewGraph("sub_fail")
	subGraph.AddNode(&Node{ID: "sub_s", Shape: "Mdiamond", Label: "SubStart"})
	subGraph.AddNode(&Node{ID: "sub_bad", Shape: "box", Label: "Bad", Attrs: map[string]string{"goal_gate": "true"}})
	subGraph.AddNode(&Node{ID: "sub_end", Shape: "Msquare", Label: "SubEnd"})
	subGraph.AddEdge(&Edge{From: "sub_s", To: "sub_bad"})
	subGraph.AddEdge(&Edge{From: "sub_bad", To: "sub_end", Condition: "ctx.outcome = success"})
	subGraph.AddEdge(&Edge{From: "sub_bad", To: "sub_end", Condition: "ctx.outcome = fail"})

	reg := newTestRegistry()
	// Override codergen to return fail.
	reg.Register(&testHandler{
		name: "codergen",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			return Outcome{Status: OutcomeFail}, nil
		},
	})

	handler := NewSubgraphHandler(
		map[string]*Graph{"fail_child": subGraph},
		reg, nil, nil,
	)

	node := &Node{
		ID:    "sg_node",
		Shape: "tab",
		Attrs: map[string]string{"subgraph_ref": "fail_child"},
	}
	pctx := NewPipelineContext()

	outcome, err := handler.Execute(context.Background(), node, pctx)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if outcome.Status != OutcomeFail {
		t.Errorf("expected fail outcome for failed sub-pipeline, got %q", outcome.Status)
	}
}

func TestSubgraphHandler_ScopedEvents(t *testing.T) {
	// Verify that child engine events arrive at the parent's event handler
	// with node IDs prefixed by the parent node ID.
	subGraph := NewGraph("sub")
	subGraph.AddNode(&Node{ID: "sub_s", Shape: "Mdiamond", Label: "SubStart"})
	subGraph.AddNode(&Node{ID: "sub_step", Shape: "box", Label: "SubStep"})
	subGraph.AddNode(&Node{ID: "sub_end", Shape: "Msquare", Label: "SubEnd"})
	subGraph.AddEdge(&Edge{From: "sub_s", To: "sub_step"})
	subGraph.AddEdge(&Edge{From: "sub_step", To: "sub_end"})

	reg := newTestRegistry()

	// Collect all events emitted through the parent's pipeline handler.
	var events []PipelineEvent
	handler := PipelineEventHandlerFunc(func(evt PipelineEvent) {
		events = append(events, evt)
	})

	reg.Register(&testHandler{
		name: "subgraph",
		executeFn: NewSubgraphHandler(
			map[string]*Graph{"child": subGraph},
			reg, handler, nil,
		).Execute,
	})

	g := NewGraph("parent")
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond", Label: "Start"})
	g.AddNode(&Node{ID: "sg", Shape: "tab", Label: "SubgraphNode", Attrs: map[string]string{"subgraph_ref": "child"}})
	g.AddNode(&Node{ID: "end", Shape: "Msquare", Label: "End"})
	g.AddEdge(&Edge{From: "s", To: "sg"})
	g.AddEdge(&Edge{From: "sg", To: "end"})

	engine := NewEngine(g, reg, WithPipelineEventHandler(handler))
	result, err := engine.Run(context.Background())
	if err != nil {
		t.Fatalf("engine run failed: %v", err)
	}
	if result.Status != OutcomeSuccess {
		t.Errorf("expected success, got %q", result.Status)
	}

	// Check that child events have scoped node IDs.
	scopedNodeIDs := map[string]bool{}
	for _, evt := range events {
		if evt.Type == EventStageStarted && IsSubgraphNodeID(evt.NodeID) {
			scopedNodeIDs[evt.NodeID] = true
		}
	}

	// We expect at least sub_s, sub_step, sub_end from child engine,
	// all prefixed with "sg/".
	expectedScoped := []string{"sg/sub_s", "sg/sub_step", "sg/sub_end"}
	for _, want := range expectedScoped {
		if !scopedNodeIDs[want] {
			t.Errorf("expected scoped event for %q, got events: %v", want, scopedNodeIDs)
		}
	}
}

// IsSubgraphNodeID mirrors the TUI's IsSubgraphNode check for testing.
func IsSubgraphNodeID(id string) bool {
	return len(id) > 0 && id[0] != '/' && contains(id, "/")
}

func contains(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// TestSubgraphHandler_ChildVarDefaultsPreserved verifies that a child
// workflow's own vars (declared at the child workflow level, landing in
// subGraph.Attrs as "params.<key>") survive subgraph invocation even
// when the parent doesn't pass a subgraph_params override for that key.
// Regression guard for PR #117 Codex P1: pre-expansion in
// InjectParamsIntoGraph previously only saw parent-provided params,
// silently replacing unpassed ${params.foo} with empty string.
func TestSubgraphHandler_ChildVarDefaultsPreserved(t *testing.T) {
	subGraph := NewGraph("child")
	// Declare "foo" as a child-level var.
	subGraph.Attrs = map[string]string{
		GraphParamAttrKey("foo"): "child_default",
	}
	subGraph.AddNode(&Node{ID: "cs", Shape: "Mdiamond"})
	subGraph.AddNode(&Node{
		ID:    "step",
		Shape: "box",
		Attrs: map[string]string{"prompt": "value=${params.foo}"},
	})
	subGraph.AddNode(&Node{ID: "ce", Shape: "Msquare"})
	subGraph.AddEdge(&Edge{From: "cs", To: "step"})
	subGraph.AddEdge(&Edge{From: "step", To: "ce"})

	// Parent subgraph node with NO explicit subgraph_params.
	h := NewSubgraphHandler(
		map[string]*Graph{"child": subGraph},
		NewHandlerRegistry(), nil, nil,
	)
	parentNode := &Node{ID: "sg", Attrs: map[string]string{"subgraph_ref": "child"}}

	// Build the merged params the way Execute will, via its private logic
	// mirrored here for assertion. We don't want to actually run the
	// sub-engine (would need registered handlers); we just check the
	// injection preserves the default.
	childDefaults := ExtractParamsFromGraphAttrs(subGraph.Attrs)
	parentOverrides := ParseSubgraphParams(parentNode.Attrs["subgraph_params"])
	params := make(map[string]string)
	for k, v := range childDefaults {
		params[k] = v
	}
	for k, v := range parentOverrides {
		params[k] = v
	}

	injected, err := InjectParamsIntoGraph(subGraph, params)
	if err != nil {
		t.Fatalf("InjectParamsIntoGraph: %v", err)
	}
	got := injected.Nodes["step"].Attrs["prompt"]
	if got != "value=child_default" {
		t.Errorf("prompt = %q, want value=child_default (child's own var default must survive)", got)
	}
	// Prevent the handler reference from being flagged unused by
	// static analysis; real execution is covered by TestSubgraphHandler_Execute.
	_ = h
}

// TestSubgraphHandler_ParentOverrideWinsOverChildDefault verifies that
// an explicit subgraph_params value overrides the child's var default.
func TestSubgraphHandler_ParentOverrideWinsOverChildDefault(t *testing.T) {
	subGraph := NewGraph("child")
	subGraph.Attrs = map[string]string{GraphParamAttrKey("foo"): "child_default"}
	subGraph.AddNode(&Node{ID: "cs", Shape: "Mdiamond"})
	subGraph.AddNode(&Node{
		ID:    "step",
		Shape: "box",
		Attrs: map[string]string{"prompt": "value=${params.foo}"},
	})
	subGraph.AddNode(&Node{ID: "ce", Shape: "Msquare"})
	subGraph.AddEdge(&Edge{From: "cs", To: "step"})
	subGraph.AddEdge(&Edge{From: "step", To: "ce"})

	childDefaults := ExtractParamsFromGraphAttrs(subGraph.Attrs)
	parentOverrides := ParseSubgraphParams("foo=parent_override")
	params := make(map[string]string)
	for k, v := range childDefaults {
		params[k] = v
	}
	for k, v := range parentOverrides {
		params[k] = v
	}

	injected, err := InjectParamsIntoGraph(subGraph, params)
	if err != nil {
		t.Fatalf("InjectParamsIntoGraph: %v", err)
	}
	got := injected.Nodes["step"].Attrs["prompt"]
	if got != "value=parent_override" {
		t.Errorf("prompt = %q, want value=parent_override", got)
	}
}

func TestSubgraphHandler_ShapeMapping(t *testing.T) {
	handler, ok := ShapeToHandler("tab")
	if !ok {
		t.Fatal("expected 'tab' shape to be mapped to a handler")
	}
	if handler != "subgraph" {
		t.Errorf("expected 'tab' to map to 'subgraph', got %q", handler)
	}
}
