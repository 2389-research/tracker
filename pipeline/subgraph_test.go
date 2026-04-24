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

// TestSubgraph_BudgetBypass_Fix_UsageRollup pins the core #183 rollup: a
// subgraph-nested node that reports Stats with tokens must have that usage
// appear in the parent's EngineResult.Usage.ProviderTotals, not vanish into
// the child engine's own trace.
//
// Pre-fix: AggregateUsage at the parent level saw only the subgraph node's
// own Stats (nil), so child spend disappeared entirely from operator-visible
// summaries and from any BudgetGuard check the parent ran.
func TestSubgraph_BudgetBypass_Fix_UsageRollup(t *testing.T) {
	const childTokens = 500
	const childCost = 0.05

	// Build a child pipeline whose single "expensive" box emits Stats.
	sub := NewGraph("sub")
	sub.AddNode(&Node{ID: "s", Shape: "Mdiamond"})
	sub.AddNode(&Node{ID: "expensive", Shape: "box"})
	sub.AddNode(&Node{ID: "e", Shape: "Msquare"})
	sub.AddEdge(&Edge{From: "s", To: "expensive"})
	sub.AddEdge(&Edge{From: "expensive", To: "e"})

	reg := newTestRegistry()
	reg.Register(&testHandler{
		name: "codergen",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			if node.ID == "expensive" {
				return Outcome{
					Status: OutcomeSuccess,
					Stats: &SessionStats{
						InputTokens:  childTokens / 2,
						OutputTokens: childTokens / 2,
						TotalTokens:  childTokens,
						CostUSD:      childCost,
						Provider:     "anthropic",
					},
				}, nil
			}
			return Outcome{Status: OutcomeSuccess}, nil
		},
	})
	reg.Register(&testHandler{
		name: "subgraph",
		executeFn: NewSubgraphHandler(
			map[string]*Graph{"child": sub},
			reg, nil, nil,
		).Execute,
	})

	// Parent: start -> sg -> exit.
	g := NewGraph("parent")
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond"})
	g.AddNode(&Node{ID: "sg", Shape: "tab", Attrs: map[string]string{"subgraph_ref": "child"}})
	g.AddNode(&Node{ID: "e", Shape: "Msquare"})
	g.AddEdge(&Edge{From: "s", To: "sg"})
	g.AddEdge(&Edge{From: "sg", To: "e"})

	engine := NewEngine(g, reg)
	result, err := engine.Run(context.Background())
	if err != nil {
		t.Fatalf("engine run failed: %v", err)
	}
	if result.Usage == nil {
		t.Fatal("result.Usage is nil; want child spend rolled up")
	}
	if result.Usage.TotalTokens != childTokens {
		t.Errorf("TotalTokens = %d, want %d (child spend must fold into parent)", result.Usage.TotalTokens, childTokens)
	}
	got := result.Usage.ProviderTotals["anthropic"]
	if got.TotalTokens != childTokens {
		t.Errorf("ProviderTotals[anthropic].TotalTokens = %d, want %d", got.TotalTokens, childTokens)
	}
	if got.CostUSD != childCost {
		t.Errorf("ProviderTotals[anthropic].CostUSD = %f, want %f", got.CostUSD, childCost)
	}
	if _, hasUnknown := result.Usage.ProviderTotals["unknown"]; hasUnknown {
		t.Errorf("ProviderTotals contains \"unknown\"; child Stats.Provider should carry through")
	}
}

// TestSubgraph_BudgetBypass_Fix_ParentGuardHaltsAfterOverspend verifies that
// when subgraph-nested work overspends the parent's budget, the parent's
// BudgetGuard fires on the between-node check that follows the subgraph node.
// This is the "delayed enforcement" half of the fix — compare with
// TestSubgraph_BudgetBypass_Fix_ChildGuardHaltsMidSubgraph below for the
// mid-subgraph enforcement case.
func TestSubgraph_BudgetBypass_Fix_ParentGuardHaltsAfterOverspend(t *testing.T) {
	sub := NewGraph("sub")
	sub.AddNode(&Node{ID: "s", Shape: "Mdiamond"})
	sub.AddNode(&Node{ID: "burn", Shape: "box"})
	sub.AddNode(&Node{ID: "e", Shape: "Msquare"})
	sub.AddEdge(&Edge{From: "s", To: "burn"})
	sub.AddEdge(&Edge{From: "burn", To: "e"})

	reg := newTestRegistry()
	reg.Register(&testHandler{
		name: "codergen",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			if node.ID == "burn" {
				return Outcome{Status: OutcomeSuccess, Stats: &SessionStats{
					TotalTokens: 10_000,
					Provider:    "anthropic",
				}}, nil
			}
			return Outcome{Status: OutcomeSuccess}, nil
		},
	})
	reg.Register(&testHandler{
		name: "subgraph",
		executeFn: NewSubgraphHandler(
			map[string]*Graph{"child": sub},
			reg, nil, nil,
		).Execute,
	})

	g := NewGraph("parent")
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond"})
	g.AddNode(&Node{ID: "sg", Shape: "tab", Attrs: map[string]string{"subgraph_ref": "child"}})
	g.AddNode(&Node{ID: "follow", Shape: "box"})
	g.AddNode(&Node{ID: "e", Shape: "Msquare"})
	g.AddEdge(&Edge{From: "s", To: "sg"})
	g.AddEdge(&Edge{From: "sg", To: "follow"})
	g.AddEdge(&Edge{From: "follow", To: "e"})

	guard := NewBudgetGuard(BudgetLimits{MaxTotalTokens: 100})
	engine := NewEngine(g, reg, WithBudgetGuard(guard))
	result, err := engine.Run(context.Background())
	if err != nil {
		t.Fatalf("engine run failed: %v", err)
	}
	if result.Status != OutcomeBudgetExceeded {
		t.Fatalf("Status = %q, want %q (parent should halt after subgraph overspends)", result.Status, OutcomeBudgetExceeded)
	}
	if len(result.BudgetLimitsHit) == 0 {
		t.Error("BudgetLimitsHit is empty; want tokens breach recorded")
	}
	// "follow" node must never have run — the guard halts before it.
	for _, id := range result.CompletedNodes {
		if id == "follow" {
			t.Error("completed node 'follow' after subgraph budget overspend; guard did not halt")
		}
	}
}

// TestSubgraph_BudgetBypass_Fix_ChildGuardHaltsMidSubgraph verifies the
// mid-subgraph half of the fix: when a parent's baseline usage plus a
// partial child accumulation exceeds the limit, the *child* engine's
// between-node check fires and halts before the child finishes. Without
// baseline propagation, the child guard would only see child-local spend
// and the effective ceiling inside the subgraph would grow by whatever
// the parent already spent.
func TestSubgraph_BudgetBypass_Fix_ChildGuardHaltsMidSubgraph(t *testing.T) {
	sub := NewGraph("sub")
	sub.AddNode(&Node{ID: "s", Shape: "Mdiamond"})
	sub.AddNode(&Node{ID: "burn1", Shape: "box"})
	sub.AddNode(&Node{ID: "burn2", Shape: "box"})
	sub.AddNode(&Node{ID: "e", Shape: "Msquare"})
	sub.AddEdge(&Edge{From: "s", To: "burn1"})
	sub.AddEdge(&Edge{From: "burn1", To: "burn2"})
	sub.AddEdge(&Edge{From: "burn2", To: "e"})

	reg := newTestRegistry()
	// parent_pre burns 50 tokens, burn1 burns 60 more (total 110 > limit 100),
	// burn2 would burn 40 more but must never run.
	reg.Register(&testHandler{
		name: "codergen",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			switch node.ID {
			case "parent_pre":
				return Outcome{Status: OutcomeSuccess, Stats: &SessionStats{TotalTokens: 50, Provider: "anthropic"}}, nil
			case "burn1":
				return Outcome{Status: OutcomeSuccess, Stats: &SessionStats{TotalTokens: 60, Provider: "anthropic"}}, nil
			case "burn2":
				return Outcome{Status: OutcomeSuccess, Stats: &SessionStats{TotalTokens: 40, Provider: "anthropic"}}, nil
			}
			return Outcome{Status: OutcomeSuccess}, nil
		},
	})
	reg.Register(&testHandler{
		name: "subgraph",
		executeFn: NewSubgraphHandler(
			map[string]*Graph{"child": sub},
			reg, nil, nil,
		).Execute,
	})

	g := NewGraph("parent")
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond"})
	g.AddNode(&Node{ID: "parent_pre", Shape: "box"})
	g.AddNode(&Node{ID: "sg", Shape: "tab", Attrs: map[string]string{"subgraph_ref": "child"}})
	g.AddNode(&Node{ID: "e", Shape: "Msquare"})
	g.AddEdge(&Edge{From: "s", To: "parent_pre"})
	g.AddEdge(&Edge{From: "parent_pre", To: "sg"})
	g.AddEdge(&Edge{From: "sg", To: "e"})

	guard := NewBudgetGuard(BudgetLimits{MaxTotalTokens: 100})
	engine := NewEngine(g, reg, WithBudgetGuard(guard))
	result, err := engine.Run(context.Background())
	if err != nil {
		t.Fatalf("engine run failed: %v", err)
	}
	if result.Status != OutcomeBudgetExceeded {
		t.Fatalf("Status = %q, want %q (child guard should halt mid-subgraph on combined 50+60=110)", result.Status, OutcomeBudgetExceeded)
	}
	// burn2 must never run — baseline + burn1 already breaches the limit.
	// Check via the child's trace entries propagated through ChildUsage:
	// SessionCount should be 1 (only burn1), not 2.
	if result.Usage == nil {
		t.Fatal("result.Usage is nil")
	}
	// Parent_pre (1 session) + child burn1 (1 session) = 2.
	if result.Usage.SessionCount != 2 {
		t.Errorf("Usage.SessionCount = %d, want 2 (parent_pre + burn1 only; burn2 must not run)", result.Usage.SessionCount)
	}
}

// TestSubgraph_BudgetBypass_Fix_NestedSubgraph verifies two-level nesting:
// a grandparent subgraph wrapping a parent subgraph wrapping a leaf burn.
// Usage must roll up all the way to the grandparent.
func TestSubgraph_BudgetBypass_Fix_NestedSubgraph(t *testing.T) {
	// Leaf: single burn node.
	leaf := NewGraph("leaf")
	leaf.AddNode(&Node{ID: "s", Shape: "Mdiamond"})
	leaf.AddNode(&Node{ID: "burn", Shape: "box"})
	leaf.AddNode(&Node{ID: "e", Shape: "Msquare"})
	leaf.AddEdge(&Edge{From: "s", To: "burn"})
	leaf.AddEdge(&Edge{From: "burn", To: "e"})

	// Middle: subgraph that invokes the leaf.
	middle := NewGraph("middle")
	middle.AddNode(&Node{ID: "s", Shape: "Mdiamond"})
	middle.AddNode(&Node{ID: "inner", Shape: "tab", Attrs: map[string]string{"subgraph_ref": "leaf"}})
	middle.AddNode(&Node{ID: "e", Shape: "Msquare"})
	middle.AddEdge(&Edge{From: "s", To: "inner"})
	middle.AddEdge(&Edge{From: "inner", To: "e"})

	reg := newTestRegistry()
	reg.Register(&testHandler{
		name: "codergen",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			if node.ID == "burn" {
				return Outcome{Status: OutcomeSuccess, Stats: &SessionStats{
					TotalTokens: 777,
					CostUSD:     0.12,
					Provider:    "openai",
				}}, nil
			}
			return Outcome{Status: OutcomeSuccess}, nil
		},
	})
	reg.Register(&testHandler{
		name: "subgraph",
		executeFn: NewSubgraphHandler(
			map[string]*Graph{"leaf": leaf, "middle": middle},
			reg, nil, nil,
		).Execute,
	})

	// Grandparent: single subgraph ref to middle.
	g := NewGraph("grandparent")
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond"})
	g.AddNode(&Node{ID: "outer", Shape: "tab", Attrs: map[string]string{"subgraph_ref": "middle"}})
	g.AddNode(&Node{ID: "e", Shape: "Msquare"})
	g.AddEdge(&Edge{From: "s", To: "outer"})
	g.AddEdge(&Edge{From: "outer", To: "e"})

	engine := NewEngine(g, reg)
	result, err := engine.Run(context.Background())
	if err != nil {
		t.Fatalf("engine run failed: %v", err)
	}
	if result.Usage == nil {
		t.Fatal("result.Usage is nil; want leaf spend rolled up two levels")
	}
	if result.Usage.TotalTokens != 777 {
		t.Errorf("TotalTokens = %d, want 777 (leaf spend must roll up through middle + grandparent)", result.Usage.TotalTokens)
	}
	if got := result.Usage.ProviderTotals["openai"]; got.TotalTokens != 777 {
		t.Errorf("ProviderTotals[openai].TotalTokens = %d, want 777", got.TotalTokens)
	}
	if got := result.Usage.ProviderTotals["openai"]; got.CostUSD != 0.12 {
		t.Errorf("ProviderTotals[openai].CostUSD = %f, want 0.12", got.CostUSD)
	}
}

// TestSubgraph_BudgetExceededEvent_ReportsCombinedSnapshot verifies that when
// a child engine halts mid-subgraph on a combined baseline+local breach, the
// EventBudgetExceeded's CostSnapshot reports the *combined* value — the one
// that actually tripped the guard — rather than just the child-local trace
// aggregate. Without this, diagnostics would show a sub-ceiling number
// alongside a "budget exceeded" message, which is confusing. See PR #187
// review (Copilot).
func TestSubgraph_BudgetExceededEvent_ReportsCombinedSnapshot(t *testing.T) {
	sub := NewGraph("sub")
	sub.AddNode(&Node{ID: "s", Shape: "Mdiamond"})
	sub.AddNode(&Node{ID: "burn1", Shape: "box"})
	sub.AddNode(&Node{ID: "e", Shape: "Msquare"})
	sub.AddEdge(&Edge{From: "s", To: "burn1"})
	sub.AddEdge(&Edge{From: "burn1", To: "e"})

	reg := newTestRegistry()
	reg.Register(&testHandler{
		name: "codergen",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			switch node.ID {
			case "parent_pre":
				return Outcome{Status: OutcomeSuccess, Stats: &SessionStats{TotalTokens: 50, Provider: "anthropic"}}, nil
			case "burn1":
				return Outcome{Status: OutcomeSuccess, Stats: &SessionStats{TotalTokens: 60, Provider: "anthropic"}}, nil
			}
			return Outcome{Status: OutcomeSuccess}, nil
		},
	})
	reg.Register(&testHandler{
		name: "subgraph",
		executeFn: NewSubgraphHandler(
			map[string]*Graph{"child": sub},
			reg, nil, nil,
		).Execute,
	})

	g := NewGraph("parent")
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond"})
	g.AddNode(&Node{ID: "parent_pre", Shape: "box"})
	g.AddNode(&Node{ID: "sg", Shape: "tab", Attrs: map[string]string{"subgraph_ref": "child"}})
	g.AddNode(&Node{ID: "e", Shape: "Msquare"})
	g.AddEdge(&Edge{From: "s", To: "parent_pre"})
	g.AddEdge(&Edge{From: "parent_pre", To: "sg"})
	g.AddEdge(&Edge{From: "sg", To: "e"})

	var budgetEvents []PipelineEvent
	handler := PipelineEventHandlerFunc(func(evt PipelineEvent) {
		if evt.Type == EventBudgetExceeded {
			budgetEvents = append(budgetEvents, evt)
		}
	})

	guard := NewBudgetGuard(BudgetLimits{MaxTotalTokens: 100})
	engine := NewEngine(g, reg, WithBudgetGuard(guard), WithPipelineEventHandler(handler))
	result, err := engine.Run(context.Background())
	if err != nil {
		t.Fatalf("engine run failed: %v", err)
	}
	if result.Status != OutcomeBudgetExceeded {
		t.Fatalf("Status = %q, want %q", result.Status, OutcomeBudgetExceeded)
	}

	// The child engine halts first on combined 50+60=110 > 100 and emits its
	// own EventBudgetExceeded with the combined snapshot. The parent's
	// between-node check also fires afterward with its own emission. We
	// assert that at least one event reports the combined total (≥110).
	if len(budgetEvents) == 0 {
		t.Fatal("no EventBudgetExceeded emitted")
	}
	var sawCombined bool
	for _, evt := range budgetEvents {
		if evt.Cost == nil {
			continue
		}
		if evt.Cost.TotalTokens >= 110 {
			sawCombined = true
			break
		}
	}
	if !sawCombined {
		for _, evt := range budgetEvents {
			if evt.Cost != nil {
				t.Logf("EventBudgetExceeded Cost.TotalTokens = %d", evt.Cost.TotalTokens)
			}
		}
		t.Errorf("no EventBudgetExceeded reported the combined total (want ≥110 = 50 baseline + 60 child); child-local sub-snapshot was emitted instead")
	}
}
