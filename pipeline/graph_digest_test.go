// ABOUTME: Tests GraphDigest: stable across map order, sensitive to any node,
// ABOUTME: edge, graph attribute, or subgraph change (incl. resolved sidecars).
package pipeline

import "testing"

func digestFixture(prompt, edgeLabel string) *Graph {
	g := NewGraph("d")
	g.Attrs["goal"] = "g"
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond"})
	g.AddNode(&Node{ID: "a", Shape: "box", Attrs: map[string]string{"prompt": prompt, "llm_model": "m"}})
	g.AddNode(&Node{ID: "e", Shape: "Msquare"})
	g.AddEdge(&Edge{From: "s", To: "a"})
	g.AddEdge(&Edge{From: "a", To: "e", Label: edgeLabel})
	return g
}

func TestGraphDigest_Sensitive(t *testing.T) {
	sub := func(cmd string) map[string]*Graph {
		c := digestFixture("child", "")
		c.Nodes["a"].Attrs["tool_command"] = cmd
		return map[string]*Graph{"child": c}
	}
	base := GraphDigest(digestFixture("do it", "go"), sub("true"))
	if base == "" || base != GraphDigest(digestFixture("do it", "go"), sub("true")) {
		t.Fatal("digest not stable for identical graphs")
	}
	for name, d := range map[string]string{
		// A prompt_file / command_file sidecar is resolved into these attrs at
		// load time, so editing the sidecar changes them.
		"node prompt (sidecar)": GraphDigest(digestFixture("do it now", "go"), sub("true")),
		"edge label":            GraphDigest(digestFixture("do it", "stop"), sub("true")),
		"subgraph command":      GraphDigest(digestFixture("do it", "go"), sub("false")),
		"no subgraphs":          GraphDigest(digestFixture("do it", "go"), nil),
	} {
		if d == base {
			t.Errorf("%s change did not change the digest", name)
		}
	}
	g := digestFixture("do it", "go")
	g.Attrs["params.change"] = "x"
	if GraphDigest(g, sub("true")) == base {
		t.Error("graph attribute change did not change the digest")
	}
}

// The workflow's directory is spelled differently from another cwd or through
// a symlink; it is not part of what the run does.
func TestGraphDigest_IgnoresWorkflowDir(t *testing.T) {
	a, b := digestFixture("do it", "go"), digestFixture("do it", "go")
	a.Attrs["workflow_dir"] = "/tmp/proj"
	b.Attrs["workflow_dir"] = "/private/tmp/proj"
	if GraphDigest(a, nil) != GraphDigest(b, nil) {
		t.Error("workflow_dir spelling changed the digest")
	}
}
