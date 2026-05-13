// ABOUTME: Unix-only symlink-defense tests for the Close-time snapshot path.
//go:build unix

package pipeline

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestJSONLEventHandler_SnapshotRefusesSymlinkedRunDir pins the
// intermediate-symlink defense for the Close-time snapshot. A tool
// subprocess that swaps <artifactDir>/<runID> for a symlink during the
// run must not redirect the snapshot to an attacker target. The
// snapshot is best-effort, so refusal is the safe outcome — the
// secure file remains authoritative and we just skip the legacy copy.
func TestJSONLEventHandler_SnapshotRefusesSymlinkedRunDir(t *testing.T) {
	secureBase := t.TempDir()
	t.Setenv(auditDirEnvVar, secureBase)
	t.Setenv(xdgStateHomeEnvVar, "")

	artifactDir := t.TempDir()
	runID := "snapshot-symlink-refuse"
	attackerTarget := filepath.Join(t.TempDir(), "attacker-stash")
	if err := os.MkdirAll(attackerTarget, 0o755); err != nil {
		t.Fatalf("mkdir attacker target: %v", err)
	}
	// Plant the symlink at <artifactDir>/<runID> before the runtime
	// gets to Close. This simulates a tool subprocess that ran with
	// cmd.Dir = workDir and did `ln -s /tmp/attacker .tracker/runs/<runID>`.
	if err := os.Symlink(attackerTarget, filepath.Join(artifactDir, runID)); err != nil {
		t.Fatalf("plant symlink: %v", err)
	}

	h := NewJSONLEventHandler(artifactDir)
	h.HandlePipelineEvent(PipelineEvent{
		Type:      EventPipelineStarted,
		Timestamp: time.Date(2026, 5, 13, 10, 0, 0, 0, time.UTC),
		RunID:     runID,
	})
	// Close returns nil even though the snapshot is skipped — the
	// secure file is authoritative; snapshot is best-effort.
	if err := h.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// The attacker target must NOT have received any writes.
	leaked := filepath.Join(attackerTarget, "activity.jsonl")
	if _, err := os.Stat(leaked); err == nil {
		t.Errorf("snapshot leaked to attacker target %s", leaked)
	}
	// Secure file still has the real event.
	securePath := filepath.Join(secureBase, runID, "activity.jsonl")
	if _, err := os.Stat(securePath); err != nil {
		t.Errorf("secure log missing: %v", err)
	}
}

// TestJSONLEventHandler_SecureOpenRefusesSymlink pins the O_NOFOLLOW
// defense on the live secure file open. If an out-of-band same-UID
// attacker pre-plants a symlink at the secure path, the runtime's
// O_APPEND writes must NOT flow into the symlink target. openFile
// returns an error and the handler stays uninitialized; events for
// this run are dropped (loud failure beats silent redirection).
func TestJSONLEventHandler_SecureOpenRefusesSymlink(t *testing.T) {
	secureBase := t.TempDir()
	t.Setenv(auditDirEnvVar, secureBase)
	t.Setenv(xdgStateHomeEnvVar, "")

	runID := "secure-open-symlink-refuse"
	secureDir := filepath.Join(secureBase, runID)
	if err := os.MkdirAll(secureDir, 0o700); err != nil {
		t.Fatalf("mkdir secureDir: %v", err)
	}
	attackerTarget := filepath.Join(t.TempDir(), "stash.log")
	if err := os.WriteFile(attackerTarget, []byte("attacker scratch\n"), 0o600); err != nil {
		t.Fatalf("plant attacker target: %v", err)
	}
	securePath := filepath.Join(secureDir, "activity.jsonl")
	if err := os.Symlink(attackerTarget, securePath); err != nil {
		t.Fatalf("plant symlink: %v", err)
	}

	h := NewJSONLEventHandler(t.TempDir())
	h.HandlePipelineEvent(PipelineEvent{
		Type:      EventPipelineStarted,
		Timestamp: time.Date(2026, 5, 13, 10, 0, 0, 0, time.UTC),
		RunID:     runID,
	})
	_ = h.Close()

	// Attacker target must be unchanged — no runtime writes flowed
	// through the symlink.
	got, err := os.ReadFile(attackerTarget)
	if err != nil {
		t.Fatalf("read attacker target: %v", err)
	}
	if string(got) != "attacker scratch\n" {
		t.Errorf("attacker target was written through symlink: %q", string(got))
	}
}
