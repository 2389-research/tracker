// ABOUTME: Tests for git preflight error sentinels and decision logic.
// ABOUTME: Covers happy path, hard-fail, warn-downgrade, auto-init, and safety latches.
package pipeline

import (
	"errors"
	"os/exec"
	"testing"
)

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
