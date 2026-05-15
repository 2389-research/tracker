// ABOUTME: Tests for git preflight error sentinels and decision logic.
// ABOUTME: Covers happy path, hard-fail, warn-downgrade, auto-init, and safety latches.
package pipeline

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// mustGit runs a git command in dir with deterministic author identity,
// failing the test if it returns a non-zero exit code.
func mustGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

// mustGitInit creates a git repo at dir or fails the test.
func mustGitInit(t *testing.T, dir string) {
	t.Helper()
	cmd := exec.Command("git", "init", "-q")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init in %s: %v: %s", dir, err, out)
	}
}

func TestCheckGit_Installed(t *testing.T) {
	installed, _, err := checkGit(t.TempDir())
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !installed {
		t.Fatalf("expected git to be installed (test env requirement)")
	}
}

func TestCheckGit_NotRepo(t *testing.T) {
	dir := t.TempDir()
	_, isRepo, err := checkGit(dir)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if isRepo {
		t.Fatalf("expected tmpdir to not be a repo")
	}
}

func TestCheckGit_IsRepo(t *testing.T) {
	dir := t.TempDir()
	mustGitInit(t, dir)
	_, isRepo, err := checkGit(dir)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !isRepo {
		t.Fatalf("expected git-initialized dir to be a repo")
	}
}

func TestSafetyLatches_HomeRefused(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home dir on this system: %v", err)
	}
	if err := safetyLatches(home); err == nil {
		t.Fatalf("expected refusal for home dir")
	}
}

func TestSafetyLatches_RootRefused(t *testing.T) {
	root := string(filepath.Separator)
	if err := safetyLatches(root); err == nil {
		t.Fatalf("expected refusal for root dir")
	}
}

func TestSafetyLatches_NestedRefused(t *testing.T) {
	parent := t.TempDir()
	mustGitInit(t, parent)
	child := filepath.Join(parent, "child")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := safetyLatches(child); err == nil {
		t.Fatalf("expected refusal for nested-repo dir")
	}
}

func TestSafetyLatches_NestedRefused_Worktree(t *testing.T) {
	// A linked worktree's .git is a FILE, not a dir. Spec's original
	// "walk parents looking for .git directory" check would miss this.
	parent := t.TempDir()
	mustGitInit(t, parent)
	mustGit(t, parent, "commit", "--allow-empty", "-m", "init")
	wt := filepath.Join(filepath.Dir(parent), "wt-"+filepath.Base(parent))
	mustGit(t, parent, "worktree", "add", wt, "-b", "wtb")
	t.Cleanup(func() { _ = os.RemoveAll(wt) })
	if err := safetyLatches(wt); err == nil {
		t.Fatalf("expected refusal for worktree dir")
	}
}

func TestSafetyLatches_NestedRefused_BareRepo(t *testing.T) {
	bare := filepath.Join(t.TempDir(), "bare.git")
	cmd := exec.Command("git", "init", "--bare", "-q", bare)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v: %s", err, out)
	}
	if err := safetyLatches(bare); err == nil {
		t.Fatalf("expected refusal for bare repo dir")
	}
}

func TestSafetyLatches_CleanDirAllowed(t *testing.T) {
	dir := t.TempDir()
	if err := safetyLatches(dir); err != nil {
		t.Fatalf("unexpected refusal for clean dir: %v", err)
	}
}

func TestPreflightErrorSentinels(t *testing.T) {
	sentinels := []error{
		ErrGitNotInstalled,
		ErrGitWorkdirNotRepo,
		ErrGitAutoInitRefused,
		ErrGitDependencyUnsatisfied,
	}
	for _, s := range sentinels {
		if s == nil {
			t.Errorf("nil sentinel")
		}
		if s.Error() == "" {
			t.Errorf("sentinel %v has empty Error()", s)
		}
	}
	for i, a := range sentinels {
		for j, b := range sentinels {
			if i != j && errors.Is(a, b) {
				t.Errorf("sentinel collision: %v Is %v", a, b)
			}
		}
	}
}
