// ABOUTME: Tests for git preflight error sentinels and decision logic.
// ABOUTME: Covers happy path, hard-fail, warn-downgrade, auto-init, and safety latches.
package pipeline

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

// mustGit runs a git command in dir with deterministic author identity,
// failing the test if it returns a non-zero exit code. Skips if git is
// not on PATH (delegates to requireGit from git_artifacts_test.go).
func mustGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	requireGit(t)
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

// mustGitInit creates a git repo at dir or fails the test. Skips when
// git is not available on PATH (matches the requireGit pattern in
// git_artifacts_test.go) so `go test ./...` remains portable.
func mustGitInit(t *testing.T, dir string) {
	t.Helper()
	requireGit(t)
	cmd := exec.Command("git", "init", "-q")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init in %s: %v: %s", dir, err, out)
	}
}

func TestCheckGit_Installed(t *testing.T) {
	requireGit(t)
	installed, _, err := checkGit(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !installed {
		t.Fatalf("expected git to be installed (test env requirement)")
	}
}

func TestCheckGit_NotRepo(t *testing.T) {
	dir := t.TempDir()
	_, isRepo, err := checkGit(context.Background(), dir)
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
	_, isRepo, err := checkGit(context.Background(), dir)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !isRepo {
		t.Fatalf("expected git-initialized dir to be a repo")
	}
}

// TestCheckGit_BareRepoIsNotRepo pins the PR #235 review fix from Copilot:
// `git rev-parse --git-dir` returns success inside a bare repository, but
// bare repos have no work tree so `git commit` / `git merge` (the operations
// `requires: git` workflows actually use) will fail. checkGit now uses
// `--is-inside-work-tree` so bare repos correctly classify as isRepo=false,
// and `requires: git` workflows fail fast at preflight with the same
// remediation message as a plain non-repo directory.
func TestCheckGit_BareRepoIsNotRepo(t *testing.T) {
	requireGit(t)
	bare := filepath.Join(t.TempDir(), "bare.git")
	cmd := exec.Command("git", "init", "--bare", "-q", bare)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v: %s", err, out)
	}
	installed, isRepo, err := checkGit(context.Background(), bare)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !installed {
		t.Fatal("expected installed=true")
	}
	if isRepo {
		t.Fatalf("expected isRepo=false for bare repo (no work tree → git commit/merge will fail)")
	}
}

func TestSafetyLatches_HomeRefused(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home dir on this system: %v", err)
	}
	if err := safetyLatches(context.Background(), home); err == nil {
		t.Fatalf("expected refusal for home dir")
	}
}

func TestSafetyLatches_RootRefused(t *testing.T) {
	root := string(filepath.Separator)
	if err := safetyLatches(context.Background(), root); err == nil {
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
	if err := safetyLatches(context.Background(), child); err == nil {
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
	if err := safetyLatches(context.Background(), wt); err == nil {
		t.Fatalf("expected refusal for worktree dir")
	}
}

func TestSafetyLatches_NestedRefused_BareRepo(t *testing.T) {
	requireGit(t)
	bare := filepath.Join(t.TempDir(), "bare.git")
	cmd := exec.Command("git", "init", "--bare", "-q", bare)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v: %s", err, out)
	}
	if err := safetyLatches(context.Background(), bare); err == nil {
		t.Fatalf("expected refusal for bare repo dir")
	}
}

// TestSafetyLatches_NestedRefused_Submodule pins the PR #235 review case:
// the README/spec promise submodule coverage. Inside a submodule's working
// dir, `.git` is a FILE (containing `gitdir: ../.git/modules/<name>`), not
// a directory — so a parent-walk for a `.git` directory would have missed
// this. safetyLatches uses `git rev-parse --git-dir` which correctly resolves
// the file pointer, so the submodule path is refused like any other
// nested-repo case.
func TestSafetyLatches_NestedRefused_Submodule(t *testing.T) {
	requireGit(t)
	root := t.TempDir()

	// Build the submodule's source repo.
	submodSrc := filepath.Join(root, "submod-src")
	if err := os.MkdirAll(submodSrc, 0o755); err != nil {
		t.Fatal(err)
	}
	mustGit(t, submodSrc, "init", "-q")
	if err := os.WriteFile(filepath.Join(submodSrc, "README"), []byte("submod\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, submodSrc, "add", "README")
	mustGit(t, submodSrc, "commit", "-q", "-m", "init")

	// Build the parent repo.
	parent := filepath.Join(root, "parent")
	if err := os.MkdirAll(parent, 0o755); err != nil {
		t.Fatal(err)
	}
	mustGit(t, parent, "init", "-q")
	if err := os.WriteFile(filepath.Join(parent, "README"), []byte("parent\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, parent, "add", "README")
	mustGit(t, parent, "commit", "-q", "-m", "init")
	// Use the local file:// path with `protocol.file.allow=always` to satisfy
	// modern git's CVE-2022-39253 file:// fetch restriction inside test runs.
	mustGit(t, parent, "-c", "protocol.file.allow=always", "submodule", "add", "../submod-src", "sub")

	subWorktree := filepath.Join(parent, "sub")
	// Sanity: confirm sub/.git is a FILE (the submodule .git pointer), not a directory.
	info, err := os.Stat(filepath.Join(subWorktree, ".git"))
	if err != nil {
		t.Fatalf("submodule .git pointer not present: %v", err)
	}
	if info.IsDir() {
		t.Fatalf("expected submodule .git to be a FILE pointer (the regression case), got dir")
	}

	if err := safetyLatches(context.Background(), subWorktree); err == nil {
		t.Fatalf("expected refusal inside a submodule worktree")
	}
}

func TestSafetyLatches_CleanDirAllowed(t *testing.T) {
	dir := t.TempDir()
	if err := safetyLatches(context.Background(), dir); err != nil {
		t.Fatalf("unexpected refusal for clean dir: %v", err)
	}
}

func TestRunAutoInit_Success(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	if err := runAutoInit(context.Background(), dir, true, false, nil); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		t.Fatalf("expected .git after init: %v", err)
	}
}

func TestRunAutoInit_RefusedByLatch_Nested(t *testing.T) {
	parent := t.TempDir()
	mustGitInit(t, parent)
	child := filepath.Join(parent, "sub")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatal(err)
	}
	err := runAutoInit(context.Background(), child, true, false, nil)
	if !errors.Is(err, ErrGitAutoInitRefused) {
		t.Fatalf("want ErrGitAutoInitRefused, got %v", err)
	}
}

func TestRunAutoInit_NeedsAllowInit_NonInteractive(t *testing.T) {
	dir := t.TempDir()
	err := runAutoInit(context.Background(), dir, false /*allowInit*/, false /*interactive*/, nil)
	if !errors.Is(err, ErrGitAutoInitRefused) {
		t.Fatalf("want ErrGitAutoInitRefused, got %v", err)
	}
	if !strings.Contains(err.Error(), "--allow-init") {
		t.Fatalf("error must mention --allow-init: %v", err)
	}
}

func TestRunAutoInit_InteractiveYesAccepted(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	yes := func(string) bool { return true }
	if err := runAutoInit(context.Background(), dir, false /*allowInit*/, true /*interactive*/, yes); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		t.Fatalf("expected .git after init: %v", err)
	}
}

func TestRunAutoInit_InteractiveNoRejected(t *testing.T) {
	dir := t.TempDir()
	no := func(string) bool { return false }
	err := runAutoInit(context.Background(), dir, false, true, no)
	if !errors.Is(err, ErrGitAutoInitRefused) {
		t.Fatalf("want ErrGitAutoInitRefused, got %v", err)
	}
}

func TestPreflight_HappyPath_NoRequires_NoCheck(t *testing.T) {
	dir := t.TempDir()
	err := Preflight(context.Background(), PreflightConfig{
		WorkDir:  dir,
		Requires: nil,
		Policy:   GitPreflightAuto,
	})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
}

func TestPreflight_HappyPath_RequiresGit_InRepo(t *testing.T) {
	dir := t.TempDir()
	mustGitInit(t, dir)
	err := Preflight(context.Background(), PreflightConfig{
		WorkDir:  dir,
		Requires: []string{"git"},
		Policy:   GitPreflightAuto,
	})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
}

func TestPreflight_HardFail_RequiresGit_NotRepo(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	err := Preflight(context.Background(), PreflightConfig{
		WorkDir:  dir,
		Requires: []string{"git"},
		Policy:   GitPreflightAuto,
	})
	if !errors.Is(err, ErrGitWorkdirNotRepo) {
		t.Fatalf("want ErrGitWorkdirNotRepo, got %v", err)
	}
	if !strings.Contains(err.Error(), "git init") {
		t.Fatalf("error must include remediation 'git init': %v", err)
	}
}

func TestPreflight_Warn_RequiresGit_NotRepo(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	var warnings []string
	err := Preflight(context.Background(), PreflightConfig{
		WorkDir:  dir,
		Requires: []string{"git"},
		Policy:   GitPreflightWarn,
		Warner: func(format string, args ...any) {
			warnings = append(warnings, fmt.Sprintf(format, args...))
		},
	})
	if err != nil {
		t.Fatalf("want nil err under warn policy, got %v", err)
	}
	if len(warnings) == 0 {
		t.Fatalf("expected at least one warning")
	}
}

func TestPreflight_OffBypass(t *testing.T) {
	dir := t.TempDir()
	var warnings []string
	err := Preflight(context.Background(), PreflightConfig{
		WorkDir:  dir,
		Requires: []string{"git"},
		Policy:   GitPreflightOff,
		Warner: func(format string, args ...any) {
			warnings = append(warnings, fmt.Sprintf(format, args...))
		},
	})
	if err != nil {
		t.Fatalf("want nil err under off policy, got %v", err)
	}
	// With only `git` declared, off is silent — the dep is recognized,
	// just not enforced. Unrecognized deps still warn (covered by the
	// dedicated test TestPreflight_OffStillWarnsForUnrecognizedDeps).
	if len(warnings) != 0 {
		t.Fatalf("--git=off with only git in requires must be silent, got: %v", warnings)
	}
}

// TestPreflight_OffStillWarnsForUnrecognizedDeps pins the PR #235 review fix
// from Copilot: --git=off disables git ENFORCEMENT, not diagnostics. Authors
// who forward-declare deps the current tracker version doesn't recognize
// (e.g. `requires: docker`) should still see the "not yet implemented"
// warning — that's the whole point of forward-compatibility. Pre-fix, the
// off bypass returned before the requires scan ran, silencing those warnings.
func TestPreflight_OffStillWarnsForUnrecognizedDeps(t *testing.T) {
	dir := t.TempDir()
	var warnings []string
	err := Preflight(context.Background(), PreflightConfig{
		WorkDir:  dir,
		Requires: []string{"git", "docker", "jq"},
		Policy:   GitPreflightOff,
		Warner: func(format string, args ...any) {
			warnings = append(warnings, fmt.Sprintf(format, args...))
		},
	})
	if err != nil {
		t.Fatalf("want nil err under off policy, got %v", err)
	}
	dockerWarn, jqWarn := false, false
	for _, w := range warnings {
		if strings.Contains(w, "docker") && strings.Contains(w, "not yet implemented") {
			dockerWarn = true
		}
		if strings.Contains(w, "jq") && strings.Contains(w, "not yet implemented") {
			jqWarn = true
		}
	}
	if !dockerWarn {
		t.Errorf("expected 'docker not yet implemented' warning under --git=off, got %v", warnings)
	}
	if !jqWarn {
		t.Errorf("expected 'jq not yet implemented' warning under --git=off, got %v", warnings)
	}
}

func TestPreflight_RequireOverrideNoRequires(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	err := Preflight(context.Background(), PreflightConfig{
		WorkDir:  dir,
		Requires: nil,
		Policy:   GitPreflightRequire,
	})
	if !errors.Is(err, ErrGitWorkdirNotRepo) {
		t.Fatalf("want ErrGitWorkdirNotRepo (CLI override), got %v", err)
	}
}

func TestPreflight_AutoInit_Success(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	err := Preflight(context.Background(), PreflightConfig{
		WorkDir:   dir,
		Requires:  []string{"git"},
		Policy:    GitPreflightInit,
		AllowInit: true,
	})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		t.Fatalf("expected .git after auto-init: %v", err)
	}
}

func TestPreflight_AutoInit_RefusedNoLatch(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	err := Preflight(context.Background(), PreflightConfig{
		WorkDir:        dir,
		Requires:       []string{"git"},
		Policy:         GitPreflightInit,
		AllowInit:      false,
		InteractiveTTY: false,
	})
	if !errors.Is(err, ErrGitAutoInitRefused) {
		t.Fatalf("want ErrGitAutoInitRefused, got %v", err)
	}
}

func TestGraph_RequiredDeps_Empty(t *testing.T) {
	g := NewGraph("test")
	if got := g.RequiredDeps(); len(got) != 0 {
		t.Fatalf("want empty, got %v", got)
	}
}

func TestGraph_RequiredDeps_Parsed(t *testing.T) {
	g := NewGraph("test")
	g.Attrs["requires"] = "git, docker , jq"
	got := g.RequiredDeps()
	want := []string{"git", "docker", "jq"}
	if len(got) != len(want) {
		t.Fatalf("want %v, got %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("idx %d: want %q, got %q", i, want[i], got[i])
		}
	}
}

func TestGraph_RequiredDeps_WhitespaceOnly(t *testing.T) {
	g := NewGraph("test")
	g.Attrs["requires"] = "   "
	if got := g.RequiredDeps(); len(got) != 0 {
		t.Fatalf("want empty for whitespace-only, got %v", got)
	}
}

func TestGraph_RequiredDeps_Deduplicates(t *testing.T) {
	g := NewGraph("test")
	g.Attrs["requires"] = "git, docker, git, jq, docker"
	got := g.RequiredDeps()
	want := []string{"git", "docker", "jq"}
	if len(got) != len(want) {
		t.Fatalf("want %v (deduped, order preserved), got %v", want, got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("idx %d: want %q, got %q", i, want[i], got[i])
		}
	}
}

func TestPreflight_UnrecognizedRequiresWarns(t *testing.T) {
	dir := t.TempDir()
	mustGitInit(t, dir)
	var warnings []string
	err := Preflight(context.Background(), PreflightConfig{
		WorkDir:  dir,
		Requires: []string{"docker"},
		Policy:   GitPreflightAuto,
		Warner: func(format string, args ...any) {
			warnings = append(warnings, fmt.Sprintf(format, args...))
		},
	})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "docker") && strings.Contains(w, "not yet implemented") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected 'docker not yet implemented' warning, got %v", warnings)
	}
}

func TestPreflightErrorSentinels(t *testing.T) {
	sentinels := []error{
		ErrGitNotInstalled,
		ErrGitWorkdirNotRepo,
		ErrGitAutoInitRefused,
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
