package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/2389-research/tracker/agent/exec"
)

func TestApplyPatchToolUpdatesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "code.txt")
	if err := os.WriteFile(path, []byte("before\n"), 0644); err != nil {
		t.Fatalf("write seed file: %v", err)
	}

	tool := NewApplyPatchTool(exec.NewLocalEnvironment(dir))
	input := json.RawMessage(`{
		"patch": "*** Begin Patch\n*** Update File: code.txt\n@@\n-before\n+after\n*** End Patch\n"
	}`)

	result, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if result == "" {
		t.Fatal("expected non-empty result")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read patched file: %v", err)
	}
	if string(data) != "after\n" {
		t.Fatalf("patched content = %q, want %q", string(data), "after\n")
	}
}

func TestApplyPatchToolUsesContextToPatchCorrectOccurrence(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "code.txt")
	original := "alpha\nbefore\nomega\nalpha\nbefore\nzeta\n"
	if err := os.WriteFile(path, []byte(original), 0644); err != nil {
		t.Fatalf("write seed file: %v", err)
	}

	tool := NewApplyPatchTool(exec.NewLocalEnvironment(dir))
	input := json.RawMessage(`{
		"patch": "*** Begin Patch\n*** Update File: code.txt\n@@\n alpha\n before\n-zeta\n+after\n*** End Patch\n"
	}`)

	_, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read patched file: %v", err)
	}
	want := "alpha\nbefore\nomega\nalpha\nbefore\nafter\n"
	if string(data) != want {
		t.Fatalf("patched content = %q, want %q", string(data), want)
	}
}

func TestApplyPatchToolSupportsEndOfFileMarker(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "code.txt")
	if err := os.WriteFile(path, []byte("before"), 0644); err != nil {
		t.Fatalf("write seed file: %v", err)
	}

	tool := NewApplyPatchTool(exec.NewLocalEnvironment(dir))
	input := json.RawMessage(`{
		"patch": "*** Begin Patch\n*** Update File: code.txt\n@@\n-before\n+after\n*** End of File\n*** End Patch\n"
	}`)

	_, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read patched file: %v", err)
	}
	if string(data) != "after" {
		t.Fatalf("patched content = %q, want %q", string(data), "after")
	}
}

func TestApplyPatchToolSupportsMoveTo(t *testing.T) {
	dir := t.TempDir()
	oldPath := filepath.Join(dir, "old.txt")
	if err := os.WriteFile(oldPath, []byte("before\n"), 0644); err != nil {
		t.Fatalf("write seed file: %v", err)
	}

	tool := NewApplyPatchTool(exec.NewLocalEnvironment(dir))
	input := json.RawMessage(`{
		"patch": "*** Begin Patch\n*** Update File: old.txt\n*** Move to: new.txt\n@@\n-before\n+after\n*** End Patch\n"
	}`)

	_, err := tool.Execute(context.Background(), input)
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatalf("expected old path to be removed, stat err = %v", err)
	}

	newPath := filepath.Join(dir, "new.txt")
	data, err := os.ReadFile(newPath)
	if err != nil {
		t.Fatalf("read moved file: %v", err)
	}
	if string(data) != "after\n" {
		t.Fatalf("moved file content = %q, want %q", string(data), "after\n")
	}
}
