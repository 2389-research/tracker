// ABOUTME: Unix-only symlink-refusal tests for the post-run capture writes (#529).
//go:build unix

package pipeline

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/2389-research/dippin-lang/ir"
)

// TestWriteRunManifestRefusesSymlink pins the O_NOFOLLOW / symlink-refusal
// defense on run.json. A tool subprocess running with cmd.Dir=workDir can
// pre-plant run.json as a symlink to an outside target; the post-run write
// must refuse it rather than follow it. Mirrors events_jsonl_symlink_test.go.
func TestWriteRunManifestRefusesSymlink(t *testing.T) {
	runDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(runDir, "activity.jsonl"),
		[]byte(`{"type":"pipeline_completed","terminal_status":"success"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	attackerTarget := filepath.Join(t.TempDir(), "stash.json")
	if err := os.WriteFile(attackerTarget, []byte("attacker scratch\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(attackerTarget, filepath.Join(runDir, RunManifestFile)); err != nil {
		t.Fatal(err)
	}

	if _, err := WriteRunManifest(runDir, "run-symlink"); err == nil {
		t.Error("WriteRunManifest followed a symlinked run.json; want refusal")
	}
	got, err := os.ReadFile(attackerTarget)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "attacker scratch\n" {
		t.Errorf("attacker target was overwritten through the symlink: %q", got)
	}
}

// TestWriteSpecArtifactsRefusesSymlink pins the same defense on a spec file.
func TestWriteSpecArtifactsRefusesSymlink(t *testing.T) {
	runDir := t.TempDir()
	attackerTarget := filepath.Join(t.TempDir(), "stash.dip")
	if err := os.WriteFile(attackerTarget, []byte("attacker scratch\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(attackerTarget, filepath.Join(runDir, SpecSourceFile)); err != nil {
		t.Fatal(err)
	}

	if _, err := WriteSpecArtifacts(runDir, SpecArtifacts{
		Source:   "workflow X\n",
		Workflow: &ir.Workflow{Name: "X"},
	}); err == nil {
		t.Error("WriteSpecArtifacts followed a symlinked workflow.dip; want refusal")
	}
	got, err := os.ReadFile(attackerTarget)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "attacker scratch\n" {
		t.Errorf("attacker target was overwritten through the symlink: %q", got)
	}
}
