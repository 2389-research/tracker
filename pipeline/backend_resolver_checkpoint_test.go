// ABOUTME: backend: auto's resolution is stamped into the checkpoint and run.json,
// ABOUTME: seeded back on resume, and a tampered recorded value fails the resume.
package pipeline

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func linearGraph() *Graph {
	g := NewGraph("auto_record")
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond"})
	g.AddNode(&Node{ID: "step1", Shape: "box"})
	g.AddNode(&Node{ID: "step2", Shape: "box"})
	g.AddNode(&Node{ID: "end", Shape: "Msquare"})
	g.AddEdge(&Edge{From: "s", To: "step1"})
	g.AddEdge(&Edge{From: "step1", To: "step2"})
	g.AddEdge(&Edge{From: "step2", To: "end"})
	return g
}

func TestCheckpoint_StampsBackendResolved(t *testing.T) {
	cpPath := filepath.Join(t.TempDir(), "checkpoint.json")
	r := NewBackendResolver()
	if _, err := r.Resolve(func() (BackendResolution, error) {
		return BackendResolution{Backend: "claude-code"}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := NewEngine(linearGraph(), newTestRegistry(), WithCheckpointPath(cpPath), WithBackendResolver(r)).Run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	cp, err := LoadCheckpoint(cpPath)
	if err != nil {
		t.Fatal(err)
	}
	if cp.BackendResolved != "claude-code" {
		t.Fatalf("checkpoint backend_resolved = %q, want claude-code", cp.BackendResolved)
	}
}

func TestCheckpoint_NoResolutionLeavesFieldEmpty(t *testing.T) {
	cpPath := filepath.Join(t.TempDir(), "checkpoint.json")
	if _, err := NewEngine(linearGraph(), newTestRegistry(), WithCheckpointPath(cpPath), WithBackendResolver(NewBackendResolver())).Run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	cp, _ := LoadCheckpoint(cpPath)
	if cp.BackendResolved != "" {
		t.Fatalf("checkpoint backend_resolved = %q, want empty when nothing resolved", cp.BackendResolved)
	}
}

func resumeCheckpoint(t *testing.T, backend string) string {
	t.Helper()
	cpPath := filepath.Join(t.TempDir(), "checkpoint.json")
	cp := &Checkpoint{
		RunID:           "auto-resume",
		CurrentNode:     "step2",
		CompletedNodes:  []string{"s", "step1"},
		RetryCounts:     map[string]int{},
		Context:         map[string]string{"outcome": "success"},
		BackendResolved: backend,
	}
	if err := SaveCheckpoint(cp, cpPath); err != nil {
		t.Fatal(err)
	}
	return cpPath
}

func TestResume_SeedsResolverFromCheckpoint(t *testing.T) {
	r := NewBackendResolver()
	cpPath := resumeCheckpoint(t, "native")
	if _, err := NewEngine(linearGraph(), newTestRegistry(), WithCheckpointPath(cpPath), WithBackendResolver(r)).Run(context.Background()); err != nil {
		t.Fatalf("resume: %v", err)
	}
	res, ok := r.Result()
	if !ok || res.Backend != "native" || !res.FromRecord {
		t.Fatalf("resolver after resume = %+v, %v; want native FromRecord", res, ok)
	}
}

func TestResume_TamperedBackendResolvedFails(t *testing.T) {
	cpPath := resumeCheckpoint(t, "acp")
	_, err := NewEngine(linearGraph(), newTestRegistry(), WithCheckpointPath(cpPath), WithBackendResolver(NewBackendResolver())).Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), `"acp"`) {
		t.Fatalf("resume with backend_resolved=acp: err = %v, want an error naming the value", err)
	}
}

func TestRunManifest_CopiesBackendResolved(t *testing.T) {
	runDir := t.TempDir()
	if err := SaveCheckpoint(&Checkpoint{RunID: "r", BackendResolved: "claude-code"}, filepath.Join(runDir, "checkpoint.json")); err != nil {
		t.Fatal(err)
	}
	var m RunManifest
	applyCheckpointToManifest(&m, runDir)
	if m.BackendResolved != "claude-code" {
		t.Fatalf("run.json backend_resolved = %q, want claude-code", m.BackendResolved)
	}
}
