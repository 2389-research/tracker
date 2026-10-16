// ABOUTME: A tool-writable artifact-dir snapshot planted as a FIFO must not hang a
// ABOUTME: resume that reads the secure checkpoint (#706 review).
//go:build unix

package tracker

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/2389-research/tracker/pipeline"
)

func TestResolveCheckpointIn_FIFOSnapshotDoesNotBlock(t *testing.T) {
	t.Setenv("TRACKER_AUDIT_DIR", t.TempDir())
	ad := t.TempDir()
	const runID = "0123456789ab"
	secure, err := pipeline.SecureCheckpointPath(runID)
	if err != nil {
		t.Fatal(err)
	}
	if err := pipeline.SaveCheckpoint(&pipeline.Checkpoint{RunID: runID, Timestamp: time.Unix(1000, 0)}, secure); err != nil {
		t.Fatal(err)
	}
	snap := filepath.Join(ad, runID, "checkpoint.json")
	if err := os.MkdirAll(filepath.Dir(snap), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(snap, 0o600); err != nil {
		t.Skipf("mkfifo: %v", err)
	}

	type res struct {
		path string
		err  error
	}
	ch := make(chan res, 1)
	go func() {
		p, err := ResolveCheckpointIn(ad, runID)
		ch <- res{p, err}
	}()
	select {
	case r := <-ch:
		if r.err != nil {
			t.Fatalf("ResolveCheckpointIn: %v", r.err)
		}
		if r.path != secure {
			t.Fatalf("got %q, want secure %q", r.path, secure)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ResolveCheckpointIn blocked on a FIFO artifact-dir snapshot; the secure checkpoint was valid")
	}
}
