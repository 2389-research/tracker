// ABOUTME: Tests for resume rewind (#651): a run halted at a fail-routed terminal resumes at the
// ABOUTME: node that failed, --from re-enters an explicit node, --resume-no-rewind keeps the old behavior.
package pipeline

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// rewindGraph builds Start -> A(tool) -> B -> Done with a graph-level
// on_failure that points at a fail-closed Abort terminal (Abort -> Done, exit 1
// shape). onFailEdge additionally wires `A -> Abort when ctx.outcome = fail`
// (build_product's Setup shape) instead of relying on the strict-failure
// fallback.
func rewindGraph(onFailEdge bool) *Graph {
	g := NewGraph("rewind")
	g.Attrs["fallback_target"] = "Abort"
	g.AddNode(&Node{ID: "Start", Shape: "Mdiamond"})
	g.AddNode(&Node{ID: "A", Shape: "parallelogram"}) // tool
	g.AddNode(&Node{ID: "B", Shape: "parallelogram"})
	g.AddNode(&Node{ID: "Abort", Shape: "parallelogram"})
	g.AddNode(&Node{ID: "Done", Shape: "Msquare"})
	g.AddEdge(&Edge{From: "Start", To: "A"})
	if onFailEdge {
		g.AddEdge(&Edge{From: "A", To: "Abort", Condition: "ctx.outcome = fail"})
	}
	g.AddEdge(&Edge{From: "A", To: "B"})
	g.AddEdge(&Edge{From: "B", To: "Done"})
	g.AddEdge(&Edge{From: "Abort", To: "Done"})
	return g
}

// rewindRun drives rewindGraph with a scripted A outcome. Abort always fails
// (exit 1). Returns the visit order, the events, the result, and the error.
func rewindRun(t *testing.T, g *Graph, cpPath string, aFails bool, opts ...EngineOption) ([]string, []PipelineEvent, *EngineResult, error) {
	t.Helper()
	var visits []string
	var events []PipelineEvent
	reg := newTestRegistry()
	reg.Register(&testHandler{name: "tool", executeFn: func(_ context.Context, node *Node, _ *PipelineContext) (Outcome, error) {
		visits = append(visits, node.ID)
		switch node.ID {
		case "A":
			if aFails {
				return Outcome{Status: OutcomeFail, FailureReason: "A: exit status 1", ContextUpdates: map[string]string{"outcome": "fail"}}, nil
			}
		case "Abort":
			return Outcome{Status: OutcomeFail, FailureReason: "BUILD ABORTED", ContextUpdates: map[string]string{"outcome": "fail"}}, nil
		}
		return Outcome{Status: OutcomeSuccess, ContextUpdates: map[string]string{"outcome": "success"}}, nil
	}})
	all := append([]EngineOption{
		WithCheckpointPath(cpPath),
		WithPipelineEventHandler(PipelineEventHandlerFunc(func(evt PipelineEvent) { events = append(events, evt) })),
	}, opts...)
	res, err := NewEngine(g, reg, all...).Run(context.Background())
	return visits, events, res, err
}

func eventsOfType(events []PipelineEvent, typ PipelineEventType) []PipelineEvent {
	var out []PipelineEvent
	for _, e := range events {
		if e.Type == typ {
			out = append(out, e)
		}
	}
	return out
}

func loadCP(t *testing.T, path string) *Checkpoint {
	t.Helper()
	cp, err := LoadCheckpoint(path)
	if err != nil {
		t.Fatalf("LoadCheckpoint: %v", err)
	}
	return cp
}

// failAtAbort runs the graph with A failing and asserts the run halts at the
// Abort terminal with provenance + halt marker persisted.
func failAtAbort(t *testing.T, g *Graph, cpPath string, wantKind FallbackOriginKind) {
	t.Helper()
	visits, _, res, err := rewindRun(t, g, cpPath, true)
	if err == nil || res == nil || res.Status != OutcomeFail {
		t.Fatalf("first run must fail at Abort: err=%v visits=%v", err, visits)
	}
	if visits[len(visits)-1] != "Abort" {
		t.Fatalf("first run must halt at Abort: visits=%v", visits)
	}
	cp := loadCP(t, cpPath)
	if cp.CurrentNode != "Abort" || cp.HaltedAt != "Abort" {
		t.Fatalf("checkpoint after halt: CurrentNode=%q HaltedAt=%q, want Abort/Abort", cp.CurrentNode, cp.HaltedAt)
	}
	rec, ok := cp.GetFallbackOrigin("Abort")
	if !ok || rec.Node != "A" || rec.Kind != wantKind || rec.Outcome != "fail" {
		t.Fatalf("FallbackOrigin[Abort] = %+v (ok=%v), want node A kind %s", rec, ok, wantKind)
	}
	if !strings.Contains(rec.Reason, "exit status 1") {
		t.Errorf("origin reason %q should carry A's failure reason", rec.Reason)
	}
	if !cp.IsCompleted("A") {
		t.Fatal("A must be in CompletedNodes after its failure routed away")
	}
}

// TestResumeRewindsPastFailClosedTerminal_FailEdge: A -> Abort via an explicit
// `when ctx.outcome = fail` edge (build_product's Setup shape). Resume with A
// fixed re-runs A, then B, then Done — Abort is never re-entered.
func TestResumeRewindsPastFailClosedTerminal_FailEdge(t *testing.T) {
	cpPath := filepath.Join(t.TempDir(), "checkpoint.json")
	g := rewindGraph(true)
	failAtAbort(t, g, cpPath, FallbackOriginFailEdge)

	visits, events, res, err := rewindRun(t, g, cpPath, false)
	if err != nil || res == nil || res.Status != OutcomeSuccess {
		t.Fatalf("resume must succeed: err=%v status=%v visits=%v", err, statusOf(res), visits)
	}
	if want := []string{"A", "B"}; strings.Join(visits, ",") != strings.Join(want, ",") {
		t.Fatalf("resume tool visits = %v, want %v (A retried, Abort never re-entered)", visits, want)
	}
	rewound := eventsOfType(events, EventResumeRewound)
	if len(rewound) != 1 {
		t.Fatalf("want exactly one resume_rewound event, got %d", len(rewound))
	}
	d := rewound[0].Decision
	if rewound[0].NodeID != "A" || d == nil || d.EdgeFrom != "Abort" || d.EdgeTo != "A" || d.RewindReason == "" || d.OutcomeStatus != "fail" {
		t.Errorf("resume_rewound payload = node=%q decision=%+v, want node A from Abort with a reason", rewound[0].NodeID, d)
	}
	if len(d.ClearedNodes) == 0 || !strings.Contains(strings.Join(d.ClearedNodes, ","), "A") || !strings.Contains(strings.Join(d.ClearedNodes, ","), "Abort") {
		t.Errorf("cleared nodes %v must include the origin A and the halted terminal Abort", d.ClearedNodes)
	}
	if !strings.Contains(rewound[0].Message, "rewinding to \"A\"") {
		t.Errorf("message should name the rewind target: %q", rewound[0].Message)
	}
	// The event must precede any node execution so a consumer can explain
	// why A runs again.
	var sawRewind bool
	for _, e := range events {
		if e.Type == EventResumeRewound {
			sawRewind = true
		}
		if e.Type == EventStageStarted && e.NodeID == "A" && !sawRewind {
			t.Error("stage_started A fired before resume_rewound")
		}
	}
	cp := loadCP(t, cpPath)
	if cp.HaltedAt != "" {
		t.Errorf("HaltedAt must be cleared after a successful resume, got %q", cp.HaltedAt)
	}
	if _, ok := cp.GetFallbackOrigin("Abort"); ok {
		t.Error("consumed FallbackOrigin[Abort] must be dropped so a later resume cannot rewind on stale provenance")
	}
	for _, id := range []string{"A", "B"} {
		if !cp.IsCompleted(id) {
			t.Errorf("%s should be completed after the resumed run", id)
		}
	}
	if cp.IsCompleted("Abort") {
		t.Error("Abort must not be completed after a rewound resume")
	}
}

// TestResumeRewindsPastFailClosedTerminal_StrictFallback: A has only an
// unconditional edge, so its failure reaches Abort via the graph-level
// on_failure (strictFailureFallback). Same rewind; the fallback latch on A is
// re-armed so a second genuine failure can still escalate.
func TestResumeRewindsPastFailClosedTerminal_StrictFallback(t *testing.T) {
	cpPath := filepath.Join(t.TempDir(), "checkpoint.json")
	g := rewindGraph(false)
	failAtAbort(t, g, cpPath, FallbackOriginStrictFailure)
	if cp := loadCP(t, cpPath); !cp.IsFallbackTaken("A") {
		t.Fatal("precondition: A's one-shot fallback latch is set after the strict-failure route")
	}

	visits, events, res, err := rewindRun(t, g, cpPath, false)
	if err != nil || res == nil || res.Status != OutcomeSuccess {
		t.Fatalf("resume must succeed: err=%v status=%v visits=%v", err, statusOf(res), visits)
	}
	if want := "A,B"; strings.Join(visits, ",") != want {
		t.Fatalf("resume tool visits = %v, want %s", visits, want)
	}
	if n := len(eventsOfType(events, EventResumeRewound)); n != 1 {
		t.Fatalf("want one resume_rewound, got %d", n)
	}
	if cp := loadCP(t, cpPath); cp.IsFallbackTaken("A") {
		t.Error("rewind must re-arm A's fallback latch so a fresh failure can escalate again")
	}
}

// TestResumeRewind_LatchReArmed_SecondFailureStillAborts: after a rewind, A
// failing AGAIN routes to Abort (the latch was re-armed) and halts — the
// rewind never turns a fail-closed terminal into a silent pass.
func TestResumeRewind_LatchReArmed_SecondFailureStillAborts(t *testing.T) {
	cpPath := filepath.Join(t.TempDir(), "checkpoint.json")
	g := rewindGraph(false)
	failAtAbort(t, g, cpPath, FallbackOriginStrictFailure)
	visits, events, res, err := rewindRun(t, g, cpPath, true) // cause NOT fixed
	if err == nil || res == nil || res.Status != OutcomeFail {
		t.Fatalf("second failure must still fail: err=%v visits=%v", err, visits)
	}
	// A first, then only Abort (once, or twice while #650's self-fallback
	// echo is still on main — never a third node).
	if len(visits) < 2 || len(visits) > 3 || visits[0] != "A" {
		t.Fatalf("visits = %v, want A then Abort (rewind, retry, re-abort)", visits)
	}
	for _, v := range visits[1:] {
		if v != "Abort" {
			t.Fatalf("visits = %v, want A then only Abort", visits)
		}
	}
	if n := len(eventsOfType(events, EventResumeRewound)); n != 1 {
		t.Errorf("want one resume_rewound, got %d", n)
	}
	if cp := loadCP(t, cpPath); cp.HaltedAt != "Abort" {
		t.Errorf("a re-halt must re-record HaltedAt=Abort, got %q", cp.HaltedAt)
	}
}

// TestResumeNoRewindKeepsOldBehavior: --resume-no-rewind re-enters the run at
// the terminal, which fails again (today's behavior), and A is not re-run.
func TestResumeNoRewindKeepsOldBehavior(t *testing.T) {
	cpPath := filepath.Join(t.TempDir(), "checkpoint.json")
	g := rewindGraph(true)
	failAtAbort(t, g, cpPath, FallbackOriginFailEdge)

	visits, events, res, err := rewindRun(t, g, cpPath, false, WithResumePolicy(ResumePolicy{NoRewind: true}))
	if err == nil || res == nil || res.Status != OutcomeFail {
		t.Fatalf("exact resume at Abort must fail again: err=%v status=%v visits=%v", err, statusOf(res), visits)
	}
	if strings.Join(visits, ",") != "Abort" {
		t.Fatalf("visits = %v, want only Abort (no rewind, terminal re-executed — not skipped to Done)", visits)
	}
	if n := len(eventsOfType(events, EventResumeRewound)); n != 0 {
		t.Errorf("no resume_rewound expected under NoRewind, got %d", n)
	}
	// A `when fail` edge is authored routing, not a fallback: the halt copy
	// must not say "reached from" (#650 contract) but does name the origin
	// kind-aware — "routed from A via fail edge" (#654).
	if err == nil || !strings.Contains(err.Error(), `"Abort" (routed from "A" via fail edge) failed`) {
		t.Errorf("error should name the terminal and its fail-edge origin: %v", err)
	}
}

// TestResumeFromReRunsNodeAndDownstream: a run that completed A and B (and
// Done) resumed with --from A re-runs A and B.
func TestResumeFromReRunsNodeAndDownstream(t *testing.T) {
	cpPath := filepath.Join(t.TempDir(), "checkpoint.json")
	g := rewindGraph(true)
	if visits, _, res, err := rewindRun(t, g, cpPath, false); err != nil || res.Status != OutcomeSuccess || strings.Join(visits, ",") != "A,B" {
		t.Fatalf("clean run: err=%v visits=%v", err, visits)
	}
	visits, events, res, err := rewindRun(t, g, cpPath, false, WithResumePolicy(ResumePolicy{From: "A"}))
	if err != nil || res == nil || res.Status != OutcomeSuccess {
		t.Fatalf("--from A resume: err=%v status=%v", err, statusOf(res))
	}
	if strings.Join(visits, ",") != "A,B" {
		t.Fatalf("--from A must re-run A and B, got %v", visits)
	}
	rewound := eventsOfType(events, EventResumeRewound)
	if len(rewound) != 1 || rewound[0].NodeID != "A" || rewound[0].Decision == nil || !strings.Contains(rewound[0].Decision.RewindReason, "--from") {
		t.Fatalf("want one resume_rewound to A with an explicit --from reason, got %+v", rewound)
	}
	// --from B on the same completed run re-runs only B.
	visits, _, _, err = rewindRun(t, g, cpPath, false, WithResumePolicy(ResumePolicy{From: "B"}))
	if err != nil || strings.Join(visits, ",") != "B" {
		t.Fatalf("--from B must re-run only B: err=%v visits=%v", err, visits)
	}
}

// TestResumeFromWinsOverAutomaticRewind: --from names a node other than the
// fail-routed origin; the explicit choice wins and no automatic rewind fires.
func TestResumeFromWinsOverAutomaticRewind(t *testing.T) {
	cpPath := filepath.Join(t.TempDir(), "checkpoint.json")
	g := rewindGraph(true)
	failAtAbort(t, g, cpPath, FallbackOriginFailEdge)
	visits, events, res, err := rewindRun(t, g, cpPath, false, WithResumePolicy(ResumePolicy{From: "Start"}))
	if err != nil || res == nil || res.Status != OutcomeSuccess {
		t.Fatalf("--from Start: err=%v status=%v visits=%v", err, statusOf(res), visits)
	}
	if strings.Join(visits, ",") != "A,B" {
		t.Fatalf("visits = %v, want A,B (whole run replayed from Start)", visits)
	}
	if n := len(eventsOfType(events, EventResumeRewound)); n != 1 {
		t.Errorf("exactly one resume_rewound (the explicit one), got %d", n)
	}
}

// TestResumeFromRefusesUnknownOrUnreachedNode: --from must name a node that
// exists AND was reached (completed or current) — otherwise fail closed before
// any node runs.
func TestResumeFromRefusesUnknownOrUnreachedNode(t *testing.T) {
	cpPath := filepath.Join(t.TempDir(), "checkpoint.json")
	g := rewindGraph(true)
	failAtAbort(t, g, cpPath, FallbackOriginFailEdge)
	cases := map[string]string{
		"Nope": "does not exist",
		"B":    "never reached",
	}
	for from, wantErr := range cases {
		visits, events, _, err := rewindRun(t, g, cpPath, false, WithResumePolicy(ResumePolicy{From: from}))
		if err == nil || !strings.Contains(err.Error(), wantErr) || !strings.Contains(err.Error(), from) {
			t.Errorf("--from %s: err=%v, want it to mention %q", from, err, wantErr)
		}
		if len(visits) != 0 {
			t.Errorf("--from %s: no node may run on a refused resume, got %v", from, visits)
		}
		if n := len(eventsOfType(events, EventPipelineStarted)); n != 0 {
			t.Errorf("--from %s: refusal must precede pipeline_started, got %d", from, n)
		}
	}
	// --from on a fresh (non-resume) run is refused too.
	_, _, _, err := rewindRun(t, g, filepath.Join(t.TempDir(), "fresh.json"), false, WithResumePolicy(ResumePolicy{From: "A"}))
	if err == nil || !strings.Contains(err.Error(), "requires a resumed run") {
		t.Errorf("--from without a checkpoint: err=%v", err)
	}
}

// TestResumeLegacyCheckpointWithoutProvenance: a pre-#651 checkpoint (no
// fallback_origin / halted_at) loads unchanged and resumes exactly at
// CurrentNode — no rewind, no crash.
func TestResumeLegacyCheckpointWithoutProvenance(t *testing.T) {
	cpPath := filepath.Join(t.TempDir(), "checkpoint.json")
	g := rewindGraph(true)
	legacy := map[string]any{
		"run_id":          "legacy123",
		"current_node":    "Abort",
		"completed_nodes": []string{"Start", "A"},
		"retry_counts":    map[string]int{},
		"context":         map[string]string{"outcome": "fail"},
		"edge_selections": map[string]string{"Start": "A", "A": "Abort"},
		"gate_states":     map[string]any{"Abort": map[string]any{"fallback_taken": true}},
	}
	data, _ := json.Marshal(legacy)
	if err := os.WriteFile(cpPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	cp := loadCP(t, cpPath)
	if _, ok := cp.GetFallbackOrigin("Abort"); ok || cp.HaltedAt != "" {
		t.Fatalf("legacy load must not synthesize provenance: %+v", cp)
	}
	visits, events, res, err := rewindRun(t, g, cpPath, false)
	if err == nil || res == nil || res.Status != OutcomeFail {
		t.Fatalf("legacy resume re-enters Abort and fails as before: err=%v status=%v visits=%v", err, statusOf(res), visits)
	}
	if strings.Join(visits, ",") != "Abort" {
		t.Fatalf("visits = %v, want only Abort", visits)
	}
	if n := len(eventsOfType(events, EventResumeRewound)); n != 0 {
		t.Errorf("no rewind on a legacy checkpoint, got %d", n)
	}
	// ...and the halt is now recorded, so the NEXT resume of this run still
	// cannot rewind (no origin), but does not crash either.
	if cp := loadCP(t, cpPath); cp.HaltedAt != "Abort" {
		t.Errorf("HaltedAt after the legacy re-halt = %q, want Abort", cp.HaltedAt)
	}
}

// TestResumeRewindSkipsHumanGateOrigin: a human gate whose "No"/abandon routed
// to the terminal is a decision — the automatic rewind must not re-ask it.
// The run resumes at the terminal (old behavior) with a warning naming why.
func TestResumeRewindSkipsHumanGateOrigin(t *testing.T) {
	cpPath := filepath.Join(t.TempDir(), "checkpoint.json")
	g := rewindGraph(true)
	g.Nodes["A"].Shape = "hexagon" // wait.human
	g.Nodes["A"].Handler = "wait.human"
	g2 := g
	var visits []string
	var events []PipelineEvent
	run := func(gateFails bool) (*EngineResult, error) {
		visits, events = nil, nil
		reg := newTestRegistry()
		reg.Register(&testHandler{name: "wait.human", executeFn: func(_ context.Context, node *Node, _ *PipelineContext) (Outcome, error) {
			visits = append(visits, node.ID)
			if gateFails {
				return Outcome{Status: OutcomeFail, ContextUpdates: map[string]string{"outcome": "fail"}}, nil
			}
			return Outcome{Status: OutcomeSuccess, ContextUpdates: map[string]string{"outcome": "success"}}, nil
		}})
		reg.Register(&testHandler{name: "tool", executeFn: func(_ context.Context, node *Node, _ *PipelineContext) (Outcome, error) {
			visits = append(visits, node.ID)
			if node.ID == "Abort" {
				return Outcome{Status: OutcomeFail, ContextUpdates: map[string]string{"outcome": "fail"}}, nil
			}
			return Outcome{Status: OutcomeSuccess, ContextUpdates: map[string]string{"outcome": "success"}}, nil
		}})
		return NewEngine(g2, reg, WithCheckpointPath(cpPath), WithPipelineEventHandler(PipelineEventHandlerFunc(func(e PipelineEvent) { events = append(events, e) }))).Run(context.Background())
	}
	if res, err := run(true); err == nil || res.Status != OutcomeFail || visits[len(visits)-1] != "Abort" {
		t.Fatalf("gate abandon must halt at Abort: err=%v visits=%v", err, visits)
	}
	res, err := run(false)
	if err == nil || res == nil || res.Status != OutcomeFail || strings.Join(visits, ",") != "Abort" {
		t.Fatalf("human-gate origin must NOT be rewound: err=%v visits=%v", err, visits)
	}
	if n := len(eventsOfType(events, EventResumeRewound)); n != 0 {
		t.Errorf("no rewind for a human-gate origin, got %d", n)
	}
	var warned bool
	for _, e := range eventsOfType(events, EventWarning) {
		if strings.Contains(e.Message, "not rewinding") && strings.Contains(e.Message, "human gate") && strings.Contains(e.Message, "--from") {
			warned = true
		}
	}
	if !warned {
		t.Error("expected a warning explaining the skipped rewind and pointing at --from")
	}
	// --from still reaches the gate: explicit operator instruction.
	visits, events = nil, nil
	reg := newTestRegistry()
	reg.Register(&testHandler{name: "wait.human", executeFn: func(_ context.Context, node *Node, _ *PipelineContext) (Outcome, error) {
		visits = append(visits, node.ID)
		return Outcome{Status: OutcomeSuccess, ContextUpdates: map[string]string{"outcome": "success"}}, nil
	}})
	reg.Register(&testHandler{name: "tool", executeFn: func(_ context.Context, node *Node, _ *PipelineContext) (Outcome, error) {
		visits = append(visits, node.ID)
		return Outcome{Status: OutcomeSuccess, ContextUpdates: map[string]string{"outcome": "success"}}, nil
	}})
	res, err = NewEngine(g2, reg, WithCheckpointPath(cpPath), WithResumePolicy(ResumePolicy{From: "A"})).Run(context.Background())
	if err != nil || res.Status != OutcomeSuccess || strings.Join(visits, ",") != "A,B" {
		t.Fatalf("--from A on a human-gate origin: err=%v visits=%v", err, visits)
	}
}

// TestResumeRewindSkipsParallelOrigin: a parallel origin may have partially
// completed branch work the parent checkpoint cannot see; the automatic
// rewind is conservative and skips it.
func TestResumeRewindSkipsParallelOrigin(t *testing.T) {
	cpPath := filepath.Join(t.TempDir(), "checkpoint.json")
	g := rewindGraph(true)
	g.Nodes["A"].Shape = "component" // parallel
	g.Nodes["A"].Handler = "parallel"
	g2 := g
	var visits []string
	var events []PipelineEvent
	mk := func(parallelFails bool) *HandlerRegistry {
		reg := newTestRegistry()
		fn := func(_ context.Context, node *Node, _ *PipelineContext) (Outcome, error) {
			visits = append(visits, node.ID)
			if node.ID == "Abort" || (node.ID == "A" && parallelFails) {
				return Outcome{Status: OutcomeFail, ContextUpdates: map[string]string{"outcome": "fail"}}, nil
			}
			return Outcome{Status: OutcomeSuccess, ContextUpdates: map[string]string{"outcome": "success"}}, nil
		}
		reg.Register(&testHandler{name: "parallel", executeFn: fn})
		reg.Register(&testHandler{name: "tool", executeFn: fn})
		return reg
	}
	if res, err := NewEngine(g2, mk(true), WithCheckpointPath(cpPath)).Run(context.Background()); err == nil || res.Status != OutcomeFail {
		t.Fatalf("parallel fail must halt at Abort: err=%v visits=%v", err, visits)
	}
	visits = nil
	res, err := NewEngine(g2, mk(false), WithCheckpointPath(cpPath), WithPipelineEventHandler(PipelineEventHandlerFunc(func(e PipelineEvent) { events = append(events, e) }))).Run(context.Background())
	if err == nil || res.Status != OutcomeFail || strings.Join(visits, ",") != "Abort" {
		t.Fatalf("parallel origin must NOT be rewound automatically: err=%v visits=%v", err, visits)
	}
	if n := len(eventsOfType(events, EventResumeRewound)); n != 0 {
		t.Errorf("no rewind for a parallel origin, got %d", n)
	}
}

// TestResumeCancelledRunDoesNotRewind: a run killed on the WAY to the
// fallback target (no halt recorded) resumes at that target as before — the
// escalation still gets to run.
func TestResumeCancelledRunDoesNotRewind(t *testing.T) {
	cpPath := filepath.Join(t.TempDir(), "checkpoint.json")
	g := rewindGraph(true)
	failAtAbort(t, g, cpPath, FallbackOriginFailEdge)
	// Simulate "killed before the terminal ran": strip the halt marker only.
	cp := loadCP(t, cpPath)
	cp.HaltedAt = ""
	cp.ClearCompleted("Abort")
	if err := SaveCheckpoint(cp, cpPath); err != nil {
		t.Fatal(err)
	}
	visits, events, _, _ := rewindRun(t, g, cpPath, false)
	if strings.Join(visits, ",") != "Abort" {
		t.Fatalf("no halt recorded → resume at CurrentNode: visits=%v", visits)
	}
	if n := len(eventsOfType(events, EventResumeRewound)); n != 0 {
		t.Errorf("no rewind without a halt marker, got %d", n)
	}
}

// TestCheckpointFallbackOriginRoundTrip pins the JSON shape and the self-route
// guard (#650's AbortRun -> AbortRun echo must not clobber the real origin).
func TestCheckpointFallbackOriginRoundTrip(t *testing.T) {
	cp := &Checkpoint{RunID: "r", CompletedNodes: []string{}, RetryCounts: map[string]int{}, Context: map[string]string{}}
	cp.RecordFallbackOrigin("Abort", "Setup", OutcomeFail, strings.Repeat("x", 1000), FallbackOriginFailEdge)
	cp.RecordFallbackOrigin("Abort", "Abort", OutcomeFail, "self", FallbackOriginStrictFailure) // ignored
	cp.RecordHalt("Abort")
	rec, ok := cp.GetFallbackOrigin("Abort")
	if !ok || rec.Node != "Setup" || len(rec.Reason) > maxFallbackReasonLen+len("…") {
		t.Fatalf("record = %+v ok=%v", rec, ok)
	}
	path := filepath.Join(t.TempDir(), "cp.json")
	if err := SaveCheckpoint(cp, path); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	for _, want := range []string{`"fallback_origin": "Setup"`, `"halted_at": "Abort"`, `"fallback_origin_kind": "fail_edge"`, `"fallback_origin_outcome": "fail"`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("checkpoint JSON missing %s:\n%s", want, raw)
		}
	}
	back := loadCP(t, path)
	if got, _ := back.GetFallbackOrigin("Abort"); got != rec || back.HaltedAt != "Abort" {
		t.Errorf("round-trip mismatch: %+v / %q", got, back.HaltedAt)
	}
	back.ClearFallbackOrigin("Abort")
	if _, ok := back.GetFallbackOrigin("Abort"); ok || back.FallbackOrigin("Abort") != "" {
		t.Error("ClearFallbackOrigin did not drop the entry")
	}
	// #650's node-only accessor reads the same state (one representation) but
	// hides fail_edge hops: an authored `when fail` edge is not a fallback, so
	// terminal copy must not say "reached from" for it (#650 contract).
	if cp.FallbackOrigin("Abort") != "" {
		t.Errorf("#650 FallbackOrigin(Abort) = %q for a fail_edge hop, want hidden", cp.FallbackOrigin("Abort"))
	}
	cp.RecordFallbackOrigin("Abort", "Setup", OutcomeFail, "r", FallbackOriginStrictFailure)
	if cp.FallbackOrigin("Abort") != "Setup" {
		t.Errorf("#650 FallbackOrigin(Abort) = %q for a strict_failure hop, want Setup", cp.FallbackOrigin("Abort"))
	}
	cp.RecordFallbackOrigin("Abort", "Setup", OutcomeFail, strings.Repeat("x", 1000), FallbackOriginFailEdge)
	// A #650-only record (node, no enrichment) still counts as provenance.
	legacy650 := &Checkpoint{}
	legacy650.SetFallbackOrigin("Abort", "Setup")
	if rec, ok := legacy650.GetFallbackOrigin("Abort"); !ok || rec.Node != "Setup" || rec.Kind != "" {
		t.Errorf("#650-only record = %+v ok=%v", rec, ok)
	}
	// A checkpoint that never fail-routed stays byte-identical (omitempty).
	clean := &Checkpoint{RunID: "r", CompletedNodes: []string{}, RetryCounts: map[string]int{}, Context: map[string]string{}}
	if err := SaveCheckpoint(clean, path); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(path)
	if strings.Contains(string(raw), "fallback_origin") || strings.Contains(string(raw), "halted_at") {
		t.Errorf("omitempty violated:\n%s", raw)
	}
}

// TestResumeRewindSharedEscalationNodeUsesLatestOrigin: Esc is shared — A
// reaches it via the graph-level fallback, later B reaches it via an explicit
// `when ctx.outcome = fail` edge, and it halts. The ordinary/fail-edge entry
// clears the stale A origin before recording B (#650 clear-before-set), so the
// rewind goes to B, not A.
func TestResumeRewindSharedEscalationNodeUsesLatestOrigin(t *testing.T) {
	cpPath := filepath.Join(t.TempDir(), "checkpoint.json")
	g := NewGraph("shared-esc")
	g.Attrs["fallback_target"] = "Esc"
	g.AddNode(&Node{ID: "Start", Shape: "Mdiamond"})
	g.AddNode(&Node{ID: "A", Shape: "parallelogram"})
	g.AddNode(&Node{ID: "B", Shape: "parallelogram"})
	g.AddNode(&Node{ID: "Esc", Shape: "parallelogram"})
	g.AddNode(&Node{ID: "Done", Shape: "Msquare"})
	g.AddEdge(&Edge{From: "Start", To: "A"})
	g.AddEdge(&Edge{From: "A", To: "B"}) // A fail → Esc via graph fallback
	g.AddEdge(&Edge{From: "B", To: "Esc", Condition: "ctx.outcome = fail"})
	g.AddEdge(&Edge{From: "B", To: "Done"})
	g.AddEdge(&Edge{From: "Esc", To: "B", Condition: "ctx.outcome = success"}) // first pass: Esc succeeds → B
	g.AddEdge(&Edge{From: "Esc", To: "Done", Condition: "ctx.outcome = fail"})

	var visits []string
	escCalls := 0
	run := func(aFails, bFails bool, opts ...EngineOption) (*EngineResult, error, []PipelineEvent) {
		visits = nil
		var events []PipelineEvent
		reg := newTestRegistry()
		reg.Register(&testHandler{name: "tool", executeFn: func(_ context.Context, node *Node, _ *PipelineContext) (Outcome, error) {
			visits = append(visits, node.ID)
			fail := func(r string) (Outcome, error) {
				return Outcome{Status: OutcomeFail, FailureReason: r, ContextUpdates: map[string]string{"outcome": "fail"}}, nil
			}
			switch node.ID {
			case "A":
				if aFails {
					return fail("A broke")
				}
			case "B":
				if bFails {
					return fail("B broke")
				}
			case "Esc":
				escCalls++
				if escCalls == 1 {
					return Outcome{Status: OutcomeSuccess, ContextUpdates: map[string]string{"outcome": "success"}}, nil
				}
				return fail("escalation abandoned") // second time: dead end (Esc -> Done when fail... but Done is exit → strict halt? no: conditional edge exists)
			}
			return Outcome{Status: OutcomeSuccess, ContextUpdates: map[string]string{"outcome": "success"}}, nil
		}})
		all := append([]EngineOption{WithCheckpointPath(cpPath), WithPipelineEventHandler(PipelineEventHandlerFunc(func(e PipelineEvent) { events = append(events, e) }))}, opts...)
		res, err := NewEngine(g, reg, all...).Run(context.Background())
		return res, err, events
	}
	// Pass 1: A fails → Esc (fallback, origin A) → Esc succeeds → B fails →
	// Esc via fail edge (origin must now be B) → Esc fails → Done (exit) which
	// ends the run fail at the exit node (recordHalt at Done).
	_, _, _ = run(true, true)
	cp := loadCP(t, cpPath)
	rec, ok := cp.GetFallbackOrigin("Esc")
	if !ok || rec.Node != "B" || rec.Kind != FallbackOriginFailEdge {
		t.Fatalf("FallbackOrigin[Esc] = %+v ok=%v, want B via fail_edge (latest hop wins, stale A cleared): visits=%v", rec, ok, visits)
	}
	// Re-point the checkpoint at Esc as the halted node. Esc has real onward
	// routing (Esc -> B when success), so it is NOT a dead end (#654): resume
	// must re-enter Esc in place — no rewind, and never back to the stale
	// origin A. (The provenance assertion above is what proves "latest origin
	// wins"; a dead-end variant of this shape is covered by
	// TestResumeRewind_LatchReArmed_SecondFailureStillAborts.)
	cp.CurrentNode = "Esc"
	cp.RecordHalt("Esc")
	if err := SaveCheckpoint(cp, cpPath); err != nil {
		t.Fatal(err)
	}
	escCalls = 0
	_, _, events := run(false, false)
	if rewound := eventsOfType(events, EventResumeRewound); len(rewound) != 0 {
		t.Fatalf("Esc has onward routing and is not a dead end: expected no rewind, got %+v visits=%v", rewound, visits)
	}
	if len(visits) == 0 || visits[0] != "Esc" || strings.Contains(strings.Join(visits, ","), "A") {
		t.Errorf("resume visits = %v, want Esc first and never A", visits)
	}
}

// ─── #654: rewind only from true dead ends; kind-aware origin copy ─────────

// fixLoopGraph builds the reviewer's shape: Start -> Test; `Test -> Fix when
// fail`; `Test -> Done when success`; Fix -> Test (unconditional). Fix is a
// fail-routed node with real onward routing — NOT a dead end.
func fixLoopGraph() *Graph {
	g := NewGraph("fixloop")
	g.Attrs["max_restarts"] = "5"
	g.AddNode(&Node{ID: "Start", Shape: "Mdiamond"})
	g.AddNode(&Node{ID: "Test", Shape: "parallelogram"})
	g.AddNode(&Node{ID: "Fix", Shape: "parallelogram"})
	g.AddNode(&Node{ID: "Done", Shape: "Msquare"})
	g.AddEdge(&Edge{From: "Start", To: "Test"})
	g.AddEdge(&Edge{From: "Test", To: "Fix", Condition: "ctx.outcome = fail"})
	g.AddEdge(&Edge{From: "Test", To: "Done", Condition: "ctx.outcome = success"})
	g.AddEdge(&Edge{From: "Fix", To: "Test"})
	return g
}

// TestResumeDoesNotRewindPastNonDeadEnd (#654): Test fails (legitimately —
// that is why the run is in Fix), Fix dies transiently with no failure route
// (strict halt at Fix). Resume re-runs Fix in place — visits [Fix, Test, ...]
// — instead of rewinding to Test and re-running a node that already did its
// job.
func TestResumeDoesNotRewindPastNonDeadEnd(t *testing.T) {
	cpPath := filepath.Join(t.TempDir(), "checkpoint.json")
	g := fixLoopGraph()
	testCalls, fixFails := 0, true
	run := func() ([]string, []PipelineEvent, *EngineResult, error) {
		var visits []string
		var events []PipelineEvent
		reg := newTestRegistry()
		reg.Register(&testHandler{name: "tool", executeFn: func(_ context.Context, node *Node, _ *PipelineContext) (Outcome, error) {
			visits = append(visits, node.ID)
			fail := func(r string) (Outcome, error) {
				return Outcome{Status: OutcomeFail, FailureReason: r, ContextUpdates: map[string]string{"outcome": "fail"}}, nil
			}
			switch node.ID {
			case "Test":
				testCalls++
				if testCalls == 1 {
					return fail("tests red")
				}
			case "Fix":
				if fixFails {
					return fail("agent died: connection reset")
				}
			}
			return Outcome{Status: OutcomeSuccess, ContextUpdates: map[string]string{"outcome": "success"}}, nil
		}})
		res, err := NewEngine(g, reg, WithCheckpointPath(cpPath),
			WithPipelineEventHandler(PipelineEventHandlerFunc(func(e PipelineEvent) { events = append(events, e) }))).Run(context.Background())
		return visits, events, res, err
	}
	visits, _, res, err := run()
	if err == nil || res == nil || res.Status != OutcomeFail || strings.Join(visits, ",") != "Test,Fix" {
		t.Fatalf("first run must halt at Fix: err=%v status=%v visits=%v", err, statusOf(res), visits)
	}
	cp := loadCP(t, cpPath)
	if cp.HaltedAt != "Fix" {
		t.Fatalf("HaltedAt = %q, want Fix", cp.HaltedAt)
	}
	if rec, ok := cp.GetFallbackOrigin("Fix"); !ok || rec.Node != "Test" || rec.Kind != FallbackOriginFailEdge {
		t.Fatalf("FallbackOrigin[Fix] = %+v ok=%v, want Test via fail_edge", rec, ok)
	}

	fixFails = false
	visits, events, res, err := run()
	if err != nil || res == nil || res.Status != OutcomeSuccess {
		t.Fatalf("resume must succeed: err=%v status=%v visits=%v", err, statusOf(res), visits)
	}
	if got := strings.Join(visits, ","); got != "Fix,Test" {
		t.Fatalf("resume visits = %s, want Fix,Test (Fix retried in place; Test not re-run first)", got)
	}
	if n := len(eventsOfType(events, EventResumeRewound)); n != 0 {
		t.Errorf("no resume_rewound expected for a non-dead-end halt, got %d", n)
	}
}

// TestIsFailDeadEnd pins the structural rule (#654): a designated failure sink
// (graph on_failure, any node's fallback_target / fallback_retry_target) or a
// node whose only continuation is the exit node is a dead end; a node with
// real onward routing is not.
func TestIsFailDeadEnd(t *testing.T) {
	g := NewGraph("deadend")
	g.Attrs["fallback_target"] = "Abort"
	for _, id := range []string{"Start", "Test", "Fix", "Abort", "Last", "NodeFB", "Sink", "Done"} {
		shape := "parallelogram"
		if id == "Start" {
			shape = "Mdiamond"
		} else if id == "Done" {
			shape = "Msquare"
		}
		g.AddNode(&Node{ID: id, Shape: shape})
	}
	g.Nodes["NodeFB"].Attrs = map[string]string{"fallback_retry_target": "Sink"}
	g.AddEdge(&Edge{From: "Start", To: "Test"})
	g.AddEdge(&Edge{From: "Test", To: "Fix", Condition: "ctx.outcome = fail"})
	g.AddEdge(&Edge{From: "Fix", To: "Test"})
	g.AddEdge(&Edge{From: "Abort", To: "Done"})
	g.AddEdge(&Edge{From: "Last", To: "Done"})
	g.AddEdge(&Edge{From: "Sink", To: "Test"})
	e := NewEngine(g, newTestRegistry())
	cases := map[string]bool{
		"Abort":  true,  // graph on_failure target
		"Sink":   false, // a declared fallback_retry_target with onward routing is NOT a dead end (#654 review)
		"Last":   true,  // only continuation is the exit node
		"NodeFB": true,  // no outgoing edges at all
		"Fix":    false, // loops back into the graph
		"Test":   false,
	}
	for id, want := range cases {
		if got := e.isFailDeadEnd(id); got != want {
			t.Errorf("isFailDeadEnd(%s) = %v, want %v", id, got, want)
		}
	}
}

// TestFailRouteOriginKindAwareCopy (#654): an authored `when fail` edge renders
// "routed from X via fail edge"; a fallback renders "reached from X failure";
// FallbackOrigin(id) keeps hiding fail_edge records (#650 contract).
func TestFailRouteOriginKindAwareCopy(t *testing.T) {
	for _, tc := range []struct {
		name     string
		onFail   bool
		kind     FallbackOriginKind
		wantCopy string
		wantHide bool
	}{
		{"fail edge", true, FallbackOriginFailEdge, `"Abort" (routed from "A" via fail edge) failed`, true},
		{"strict fallback", false, FallbackOriginStrictFailure, `"Abort" (reached from "A" failure) failed`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cpPath := filepath.Join(t.TempDir(), "checkpoint.json")
			g := rewindGraph(tc.onFail)
			visits, events, _, err := rewindRun(t, g, cpPath, true)
			if err == nil || !strings.Contains(err.Error(), tc.wantCopy) {
				t.Fatalf("err = %v, want copy %q (visits %v)", err, tc.wantCopy, visits)
			}
			halts := stageFailedFor(events, "Abort")
			if len(halts) == 0 || !strings.Contains(halts[len(halts)-1].Message, tc.wantCopy) {
				t.Errorf("terminal stage_failed should carry the same copy, got %+v", halts)
			}
			cp := loadCP(t, cpPath)
			origin, kind := cp.FailRouteOrigin("Abort")
			if origin != "A" || kind != tc.kind {
				t.Errorf("FailRouteOrigin(Abort) = %q,%q, want A,%s", origin, kind, tc.kind)
			}
			hidden := cp.FallbackOrigin("Abort") == ""
			if hidden != tc.wantHide {
				t.Errorf("FallbackOrigin(Abort) hidden=%v, want %v (#650 contract unchanged)", hidden, tc.wantHide)
			}
		})
	}
}

// TestResumeRewindClearsElseOnlyDownstream (#654): an else-only node
// downstream of the rewound origin — reachable only through the section-level
// `else ->` default, never through an explicit edge — is in the rewind's
// cleared set (the shared downstreamNodes walk follows the else route).
func TestResumeRewindClearsElseOnlyDownstream(t *testing.T) {
	cpPath := filepath.Join(t.TempDir(), "checkpoint.json")
	g := NewGraph("else-rewind")
	g.Attrs["fallback_target"] = "Abort"
	g.ElseTarget = "Funnel"
	g.AddNode(&Node{ID: "Start", Shape: "Mdiamond"})
	g.AddNode(&Node{ID: "A", Shape: "parallelogram"})
	g.AddNode(&Node{ID: "Classify", Shape: "parallelogram"})
	g.AddNode(&Node{ID: "Funnel", Shape: "parallelogram"}) // else-only
	g.AddNode(&Node{ID: "B", Shape: "parallelogram"})
	g.AddNode(&Node{ID: "Abort", Shape: "parallelogram"})
	g.AddNode(&Node{ID: "Done", Shape: "Msquare"})
	g.AddEdge(&Edge{From: "Start", To: "A"})
	g.AddEdge(&Edge{From: "A", To: "Classify"})
	g.AddEdge(&Edge{From: "Classify", To: "B", Condition: "ctx.tool_marker = ok"})
	g.AddEdge(&Edge{From: "Funnel", To: "B"})
	g.AddEdge(&Edge{From: "B", To: "Done"})
	g.AddEdge(&Edge{From: "Abort", To: "Done"})

	// Pass 1: everything succeeds, Classify's guard misses → Funnel via else → B → Done.
	visits, _, res, err := rewindRun(t, g, cpPath, false)
	if err != nil || res == nil || res.Status != OutcomeSuccess || !containsString(visits, "Funnel") {
		t.Fatalf("pass 1: err=%v status=%v visits=%v (Funnel must run via else)", err, statusOf(res), visits)
	}
	// Forge a halt at Abort reached from A's failure, with Funnel completed
	// from pass 1, and rewind to A: Funnel must be in the cleared set.
	cp := loadCP(t, cpPath)
	cp.CurrentNode = "Abort"
	cp.RecordFallbackOrigin("Abort", "A", OutcomeFail, "A broke", FallbackOriginStrictFailure)
	cp.RecordHalt("Abort")
	if err := SaveCheckpoint(cp, cpPath); err != nil {
		t.Fatal(err)
	}
	visits, events, res, err := rewindRun(t, g, cpPath, false)
	if err != nil || res == nil || res.Status != OutcomeSuccess {
		t.Fatalf("resume: err=%v status=%v visits=%v", err, statusOf(res), visits)
	}
	rewound := eventsOfType(events, EventResumeRewound)
	if len(rewound) != 1 || rewound[0].Decision == nil {
		t.Fatalf("want one resume_rewound, got %+v", rewound)
	}
	if !containsString(rewound[0].Decision.ClearedNodes, "Funnel") {
		t.Errorf("cleared nodes %v must include the else-only Funnel downstream of A", rewound[0].Decision.ClearedNodes)
	}
	if !containsString(visits, "Funnel") {
		t.Errorf("Funnel must re-run after the rewind (it was un-completed), visits=%v", visits)
	}
}
