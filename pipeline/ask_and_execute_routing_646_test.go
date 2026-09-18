// ABOUTME: Issue #646 items 2/3 regression guards on the REAL ask_and_execute.dip graph.
// ABOUTME: Scripted engine sims prove the fail-closed routing (AbortRun, never the accept gate).
package pipeline

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

// sim646 drives a real example graph through the engine with per-node
// scripted outcomes. `script` maps a node ID to a function of the node's
// prior visit count; unscripted nodes succeed. Human gates answer the label
// in `gates`; an unscripted gate fails the run with errGateReached so a test
// can prove "this path never asks a human". Terminals (`terminals`) fail
// unconditionally, like the exit-1 AbortRun script.
type sim646 struct {
	mu        sync.Mutex
	seen      map[string]int
	script    map[string]func(n int) Outcome
	gates     map[string]string
	terminals map[string]bool
}

func (s *sim646) visited(id string) bool { return s.seen[id] > 0 }

func (s *sim646) run(t *testing.T, g *Graph) (*EngineResult, error) {
	t.Helper()
	s.seen = map[string]int{}
	exec := func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
		s.mu.Lock()
		defer s.mu.Unlock()
		n := s.seen[node.ID]
		s.seen[node.ID]++
		if fn, ok := s.script[node.ID]; ok {
			return fn(n), nil
		}
		if s.terminals[node.ID] {
			return bpFail("RUN ABORTED"), nil
		}
		if node.Handler == "wait.human" {
			label, ok := s.gates[node.ID]
			if !ok {
				return bpFail(""), fmt.Errorf("%w: %s", errGateReached, node.ID)
			}
			return Outcome{Status: OutcomeSuccess, PreferredLabel: label}, nil
		}
		return bpOK(""), nil
	}
	reg := NewHandlerRegistry()
	for _, name := range []string{"start", "exit", "codergen", "wait.human", "conditional", "parallel", "parallel.fan_in", "tool"} {
		reg.Register(&testHandler{name: name, executeFn: exec})
	}
	res, err := NewEngine(g, reg).Run(context.Background())
	s.mu.Lock()
	defer s.mu.Unlock()
	return res, err
}

func aaeSim() *sim646 {
	return &sim646{
		script:    map[string]func(n int) Outcome{},
		gates:     map[string]string{"AskUser": "", "ApproveSpec": "approve"},
		terminals: map[string]bool{"AbortRun": true},
	}
}

// TestAskAndExecute646HappyPath: with every candidate usable the run reaches
// CommitFinal and completes; no gate other than ApproveSpec is asked.
func TestAskAndExecute646HappyPath(t *testing.T) {
	g := loadAskAndExecute(t)
	s := aaeSim()
	s.script["CaptureAndTest"] = func(int) Outcome { return bpOK("claude: TESTS PASS\ncandidates-captured") }
	res, err := s.run(t, g)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Status != OutcomeSuccess {
		t.Fatalf("status = %s, want success", res.Status)
	}
	for _, id := range []string{"CrossCritique", "SelectWinner", "ApplyWinner", "FinalBuild", "FinalVerify", "CommitFinal"} {
		if !s.visited(id) {
			t.Errorf("%s not visited", id)
		}
	}
	for _, id := range []string{"AbortRun", "AllCandidatesRed", "EscalateToHuman"} {
		if s.visited(id) {
			t.Errorf("%s visited on the happy path", id)
		}
	}
}

// TestAskAndExecute646AllCandidatesRed (item 2): CaptureAndTest failing with
// the all-candidates-red marker reaches the AllCandidatesRed gate; "abort"
// (the unattended default) ends at the AbortRun terminal with the run
// failed — never CrossCritique, never the accept gate, never CommitFinal.
func TestAskAndExecute646AllCandidatesRed(t *testing.T) {
	g := loadAskAndExecute(t)
	s := aaeSim()
	s.script["CaptureAndTest"] = func(int) Outcome { return bpFail("claude: TESTS FAIL\nall-candidates-red") }
	s.gates["AllCandidatesRed"] = "abort"
	res, err := s.run(t, g)
	if err != nil && !errors.Is(err, errGateReached) {
		t.Logf("run error: %v", err)
	}
	if res != nil && res.Status == OutcomeSuccess {
		t.Fatal("run succeeded — an all-red candidate set must not ship")
	}
	if !s.visited("AllCandidatesRed") || !s.visited("AbortRun") {
		t.Errorf("expected AllCandidatesRed → AbortRun, seen=%v", s.seen)
	}
	for _, id := range []string{"CrossCritique", "EscalateToHuman", "CommitFinal", "ApplyWinner"} {
		if s.visited(id) {
			t.Errorf("%s visited after an all-red capture", id)
		}
	}

	// "critique-anyway" continues to the critique.
	s = aaeSim()
	s.script["CaptureAndTest"] = func(int) Outcome { return bpFail("all-candidates-red") }
	s.script["FinalVerify"] = func(int) Outcome { return bpOK("") }
	s.gates["AllCandidatesRed"] = "critique-anyway"
	s.script["CaptureAndTest"] = func(int) Outcome { return bpFail("all-candidates-red") }
	if _, err := s.run(t, g); err != nil {
		t.Fatalf("critique-anyway run: %v", err)
	}
	if !s.visited("CrossCritique") || s.visited("AbortRun") {
		t.Errorf("critique-anyway must continue to CrossCritique without aborting, seen=%v", s.seen)
	}
}

// TestAskAndExecute646CaptureAbortBeforeMarker: a `set -e` abort inside
// CaptureAndTest (no marker at all) takes the unconditional fallback to the
// AbortRun terminal — not the gate, not the critique.
func TestAskAndExecute646CaptureAbortBeforeMarker(t *testing.T) {
	g := loadAskAndExecute(t)
	s := aaeSim()
	s.script["CaptureAndTest"] = func(int) Outcome { return bpFail("git: fatal") }
	res, _ := s.run(t, g)
	if res != nil && res.Status == OutcomeSuccess {
		t.Fatal("run succeeded after a mechanical CaptureAndTest failure")
	}
	if !s.visited("AbortRun") {
		t.Errorf("AbortRun not visited, seen=%v", s.seen)
	}
	for _, id := range []string{"AllCandidatesRed", "CrossCritique", "EscalateToHuman", "CommitFinal"} {
		if s.visited(id) {
			t.Errorf("%s visited", id)
		}
	}
}

// TestAskAndExecute646ApplyWinnerFailureAborts (item 3): an unparseable or
// ambiguous WINNER line, or a merge conflict, fails ApplyWinner; the run
// aborts with nothing merged instead of reaching EscalateToHuman, whose
// unattended "accept" default used to run CommitFinal on an empty merge.
func TestAskAndExecute646ApplyWinnerFailureAborts(t *testing.T) {
	g := loadAskAndExecute(t)
	s := aaeSim()
	s.script["CaptureAndTest"] = func(int) Outcome { return bpOK("candidates-captured") }
	s.script["ApplyWinner"] = func(int) Outcome { return bpFail("ERROR: ambiguous winner") }
	res, _ := s.run(t, g)
	if res != nil && res.Status == OutcomeSuccess {
		t.Fatal("run succeeded after ApplyWinner failed")
	}
	if !s.visited("AbortRun") {
		t.Errorf("AbortRun not visited, seen=%v", s.seen)
	}
	for _, id := range []string{"FinalBuild", "EscalateToHuman", "CommitFinal"} {
		if s.visited(id) {
			t.Errorf("%s visited after ApplyWinner failed", id)
		}
	}
}

// TestAskAndExecute646UnroutedAgentFailureAborts: the graph-level on_failure
// is the AbortRun terminal, so an agent with no failure route of its own
// (CrossCritique) aborts rather than reaching the accept gate.
func TestAskAndExecute646UnroutedAgentFailureAborts(t *testing.T) {
	g := loadAskAndExecute(t)
	if got := g.Attrs["fallback_target"]; got != "AbortRun" {
		t.Fatalf("graph-level on_failure/fallback_target = %q, want AbortRun", got)
	}
	s := aaeSim()
	s.script["CaptureAndTest"] = func(int) Outcome { return bpOK("candidates-captured") }
	s.script["CrossCritique"] = func(int) Outcome { return bpFail("") }
	res, _ := s.run(t, g)
	if res != nil && res.Status == OutcomeSuccess {
		t.Fatal("run succeeded after CrossCritique failed")
	}
	if !s.visited("AbortRun") || s.visited("EscalateToHuman") || s.visited("CommitFinal") {
		t.Errorf("expected AbortRun only, seen=%v", s.seen)
	}
}

// TestAskAndExecute646MechanicalNodesRouteToAbort pins the edge shape: every
// mechanical tool node owns a fail route to AbortRun, and CaptureAndTest's
// success/gate edges are exact-marker (`endswith`) with the terminal as the
// unconditional fallback.
func TestAskAndExecute646MechanicalNodesRouteToAbort(t *testing.T) {
	g := loadAskAndExecute(t)
	for _, from := range []string{"SetupWorkspace", "SetupWorktrees", "ApplyWinner"} {
		if edgeIndex(g, from, "AbortRun", "ctx.outcome = fail") < 0 {
			t.Errorf("%s has no `when ctx.outcome = fail` edge to AbortRun", from)
		}
	}
	if edgeIndex(g, "CaptureAndTest", "AbortRun", "") < 0 {
		t.Error("CaptureAndTest lacks the unconditional fallback to AbortRun")
	}
	var sawCritique, sawGate bool
	for _, e := range g.OutgoingEdges("CaptureAndTest") {
		switch e.To {
		case "CrossCritique":
			sawCritique = e.Condition != "" && containsAll(e.Condition, "ctx.outcome = success", "endswith candidates-captured")
		case "AllCandidatesRed":
			sawGate = e.Condition != "" && containsAll(e.Condition, "ctx.outcome = fail", "endswith all-candidates-red")
		}
	}
	if !sawCritique || !sawGate {
		t.Errorf("CaptureAndTest marker edges drifted: %+v", g.OutgoingEdges("CaptureAndTest"))
	}
	if edgeIndex(g, "AbortRun", "Done", "") < 0 {
		t.Error("AbortRun must have a single unconditional edge to Done (strict-failure halt)")
	}
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}
