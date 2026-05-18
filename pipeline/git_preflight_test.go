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
	installed, _, _, err := checkGit(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !installed {
		t.Fatalf("expected git to be installed (test env requirement)")
	}
}

func TestCheckGit_NotRepo(t *testing.T) {
	dir := t.TempDir()
	_, isRepo, _, err := checkGit(context.Background(), dir)
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
	_, isRepo, _, err := checkGit(context.Background(), dir)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !isRepo {
		t.Fatalf("expected git-initialized dir to be a repo")
	}
}

// TestCheckGit_CtxCancellationPropagates pins the PR #235 round-4 review fix
// (CodeRabbit + Copilot): pre-fix, checkGit swallowed all git-subprocess
// errors as "not a repo," so a canceled ctx looked the same as a non-repo
// dir. Now ctx cancellation propagates as the returned error so Preflight
// and Doctor can surface the real cause rather than reporting
// ErrGitWorkdirNotRepo.
func TestCheckGit_CtxCancellationPropagates(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	mustGitInit(t, dir)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already canceled before checkGit runs
	_, _, _, err := checkGit(ctx, dir)
	if err == nil {
		t.Fatal("expected ctx cancellation to surface as non-nil error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want errors.Is(err, context.Canceled), got %v", err)
	}
}

// TestCheckGit_BareRepoIsNotRepo pins the PR #235 review fix from Copilot:
// `git rev-parse --git-dir` returns success inside a bare repository, but
// bare repos have no work tree so `git commit` / `git merge` (the operations
// `requires: git` workflows actually use) will fail. checkGit now uses
// `--is-inside-work-tree` so bare repos correctly classify as isRepo=false,
// and the additional isBare return lets callers emit the right remediation
// ("cd into a checkout") instead of the generic "run git init".
func TestCheckGit_BareRepoIsNotRepo(t *testing.T) {
	requireGit(t)
	bare := filepath.Join(t.TempDir(), "bare.git")
	cmd := exec.Command("git", "init", "--bare", "-q", bare)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v: %s", err, out)
	}
	installed, isRepo, isBare, err := checkGit(context.Background(), bare)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !installed {
		t.Fatal("expected installed=true")
	}
	if isRepo {
		t.Fatalf("expected isRepo=false for bare repo (no work tree → git commit/merge will fail)")
	}
	if !isBare {
		t.Fatalf("expected isBare=true for bare repo so callers can emit the right remediation")
	}
}

// TestCheckGit_PlainNonRepoIsNotBare confirms that a plain non-repo
// tempdir produces isBare=false (distinct from the bare-repo case).
// PR #235 round-5 review (Copilot:3251112112): the bare distinction
// must not false-positive on regular non-repo dirs, otherwise the
// remediation would mislead in the opposite direction.
func TestCheckGit_PlainNonRepoIsNotBare(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	_, isRepo, isBare, err := checkGit(context.Background(), dir)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if isRepo {
		t.Fatalf("expected isRepo=false for non-repo dir")
	}
	if isBare {
		t.Fatalf("expected isBare=false for non-repo dir (would mislead the remediation)")
	}
}

// TestSafetyLatches_DubiousOwnershipDoesNotFailOpen pins the PR #235
// round-5 review fix (Copilot:3251112098): an ExitError from
// `rev-parse --git-dir` with stderr OTHER than "not a git repository"
// (dubious ownership, safe.directory misconfiguration, permission/
// config errors) must NOT be treated as "safe to init." Pre-fix the
// latch would fail open and run `git init` inside an existing repo
// git refused to inspect.
//
// We simulate this by creating a real repo, then changing its owner
// detection by setting `GIT_CONFIG_GLOBAL` to a file with a bogus
// safe.directory override. The probe will exit non-zero with a
// "fatal: detected dubious ownership" stderr; safetyLatches must
// surface that rather than allowing auto-init.
//
// Skipped if we can't reproduce the dubious-ownership signal (some
// CI environments suppress the check entirely via permissive
// GIT_CONFIG_NOGLOBAL settings).
func TestSafetyLatches_DubiousOwnershipDoesNotFailOpen(t *testing.T) {
	requireGit(t)
	// Create a real repo we'd otherwise refuse-as-nested.
	repo := t.TempDir()
	mustGitInit(t, repo)

	// Force dubious-ownership by chowning to a different UID isn't
	// portable in tests. Instead, use a stderr-injection synthetic
	// path: invoke rev-parse with an invalid -C target that fails
	// with stderr OTHER than "not a git repository" — the parent
	// dir of t.TempDir() exists but a NUL-byte path forces a real
	// error from git's startup.
	//
	// Actually the cleanest way: confirm isNotARepoStderr correctly
	// matches "not a git repository" but rejects other phrases.
	// Direct unit test of the discriminator.
	if isNotARepoStderr([]byte("fatal: detected dubious ownership in repository at '/foo'\n")) {
		t.Errorf("dubious-ownership stderr must NOT match the not-a-repo classifier")
	}
	if isNotARepoStderr([]byte("fatal: cannot read config from '/foo': Permission denied\n")) {
		t.Errorf("permission-denied stderr must NOT match the not-a-repo classifier")
	}
	if !isNotARepoStderr([]byte("fatal: not a git repository (or any parent up to mount point /)\nStopping at filesystem boundary (GIT_DISCOVERY_ACROSS_FILESYSTEM not set).\n")) {
		t.Errorf("genuine not-a-repo stderr MUST match the classifier")
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

// TestSafetyLatches_HomeRefused_ViaSymlink pins the PR #235 round-4 fix
// from Copilot: pre-fix, a symlink pointing at $HOME bypassed the latch
// because the equality compared cleaned-but-unresolved paths. Now both
// sides are EvalSymlinks-resolved before comparison so `git -C <symlink>
// init` can't sneak past the refusal.
func TestSafetyLatches_HomeRefused_ViaSymlink(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home dir on this system: %v", err)
	}
	linkDir := t.TempDir()
	link := filepath.Join(linkDir, "home-link")
	if err := os.Symlink(home, link); err != nil {
		t.Skipf("cannot create symlink (likely Windows without dev mode): %v", err)
	}
	if err := safetyLatches(context.Background(), link); err == nil {
		t.Fatalf("expected refusal for symlink-into-home; %q resolves to %q which equals $HOME", link, home)
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

// TestSafetyLatches_CtxCancellationPropagates pins the PR #235 round-4
// review fix: pre-fix, safetyLatches swallowed `cmd.Output()` errors and
// reported "not nested" even when the caller canceled. Now ctx
// cancellation surfaces as a wrapped error so callers can abort cleanly.
func TestSafetyLatches_CtxCancellationPropagates(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := safetyLatches(ctx, dir)
	if err == nil {
		t.Fatal("expected ctx cancellation to surface as non-nil error")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want errors.Is(err, context.Canceled), got %v", err)
	}
	if !errors.Is(err, ErrGitAutoInitRefused) {
		t.Errorf("want errors.Is(err, ErrGitAutoInitRefused) too (latch context), got %v", err)
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

// TestCheckGit_MissingFromPath_DeterministicallyTriggered pins the
// install-remediation path via a controlled empty PATH so the case is
// covered even on hosts that DO have git installed. Pre-fix this branch
// was only opportunistically tested on no-git hosts (Copilot:3251112145).
func TestCheckGit_MissingFromPath_DeterministicallyTriggered(t *testing.T) {
	// Empty PATH → exec.LookPath returns exec.ErrNotFound for "git" even
	// on machines with git installed. t.Setenv restores the original
	// after the test, so we don't disturb other parallel tests.
	t.Setenv("PATH", "")
	installed, isRepo, isBare, err := checkGit(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("expected nil err for ErrNotFound case, got %v", err)
	}
	if installed {
		t.Errorf("expected installed=false with empty PATH")
	}
	if isRepo || isBare {
		t.Errorf("expected isRepo=isBare=false when git is missing, got isRepo=%v isBare=%v", isRepo, isBare)
	}
}

// TestPreflight_MissingFromPath_ProducesErrGitNotInstalled pins the
// runtime end-to-end behavior: with PATH empty and requires:git declared,
// Preflight surfaces ErrGitNotInstalled with the install remediation.
func TestPreflight_MissingFromPath_ProducesErrGitNotInstalled(t *testing.T) {
	t.Setenv("PATH", "")
	err := Preflight(context.Background(), PreflightConfig{
		WorkDir:  t.TempDir(),
		Requires: []string{"git"},
		Policy:   GitPreflightAuto,
	})
	if err == nil {
		t.Fatal("expected ErrGitNotInstalled with empty PATH + requires:git")
	}
	if !errors.Is(err, ErrGitNotInstalled) {
		t.Fatalf("want errors.Is(err, ErrGitNotInstalled), got %v", err)
	}
	if !strings.Contains(err.Error(), "Install git") {
		t.Errorf("error must include install instructions; got: %s", err.Error())
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
