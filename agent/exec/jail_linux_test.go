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
	"sync"
	"testing"

	"pgregory.net/rapid"
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
	if err := os.MkdirAll(filepath.Join(anchor, "workspace"), 0755); err != nil {
		t.Fatal(err)
	}
	outsideRoot := t.TempDir()
	outsidePath := filepath.Join(outsideRoot, "escape.txt")
	insidePath := filepath.Join(anchor, "workspace", "ok.txt")
	// Run an allowed write FIRST so we can prove the re-exec path is
	// reachable, then attempt the denied write. Without the positive
	// side-effect the test would pass vacuously on any sh / __jail-exec
	// startup failure (#272 review, coderabbitai jail_linux_test.go:100).
	cmd := exec.Command(os.Args[0], "--", anchor, "workspace/**", "--",
		"sh", "-c", fmt.Sprintf("echo allowed > %s && echo pwned > %s", insidePath, outsidePath))
	cmd.Env = append(os.Environ(), "TRACKER_TEST_JAIL_EXEC=1")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Errorf("re-exec succeeded; expected non-zero exit. Output: %s", out)
	}
	if _, statErr := os.Stat(insidePath); statErr != nil {
		t.Fatalf("inside write did not succeed; test cannot prove the re-exec path was reached: %v. Output: %s", statErr, out)
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
	// Agent forges a symlink inside the jail pointing outside, then writes
	// through it. Assert the symlink itself was created so we can prove
	// the re-exec path was reachable — without that, "outside file
	// absent" passes vacuously when sh / __jail-exec startup fails
	// (#272 review, coderabbitai jail_linux_test.go:179-198).
	outsideDir := t.TempDir()
	linkPath := filepath.Join(anchor, "workspace", "link")
	cmdStr := fmt.Sprintf("ln -s %s %s && echo pwned > %s/escape.txt",
		outsideDir, linkPath, linkPath)
	cmd := exec.Command(os.Args[0], "--", anchor, "workspace/**", "--",
		"sh", "-c", cmdStr)
	cmd.Env = append(os.Environ(), "TRACKER_TEST_JAIL_EXEC=1")
	out, _ := cmd.CombinedOutput()
	if lst, statErr := os.Lstat(linkPath); statErr != nil {
		t.Fatalf("symlink at %q not created; re-exec path may not have run: %v. Output: %s", linkPath, statErr, out)
	} else if lst.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("path %q exists but is not a symlink; sh didn't run ln -s as expected. Output: %s", linkPath, out)
	}
	escapePath := filepath.Join(outsideDir, "escape.txt")
	if _, err := os.Stat(escapePath); err == nil {
		t.Errorf("file %q exists; symlink-escape was not blocked", escapePath)
	}
}

// --- Property/invariant tests (issue #282) ---
//
// These generalize the example-based tests above. The literal-segment/path
// generators (genLiteralSegment / genLiteralPath / genLiteralSegments) live in
// jail_property_test.go (no build constraint) and are shared across the
// package's Linux and non-Linux test files.

// TestOpenForWrite_AllowsLiteralLeaf_Rapid: any single literal filename under a
// fresh anchor opens and writes successfully. openat2's RESOLVE_BENEATH binds
// resolution to the anchor fd, so a benign relative name always lands inside.
func TestOpenForWrite_AllowsLiteralLeaf_Rapid(t *testing.T) {
	anchor := t.TempDir()
	rapid.Check(t, func(rt *rapid.T) {
		leaf := genLiteralSegment(rt, "leaf") + ".txt"
		f, err := OpenForWrite(anchor, leaf, 0o644)
		if err != nil {
			rt.Fatalf("OpenForWrite(%q) = %v, want success", leaf, err)
		}
		if _, err := f.Write([]byte("ok")); err != nil {
			rt.Errorf("Write: %v", err)
		}
		_ = f.Close()
		if _, err := os.Stat(filepath.Join(anchor, leaf)); err != nil {
			rt.Fatalf("file %q not created under anchor: %v", leaf, err)
		}
	})
}

// TestOpenForWrite_RejectsAbsolute_Rapid: any absolute path is refused with
// ErrPathEscape, regardless of where it points. RESOLVE_BENEATH only constrains
// relative resolution after the anchor fd, so OpenForWrite must reject absolute
// inputs defensively before the syscall.
func TestOpenForWrite_RejectsAbsolute_Rapid(t *testing.T) {
	anchor := t.TempDir()
	rapid.Check(t, func(rt *rapid.T) {
		abs := "/" + genLiteralPath(rt, "abs")
		_, err := OpenForWrite(anchor, abs, 0o644)
		if !errors.Is(err, ErrPathEscape) {
			rt.Fatalf("OpenForWrite(absolute %q) = %v, want errors.Is ErrPathEscape", abs, err)
		}
	})
}

// TestOpenForWrite_RejectsParentEscape_Rapid: any number of leading `../`
// segments escapes the anchor and the kernel returns EXDEV, which OpenForWrite
// maps to ErrPathEscape.
func TestOpenForWrite_RejectsParentEscape_Rapid(t *testing.T) {
	anchor := t.TempDir()
	rapid.Check(t, func(rt *rapid.T) {
		n := rapid.IntRange(1, 5).Draw(rt, "depth")
		rel := strings.Repeat("../", n) + genLiteralSegment(rt, "leaf")
		_, err := OpenForWrite(anchor, rel, 0o644)
		if !errors.Is(err, ErrPathEscape) {
			rt.Fatalf("OpenForWrite(parent-escape %q) = %v, want errors.Is ErrPathEscape", rel, err)
		}
	})
}

// TestOpenForWrite_RejectsSymlinkEscape_Rapid: a symlink component pointing
// outside the anchor is rejected (ELOOP → ErrPathEscape) no matter what the
// leaf written through it is named.
func TestOpenForWrite_RejectsSymlinkEscape_Rapid(t *testing.T) {
	base := t.TempDir()
	rapid.Check(t, func(rt *rapid.T) {
		anchor, err := os.MkdirTemp(base, "anchor")
		if err != nil {
			rt.Fatal(err)
		}
		outside, err := os.MkdirTemp(base, "outside")
		if err != nil {
			rt.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(anchor, "lnk")); err != nil {
			rt.Skipf("symlink unsupported: %v", err)
		}
		rel := "lnk/" + genLiteralSegment(rt, "leaf") + ".txt"
		_, err = OpenForWrite(anchor, rel, 0o644)
		if !errors.Is(err, ErrPathEscape) {
			rt.Fatalf("OpenForWrite through symlink %q = %v, want errors.Is ErrPathEscape", rel, err)
		}
	})
}

// TestOpenForWrite_PermissionDenied_NotEscape pins the EACCES-vs-escape
// classification (#275 review): a plain permission failure must NOT be reported
// as ErrPathEscape, because EACCES also fires for ordinary mode/ownership
// reasons and over-claiming "escape" would mislead operators.
func TestOpenForWrite_PermissionDenied_NotEscape(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root bypasses mode bits; cannot provoke EACCES")
	}
	anchor := t.TempDir()
	noWrite := filepath.Join(anchor, "ro")
	if err := os.Mkdir(noWrite, 0o500); err != nil {
		t.Fatal(err)
	}
	_, err := OpenForWrite(anchor, "ro/denied.txt", 0o644)
	if err == nil {
		t.Fatal("OpenForWrite into a 0500 dir succeeded; expected permission error")
	}
	// It must read as a permission error (EACCES → os.ErrPermission) ...
	if !errors.Is(err, os.ErrPermission) {
		t.Errorf("err = %v, want errors.Is(err, os.ErrPermission)", err)
	}
	// ... and must NOT be over-claimed as a path-escape (the #275 distinction).
	if errors.Is(err, ErrPathEscape) {
		t.Errorf("EACCES misclassified as ErrPathEscape: %v", err)
	}
}

// TestSafeMkdirAll_CreatesTree_Rapid: any literal relative directory path is
// created in full, and a second call over the same path is a no-op (the EEXIST
// tolerance — re-running over already-present components must not error).
func TestSafeMkdirAll_CreatesTree_Rapid(t *testing.T) {
	anchor := t.TempDir()
	rapid.Check(t, func(rt *rapid.T) {
		relDir := genLiteralPath(rt, "dir")
		if err := SafeMkdirAll(anchor, relDir, 0o755); err != nil {
			rt.Fatalf("SafeMkdirAll(%q) = %v, want nil", relDir, err)
		}
		fi, err := os.Stat(filepath.Join(anchor, relDir))
		if err != nil || !fi.IsDir() {
			rt.Fatalf("dir %q not created (stat err=%v)", relDir, err)
		}
		// Idempotent: existing components take the openat2-success branch.
		if err := SafeMkdirAll(anchor, relDir, 0o755); err != nil {
			rt.Fatalf("second SafeMkdirAll(%q) = %v, want nil (idempotent)", relDir, err)
		}
	})
}

// TestSafeMkdirAll_ConcurrentSamePath_Rapid exercises the EEXIST race branch:
// many goroutines create the same deep path at once. Each must return nil; the
// loser of the ENOENT-openat2 / mkdirat race re-opens the winner's directory
// (still under RESOLVE_NO_SYMLINKS). Run with -race for the data-race check.
func TestSafeMkdirAll_ConcurrentSamePath_Rapid(t *testing.T) {
	base := t.TempDir()
	rapid.Check(t, func(rt *rapid.T) {
		anchor, err := os.MkdirTemp(base, "anchor")
		if err != nil {
			rt.Fatal(err)
		}
		relDir := genLiteralPath(rt, "dir")
		const workers = 8
		var wg sync.WaitGroup
		errs := make([]error, workers)
		wg.Add(workers)
		for i := range workers {
			go func(i int) {
				defer wg.Done()
				errs[i] = SafeMkdirAll(anchor, relDir, 0o755)
			}(i)
		}
		wg.Wait()
		for i, e := range errs {
			if e != nil {
				rt.Fatalf("worker %d: SafeMkdirAll(%q) = %v, want nil under concurrency", i, relDir, e)
			}
		}
		if fi, err := os.Stat(filepath.Join(anchor, relDir)); err != nil || !fi.IsDir() {
			rt.Fatalf("dir %q missing after concurrent creation (err=%v)", relDir, err)
		}
	})
}

// TestSafeMkdirAll_RejectsSymlinkAtAnyDepth_Rapid: a symlink planted at any
// depth in the path chain causes resolution to fail with EXDEV/ELOOP →
// ErrPathEscape. depth==0 puts the symlink directly under the anchor.
func TestSafeMkdirAll_RejectsSymlinkAtAnyDepth_Rapid(t *testing.T) {
	base := t.TempDir()
	rapid.Check(t, func(rt *rapid.T) {
		anchor, err := os.MkdirTemp(base, "anchor")
		if err != nil {
			rt.Fatal(err)
		}
		outside, err := os.MkdirTemp(base, "outside")
		if err != nil {
			rt.Fatal(err)
		}
		depth := rapid.IntRange(0, 3).Draw(rt, "depth")
		comps := make([]string, depth)
		cur := anchor
		for i := range depth {
			comps[i] = genLiteralSegment(rt, fmt.Sprintf("c%d", i))
			cur = filepath.Join(cur, comps[i])
		}
		if depth > 0 {
			if err := os.MkdirAll(cur, 0o755); err != nil {
				rt.Fatal(err)
			}
		}
		if err := os.Symlink(outside, filepath.Join(cur, "lnk")); err != nil {
			rt.Skipf("symlink unsupported: %v", err)
		}
		parts := append(append([]string{}, comps...), "lnk", "child")
		relDir := filepath.Join(parts...)
		err = SafeMkdirAll(anchor, relDir, 0o755)
		if !errors.Is(err, ErrPathEscape) {
			rt.Fatalf("SafeMkdirAll through symlink at depth %d (%q) = %v, want errors.Is ErrPathEscape", depth, relDir, err)
		}
	})
}

// TestSafeRemove_RemovesFile_Rapid: a file created under the anchor is removed,
// and removing it again returns an error that is NOT ErrPathEscape (a missing
// file is ENOENT, not an escape attempt).
func TestSafeRemove_RemovesFile_Rapid(t *testing.T) {
	anchor := t.TempDir()
	rapid.Check(t, func(rt *rapid.T) {
		leaf := genLiteralSegment(rt, "leaf") + ".txt"
		f, err := OpenForWrite(anchor, leaf, 0o644)
		if err != nil {
			rt.Fatalf("setup OpenForWrite(%q): %v", leaf, err)
		}
		_ = f.Close()
		if err := SafeRemove(anchor, leaf); err != nil {
			rt.Fatalf("SafeRemove(%q) = %v, want nil", leaf, err)
		}
		if _, err := os.Stat(filepath.Join(anchor, leaf)); !os.IsNotExist(err) {
			rt.Fatalf("file %q still present after SafeRemove (stat err=%v)", leaf, err)
		}
		// Second remove: error, but not an escape misclassification.
		err = SafeRemove(anchor, leaf)
		if err == nil {
			rt.Fatalf("second SafeRemove(%q) = nil, want ENOENT error", leaf)
		}
		if errors.Is(err, ErrPathEscape) {
			rt.Fatalf("ENOENT misclassified as ErrPathEscape: %v", err)
		}
	})
}

// TestSafeRemove_RejectsSymlinkParent_Rapid: when any parent component is a
// symlink pointing outside the anchor, SafeRemove refuses with ErrPathEscape
// before unlinking — even if the named leaf doesn't exist.
func TestSafeRemove_RejectsSymlinkParent_Rapid(t *testing.T) {
	base := t.TempDir()
	rapid.Check(t, func(rt *rapid.T) {
		anchor, err := os.MkdirTemp(base, "anchor")
		if err != nil {
			rt.Fatal(err)
		}
		outside, err := os.MkdirTemp(base, "outside")
		if err != nil {
			rt.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(anchor, "lnk")); err != nil {
			rt.Skipf("symlink unsupported: %v", err)
		}
		rel := "lnk/" + genLiteralSegment(rt, "leaf") + ".txt"
		err = SafeRemove(anchor, rel)
		if !errors.Is(err, ErrPathEscape) {
			rt.Fatalf("SafeRemove through symlink parent %q = %v, want errors.Is ErrPathEscape", rel, err)
		}
	})
}

// TestSafeRemove_EmptyLeaf rejects degenerate leaf names that have no file to
// unlink (the guard that prevents unlinkat from acting on the parent dir).
func TestSafeRemove_EmptyLeaf(t *testing.T) {
	anchor := t.TempDir()
	for _, leaf := range []string{"", ".", "/"} {
		if err := SafeRemove(anchor, leaf); !errors.Is(err, ErrPathEscape) {
			t.Errorf("SafeRemove(%q) = %v, want errors.Is ErrPathEscape", leaf, err)
		}
	}
}

// TestLandlockDirForGlob_Rapid pins the static-prefix-ancestor mapping and the
// security-critical invariant that the derived Landlock RW directory is always
// the anchor itself or a descendant — never broader/outside. A regression here
// would grant the Bash tier write access outside the authored subtree.
func TestLandlockDirForGlob_Rapid(t *testing.T) {
	rapid.Check(t, func(rt *rapid.T) {
		anchor := "/" + strings.Join(genLiteralSegments(rt, "anchor", 1, 3), "/")
		g := genValidGlobForLandlock(rt, "g")

		got := landlockDirForGlob(anchor, g)

		// Invariant: result never escapes the anchor.
		if got != anchor && !strings.HasPrefix(got, anchor+"/") {
			rt.Fatalf("landlockDirForGlob(%q, %q) = %q, escapes anchor", anchor, g, got)
		}

		// Shape: result is anchor joined with the directory of the glob's
		// static prefix (the part before the first metachar), matching the
		// documented examples in jail_linux.go.
		idx := strings.IndexAny(g, "*?[{")
		prefix := g
		if idx >= 0 {
			prefix = g[:idx]
		}
		want := anchor
		if d := filepath.Dir(prefix); d != "." {
			want = filepath.Join(anchor, d)
		}
		if got != want {
			rt.Fatalf("landlockDirForGlob(%q, %q) = %q, want %q", anchor, g, got, want)
		}
	})
}

// genValidGlobForLandlock draws a glob in one of the shapes that pass
// validateGlobEntry and reach landlockDirForGlob at runtime.
func genValidGlobForLandlock(t *rapid.T, label string) string {
	switch rapid.IntRange(0, 3).Draw(t, label+"_shape") {
	case 0:
		return "**"
	case 1:
		return genLiteralPath(t, label+"_p") + "/**"
	case 2:
		return genLiteralPath(t, label+"_lit") // static literal path
	default:
		return genLiteralSegment(t, label+"_top") // single top-level name
	}
}
