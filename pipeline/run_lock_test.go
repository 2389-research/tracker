// ABOUTME: Tests the per-run lock: a second holder is refused (also from
// ABOUTME: another process), and a holder that exits leaves no lock behind.
package pipeline

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestRunLock_ConcurrentRefusedAndReleased(t *testing.T) {
	t.Setenv("TRACKER_AUDIT_DIR", t.TempDir())
	release, err := AcquireRunLock("run1")
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	_, err = AcquireRunLock("run1")
	if !errors.Is(err, ErrRunLocked) || !strings.Contains(err.Error(), "pid") {
		t.Fatalf("second acquire err = %v, want ErrRunLocked naming the holder", err)
	}
	release()
	release() // idempotent
	again, err := AcquireRunLock("run1")
	if err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
	again()
}

// A lock file left by a process that has exited (crash, kill) is not held: the
// OS released the lock with the process, whatever the file still says.
func TestRunLock_LeftoverFileIsNotHeld(t *testing.T) {
	t.Setenv("TRACKER_AUDIT_DIR", t.TempDir())
	path, _ := SecureRunLockPath("run2")
	release, err := AcquireRunLock("run2")
	if err != nil {
		t.Fatal(err)
	}
	release()
	if err := os.WriteFile(path, []byte("999999"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := AcquireRunLock("run2")
	if err != nil {
		t.Fatalf("acquire over a leftover lock file: %v", err)
	}
	got()
}

// The lock excludes another process, not just another call in this one.
func TestRunLock_HeldAcrossProcesses(t *testing.T) {
	if os.Getenv("TRACKER_RUN_LOCK_CHILD") == "1" {
		if _, err := AcquireRunLock("run3"); errors.Is(err, ErrRunLocked) {
			os.Exit(3)
		}
		os.Exit(0)
	}
	dir := t.TempDir()
	t.Setenv("TRACKER_AUDIT_DIR", dir)
	release, err := AcquireRunLock("run3")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	cmd := exec.Command(os.Args[0], "-test.run", "^TestRunLock_HeldAcrossProcesses$")
	cmd.Env = append(os.Environ(), "TRACKER_RUN_LOCK_CHILD=1", "TRACKER_AUDIT_DIR="+dir)
	err = cmd.Run()
	var ee *exec.ExitError
	if !errors.As(err, &ee) || ee.ExitCode() != 3 {
		t.Fatalf("child process acquired the held lock (err %v)", err)
	}
}
