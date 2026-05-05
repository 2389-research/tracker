// ABOUTME: Tests for resolveUnderRoot — the path-confinement helper used by
// ABOUTME: tools that read or write LLM-supplied paths (write_enriched_sprint, generate_code).
package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveUnderRoot_RejectsTraversal(t *testing.T) {
	dir := t.TempDir()
	if _, err := resolveUnderRoot(dir, "../escape.txt"); err == nil {
		t.Errorf("expected escape via .. to be rejected")
	}
	if _, err := resolveUnderRoot(dir, "subdir/../../escape.txt"); err == nil {
		t.Errorf("expected escape via subdir/../.. to be rejected")
	}
}

func TestResolveUnderRoot_RejectsAbsoluteOutside(t *testing.T) {
	dir := t.TempDir()
	other := t.TempDir() // distinct directory
	if _, err := resolveUnderRoot(dir, filepath.Join(other, "x.txt")); err == nil {
		t.Errorf("expected absolute path outside root to be rejected")
	}
}

func TestResolveUnderRoot_AcceptsAbsoluteInside(t *testing.T) {
	dir := t.TempDir()
	got, err := resolveUnderRoot(dir, filepath.Join(dir, "sub", "x.txt"))
	if err != nil {
		t.Fatalf("expected absolute path inside root to be accepted, got: %v", err)
	}
	if !strings.HasPrefix(got, dir) {
		t.Errorf("resolved path %q should start with root %q", got, dir)
	}
}

// TestResolveUnderRoot_RejectsSymlinkEscape is the regression test for
// CodeRabbit's pr-feedback-5 finding: a symlink inside root pointing
// outside should not be usable to escape. The pure string-prefix check
// passed because the symlink path itself looked fine; symlink evaluation
// catches the escape.
func TestResolveUnderRoot_RejectsSymlinkEscape(t *testing.T) {
	if _, err := os.Stat("/tmp"); err != nil {
		t.Skip("symlink test requires a writable tmp")
	}
	root := t.TempDir()
	outside := t.TempDir()

	// Create a symlink inside root that points to outside.
	link := filepath.Join(root, "escape")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unsupported on this filesystem: %v", err)
	}

	// A path "escape/secret.txt" string-resolves to root/escape/secret.txt
	// (under root), but symlink-resolves to outside/secret.txt (outside).
	// The fixed helper must reject it.
	if _, err := resolveUnderRoot(root, "escape/secret.txt"); err == nil {
		t.Errorf("expected symlink-escape path to be rejected; symlink-aware containment check is missing or broken")
	}
}

// TestResolveUnderRoot_AcceptsNonexistentInRoot covers the write-path
// case: the file we're going to write doesn't exist yet, but the parent
// directory does. The helper should resolve the parent's symlinks and
// accept the path when the parent is under root.
func TestResolveUnderRoot_AcceptsNonexistentInRoot(t *testing.T) {
	root := t.TempDir()
	got, err := resolveUnderRoot(root, "newfile.txt")
	if err != nil {
		t.Fatalf("expected nonexistent path under root to be accepted, got: %v", err)
	}
	if !strings.HasPrefix(got, root) {
		t.Errorf("resolved path %q should start with root %q", got, root)
	}
}
