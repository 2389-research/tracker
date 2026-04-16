// ABOUTME: Tests for detectVerifyCommand build-system auto-detection.
package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectVerifyCommand_GoProject(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/test\n\ngo 1.21\n"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	got := detectVerifyCommand(dir)
	want := "go test ./..."
	if got != want {
		t.Errorf("detectVerifyCommand = %q, want %q", got, want)
	}
}

func TestDetectVerifyCommand_NodeProject(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"name":"test","scripts":{"test":"jest"}}`), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	got := detectVerifyCommand(dir)
	want := "npm test"
	if got != want {
		t.Errorf("detectVerifyCommand = %q, want %q", got, want)
	}
}

func TestDetectVerifyCommand_CargoProject(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Cargo.toml"), []byte("[package]\nname = \"test\"\n"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	got := detectVerifyCommand(dir)
	want := "cargo test"
	if got != want {
		t.Errorf("detectVerifyCommand = %q, want %q", got, want)
	}
}

func TestDetectVerifyCommand_MakefileWithTestTarget(t *testing.T) {
	dir := t.TempDir()
	makefile := ".PHONY: test\ntest:\n\techo running tests\n"
	if err := os.WriteFile(filepath.Join(dir, "Makefile"), []byte(makefile), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	got := detectVerifyCommand(dir)
	want := "make test"
	if got != want {
		t.Errorf("detectVerifyCommand = %q, want %q", got, want)
	}
}

func TestDetectVerifyCommand_MakefileWithoutTestTarget(t *testing.T) {
	dir := t.TempDir()
	// Makefile exists but has no "test:" target — should fall through to "".
	makefile := ".PHONY: build\nbuild:\n\tgo build ./...\n"
	if err := os.WriteFile(filepath.Join(dir, "Makefile"), []byte(makefile), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	got := detectVerifyCommand(dir)
	if got != "" {
		t.Errorf("detectVerifyCommand = %q, want %q (Makefile without test: target)", got, "")
	}
}

func TestDetectVerifyCommand_NoMarkers(t *testing.T) {
	dir := t.TempDir()
	// Completely empty directory.

	got := detectVerifyCommand(dir)
	if got != "" {
		t.Errorf("detectVerifyCommand = %q, want %q (empty dir)", got, "")
	}
}

func TestDetectVerifyCommand_PriorityGoOverNode(t *testing.T) {
	// go.mod takes priority over package.json (Go first in the list).
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/test\n\ngo 1.21\n"), 0644); err != nil {
		t.Fatalf("WriteFile go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{}`), 0644); err != nil {
		t.Fatalf("WriteFile package.json: %v", err)
	}

	got := detectVerifyCommand(dir)
	want := "go test ./..."
	if got != want {
		t.Errorf("detectVerifyCommand = %q, want %q (go.mod should take priority)", got, want)
	}
}

func TestTruncateTail(t *testing.T) {
	tests := []struct {
		name string
		s    string
		n    int
		want string
	}{
		{
			name: "short string unchanged",
			s:    "hello",
			n:    10,
			want: "hello",
		},
		{
			name: "exact length unchanged",
			s:    "hello",
			n:    5,
			want: "hello",
		},
		{
			name: "long string truncated from front",
			s:    "abcdefghij",
			n:    5,
			want: "...(truncated)\nfghij",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := truncateTail(tc.s, tc.n)
			if got != tc.want {
				t.Errorf("truncateTail(%q, %d) = %q, want %q", tc.s, tc.n, got, tc.want)
			}
		})
	}
}

func TestIsEditTool(t *testing.T) {
	editTools := []string{"write", "edit", "apply_patch", "notebook_edit"}
	for _, name := range editTools {
		if !isEditTool(name) {
			t.Errorf("isEditTool(%q) = false, want true", name)
		}
	}

	nonEditTools := []string{"read", "grep", "glob", "bash", "spawn_agent"}
	for _, name := range nonEditTools {
		if isEditTool(name) {
			t.Errorf("isEditTool(%q) = true, want false", name)
		}
	}
}
