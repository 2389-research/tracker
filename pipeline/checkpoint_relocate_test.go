// ABOUTME: Tests that the authoritative checkpoint is relocated to the secure
// ABOUTME: state dir with a best-effort workdir snapshot for tooling (#559).
package pipeline

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestEngine_CheckpointRelocatedToSecureDir: with WithArtifactDir set (the
// auto-derive path) and a secure base configured, the authoritative checkpoint
// lands in the secure state dir (out of the tool-reachable workdir) and a
// best-effort snapshot lands under the artifact dir for read-only tooling.
func TestEngine_CheckpointRelocatedToSecureDir(t *testing.T) {
	secureBase := t.TempDir()
	t.Setenv(auditDirEnvVar, secureBase) // TRACKER_AUDIT_DIR wins the secure-base race

	artifactBase := t.TempDir()
	g := NewGraph("cp_relocate_test")
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond", Label: "Start"})
	g.AddNode(&Node{ID: "end", Shape: "Msquare", Label: "End"})
	g.AddEdge(&Edge{From: "s", To: "end"})

	engine := NewEngine(g, newTestRegistry(), WithArtifactDir(artifactBase))
	res, err := engine.Run(context.Background())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	runID := res.RunID
	if runID == "" {
		t.Fatal("no run id")
	}

	secure := filepath.Join(secureBase, runID, "checkpoint.json")
	snapshot := filepath.Join(artifactBase, runID, "checkpoint.json")

	// Authoritative checkpoint in the secure dir, mode 0600.
	info, err := os.Stat(secure)
	if err != nil {
		t.Fatalf("authoritative checkpoint not in secure dir: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("secure checkpoint mode = %o, want 600", info.Mode().Perm())
	}
	// Best-effort snapshot under the artifact dir for read-only tooling.
	if _, err := os.Stat(snapshot); err != nil {
		t.Fatalf("workdir snapshot not written: %v", err)
	}
	// Both parse and agree on the run id.
	scp, err := LoadCheckpoint(secure)
	if err != nil {
		t.Fatalf("load secure: %v", err)
	}
	if scp.RunID != runID {
		t.Errorf("secure checkpoint runID = %q, want %q", scp.RunID, runID)
	}
}

// TestEngine_ExplicitCheckpointPathNotRelocated: an explicit WithCheckpointPath
// is honored as-is (the caller owns it) — no secure relocation, no snapshot.
func TestEngine_ExplicitCheckpointPathNotRelocated(t *testing.T) {
	t.Setenv(auditDirEnvVar, t.TempDir())
	cpPath := filepath.Join(t.TempDir(), "explicit-cp.json")

	g := NewGraph("cp_explicit_test")
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond", Label: "Start"})
	g.AddNode(&Node{ID: "end", Shape: "Msquare", Label: "End"})
	g.AddEdge(&Edge{From: "s", To: "end"})

	engine := NewEngine(g, newTestRegistry(), WithCheckpointPath(cpPath), WithArtifactDir(t.TempDir()))
	if _, err := engine.Run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if _, err := os.Stat(cpPath); err != nil {
		t.Fatalf("explicit checkpoint path not used: %v", err)
	}
}
