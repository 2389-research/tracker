// ABOUTME: Tests for secure file-input staging into the run workdir (#553 Phase 3).
package pipeline

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStageInputFile_WritesToFixedPath(t *testing.T) {
	workDir := t.TempDir()
	rel, err := StageInputFile(workDir, "spec", []byte("hello spec"))
	if err != nil {
		t.Fatalf("stage: %v", err)
	}
	if rel != ".tracker/inputs/spec" {
		t.Fatalf("relative path = %q, want .tracker/inputs/spec", rel)
	}
	got, err := os.ReadFile(filepath.Join(workDir, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read staged: %v", err)
	}
	if string(got) != "hello spec" {
		t.Fatalf("staged contents = %q", got)
	}
	// Mode 0600 — no wider access to a possibly-sensitive input.
	info, _ := os.Stat(filepath.Join(workDir, filepath.FromSlash(rel)))
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("staged mode = %o, want 600", info.Mode().Perm())
	}
}

func TestStageInputFile_RejectsUnsafeName(t *testing.T) {
	workDir := t.TempDir()
	for _, name := range []string{"../escape", "a/b", "..", "", "sub/../x"} {
		if _, err := StageInputFile(workDir, name, []byte("x")); err == nil {
			t.Fatalf("expected rejection for unsafe name %q", name)
		}
	}
	// Nothing escaped the workdir.
	if _, err := os.Stat(filepath.Join(filepath.Dir(workDir), "escape")); err == nil {
		t.Fatal("a file escaped the workdir")
	}
}

func TestStageInputFile_SizeCap(t *testing.T) {
	workDir := t.TempDir()
	big := make([]byte, MaxInputFileBytes+1)
	if _, err := StageInputFile(workDir, "spec", big); err == nil || !strings.Contains(err.Error(), "cap") {
		t.Fatalf("expected size-cap rejection, got %v", err)
	}
}
