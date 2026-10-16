// ABOUTME: LoadCheckpointBounded refuses an untrusted checkpoint path that is a FIFO,
// ABOUTME: a symlink or oversized, without blocking, and still reads a regular file (#706 review).
//go:build unix

package pipeline

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// loadWithin runs LoadCheckpointBounded and fails the test if it does not
// return within d — a FIFO opened for a blocking read would hang forever.
func loadWithin(t *testing.T, d time.Duration, path string, max int64) (*Checkpoint, error) {
	t.Helper()
	type res struct {
		cp  *Checkpoint
		err error
	}
	ch := make(chan res, 1)
	go func() {
		cp, err := LoadCheckpointBounded(path, max)
		ch <- res{cp, err}
	}()
	select {
	case r := <-ch:
		return r.cp, r.err
	case <-time.After(d):
		t.Fatalf("LoadCheckpointBounded(%s) did not return within %s", path, d)
		return nil, nil
	}
}

func TestLoadCheckpointBounded_RefusesFIFOWithoutBlocking(t *testing.T) {
	path := filepath.Join(t.TempDir(), "checkpoint.json")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	_, err := loadWithin(t, 5*time.Second, path, 1<<20)
	if err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("err = %v, want a not-a-regular-file refusal", err)
	}
}

func TestLoadCheckpointBounded_RefusesSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "real.json")
	if err := SaveCheckpoint(&Checkpoint{RunID: "r"}, target); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "checkpoint.json")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := loadWithin(t, 5*time.Second, link, 1<<20); err == nil {
		t.Fatal("LoadCheckpointBounded followed a symlink, want a refusal")
	}
}

func TestLoadCheckpointBounded_RefusesOversize(t *testing.T) {
	path := filepath.Join(t.TempDir(), "checkpoint.json")
	if err := os.WriteFile(path, []byte(`{"run_id":"r","pad":"`+strings.Repeat("x", 4096)+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := loadWithin(t, 5*time.Second, path, 1024)
	if err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Fatalf("err = %v, want a size refusal", err)
	}
}

func TestLoadCheckpointBounded_ReadsRegularFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "checkpoint.json")
	want := time.Unix(1000, 0).UTC()
	if err := SaveCheckpoint(&Checkpoint{RunID: "r", Timestamp: want}, path); err != nil {
		t.Fatal(err)
	}
	cp, err := loadWithin(t, 5*time.Second, path, 1<<20)
	if err != nil {
		t.Fatalf("LoadCheckpointBounded: %v", err)
	}
	if cp.RunID != "r" || !cp.Timestamp.Equal(want) {
		t.Fatalf("got run %q at %s, want r at %s", cp.RunID, cp.Timestamp, want)
	}
}
