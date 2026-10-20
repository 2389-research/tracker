// ABOUTME: End-to-end pause/resume of a human gate through a real engine: the
// ABOUTME: resumed prompt matches the recorded one, and a plain resume re-pauses.
package handlers

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/2389-research/tracker/pipeline"
)

// reviewGraph is Start -> Work -> Review(choice approve/reject) -> Ship|Drop -> End,
// with default_fidelity compact (a resume would normally compact the context).
// Work writes a long last_response, which Review's prompt shows.
func reviewGraph() *pipeline.Graph {
	g := pipeline.NewGraph("review")
	g.Attrs["default_fidelity"] = "compact"
	g.AddNode(&pipeline.Node{ID: "Start", Shape: "Mdiamond"})
	g.AddNode(&pipeline.Node{ID: "Work", Shape: "box", Attrs: map[string]string{"prompt": "work"}})
	g.AddNode(&pipeline.Node{ID: "Review", Shape: "hexagon", Label: "Review the change"})
	g.AddNode(&pipeline.Node{ID: "Ship", Shape: "box", Attrs: map[string]string{"prompt": "ship"}})
	g.AddNode(&pipeline.Node{ID: "Drop", Shape: "box", Attrs: map[string]string{"prompt": "drop"}})
	g.AddNode(&pipeline.Node{ID: "End", Shape: "Msquare"})
	g.AddEdge(&pipeline.Edge{From: "Start", To: "Work"})
	g.AddEdge(&pipeline.Edge{From: "Work", To: "Review"})
	g.AddEdge(&pipeline.Edge{From: "Review", To: "Ship", Label: "approve"})
	g.AddEdge(&pipeline.Edge{From: "Review", To: "Drop", Label: "reject"})
	g.AddEdge(&pipeline.Edge{From: "Ship", To: "End"})
	g.AddEdge(&pipeline.Edge{From: "Drop", To: "End"})
	return g
}

var longDiff = strings.Repeat("diff line with words ", 200)

func runReview(t *testing.T, g *pipeline.Graph, cpPath string, iv Interviewer) (*pipeline.EngineResult, error) {
	t.Helper()
	work := func(_ context.Context, node *pipeline.Node, _ *pipeline.PipelineContext) (pipeline.Outcome, error) {
		out := pipeline.Outcome{Status: pipeline.OutcomeSuccess}
		if node.ID == "Work" {
			out.ContextUpdates = map[string]string{pipeline.ContextKeyLastResponse: longDiff}
		}
		return out, nil
	}
	reg := NewDefaultRegistry(g, WithCodergenFunc(work), WithInterviewer(iv, g))
	return pipeline.NewEngine(g, reg, pipeline.WithCheckpointPath(cpPath)).Run(context.Background())
}

func pausedRecord(t *testing.T, cpPath string) pipeline.PausedGate {
	t.Helper()
	cp, err := pipeline.LoadCheckpoint(cpPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(cp.PausedGates) != 1 {
		t.Fatalf("PausedGates = %+v, want one", cp.PausedGates)
	}
	return cp.PausedGates[0]
}

// RF1: the resumed gate re-renders the identical prompt (no compaction), so
// an answer built from the record applies on the first try.
func TestResume_PausedGateSkipsCompaction(t *testing.T) {
	g := reviewGraph()
	cpPath := filepath.Join(t.TempDir(), "checkpoint.json")
	res, _ := runReview(t, g, cpPath, NewPausingInterviewer(nil))
	if res.Status != pipeline.OutcomePausedGate {
		t.Fatalf("first run status = %q", res.Status)
	}
	rec := pausedRecord(t, cpPath)
	if !strings.Contains(rec.Prompt, longDiff) {
		t.Fatal("recorded prompt lacks the upstream last_response")
	}

	ans := &PendingAnswer{NodeID: rec.NodeID, ScopePath: rec.ScopePath, Fingerprint: rec.Fingerprint, Nonce: rec.Nonce, Label: "approve"}
	res, err := runReview(t, g, cpPath, NewPausingInterviewer(ans))
	if err != nil || res.Status != pipeline.OutcomeSuccess {
		t.Fatalf("answered resume = %v, %v; want success (a re-pause here means the prompt changed)", res, err)
	}
	if !slicesContain(res.CompletedNodes, "Ship") || slicesContain(res.CompletedNodes, "Drop") {
		t.Errorf("completed = %v, want the approve edge (Ship)", res.CompletedNodes)
	}
}

// RF2 (engine half): a plain resume with no answer re-pauses at the same
// gate; a resume with an interactive answer completes it and clears the record.
func TestResume_PlainResumeRepausesAndTTYAnswers(t *testing.T) {
	g := reviewGraph()
	cpPath := filepath.Join(t.TempDir(), "checkpoint.json")
	if res, _ := runReview(t, g, cpPath, NewPausingInterviewer(nil)); res.Status != pipeline.OutcomePausedGate {
		t.Fatalf("first run status = %q", res.Status)
	}
	first := pausedRecord(t, cpPath)

	res, err := runReview(t, g, cpPath, NewPausingInterviewer(nil))
	var pe *pipeline.PauseError
	if res.Status != pipeline.OutcomePausedGate || !errors.As(err, &pe) {
		t.Fatalf("plain resume = %q, %v; want a re-pause", res.Status, err)
	}
	again := pausedRecord(t, cpPath)
	if again.NodeID != "Review" || again.Fingerprint != first.Fingerprint {
		t.Errorf("re-pause record %+v differs from the first %+v", again, first)
	}

	res, err = runReview(t, g, cpPath, &QueueInterviewer{Answers: []string{"reject"}})
	if err != nil || res.Status != pipeline.OutcomeSuccess {
		t.Fatalf("interactive resume = %v, %v", res, err)
	}
	cp, _ := pipeline.LoadCheckpoint(cpPath)
	if len(cp.PausedGates) != 0 {
		t.Errorf("PausedGates not cleared after the gate was answered: %+v", cp.PausedGates)
	}
}

func slicesContain(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
