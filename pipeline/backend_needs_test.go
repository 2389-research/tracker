// ABOUTME: ComputeBackendNeeds reports which backends a graph's agent nodes (and
// ABOUTME: its subgraph children) need, so a run without a native client can start.
package pipeline

import "testing"

func agentGraph(backends ...string) *Graph {
	g := NewGraph("needs")
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond", Handler: "start"})
	for i, b := range backends {
		n := &Node{ID: string(rune('A' + i)), Shape: "box", Handler: "codergen", Attrs: map[string]string{}}
		if b != "" {
			n.Attrs["backend"] = b
		}
		g.AddNode(n)
	}
	g.AddNode(&Node{ID: "e", Shape: "Msquare", Handler: "exit"})
	return g
}

func TestComputeBackendNeeds(t *testing.T) {
	cases := []struct {
		name       string
		g          *Graph
		subgraphs  map[string]*Graph
		global     string
		wantNative bool
		want       BackendNeeds
	}{
		{"all auto", agentGraph("auto", "auto"), nil, "", false, BackendNeeds{Auto: true}},
		{"auto + claude-code", agentGraph("auto", "claude-code"), nil, "", false, BackendNeeds{Auto: true, ClaudeCode: true}},
		{"one explicit native", agentGraph("auto", "native"), nil, "", true, BackendNeeds{Auto: true, Native: true}},
		{"codergen alias", agentGraph("codergen"), nil, "", true, BackendNeeds{Native: true}},
		{"unset, no global", agentGraph(""), nil, "", true, BackendNeeds{Native: true}},
		{"unset, global claude-code", agentGraph(""), nil, "claude-code", false, BackendNeeds{ClaudeCode: true}},
		{"unset, global acp", agentGraph(""), nil, "acp", false, BackendNeeds{ACP: true}},
		{"subgraph child needs native", agentGraph("auto"), map[string]*Graph{"child": agentGraph("native")}, "", true, BackendNeeds{Auto: true, Native: true}},
		{"no agent nodes", agentGraph(), nil, "", false, BackendNeeds{}},
	}
	for _, c := range cases {
		got := ComputeBackendNeeds(c.g, c.subgraphs, c.global)
		if got != c.want || got.NeedsNativeClient() != c.wantNative {
			t.Errorf("%s: got %+v (native=%v), want %+v (native=%v)", c.name, got, got.NeedsNativeClient(), c.want, c.wantNative)
		}
	}
}
