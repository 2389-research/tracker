// ABOUTME: Contract tests for writable_paths_mode parsing/validation (#648 spec C1)
// ABOUTME: and the run.json jail_degraded_nodes record (spec C7).
package pipeline

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/2389-research/dippin-lang/ir"
	"pgregory.net/rapid"
)

// C1: exactly "require" and "prefer" validate; everything else — including
// case and whitespace variants and the present-but-empty string — is rejected.
func TestWritablePathsMode_C1_ExactMatchOnly_Rapid(t *testing.T) {
	if err := ValidateWritablePathsMode(WritablePathsModeRequire); err != nil {
		t.Fatalf("require rejected: %v", err)
	}
	if err := ValidateWritablePathsMode(WritablePathsModePrefer); err != nil {
		t.Fatalf("prefer rejected: %v", err)
	}
	for _, bad := range []string{"", "Prefer", "prefer ", " prefer", "PREFER", "Require", "require\n", "preferred", "req"} {
		if err := ValidateWritablePathsMode(bad); err == nil {
			t.Errorf("%q accepted; must fail closed", bad)
		}
	}
	rapid.Check(t, func(rt *rapid.T) {
		s := rapid.String().Filter(func(s string) bool {
			return s != WritablePathsModeRequire && s != WritablePathsModePrefer
		}).Draw(rt, "mode")
		if err := ValidateWritablePathsMode(s); err == nil {
			rt.Fatalf("%q accepted; only the two exact constants are legal", s)
		}
	})
}

// C1 (accessor): absent normalizes to require; prefer is carried verbatim.
func TestWritablePathsMode_C1_AccessorDefaultsToRequire(t *testing.T) {
	n := &Node{ID: "A", Attrs: map[string]string{"writable_paths": ".git/**"}}
	if got := n.AgentConfig(nil).WritablePathsMode; got != WritablePathsModeRequire {
		t.Fatalf("absent mode = %q, want require", got)
	}
	n.Attrs[AttrWritablePathsMode] = WritablePathsModePrefer
	if got := n.AgentConfig(nil).WritablePathsMode; got != WritablePathsModePrefer {
		t.Fatalf("prefer mode = %q, want prefer", got)
	}
}

// C1 (validateGraph): a bad value fails run-path validation naming the node,
// for every graph source (not gated on DippinValidated).
func TestWritablePathsMode_C1_ValidateGraphRejectsTypo(t *testing.T) {
	for _, dippinValidated := range []bool{false, true} {
		g := NewGraph("t")
		g.DippinValidated = dippinValidated
		g.AddNode(&Node{ID: "start", Shape: "Mdiamond"})
		g.AddNode(&Node{ID: "Jailed", Shape: "box", Attrs: map[string]string{
			"writable_paths":      ".git/**",
			AttrWritablePathsMode: "Prefer",
		}})
		g.AddNode(&Node{ID: "exit", Shape: "Msquare"})
		g.AddEdge(&Edge{From: "start", To: "Jailed"})
		g.AddEdge(&Edge{From: "Jailed", To: "exit"})
		err := Validate(g)
		if err == nil {
			t.Fatalf("DippinValidated=%v: Validate accepted writable_paths_mode: Prefer", dippinValidated)
		}
		if !strings.Contains(err.Error(), `node "Jailed"`) || !strings.Contains(err.Error(), "writable_paths_mode") {
			t.Fatalf("error does not name the node and attr: %v", err)
		}
	}
}

// C1 (adapter): the params passthrough delivers the mode; an invalid value is
// a FromDippinIR error naming the node.
func TestWritablePathsMode_C1_AdapterParamsPassthrough(t *testing.T) {
	wf := func(mode string) *ir.Workflow {
		return &ir.Workflow{
			Name: "m", Start: "s", Exit: "e",
			Nodes: []*ir.Node{
				{ID: "s", Kind: ir.NodeAgent, Config: ir.AgentConfig{}},
				{ID: "FinalCommit", Kind: ir.NodeAgent, Config: ir.AgentConfig{
					Prompt:        "commit",
					WritablePaths: []string{".git/**", ".ai/**"},
					Params:        map[string]string{"writable_paths_mode": mode},
				}},
				{ID: "e", Kind: ir.NodeAgent, Config: ir.AgentConfig{}},
			},
			Edges: []*ir.Edge{{From: "s", To: "FinalCommit"}, {From: "FinalCommit", To: "e"}},
		}
	}
	g, err := FromDippinIR(wf("prefer"))
	if err != nil {
		t.Fatalf("FromDippinIR(prefer) = %v", err)
	}
	cfg := g.Nodes["FinalCommit"].AgentConfig(nil)
	if !cfg.WritablePathsSet || cfg.WritablePathsMode != WritablePathsModePrefer {
		t.Fatalf("cfg = %+v, want WritablePathsSet + prefer", cfg)
	}
	for _, bad := range []string{"Prefer", "prefer ", "", "optional"} {
		_, err := FromDippinIR(wf(bad))
		if err == nil {
			t.Errorf("FromDippinIR accepted writable_paths_mode %q", bad)
			continue
		}
		if !strings.Contains(err.Error(), "node FinalCommit") {
			t.Errorf("error for %q does not name the node: %v", bad, err)
		}
	}
}

// C7: a jail_degraded log line lands on run.json as jail_degraded_nodes and
// nodes[].jail = "degraded"; repeated attempts de-duplicate; a jailed or
// undeclared node carries no jail field.
func TestRunManifest_C7_JailDegradedNodes(t *testing.T) {
	runDir := filepath.Join(t.TempDir(), "r1")
	writeLog(t, runDir,
		`{"ts":"2026-01-01T00:00:00.000Z","source":"pipeline","type":"pipeline_started","run_id":"r1"}`,
		`{"ts":"2026-01-01T00:00:01.000Z","source":"pipeline","type":"stage_started","node_id":"Build","node_kind":"codergen","attempt_no":1}`,
		`{"ts":"2026-01-01T00:00:02.000Z","source":"pipeline","type":"stage_completed","node_id":"Build"}`,
		`{"ts":"2026-01-01T00:00:03.000Z","source":"pipeline","type":"stage_started","node_id":"FinalCommit","node_kind":"codergen","attempt_no":1}`,
		`{"ts":"2026-01-01T00:00:03.500Z","source":"pipeline","type":"jail_degraded","node_id":"FinalCommit","jail_mode":"prefer","jail_reason":"landlock unavailable","jail_declared_globs":[".git/**"]}`,
		`{"ts":"2026-01-01T00:00:04.000Z","source":"pipeline","type":"stage_retrying","node_id":"FinalCommit","attempt_no":2}`,
		`{"ts":"2026-01-01T00:00:04.500Z","source":"pipeline","type":"jail_degraded","node_id":"FinalCommit","jail_mode":"prefer","jail_reason":"landlock unavailable","jail_declared_globs":[".git/**"]}`,
		`{"ts":"2026-01-01T00:00:05.000Z","source":"pipeline","type":"pipeline_completed","terminal_status":"success"}`,
	)
	m, err := AssembleRunManifest(runDir, "r1")
	if err != nil {
		t.Fatalf("AssembleRunManifest: %v", err)
	}
	if len(m.JailDegradedNodes) != 1 || m.JailDegradedNodes[0] != "FinalCommit" {
		t.Fatalf("JailDegradedNodes = %v, want [FinalCommit] (de-duplicated)", m.JailDegradedNodes)
	}
	byID := map[string]NodeSummary{}
	for _, n := range m.Nodes {
		byID[n.ID] = n
	}
	if byID["FinalCommit"].Jail != JailDegraded {
		t.Errorf("FinalCommit.Jail = %q, want %q", byID["FinalCommit"].Jail, JailDegraded)
	}
	if byID["Build"].Jail != "" {
		t.Errorf("Build.Jail = %q, want empty", byID["Build"].Jail)
	}
	if m.EventCounts["jail_degraded"] != 2 {
		t.Errorf("event_counts[jail_degraded] = %d, want 2", m.EventCounts["jail_degraded"])
	}
}
