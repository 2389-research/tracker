//go:build !linux

package exec

import (
	"context"
	"errors"
	"os/exec"
	"testing"
)

func TestProbeLandlock_NonLinux(t *testing.T) {
	err := ProbeLandlock()
	if !errors.Is(err, ErrLandlockUnavailable) {
		t.Errorf("ProbeLandlock on non-Linux = %v, want ErrLandlockUnavailable", err)
	}
}

func TestWrapBashCmd_NonLinux_Passthrough(t *testing.T) {
	cmd := exec.CommandContext(context.Background(), "/bin/echo", "hello")
	out := WrapBashCmd(cmd, "/tmp/anchor", []string{"workspace/**"})
	if out != cmd {
		t.Errorf("WrapBashCmd on non-Linux returned %p, want passthrough %p", out, cmd)
	}
	if len(out.Args) != 2 || out.Args[1] != "hello" {
		t.Errorf("argv mutated: %v", out.Args)
	}
}

func TestOpenForWrite_NonLinux_FailsClosed(t *testing.T) {
	f, err := OpenForWrite("/tmp/anchor", "test.txt", 0644)
	if f != nil {
		t.Error("OpenForWrite on non-Linux returned non-nil file; should fail closed")
		_ = f.Close()
	}
	if !errors.Is(err, ErrLandlockUnavailable) {
		t.Errorf("OpenForWrite on non-Linux = %v, want ErrLandlockUnavailable", err)
	}
}
