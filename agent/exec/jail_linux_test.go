//go:build linux

package exec

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestMain dispatches to the jail-exec helper when the test binary is invoked
// as a re-exec child (TRACKER_TEST_JAIL_EXEC=1).
func TestMain(m *testing.M) {
	if os.Getenv("TRACKER_TEST_JAIL_EXEC") == "1" {
		// We are the re-exec child. Args are the same shape RunJailExec
		// expects after the binary path: -- anchor glob... -- cmd...
		os.Exit(RunJailExec(os.Args[1:]))
	}
	os.Exit(m.Run())
}

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

func TestRunJailExec_DeniesOutsideWrite(t *testing.T) {
	if errors.Is(ProbeLandlock(), ErrLandlockUnavailable) {
		t.Skip("Landlock unavailable on this host")
	}
	anchor := t.TempDir()
	outsideRoot := t.TempDir()
	outsidePath := filepath.Join(outsideRoot, "escape.txt")
	cmd := exec.Command(os.Args[0], "--", anchor, "workspace/**", "--",
		"sh", "-c", fmt.Sprintf("echo pwned > %s", outsidePath))
	cmd.Env = append(os.Environ(), "TRACKER_TEST_JAIL_EXEC=1")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Errorf("re-exec succeeded; expected non-zero exit. Output: %s", out)
	}
	if _, statErr := os.Stat(outsidePath); statErr == nil {
		t.Errorf("file %q exists; jail let the write through. Output: %s", outsidePath, out)
	}
}

func TestRunJailExec_AllowsInsideWrite(t *testing.T) {
	if errors.Is(ProbeLandlock(), ErrLandlockUnavailable) {
		t.Skip("Landlock unavailable on this host")
	}
	anchor := t.TempDir()
	if err := os.MkdirAll(filepath.Join(anchor, "workspace"), 0755); err != nil {
		t.Fatal(err)
	}
	insidePath := filepath.Join(anchor, "workspace", "ok.txt")
	cmd := exec.Command(os.Args[0], "--", anchor, "workspace/**", "--",
		"sh", "-c", fmt.Sprintf("echo allowed > %s", insidePath))
	cmd.Env = append(os.Environ(), "TRACKER_TEST_JAIL_EXEC=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("re-exec failed: %v. Output: %s", err, out)
	}
	contents, err := os.ReadFile(insidePath)
	if err != nil {
		t.Fatalf("inside file not created: %v", err)
	}
	if strings.TrimSpace(string(contents)) != "allowed" {
		t.Errorf("inside file contents = %q, want %q", string(contents), "allowed")
	}
}

func TestOpenForWrite_AllowsInsideAnchor(t *testing.T) {
	anchor := t.TempDir()
	f, err := OpenForWrite(anchor, "ok.txt", 0644)
	if err != nil {
		t.Fatalf("OpenForWrite: %v", err)
	}
	defer f.Close()
	if _, err := f.Write([]byte("hello")); err != nil {
		t.Errorf("Write: %v", err)
	}
}

func TestOpenForWrite_RejectsParentEscape(t *testing.T) {
	anchor := t.TempDir()
	_, err := OpenForWrite(anchor, "../escape.txt", 0644)
	if err == nil {
		t.Fatal("OpenForWrite for parent escape = nil error; want refuse")
	}
	if !errors.Is(err, ErrPathEscape) {
		t.Errorf("err = %v, want errors.Is(err, ErrPathEscape)", err)
	}
}

func TestOpenForWrite_RejectsSymlinkEscape(t *testing.T) {
	anchor := t.TempDir()
	// Create a symlink inside the anchor pointing outside.
	outside := t.TempDir()
	linkPath := filepath.Join(anchor, "link")
	if err := os.Symlink(outside, linkPath); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}
	_, err := OpenForWrite(anchor, "link/payload.txt", 0644)
	if err == nil {
		t.Fatal("OpenForWrite through symlink to outside = nil error; want refuse")
	}
	if !errors.Is(err, ErrPathEscape) {
		t.Errorf("err = %v, want errors.Is(err, ErrPathEscape)", err)
	}
}

func TestOpenForWrite_RejectsAbsolutePath(t *testing.T) {
	// Absolute paths must be rejected — they could escape the anchor regardless
	// of openat2's RESOLVE_BENEATH (which applies to relative components only
	// after the anchor FD is set).
	anchor := t.TempDir()
	_, err := OpenForWrite(anchor, "/etc/passwd", 0644)
	if err == nil {
		t.Fatal("OpenForWrite for absolute path = nil error; want refuse")
	}
}

func TestRunJailExec_DeniesSymlinkEscape(t *testing.T) {
	if errors.Is(ProbeLandlock(), ErrLandlockUnavailable) {
		t.Skip("Landlock unavailable on this host")
	}
	anchor := t.TempDir()
	if err := os.MkdirAll(filepath.Join(anchor, "workspace"), 0755); err != nil {
		t.Fatal(err)
	}
	// Agent forges a symlink inside the jail pointing outside, then writes through it.
	outsideDir := t.TempDir()
	cmdStr := fmt.Sprintf("ln -s %s %s/link && echo pwned > %s/link/escape.txt",
		outsideDir, filepath.Join(anchor, "workspace"), filepath.Join(anchor, "workspace"))
	cmd := exec.Command(os.Args[0], "--", anchor, "workspace/**", "--",
		"sh", "-c", cmdStr)
	cmd.Env = append(os.Environ(), "TRACKER_TEST_JAIL_EXEC=1")
	_, _ = cmd.CombinedOutput() // We don't care about exit code; symlink creation may succeed.
	escapePath := filepath.Join(outsideDir, "escape.txt")
	if _, err := os.Stat(escapePath); err == nil {
		t.Errorf("file %q exists; symlink-escape was not blocked", escapePath)
	}
}
