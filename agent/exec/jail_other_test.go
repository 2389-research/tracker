//go:build !linux

package exec

import (
	"context"
	"errors"
	"os/exec"
	"testing"

	"pgregory.net/rapid"
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

func TestRunJailExec_NonLinux_HardError(t *testing.T) {
	// The __jail-exec subcommand is unreachable in production on non-Linux
	// (the handler refuses to install the wrap). A stray invocation must exit
	// non-zero rather than silently no-op into an unjailed exec.
	if code := RunJailExec([]string{"--", "/tmp/anchor", "workspace/**", "--", "true"}); code == 0 {
		t.Errorf("RunJailExec on non-Linux = 0, want non-zero exit")
	}
}

func TestSafeMkdirAll_NonLinux_FailsClosed(t *testing.T) {
	if err := SafeMkdirAll("/tmp/anchor", "workspace/sub", 0o755); !errors.Is(err, ErrLandlockUnavailable) {
		t.Errorf("SafeMkdirAll on non-Linux = %v, want ErrLandlockUnavailable", err)
	}
}

func TestSafeRemove_NonLinux_FailsClosed(t *testing.T) {
	if err := SafeRemove("/tmp/anchor", "workspace/out.txt"); !errors.Is(err, ErrLandlockUnavailable) {
		t.Errorf("SafeRemove on non-Linux = %v, want ErrLandlockUnavailable", err)
	}
}

// TestNonLinuxStubs_RefuseForAnyInput_Rapid pins the contract that the
// non-Linux fail-closed behavior is input-independent: no anchor/path
// combination can coax a write/mkdir/remove past the stub. (genLiteralSegment /
// genLiteralPath are defined in jail_property_test.go, which has no build
// constraint, so they are available in this !linux file too.)
func TestNonLinuxStubs_RefuseForAnyInput_Rapid(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		anchor := "/" + genLiteralPath(rt, "anchor")
		rel := genLiteralPath(rt, "rel")

		if f, err := OpenForWrite(anchor, rel, 0o644); !errors.Is(err, ErrLandlockUnavailable) {
			if f != nil {
				_ = f.Close()
			}
			rt.Fatalf("OpenForWrite(%q, %q) = %v, want ErrLandlockUnavailable", anchor, rel, err)
		}
		if err := SafeMkdirAll(anchor, rel, 0o755); !errors.Is(err, ErrLandlockUnavailable) {
			rt.Fatalf("SafeMkdirAll(%q, %q) = %v, want ErrLandlockUnavailable", anchor, rel, err)
		}
		if err := SafeRemove(anchor, rel); !errors.Is(err, ErrLandlockUnavailable) {
			rt.Fatalf("SafeRemove(%q, %q) = %v, want ErrLandlockUnavailable", anchor, rel, err)
		}
	})
}
