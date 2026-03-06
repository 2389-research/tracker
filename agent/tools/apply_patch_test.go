package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/2389-research/mammoth-lite/agent/exec"
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
