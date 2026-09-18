// ABOUTME: Pins tracker.ParseSource — the graph-in-hand seam for embedders that mutate
// ABOUTME: node attrs before NewEngineFromGraph — resolving built-in sidecars from the embed FS.
package tracker

import (
	"strings"
	"testing"

	"github.com/2389-research/tracker/pipeline"
)

// An embedder that parses a built-in's text itself (to tier models per node
// before NewEngineFromGraph) must get the same graph NewEngine builds: every
// prompt_file / command_file resolved from the embed FS, and the graph marked
// as the built-in so its workflow_dir tree is materialized at run time.
func TestParseSource_BuiltinResolvesSidecarsAndMarksGraph(t *testing.T) {
	src, info, err := OpenWorkflow("build_product")
	if err != nil {
		t.Fatal(err)
	}
	graph, err := ParseSource(string(src), FormatDip, WithSource(info.Ref()))
	if err != nil {
		t.Fatalf("ParseSource: %v", err)
	}
	impl := graph.Nodes["Implement"]
	if impl == nil {
		t.Fatal("Implement node missing")
	}
	if p := impl.Attrs["prompt"]; !strings.Contains(p, "Contract tests") {
		t.Errorf("Implement prompt_file not resolved from the embed FS: %.80q", p)
	}
	if c := graph.Nodes["Setup"].Attrs["tool_command"]; !strings.Contains(c, "LIB=") {
		t.Errorf("Setup command_file not resolved: %.80q", c)
	}
	if got := graph.Attrs[pipeline.WorkflowBuiltinAttr]; got != "build_product" {
		t.Errorf("graph not marked as the built-in: %q", got)
	}
	// The graph is the embedder's to mutate: a per-node model override lands on
	// the attr the engine reads (node attr beats graph default beats Config).
	impl.Attrs["llm_model"] = "gemini-3.6-flash"
	impl.Attrs["llm_provider"] = "gemini"
	if graph.Nodes["Implement"].Attrs["llm_model"] != "gemini-3.6-flash" {
		t.Error("node attr mutation not visible on the graph")
	}
}

// Unanchored raw text of a built-in still resolves via the byte-identical
// fallback, but an unknown built-in name is an error, not a cwd guess.
func TestParseSource_RawBuiltinTextAndUnknownName(t *testing.T) {
	src, _, err := OpenWorkflow("ask_and_execute")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseSource(string(src), ""); err != nil {
		t.Fatalf("raw built-in text: %v", err)
	}
	if _, err := ParseSource(string(src), FormatDip, WithSource(SourceRef{Builtin: "no_such_workflow"})); err == nil {
		t.Fatal("expected an error for an unknown built-in name")
	}
	if _, err := ParseSource("digraph {}", "bogus"); err == nil || !strings.Contains(err.Error(), "unknown format") {
		t.Fatalf("expected unknown-format error, got %v", err)
	}
}
