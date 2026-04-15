// ABOUTME: Tests for ResolveSource and ResolveCheckpoint library helpers.
// ABOUTME: Covers filesystem-first, built-in fallback, and run-ID resolution.
package tracker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveSource_EmptyName(t *testing.T) {
	_, _, err := ResolveSource("", "")
	if err == nil {
		t.Fatal("expected error for empty name")
	}
}

func TestResolveSource_ExplicitFilePath(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "test.dip")
	content := "workflow test\n  goal: \"t\"\n  start: s\n  exit: e\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	src, info, err := ResolveSource(path, "")
	if err != nil {
		t.Fatalf("ResolveSource: %v", err)
	}
	if src != content {
		t.Errorf("got source %q, want %q", src, content)
	}
	if info.Name != "" {
		t.Errorf("filesystem hit should return empty WorkflowInfo, got %+v", info)
	}
}

func TestResolveSource_BareNameDipSuffix(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "myflow.dip")
	if err := os.WriteFile(path, []byte("workflow mine\n  goal: \"x\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src, _, err := ResolveSource("myflow", tmp)
	if err != nil {
		t.Fatalf("ResolveSource: %v", err)
	}
	if !strings.Contains(src, "workflow mine") {
		t.Errorf("expected bare-name with .dip suffix to resolve to file; got %q", src)
	}
}

func TestResolveSource_BareNameBuiltIn(t *testing.T) {
	tmp := t.TempDir()
	src, info, err := ResolveSource("build_product", tmp)
	if err != nil {
		t.Fatalf("ResolveSource built-in: %v", err)
	}
	if info.Name != "build_product" {
		t.Errorf("info.Name = %q", info.Name)
	}
	if !strings.Contains(src, "workflow ") {
		t.Errorf("built-in source missing workflow declaration")
	}
}

func TestResolveSource_LocalBeatsBuiltIn(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "build_product.dip")
	local := "workflow local_override\n"
	if err := os.WriteFile(path, []byte(local), 0o644); err != nil {
		t.Fatal(err)
	}
	src, info, err := ResolveSource("build_product", tmp)
	if err != nil {
		t.Fatalf("ResolveSource: %v", err)
	}
	if !strings.Contains(src, "local_override") {
		t.Errorf("local file should win over built-in, got %q", src)
	}
	if info.Name != "" {
		t.Errorf("local file hit should return empty WorkflowInfo, got %+v", info)
	}
}

func TestResolveSource_NotFound(t *testing.T) {
	tmp := t.TempDir()
	_, _, err := ResolveSource("no_such_workflow_anywhere", tmp)
	if err == nil {
		t.Fatal("expected error for unknown workflow")
	}
	if !strings.Contains(err.Error(), "Available built-in workflows") {
		t.Errorf("error should list available workflows: %q", err.Error())
	}
}

func TestResolveCheckpoint_EmptyRunID(t *testing.T) {
	_, err := ResolveCheckpoint(t.TempDir(), "")
	if err == nil {
		t.Fatal("expected error for empty run ID")
	}
}

func TestResolveCheckpoint_Found(t *testing.T) {
	tmp := t.TempDir()
	runID := "abc123def456"
	runDir := filepath.Join(tmp, ".tracker", "runs", runID)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cpPath := filepath.Join(runDir, "checkpoint.json")
	if err := os.WriteFile(cpPath, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveCheckpoint(tmp, runID)
	if err != nil {
		t.Fatalf("ResolveCheckpoint: %v", err)
	}
	if got != cpPath {
		t.Errorf("got %q, want %q", got, cpPath)
	}
}

func TestResolveCheckpoint_PrefixMatch(t *testing.T) {
	tmp := t.TempDir()
	runID := "abc123def456"
	runDir := filepath.Join(tmp, ".tracker", "runs", runID)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cpPath := filepath.Join(runDir, "checkpoint.json")
	if err := os.WriteFile(cpPath, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveCheckpoint(tmp, "abc123")
	if err != nil {
		t.Fatalf("ResolveCheckpoint prefix: %v", err)
	}
	if got != cpPath {
		t.Errorf("got %q, want %q", got, cpPath)
	}
}

func TestResolveCheckpoint_Ambiguous(t *testing.T) {
	tmp := t.TempDir()
	for _, id := range []string{"abc123", "abc456"} {
		d := filepath.Join(tmp, ".tracker", "runs", id)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "checkpoint.json"), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	_, err := ResolveCheckpoint(tmp, "abc")
	if err == nil {
		t.Fatal("expected ambiguous error")
	}
	if !strings.Contains(err.Error(), "ambiguous") {
		t.Errorf("error should mention 'ambiguous', got %q", err.Error())
	}
}

func TestResolveCheckpoint_NotFound(t *testing.T) {
	tmp := t.TempDir()
	if err := os.MkdirAll(filepath.Join(tmp, ".tracker", "runs"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := ResolveCheckpoint(tmp, "nonexistent")
	if err == nil {
		t.Fatal("expected not-found error")
	}
}

func TestResolveCheckpoint_CheckpointFileMissing(t *testing.T) {
	tmp := t.TempDir()
	runDir := filepath.Join(tmp, ".tracker", "runs", "halfrun")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := ResolveCheckpoint(tmp, "halfrun")
	if err == nil {
		t.Fatal("expected error when checkpoint.json is missing")
	}
	if !strings.Contains(err.Error(), "checkpoint not found") {
		t.Errorf("error should mention 'checkpoint not found', got %q", err.Error())
	}
}
