// ABOUTME: Pins the O_NOFOLLOW / symlink-refusal on the atomic checkpoint write (#559).
package pipeline

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestSaveCheckpointRefusesSymlinkedTemp guards #559: the checkpoint lives in the
// tool-reachable workdir, so a subprocess could pre-plant a symlink at
// checkpoint.json.tmp to redirect the runtime's own write to an outside target.
// O_NOFOLLOW must refuse it rather than write through the link.
func TestSaveCheckpointRefusesSymlinkedTemp(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("O_NOFOLLOW is a no-op on Windows; the threat model differs")
	}
	dir := t.TempDir()
	outside := filepath.Join(dir, "outside.txt")
	if err := os.WriteFile(outside, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "checkpoint.json")
	// Pre-plant the symlink an attacker would use.
	if err := os.Symlink(outside, path+".tmp"); err != nil {
		t.Fatal(err)
	}

	err := SaveCheckpoint(&Checkpoint{RunID: "r1", CurrentNode: "n"}, path)
	if err == nil {
		t.Fatal("SaveCheckpoint followed a symlinked temp file — want refusal")
	}
	// The outside target must be untouched (not overwritten with checkpoint JSON).
	got, _ := os.ReadFile(outside)
	if string(got) != "original" {
		t.Fatalf("outside target was written through the symlink: %q", got)
	}
}
