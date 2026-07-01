// ABOUTME: Tests that writeTempPlan surfaces file-write errors instead of swallowing them (#397).
package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteTempPlanSuccess(t *testing.T) {
	plan := "# Plan\n\nstep one\n"
	path, err := writeTempPlan(plan)
	if err != nil {
		t.Fatalf("writeTempPlan returned error: %v", err)
	}
	t.Cleanup(func() { os.Remove(path) })

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read temp plan: %v", err)
	}
	if string(got) != plan {
		t.Fatalf("temp plan content = %q, want %q", string(got), plan)
	}
}

func TestWriteTempPlanPropagatesCreateError(t *testing.T) {
	// Point the temp dir at a non-existent path so os.CreateTemp fails,
	// proving writeTempPlan surfaces the error rather than swallowing it.
	t.Setenv("TMPDIR", filepath.Join(t.TempDir(), "does-not-exist"))

	path, err := writeTempPlan("plan")
	if err == nil {
		os.Remove(path)
		t.Fatal("writeTempPlan = nil error, want error for unwritable temp dir")
	}
	if path != "" {
		t.Fatalf("writeTempPlan path = %q, want empty on error", path)
	}
}

func TestWriteTempPlanPropagatesWriteError(t *testing.T) {
	// Inject a read-only file so WriteString fails, proving writeTempPlan
	// surfaces the write error rather than swallowing it (#397).
	orig := createTempPlanFile
	t.Cleanup(func() { createTempPlanFile = orig })

	name := filepath.Join(t.TempDir(), "readonly.md")
	createTempPlanFile = func() (*os.File, error) {
		if err := os.WriteFile(name, nil, 0o400); err != nil {
			return nil, err
		}
		return os.OpenFile(name, os.O_RDONLY, 0)
	}

	path, err := writeTempPlan("plan")
	if err == nil {
		os.Remove(path)
		t.Fatal("writeTempPlan = nil error, want error when WriteString fails")
	}
	if path != "" {
		t.Fatalf("writeTempPlan path = %q, want empty on error", path)
	}
	if !strings.Contains(err.Error(), "write temp plan") {
		t.Fatalf("error = %v, want wrapped write temp plan error", err)
	}
}
