//go:build linux

package exec

import (
	"context"
	"errors"
	"os/exec"
	"testing"
)

func TestProbeLandlock_OnSupportedKernel(t *testing.T) {
	// GHA ubuntu-latest, Ubuntu 24.04, RHEL 9.4+ all have ABI v3.
	// If this test runs on an older kernel, t.Skip so the suite stays green.
	err := ProbeLandlock()
	if errors.Is(err, ErrLandlockUnavailable) {
		t.Skipf("kernel doesn't support Landlock ABI v3: %v", err)
	}
	if err != nil {
		t.Errorf("ProbeLandlock = %v, want nil on a supported kernel", err)
	}
}

func TestWrapBashCmd_ArgvShape(t *testing.T) {
	cmd := exec.CommandContext(context.Background(), "sh", "-c", "echo hello")
	wrapped := WrapBashCmd(cmd, "/home/user/run", []string{"workspace/**", ".ai/sprints/**"})

	// Expected argv: /proc/self/exe __jail-exec -- /home/user/run workspace/** .ai/sprints/** -- sh -c echo hello
	want := []string{
		"/proc/self/exe", "__jail-exec", "--",
		"/home/user/run", "workspace/**", ".ai/sprints/**",
		"--", "sh", "-c", "echo hello",
	}
	if len(wrapped.Args) != len(want) {
		t.Fatalf("wrapped argv length = %d, want %d (got %v)", len(wrapped.Args), len(want), wrapped.Args)
	}
	for i, a := range want {
		if wrapped.Args[i] != a {
			t.Errorf("arg[%d] = %q, want %q", i, wrapped.Args[i], a)
		}
	}
	if wrapped.Path != "/proc/self/exe" {
		t.Errorf("Path = %q, want /proc/self/exe", wrapped.Path)
	}
}

func TestWrapBashCmd_PreservesOriginalFields(t *testing.T) {
	// The wrapper rewrites Path/Args; everything else (Dir, Env, SysProcAttr,
	// Stdin/Stdout/Stderr, ctx) must survive untouched so the wrapped command
	// runs in the same environment as the original would have.
	cmd := exec.CommandContext(context.Background(), "sh", "-c", "true")
	cmd.Dir = "/tmp/some/dir"
	cmd.Env = []string{"FOO=bar", "BAZ=qux"}

	wrapped := WrapBashCmd(cmd, "/tmp/run", []string{"workspace/**"})

	if wrapped.Dir != "/tmp/some/dir" {
		t.Errorf("Dir = %q, want preserved %q", wrapped.Dir, "/tmp/some/dir")
	}
	if len(wrapped.Env) != 2 || wrapped.Env[0] != "FOO=bar" || wrapped.Env[1] != "BAZ=qux" {
		t.Errorf("Env not preserved: %v", wrapped.Env)
	}
	// Returned cmd should be the same instance (mutation, not new allocation).
	if wrapped != cmd {
		t.Errorf("WrapBashCmd returned a different *Cmd; expected in-place mutation")
	}
}
