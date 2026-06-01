//go:build linux

package exec

import (
	"context"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

func TestApplyParentDeathSig_LocalEnvironment(t *testing.T) {
	// Both Exec entry points must wire Pdeathsig so spawned children die
	// when the test/tracker parent dies — defends against the #272 orphan
	// accumulation that fork-bombed a 4-core host (load avg 74).
	env := NewLocalEnvironment(t.TempDir())

	// We can't easily test the kernel behavior without forking, but we can
	// confirm the SysProcAttr.Pdeathsig field is set on the *exec.Cmd that
	// Exec* constructs. Use CommandWrapper to capture the cmd.
	var captured *exec.Cmd
	env.CommandWrapper = func(cmd *exec.Cmd) *exec.Cmd {
		captured = cmd
		return cmd
	}
	_, err := env.ExecCommand(context.Background(), "/bin/true", nil, 5*time.Second)
	if err != nil {
		t.Fatalf("ExecCommand: %v", err)
	}
	if captured == nil {
		t.Fatal("CommandWrapper not invoked")
	}
	if captured.SysProcAttr == nil {
		t.Fatal("SysProcAttr nil")
	}
	if captured.SysProcAttr.Pdeathsig != syscall.SIGKILL {
		t.Errorf("ExecCommand Pdeathsig = %v, want SIGKILL", captured.SysProcAttr.Pdeathsig)
	}

	captured = nil
	_, err = env.ExecCommandWithLimit(context.Background(), "/bin/true", nil, 5*time.Second, 1024)
	if err != nil {
		t.Fatalf("ExecCommandWithLimit: %v", err)
	}
	if captured == nil {
		t.Fatal("CommandWrapper not invoked for ExecCommandWithLimit")
	}
	if captured.SysProcAttr.Pdeathsig != syscall.SIGKILL {
		t.Errorf("ExecCommandWithLimit Pdeathsig = %v, want SIGKILL", captured.SysProcAttr.Pdeathsig)
	}
}
