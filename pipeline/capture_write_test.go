// ABOUTME: Mode tests for the hardened post-run capture writes (spec artifacts, run.json).
package pipeline

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/2389-research/dippin-lang/ir"
)

// TestCaptureFilesCreatedMode0600 pins that every post-run capture file lands
// at 0o600 (#521, #529 parity with the activity-log mirror). The spec files
// hold params and reference material; run.json embeds the spec manifest. None
// may be world-readable.
func TestCaptureFilesCreatedMode0600(t *testing.T) {
	runDir := t.TempDir()
	body := []byte("reference material")
	sha := digest(body)

	if _, err := WriteSpecArtifacts(runDir, SpecArtifacts{
		Source:   "workflow X\n",
		Workflow: &ir.Workflow{Name: "X"},
		Inputs:   []SpecInput{{Name: "ref.md", Content: body}},
	}); err != nil {
		t.Fatalf("WriteSpecArtifacts: %v", err)
	}

	// A minimal activity log so the manifest assembler has something to read.
	if err := os.WriteFile(filepath.Join(runDir, "activity.jsonl"),
		[]byte(`{"type":"pipeline_completed","terminal_status":"success"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteRunManifest(runDir, "run-mode"); err != nil {
		t.Fatalf("WriteRunManifest: %v", err)
	}

	for _, name := range []string{
		SpecSourceFile,
		SpecIRFile,
		filepath.Join(SpecInputsDir, sha+".md"),
		RunManifestFile,
	} {
		info, err := os.Stat(filepath.Join(runDir, name))
		if err != nil {
			t.Fatalf("stat %s: %v", name, err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Errorf("%s mode = %#o, want 0600", name, got)
		}
	}
}

// TestRunManifestNarrowedOnRewrite covers the write-then-chmod ordering gap for
// run.json: a pre-existing wider-mode file must not retain its mode after the
// rewrite.
func TestRunManifestNarrowedOnRewrite(t *testing.T) {
	runDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(runDir, "activity.jsonl"),
		[]byte(`{"type":"pipeline_completed","terminal_status":"success"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Pre-create run.json wide open, as a prior run would have left it.
	if err := os.WriteFile(filepath.Join(runDir, RunManifestFile), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := WriteRunManifest(runDir, "run-rewrite"); err != nil {
		t.Fatalf("WriteRunManifest: %v", err)
	}
	info, err := os.Stat(filepath.Join(runDir, RunManifestFile))
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("run.json mode = %#o, want 0600 — pre-existing wide file not narrowed", got)
	}
}
