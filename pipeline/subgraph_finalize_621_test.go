// ABOUTME: Regression guard for issue #621 — a subgraph child engine must run the
// ABOUTME: same FINALIZED topology as top-level execution (PrepareForExecution),
// ABOUTME: so a structurally-invalid child graph fails closed instead of running.
package pipeline

import (
	"context"
	"strings"
	"testing"
)

// TestSubgraph_FinalizesChildGraph pins the #621 gap: subgraph child engines
// used bare NewEngine and were never finalized, so unlike top-level execution
// (NewEngineFromGraph -> PrepareForExecution) they trusted caller-maintained
// adjacency and skipped the final endpoint-validation invariant. A child graph
// with a dangling edge OFF the executed path therefore ran to success; under
// the fix, finalizing the child surfaces the invalid edge and fails the
// subgraph node closed — the same outcome top-level execution would give.
func TestSubgraph_FinalizesChildGraph(t *testing.T) {
	sub := NewGraph("sub")
	sub.AddNode(&Node{ID: "sub_s", Shape: "Mdiamond", Label: "SubStart"})
	sub.AddNode(&Node{ID: "sub_end", Shape: "Msquare", Label: "SubEnd"})
	sub.AddEdge(&Edge{From: "sub_s", To: "sub_end"})
	// An orphan real node with an edge to an UNDECLARED node, off the executed
	// path (never reached from sub_s). finalInvariants/validateEdgeEndpoints
	// checks every edge, so finalization rejects it; a bare run never traverses
	// it and would report success.
	sub.AddNode(&Node{ID: "orphan", Shape: "box", Label: "Orphan"})
	sub.AddEdge(&Edge{From: "orphan", To: "ghost"})

	reg := newTestRegistry()
	reg.Register(&testHandler{
		name:      "subgraph",
		executeFn: NewSubgraphHandler(map[string]*Graph{"child": sub}, reg, nil, nil).Execute,
	})

	g := NewGraph("parent")
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond", Label: "Start"})
	g.AddNode(&Node{ID: "sg", Shape: "tab", Label: "SubgraphNode", Attrs: map[string]string{"subgraph_ref": "child"}})
	g.AddNode(&Node{ID: "end", Shape: "Msquare", Label: "End"})
	g.AddEdge(&Edge{From: "s", To: "sg"})
	g.AddEdge(&Edge{From: "sg", To: "end"})

	result, err := NewEngine(g, reg).Run(context.Background())

	// The subgraph node must fail because its child graph is structurally
	// invalid — the same verdict top-level execution gives such a graph.
	if err == nil && result.Status == OutcomeSuccess {
		t.Fatalf("subgraph with a dangling child edge ran to success — the child engine was not finalized (issue #621)")
	}
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	if !strings.Contains(msg, "undeclared") && !strings.Contains(msg, "ghost") {
		t.Errorf("expected a finalization endpoint error (undeclared node %q), got err=%v status=%v", "ghost", err, result.Status)
	}
}
