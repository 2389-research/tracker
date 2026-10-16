// ABOUTME: Tests that the authoritative checkpoint is relocated to the secure
// ABOUTME: state dir with a best-effort workdir snapshot for tooling (#559).
package pipeline

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
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

// threeNodeGraph696 is s -> mid -> end, so a "halted after s" checkpoint has
// work left to resume.
func threeNodeGraph696() *Graph {
	g := NewGraph("cp_resume_snapshot_test")
	g.AddNode(&Node{ID: "s", Shape: "Mdiamond", Label: "Start"})
	g.AddNode(&Node{ID: "mid", Shape: "box", Label: "Mid"})
	g.AddNode(&Node{ID: "end", Shape: "Msquare", Label: "End"})
	g.AddEdge(&Edge{From: "s", To: "mid"})
	g.AddEdge(&Edge{From: "mid", To: "end"})
	return g
}

// rewindCheckpoint696 rewrites the checkpoint at path to "only s completed".
func rewindCheckpoint696(t *testing.T, path string) {
	t.Helper()
	cp, err := LoadCheckpoint(path)
	if err != nil {
		t.Fatal(err)
	}
	cp.CompletedNodes = []string{"s"}
	cp.CurrentNode = "mid"
	if err := SaveCheckpoint(cp, path); err != nil {
		t.Fatal(err)
	}
}

// TestEngine_ResumeRefreshesArtifactSnapshot (#696): a resume pins the secure
// checkpoint via WithCheckpointPath; the artifact-dir snapshot that diagnose /
// audit / ListRuns read must keep tracking it instead of freezing at the
// pre-halt state.
func TestEngine_ResumeRefreshesArtifactSnapshot(t *testing.T) {
	secureBase := t.TempDir()
	t.Setenv(auditDirEnvVar, secureBase)
	ab := t.TempDir()
	g := threeNodeGraph696()

	res, err := NewEngine(g, newTestRegistry(), WithArtifactDir(ab)).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	secure := filepath.Join(secureBase, res.RunID, "checkpoint.json")
	snap := filepath.Join(ab, res.RunID, "checkpoint.json")
	rewindCheckpoint696(t, secure)
	stale := &Checkpoint{RunID: res.RunID, CompletedNodes: []string{"s"}, Timestamp: time.Unix(1, 0)}
	if err := SaveCheckpoint(stale, snap); err != nil {
		t.Fatal(err)
	}

	if _, err := NewEngine(g, newTestRegistry(), WithCheckpointPath(secure), WithArtifactDir(ab)).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, err := LoadCheckpoint(snap)
	if err != nil {
		t.Fatal(err)
	}
	sec, err := LoadCheckpoint(secure)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Timestamp.Equal(sec.Timestamp) || !reflect.DeepEqual(got.CompletedNodes, sec.CompletedNodes) {
		t.Fatalf("artifact-dir snapshot is stale after resume: snapshot completed=%v ts=%v, secure completed=%v ts=%v",
			got.CompletedNodes, got.Timestamp, sec.CompletedNodes, sec.Timestamp)
	}
}

// TestEngine_ResumeExplicitNonSecurePathNoSnapshot: only the secure checkpoint
// gets an artifact-dir snapshot on resume; any other explicit
// WithCheckpointPath is honored as-is with no snapshot.
func TestEngine_ResumeExplicitNonSecurePathNoSnapshot(t *testing.T) {
	t.Setenv(auditDirEnvVar, t.TempDir())
	ab := t.TempDir()
	g := threeNodeGraph696()
	cpPath := filepath.Join(t.TempDir(), "explicit-cp.json")

	res, err := NewEngine(g, newTestRegistry(), WithCheckpointPath(cpPath)).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	rewindCheckpoint696(t, cpPath)
	if _, err := NewEngine(g, newTestRegistry(), WithCheckpointPath(cpPath), WithArtifactDir(ab)).Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	snap := filepath.Join(ab, res.RunID, "checkpoint.json")
	if _, err := os.Stat(snap); !os.IsNotExist(err) {
		t.Fatalf("explicit non-secure checkpoint must not get a snapshot; stat %s: %v", snap, err)
	}
}
