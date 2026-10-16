// ABOUTME: Tests that the CLI resume path (-r) resolves checkpoints through the
// ABOUTME: library resolver: honors --artifact-dir and reads the secure copy first (#696, #559).
package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/2389-research/tracker/pipeline"
)

// resolveCLICheckpoint drives the real CLI resume resolution
// (resolveRunCheckpoint) and returns the checkpoint path it picked.
func resolveCLICheckpoint(t *testing.T, workdir, artifactDir, runID string) (string, error) {
	t.Helper()
	info, err := resolveRunCheckpoint(runConfig{
		workdir:      workdir,
		artifactDir:  artifactDir,
		resumeID:     runID,
		pipelineFile: "pipeline.dip",
	})
	return info.CheckpointPath, err
}

func plantCLICheckpoint(t *testing.T, path, runID, gate string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := pipeline.SaveCheckpoint(&pipeline.Checkpoint{
		RunID: runID, EdgeSelections: map[string]string{"Gate": gate},
	}, path); err != nil {
		t.Fatal(err)
	}
}

// TestResolveRunCheckpoint_HonorsArtifactDir: a run started with
// --artifact-dir D lives in D/<runID>/, so `tracker -r <id> --artifact-dir D`
// must resolve there instead of failing on <workdir>/.tracker/runs (#696).
func TestResolveRunCheckpoint_HonorsArtifactDir(t *testing.T) {
	t.Setenv("TRACKER_AUDIT_DIR", t.TempDir())
	wd, ad := t.TempDir(), t.TempDir()
	const runID = "0123456789ab"
	legacy := filepath.Join(ad, runID, "checkpoint.json")
	plantCLICheckpoint(t, legacy, runID, "Approve")

	got, err := resolveCLICheckpoint(t, wd, ad, "0123")
	if err != nil {
		t.Fatalf("resume with --artifact-dir failed: %v", err)
	}
	if got != legacy {
		t.Fatalf("got %q, want %q", got, legacy)
	}
}

// TestResolveRunCheckpoint_PrefersSecureOverTamperedSnapshot is the #559
// invariant for the CLI path: `tracker -r` must read the authoritative secure
// checkpoint, never the tool-reachable workdir snapshot.
func TestResolveRunCheckpoint_PrefersSecureOverTamperedSnapshot(t *testing.T) {
	t.Setenv("TRACKER_AUDIT_DIR", t.TempDir())
	wd := t.TempDir()
	const runID = "0123456789cd"
	plantCLICheckpoint(t, filepath.Join(wd, ".tracker", "runs", runID, "checkpoint.json"), runID, "SkipReview")
	secure, err := pipeline.SecureCheckpointPath(runID)
	if err != nil {
		t.Fatal(err)
	}
	plantCLICheckpoint(t, secure, runID, "Approve")

	got, err := resolveCLICheckpoint(t, wd, "", runID)
	if err != nil {
		t.Fatalf("resolveRunCheckpoint: %v", err)
	}
	if got != secure {
		t.Fatalf("CLI resume resolved to %q, want secure %q", got, secure)
	}
	cp, err := pipeline.LoadCheckpoint(got)
	if err != nil {
		t.Fatal(err)
	}
	if cp.EdgeSelections["Gate"] != "Approve" {
		t.Fatalf("CLI resume loaded TAMPERED routing %q, want %q", cp.EdgeSelections["Gate"], "Approve")
	}
}

// TestResumeHintTarget_IncludesArtifactDir: the printed resume command must
// carry the run's --artifact-dir, or copying it fails exactly as #696 did.
func TestResumeHintTarget_IncludesArtifactDir(t *testing.T) {
	if got, want := resumeHintTarget("p.dip", "/tmp/ad"), "--artifact-dir /tmp/ad p.dip"; got != want {
		t.Fatalf("resumeHintTarget = %q, want %q", got, want)
	}
	if got, want := resumeHintTarget("p.dip", ""), "p.dip"; got != want {
		t.Fatalf("resumeHintTarget (default dir) = %q, want %q", got, want)
	}
}
