// ABOUTME: Tests the per-run lock: a second holder is refused, a dead holder's
// ABOUTME: lock is replaced, and release frees it.
package pipeline

import (
	"errors"
	"os"
	"os/exec"
	"strconv"
	"testing"
)

func TestRunLock_ConcurrentRefusedStaleReplaced(t *testing.T) {
	t.Setenv("TRACKER_AUDIT_DIR", t.TempDir())
	release, err := AcquireRunLock("run1")
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	if _, err := AcquireRunLock("run1"); !errors.Is(err, ErrRunLocked) {
		t.Fatalf("second acquire err = %v, want ErrRunLocked", err)
	}
	release()
	release() // idempotent
	again, err := AcquireRunLock("run1")
	if err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
	again()

	// A lock left by a process that has exited is stale and replaced.
	cmd := exec.Command("sh", "-c", "exit 0")
	if err := cmd.Run(); err != nil {
		t.Skipf("cannot start a child process: %v", err)
	}
	path, _ := SecureRunLockPath("run1")
	if err := os.WriteFile(path, []byte(strconv.Itoa(cmd.Process.Pid)), 0o600); err != nil {
		t.Fatal(err)
	}
	stale, err := AcquireRunLock("run1")
	if err != nil {
		t.Fatalf("acquire over a dead holder's lock: %v", err)
	}
	stale()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("lock file survives release (stat err %v)", err)
	}
}
