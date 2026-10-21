// ABOUTME: Tests the subgraph scope carried on context.Context, which tells a
// ABOUTME: human gate it is running inside a subgraph / manager_loop child.
package pipeline

import (
	"context"
	"slices"
	"testing"
)

func TestSubgraphScope_Nested(t *testing.T) {
	if got := SubgraphScope(context.Background()); len(got) != 0 {
		t.Fatalf("empty ctx scope = %v", got)
	}
	ctx := WithSubgraphScope(WithSubgraphScope(context.Background(), "outer"), "inner")
	if got := SubgraphScope(ctx); !slices.Equal(got, []string{"outer", "inner"}) {
		t.Errorf("scope = %v, want [outer inner]", got)
	}
}

func TestSubgraph_SetsScope(t *testing.T) {
	child := NewGraph("child")
	child.AddNode(&Node{ID: "cs", Shape: "Mdiamond", Label: "Start"})
	child.AddNode(&Node{ID: "work", Shape: "box", Label: "Work"})
	child.AddNode(&Node{ID: "ce", Shape: "Msquare", Label: "End"})
	child.AddEdge(&Edge{From: "cs", To: "work"})
	child.AddEdge(&Edge{From: "work", To: "ce"})

	var seen []string
	reg := newTestRegistry()
	reg.Register(&testHandler{
		name: "codergen",
		executeFn: func(ctx context.Context, node *Node, _ *PipelineContext) (Outcome, error) {
			if node.ID == "work" {
				seen = SubgraphScope(ctx)
			}
			return Outcome{Status: OutcomeSuccess}, nil
		},
	})
	reg.Register(&testHandler{
		name:      "subgraph",
		executeFn: NewSubgraphHandler(map[string]*Graph{"child": child}, reg, nil, nil).Execute,
	})
	g := NewGraph("parent")
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond", Label: "Start"})
	g.AddNode(&Node{ID: "sg", Shape: "tab", Label: "Sub", Attrs: map[string]string{"subgraph_ref": "child"}})
	g.AddNode(&Node{ID: "end", Shape: "Msquare", Label: "End"})
	g.AddEdge(&Edge{From: "s", To: "sg"})
	g.AddEdge(&Edge{From: "sg", To: "end"})
	if _, err := NewEngine(g, reg).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(seen, []string{"sg"}) {
		t.Errorf("child node saw scope %v, want [sg]", seen)
	}
}
