// ABOUTME: Tests for the paused-gate record: fingerprint, checkpoint storage,
// ABOUTME: the gate.json mirror, clearing on completion, and the gate_paused event.
package pipeline

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func sampleGate() PausedGate {
	return PausedGate{
		NodeID:    "Review",
		ScopePath: []string{"B1"},
		Mode:      "choice",
		Label:     "Review the change",
		Prompt:    "Review the change\n\n---\ndiff",
		Choices:   []string{"approve", "reject"},
		Default:   "reject",
		Questions: []GateQuestion{{ID: "q1", Text: "Ship?", Options: []string{"yes", "no"}}},
		Nonce:     "n1",
		At:        time.Unix(1, 0),
	}
}

func TestGateFingerprint_StableAndSensitive(t *testing.T) {
	base := GateFingerprint(sampleGate())
	if base == "" || base != GateFingerprint(sampleGate()) {
		t.Fatalf("fingerprint not stable: %q", base)
	}
	changes := map[string]func(*PausedGate){
		"prompt":        func(g *PausedGate) { g.Prompt += "!" },
		"choices order": func(g *PausedGate) { g.Choices = []string{"reject", "approve"} },
		"default":       func(g *PausedGate) { g.Default = "approve" },
		"scope":         func(g *PausedGate) { g.ScopePath = []string{"B2"} },
		"node":          func(g *PausedGate) { g.NodeID = "Other" },
		"mode":          func(g *PausedGate) { g.Mode = "freeform" },
		"question opt":  func(g *PausedGate) { g.Questions[0].Options = []string{"yes", "maybe"} },
	}
	for name, mutate := range changes {
		g := sampleGate()
		mutate(&g)
		if GateFingerprint(g) == base {
			t.Errorf("%s change did not change the fingerprint", name)
		}
	}
	same := map[string]func(*PausedGate){
		"nonce": func(g *PausedGate) { g.Nonce = "n2" },
		"at":    func(g *PausedGate) { g.At = time.Unix(2, 0) },
		"note":  func(g *PausedGate) { g.Note = "changed" },
		"top":   func(g *PausedGate) { g.TopNode = "Par" },
	}
	for name, mutate := range same {
		g := sampleGate()
		mutate(&g)
		if GateFingerprint(g) != base {
			t.Errorf("%s change altered the fingerprint", name)
		}
	}
}

// gatePauseGraph is start -> gate -> end, where gate is a codergen test node
// that pauses with sampleGate() while *pause is true, else succeeds.
func gatePauseGraph(pause *bool) (*Graph, *HandlerRegistry) {
	g := NewGraph("gate_pause")
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond", Label: "Start"})
	g.AddNode(&Node{ID: "gate", Shape: "box", Label: "Gate"})
	g.AddNode(&Node{ID: "end", Shape: "Msquare", Label: "End"})
	g.AddEdge(&Edge{From: "s", To: "gate"})
	g.AddEdge(&Edge{From: "gate", To: "end"})
	reg := newTestRegistry()
	reg.Register(&testHandler{
		name: "codergen",
		executeFn: func(_ context.Context, node *Node, _ *PipelineContext) (Outcome, error) {
			if node.ID != "gate" {
				return Outcome{Status: OutcomeSuccess}, nil
			}
			if *pause {
				return Outcome{}, NewPausedGateError(sampleGate())
			}
			return Outcome{Status: OutcomeSuccess}, nil
		},
	})
	return g, reg
}

func runGatePause(t *testing.T, pause *bool, opts ...EngineOption) (*EngineResult, string, string) {
	t.Helper()
	dir := t.TempDir()
	cpPath := filepath.Join(dir, "checkpoint.json")
	artifacts := filepath.Join(dir, "runs")
	g, reg := gatePauseGraph(pause)
	all := append([]EngineOption{WithCheckpointPath(cpPath), WithArtifactDir(artifacts)}, opts...)
	result, _ := NewEngine(g, reg, all...).Run(context.Background())
	if result == nil {
		t.Fatal("nil result")
	}
	return result, cpPath, filepath.Join(artifacts, result.RunID)
}

func TestPausedGate_StoredInCheckpoint(t *testing.T) {
	pause := true
	result, cpPath, _ := runGatePause(t, &pause)
	if result.Status != OutcomePausedGate {
		t.Fatalf("status = %q, want paused_gate", result.Status)
	}
	cp, err := LoadCheckpoint(cpPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(cp.PausedGates) != 1 {
		t.Fatalf("PausedGates = %+v, want one record", cp.PausedGates)
	}
	got := cp.PausedGates[0]
	if got.TopNode != "gate" || got.NodeID != "Review" || got.Prompt != sampleGate().Prompt {
		t.Errorf("record = %+v", got)
	}
	if cp.CurrentNode != "gate" || cp.IsCompleted("gate") {
		t.Errorf("CurrentNode=%q completed=%v; want gate, false", cp.CurrentNode, cp.IsCompleted("gate"))
	}
	if cp.PriorUsage == nil {
		t.Error("PriorUsage not stamped on pause")
	}
}

func TestPausedGate_MirrorWritten(t *testing.T) {
	pause := true
	_, cpPath, runDir := runGatePause(t, &pause)
	path := filepath.Join(runDir, GateMirrorFile)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("gate.json not written: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("gate.json mode = %v, want 0600", info.Mode().Perm())
	}
	data, _ := os.ReadFile(path)
	var mirror []PausedGate
	if err := json.Unmarshal(data, &mirror); err != nil {
		t.Fatalf("gate.json: %v", err)
	}
	cp, _ := LoadCheckpoint(cpPath)
	if len(mirror) != 1 || mirror[0].Fingerprint != cp.PausedGates[0].Fingerprint {
		t.Errorf("mirror %+v does not match checkpoint %+v", mirror, cp.PausedGates)
	}
}

func TestPausedGate_MirrorRefusesSymlink(t *testing.T) {
	dir := t.TempDir()
	runDir := filepath.Join(dir, "runs", "r1")
	if err := os.MkdirAll(runDir, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "outside.json")
	if err := os.Symlink(target, filepath.Join(runDir, GateMirrorFile)); err != nil {
		t.Skipf("symlinks unsupported: %v", err)
	}
	if err := writeGateMirror(runDir, []PausedGate{sampleGate()}); err == nil {
		t.Error("mirror write followed a planted symlink")
	}
	if _, err := os.Stat(target); err == nil {
		t.Error("symlink target was written")
	}
}

func TestPausedGate_ClearedOnCompletion(t *testing.T) {
	pause := true
	dir := t.TempDir()
	cpPath := filepath.Join(dir, "checkpoint.json")
	artifacts := filepath.Join(dir, "runs")
	g, reg := gatePauseGraph(&pause)
	first, _ := NewEngine(g, reg, WithCheckpointPath(cpPath), WithArtifactDir(artifacts)).Run(context.Background())
	mirror := filepath.Join(artifacts, first.RunID, GateMirrorFile)
	if _, err := os.Stat(mirror); err != nil {
		t.Fatalf("mirror missing after pause: %v", err)
	}

	pause = false
	second, err := NewEngine(g, reg, WithCheckpointPath(cpPath), WithArtifactDir(artifacts)).Run(context.Background())
	if err != nil || second.Status != OutcomeSuccess {
		t.Fatalf("resume = %v, %v", second, err)
	}
	cp, _ := LoadCheckpoint(cpPath)
	if len(cp.PausedGates) != 0 {
		t.Errorf("PausedGates not cleared: %+v", cp.PausedGates)
	}
	if _, err := os.Stat(mirror); !os.IsNotExist(err) {
		t.Errorf("gate.json not removed after the gate completed (stat err %v)", err)
	}
}

func TestRunStart_RemovesStaleMirror(t *testing.T) {
	// A checkpoint with no waiting gate (e.g. a billing pause) whose run dir
	// holds a stale gate.json: the resumed run removes it at start.
	pause := false
	dir := t.TempDir()
	cpPath := filepath.Join(dir, "checkpoint.json")
	artifacts := filepath.Join(dir, "runs")
	cp := &Checkpoint{RunID: "stale1", CurrentNode: "gate", CompletedNodes: []string{"s"}, Context: map[string]string{}}
	if err := SaveCheckpoint(cp, cpPath); err != nil {
		t.Fatal(err)
	}
	runDir := filepath.Join(artifacts, "stale1")
	if err := os.MkdirAll(runDir, 0o700); err != nil {
		t.Fatal(err)
	}
	mirror := filepath.Join(runDir, GateMirrorFile)
	if err := os.WriteFile(mirror, []byte("[]"), 0o600); err != nil {
		t.Fatal(err)
	}
	g, reg := gatePauseGraph(&pause)
	if _, err := NewEngine(g, reg, WithCheckpointPath(cpPath), WithArtifactDir(artifacts)).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(mirror); !os.IsNotExist(err) {
		t.Errorf("stale gate.json survived run start (stat err %v)", err)
	}
}

func TestGatePausedEvent_CarriesGate(t *testing.T) {
	pause := true
	var gate *GateDetail
	_, _, _ = runGatePause(t, &pause, WithPipelineEventHandler(PipelineEventHandlerFunc(func(evt PipelineEvent) {
		if evt.Type == EventGatePaused {
			gate = evt.Gate
		}
	})))
	if gate == nil {
		t.Fatal("gate_paused event carried no Gate")
	}
	want := sampleGate()
	if gate.Resolution != "paused" || gate.Label != want.Label || gate.Mode != want.Mode || gate.Default != want.Default {
		t.Errorf("gate detail = %+v", gate)
	}
	if gate.Fingerprint != GateFingerprint(want) {
		t.Errorf("fingerprint = %q, want %q", gate.Fingerprint, GateFingerprint(want))
	}
}
