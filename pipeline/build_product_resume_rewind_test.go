// ABOUTME: #651 on the REAL build_product.dip: a Setup failure aborts at AbortRun; `tracker -r`
// ABOUTME: with the cause fixed rewinds to Setup and proceeds to SpecLint instead of re-aborting.
package pipeline

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// runBP651 drives the real build_product graph with a bp640Sim against a
// persistent checkpoint so a second call is a genuine resume.
func runBP651(t *testing.T, g *Graph, sim *bp640Sim, cpPath string, opts ...EngineOption) (*EngineResult, error, []PipelineEvent) {
	t.Helper()
	var events []PipelineEvent
	sim.seen = map[string]int{}
	sim.visits = nil
	exec := func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
		sim.mu.Lock()
		defer sim.mu.Unlock()
		n := sim.seen[node.ID]
		sim.seen[node.ID]++
		sim.visits = append(sim.visits, node.ID)
		if fn, ok := sim.script[node.ID]; ok {
			return fn(n), nil
		}
		switch node.ID {
		case "AbortRun", "SpecForgeFailed":
			return bpFail("BUILD ABORTED"), nil
		case "SpecLint":
			// Stop the sim right after the node we want to prove is reached:
			// a failing SpecLint routes into the spec-forge loop, which is not
			// under test here, and the run ends fail without touching a human.
			return bpFail("sim stop"), nil
		}
		return bpOK(""), nil
	}
	reg := NewHandlerRegistry()
	for _, name := range []string{"start", "exit", "codergen", "wait.human", "conditional", "parallel", "parallel.fan_in", "tool"} {
		reg.Register(&testHandler{name: name, executeFn: exec})
	}
	all := append([]EngineOption{
		WithCheckpointPath(cpPath),
		WithPipelineEventHandler(PipelineEventHandlerFunc(func(e PipelineEvent) { events = append(events, e) })),
	}, opts...)
	res, err := NewEngine(g, reg, all...).Run(context.Background())
	return res, err, events
}

// TestBuildProduct651ResumeRewindsSetupAbort: Setup exits 1 → `Setup ->
// AbortRun when ctx.outcome = fail` → AbortRun exits 1 → run halts `fail`.
// Pre-#651 `tracker -r` re-entered AbortRun and failed again ("start a new
// run"). Now the resume rewinds to Setup; with the cause fixed it succeeds and
// the run proceeds to SpecLint — AbortRun is never re-entered.
func TestBuildProduct651ResumeRewindsSetupAbort(t *testing.T) {
	g := loadBuildProduct(t)
	cpPath := filepath.Join(t.TempDir(), "checkpoint.json")
	setupOK := false
	sim := &bp640Sim{script: map[string]func(int) Outcome{
		"Setup": func(int) Outcome {
			if setupOK {
				return bpOK("")
			}
			return bpFail("ERROR: SPEC.md not found")
		},
	}}

	res, err, _ := runBP651(t, g, sim, cpPath)
	if err == nil || res == nil || res.Status != OutcomeFail || !sim.visited("AbortRun") || sim.visited("SpecLint") {
		t.Fatalf("first run must abort at AbortRun before SpecLint: err=%v status=%v visits=%v", err, statusOf(res), sim.visits)
	}
	cp, lerr := LoadCheckpoint(cpPath)
	if lerr != nil {
		t.Fatal(lerr)
	}
	if cp.CurrentNode != "AbortRun" || cp.HaltedAt != "AbortRun" || !cp.IsCompleted("Setup") {
		t.Fatalf("checkpoint after abort: current=%q halted=%q setupDone=%v", cp.CurrentNode, cp.HaltedAt, cp.IsCompleted("Setup"))
	}
	if rec, ok := cp.GetFallbackOrigin("AbortRun"); !ok || rec.Node != "Setup" || rec.Kind != FallbackOriginFailEdge {
		t.Fatalf("FallbackOrigin[AbortRun] = %+v ok=%v, want Setup via fail_edge", rec, ok)
	}

	// Operator fixes the cause (creates SPEC.md) and resumes.
	setupOK = true
	res, err, events := runBP651(t, g, sim, cpPath)
	if !sim.visited("Setup") || !sim.visited("SpecLint") {
		t.Fatalf("resume must re-run Setup and reach SpecLint: err=%v visits=%v", err, sim.visits)
	}
	if sim.visited("AbortRun") {
		t.Fatalf("resume must not re-enter AbortRun: visits=%v", sim.visits)
	}
	if i := strings.Index(strings.Join(sim.visits, ","), "Setup,SpecLint"); i < 0 {
		t.Errorf("Setup must flow straight into SpecLint on resume: visits=%v", sim.visits)
	}
	rewound := eventsOfType(events, EventResumeRewound)
	if len(rewound) != 1 || rewound[0].NodeID != "Setup" || rewound[0].Decision == nil || rewound[0].Decision.EdgeFrom != "AbortRun" {
		t.Fatalf("want one resume_rewound AbortRun -> Setup, got %+v", rewound)
	}
	_ = res
}

// TestBuildProduct651NoRewindKeepsAbort: --resume-no-rewind re-enters AbortRun
// and the run fails again exactly as before (#651 opt-out).
func TestBuildProduct651NoRewindKeepsAbort(t *testing.T) {
	g := loadBuildProduct(t)
	cpPath := filepath.Join(t.TempDir(), "checkpoint.json")
	sim := &bp640Sim{script: map[string]func(int) Outcome{
		"Setup": func(int) Outcome { return bpFail("ERROR: SPEC.md not found") },
	}}
	if res, err, _ := runBP651(t, g, sim, cpPath); err == nil || res.Status != OutcomeFail {
		t.Fatalf("first run: err=%v", err)
	}
	sim.script["Setup"] = func(int) Outcome { return bpOK("") }
	res, err, events := runBP651(t, g, sim, cpPath, WithResumePolicy(ResumePolicy{NoRewind: true}))
	if err == nil || res == nil || res.Status != OutcomeFail {
		t.Fatalf("exact resume at AbortRun must fail again: err=%v visits=%v", err, sim.visits)
	}
	if sim.visited("Setup") || sim.visited("SpecLint") || !sim.visited("AbortRun") {
		t.Errorf("exact resume must only re-run AbortRun: visits=%v", sim.visits)
	}
	if n := len(eventsOfType(events, EventResumeRewound)); n != 0 {
		t.Errorf("no resume_rewound under --resume-no-rewind, got %d", n)
	}
}
