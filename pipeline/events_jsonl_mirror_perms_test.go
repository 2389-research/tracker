// ABOUTME: Unix-only permission test for the Close-time run-dir mirror.
// ABOUTME: The mirror holds verbatim request bodies + full tool I/O (#519), so
// it must be 0600-in-0700 like the secure log (#213), not world-readable 0644.
//go:build unix

package pipeline

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestJSONLEventHandler_SnapshotMirrorPerms pins the mirror file to mode
// 0600 inside a 0700 run dir. Post-#519 the mirror carries verbatim
// provider request bodies and full untruncated tool stdout/stderr; a
// world-readable 0644-in-0755 mirror leaks every run's prompts and tool
// output to any local user on a shared host (#525).
func TestJSONLEventHandler_SnapshotMirrorPerms(t *testing.T) {
	secureBase := t.TempDir()
	t.Setenv(auditDirEnvVar, secureBase)
	t.Setenv(xdgStateHomeEnvVar, "")

	artifactDir := t.TempDir()
	runID := "snapshot-mirror-perms"

	h := NewJSONLEventHandler(artifactDir)
	h.HandlePipelineEvent(PipelineEvent{
		Type:      EventPipelineStarted,
		Timestamp: time.Date(2026, 5, 13, 10, 0, 0, 0, time.UTC),
		RunID:     runID,
	})
	if err := h.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	legacyDir := filepath.Join(artifactDir, runID)
	dirInfo, err := os.Stat(legacyDir)
	if err != nil {
		t.Fatalf("stat mirror dir: %v", err)
	}
	if perm := dirInfo.Mode().Perm(); perm != 0o700 {
		t.Errorf("mirror dir mode = %#o, want 0700", perm)
	}

	mirror := filepath.Join(legacyDir, "activity.jsonl")
	fileInfo, err := os.Stat(mirror)
	if err != nil {
		t.Fatalf("stat mirror file: %v", err)
	}
	if perm := fileInfo.Mode().Perm(); perm != 0o600 {
		t.Errorf("mirror file mode = %#o, want 0600", perm)
	}
}
