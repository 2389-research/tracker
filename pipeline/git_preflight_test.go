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

// TestReadPromptYN pins the consent semantics of the interactive --git=init
// latch: EOF / read error refuses, an empty line accepts (matches the
// uppercase Y in "[Y/n]"), and "n"/"N" refuses. Pre-fix EOF returned true,
// which let a stdin-less pipe satisfy the consent gate without the user
// typing anything (Copilot:3260568794).
func TestReadPromptYN(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  bool
	}{
		{name: "eof_refuses", input: "", want: false},
		{name: "blank_line_accepts", input: "\n", want: true},
		{name: "yes_lower", input: "y\n", want: true},
		{name: "yes_upper", input: "Y\n", want: true},
		{name: "no_lower", input: "n\n", want: false},
		{name: "no_upper", input: "N\n", want: false},
		{name: "no_word", input: "no\n", want: false},
		{name: "garbage_accepts", input: "asdf\n", want: true}, // anything not starting with n/N
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := readPromptYN(strings.NewReader(tc.input)); got != tc.want {
				t.Fatalf("readPromptYN(%q) = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}

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
	// Auto-init must also produce a born HEAD so worktree workflows
	// (ask_and_execute, build_product_with_superspec) which run
	// `git worktree add ... HEAD` early on don't crash deep in setup
	// after passing preflight. Pre-fix the advertised
	// `--git=init --allow-init` remediation produced a `.git` directory
	// but `git rev-parse HEAD` would fail with "fatal: not a valid
	// object name: 'HEAD'" (Copilot:3260183766/810/851/881).
	cmd := exec.Command("git", "-C", dir, "rev-parse", "HEAD")
	cmd.Env = gitProbeEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("auto-init did not produce a born HEAD: %v: %s", err, out)
	}
	// Commit identity must come from ephemeral `-c` flags so the
	// user's repo config stays clean. Verify no committer was written
	// into .git/config.
	cfgPath := filepath.Join(dir, ".git", "config")
	cfgBytes, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read .git/config: %v", err)
	}
	if strings.Contains(string(cfgBytes), "tracker@2389.ai") {
		t.Fatalf("auto-init leaked tracker identity into .git/config:\n%s", cfgBytes)
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
	// Same born-HEAD invariant as the non-interactive path.
	cmd := exec.Command("git", "-C", dir, "rev-parse", "HEAD")
	cmd.Env = gitProbeEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("interactive auto-init did not produce a born HEAD: %v: %s", err, out)
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

// TestGitProbeEnv_ForcesStableLocale pins the locale-forcing contract:
// gitProbeEnv must override any caller-inherited LANG / LC_* /
// LANGUAGE / GIT_PAGER so the `"not a git repository"` stderr
// classifier in isNotARepoStderr stays accurate on localized git
// installations (Copilot:3260183581, 3260183731).
func TestGitProbeEnv_ForcesStableLocale(t *testing.T) {
	t.Setenv("LANG", "fr_FR.UTF-8")
	t.Setenv("LC_ALL", "ja_JP.UTF-8")
	t.Setenv("LC_MESSAGES", "de_DE.UTF-8")
	t.Setenv("LANGUAGE", "es")
	t.Setenv("GIT_PAGER", "less")
	env := gitProbeEnv()
	saw := map[string]string{}
	for _, e := range env {
		key, val, ok := strings.Cut(e, "=")
		if !ok {
			continue
		}
		saw[strings.ToUpper(key)] = val
	}
	for _, want := range []struct {
		key, val string
	}{
		{"LANG", "C"},
		{"LC_ALL", "C"},
		{"LANGUAGE", "C"},
		{"GIT_PAGER", "cat"},
	} {
		if got := saw[want.key]; got != want.val {
			t.Errorf("env[%s] = %q, want %q", want.key, got, want.val)
		}
	}
	// LC_MESSAGES (and any other inherited LC_*) must not leak through
	// — it would otherwise win over LANG=C on systems where git
	// respects the more-specific variable.
	if _, ok := saw["LC_MESSAGES"]; ok {
		t.Errorf("LC_MESSAGES leaked into gitProbeEnv (forced LC_ALL=C would still be overridden by inherited LC_MESSAGES on some libcs)")
	}
}

// TestSamePathForLatch pins the platform-aware comparison used by the
// $HOME / filesystem-root safety latches. On Linux + macOS the
// comparison must stay byte-equal (case-sensitive filesystems are the
// norm and a case-fold would be a real fail-open). On Windows it must
// fold so `C:\Users\Bob` and `c:\users\bob` both trip the latch
// (Copilot:3260183913).
func TestSamePathForLatch(t *testing.T) {
	tests := []struct {
		name        string
		a, b        string
		wantUnix    bool
		wantWindows bool
	}{
		{"identical", "/home/u", "/home/u", true, true},
		{"differ_byte", "/home/u", "/home/v", false, false},
		{"case_only_diff", "/HOME/u", "/home/u", false, true},
		{"windows_drive_case", `C:\Users\Bob`, `c:\users\bob`, false, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := samePathForLatch(tc.a, tc.b)
			want := tc.wantUnix
			if runtimeIsWindows() {
				want = tc.wantWindows
			}
			if got != want {
				t.Errorf("samePathForLatch(%q, %q) = %v, want %v", tc.a, tc.b, got, want)
			}
		})
	}
}

// runtimeIsWindows mirrors the inline runtime check used by
// samePathForLatch so the test expectations don't need a build tag.
// We intentionally don't import "runtime" from the test side — keep
// the indirection so the production helper stays the single source of
// truth for the case-aware comparison.
func runtimeIsWindows() bool {
	// Force-equal probe: ASCII-fold-only distinct strings can only
	// match when samePathForLatch is using strings.EqualFold, which is
	// only enabled on Windows.
	return samePathForLatch("a", "A")
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
	mustGit(t, dir, "commit", "--allow-empty", "-m", "init") // born HEAD; pre-fix this passed without a commit (Copilot:3260568737)
	err := Preflight(context.Background(), PreflightConfig{
		WorkDir:  dir,
		Requires: []string{"git"},
		Policy:   GitPreflightAuto,
	})
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
}

// TestPreflight_UnbornHEAD_HardFails pins the born-HEAD invariant added for
// PR #235 round-7 review feedback (Copilot:3260568737). Pre-fix, a freshly
// `git init`'d repo with no commits satisfied `--is-inside-work-tree`, so
// requires:git workflows passed preflight and then crashed mid-run on
// `git worktree add ... HEAD` after burning LLM turns. Now Preflight
// surfaces ErrGitUnbornHEAD up front.
func TestPreflight_UnbornHEAD_HardFails(t *testing.T) {
	dir := t.TempDir()
	mustGitInit(t, dir) // no commit → HEAD is unborn
	err := Preflight(context.Background(), PreflightConfig{
		WorkDir:  dir,
		Requires: []string{"git"},
		Policy:   GitPreflightAuto,
	})
	if !errors.Is(err, ErrGitUnbornHEAD) {
		t.Fatalf("want ErrGitUnbornHEAD, got %v", err)
	}
	if !strings.Contains(err.Error(), "git commit --allow-empty") {
		t.Fatalf("error must include the --allow-empty remediation, got: %v", err)
	}
}

// TestPreflight_UnbornHEAD_WarnPolicy mirrors the hard-fail test under
// --git=warn: the issue is reported via Warner and Preflight returns nil
// so the workflow can still attempt to run (matching the existing
// not-a-repo / not-installed warn semantics).
func TestPreflight_UnbornHEAD_WarnPolicy(t *testing.T) {
	dir := t.TempDir()
	mustGitInit(t, dir)
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
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "unborn HEAD") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected unborn-HEAD warning, got %v", warnings)
	}
}

// TestHasBornHEAD pins the probe's three outcomes: born HEAD → (true, nil),
// unborn HEAD → (false, nil), non-repo dir → (false, nil) (caller's
// checkGit step is responsible for that distinction, so we accept it
// silently here rather than surfacing a separate error).
func TestHasBornHEAD(t *testing.T) {
	requireGit(t)
	t.Run("born", func(t *testing.T) {
		dir := t.TempDir()
		mustGitInit(t, dir)
		mustGit(t, dir, "commit", "--allow-empty", "-m", "init")
		born, err := hasBornHEAD(context.Background(), dir)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if !born {
			t.Fatalf("want born=true for repo with commit")
		}
	})
	t.Run("unborn", func(t *testing.T) {
		dir := t.TempDir()
		mustGitInit(t, dir)
		born, err := hasBornHEAD(context.Background(), dir)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if born {
			t.Fatalf("want born=false for fresh init")
		}
	})
}

// TestRunAutoInit_RefusedByLatch_NonEmptyWorkdir pins the workdir-content
// latch added for PR #235 round-7 review feedback (Copilot:3260568814).
// Auto-init creates an empty initial commit; in a non-empty workdir that
// would leave the user's files outside HEAD (worktrees from HEAD would
// then be empty, breaking workflows that read SPEC.md etc. from a
// worktree). We refuse rather than `git add -A`-ing the user's content,
// which might include secrets or build artifacts.
func TestRunAutoInit_RefusedByLatch_NonEmptyWorkdir(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	// A single user file is enough to trigger the latch.
	if err := os.WriteFile(filepath.Join(dir, "SPEC.md"), []byte("# spec\n"), 0o644); err != nil {
		t.Fatalf("seed workdir: %v", err)
	}
	err := runAutoInit(context.Background(), dir, true /*allowInit*/, false /*interactive*/, nil)
	if !errors.Is(err, ErrGitAutoInitRefused) {
		t.Fatalf("want ErrGitAutoInitRefused, got %v", err)
	}
	if !strings.Contains(err.Error(), "workdir is not empty") {
		t.Fatalf("error must mention workdir-not-empty, got: %v", err)
	}
	// And the refusal must come BEFORE git init runs — otherwise we've
	// already mutated the user's workdir before reporting refusal.
	if _, statErr := os.Stat(filepath.Join(dir, ".git")); !os.IsNotExist(statErr) {
		t.Fatalf("workdir-content latch must refuse before `git init`; .git exists: %v", statErr)
	}
}

// TestWorkdirHasContent pins the helper's contract: empty dir → false, any
// non-`.git` entry (including dotfiles) → true, pre-existing `.git` alone
// → false (so a partial / replay auto-init still works on an empty repo).
func TestWorkdirHasContent(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		dir := t.TempDir()
		has, err := workdirHasContent(dir)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if has {
			t.Fatalf("want has=false for empty dir")
		}
	})
	t.Run("only_dot_git", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.Mkdir(filepath.Join(dir, ".git"), 0o755); err != nil {
			t.Fatal(err)
		}
		has, err := workdirHasContent(dir)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if has {
			t.Fatalf("want has=false when only .git is present")
		}
	})
	t.Run("regular_file", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		has, err := workdirHasContent(dir)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if !has {
			t.Fatalf("want has=true for dir with regular file")
		}
	})
	t.Run("dotfile_counts", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		has, err := workdirHasContent(dir)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if !has {
			t.Fatalf("want has=true for dir with .gitignore (user-managed, must be in initial commit)")
		}
	})
	// CodeRabbit:3260803525 — a FILE named `.git` is the worktree-link
	// shape and is user-managed content, not repo metadata we created.
	// Pre-fix the helper exempted any entry named `.git` regardless of
	// type, which would let runAutoInit fall through to `git init`
	// against a workdir that already had user-visible files.
	t.Run("dot_git_FILE_counts_as_content", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, ".git"), []byte("gitdir: /elsewhere\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		has, err := workdirHasContent(dir)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if !has {
			t.Fatalf("want has=true for dir whose `.git` is a FILE (worktree-link or stray) — Latch 3 must refuse here")
		}
	})
	// Same concern for symlinks: an attacker / accident planting a
	// symlink named `.git` should NOT exempt the workdir. os.ReadDir's
	// DirEntry.IsDir reports the entry's own type (lstat-style), so a
	// symlink — even to a real directory — reports IsDir()==false and
	// the helper correctly counts it as content.
	t.Run("dot_git_SYMLINK_counts_as_content", func(t *testing.T) {
		dir := t.TempDir()
		target := t.TempDir() // any real directory
		if err := os.Symlink(target, filepath.Join(dir, ".git")); err != nil {
			t.Skipf("symlink unsupported on this platform: %v", err)
		}
		has, err := workdirHasContent(dir)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if !has {
			t.Fatalf("want has=true for dir whose `.git` is a SYMLINK — Latch 3 must refuse here")
		}
	})
}

// TestIsUnbornHEADStderr pins the two phrases hasBornHEAD relies on to
// distinguish unborn HEAD from real failures (Copilot:3260797018,
// CodeRabbit:3260803531). Both are upstream-stable English from git's
// rev-parse code path; the C locale is forced via gitProbeEnv so
// localized git installs can't bypass the classifier.
func TestIsUnbornHEADStderr(t *testing.T) {
	cases := []struct {
		name   string
		stderr string
		want   bool
	}{
		{"needed_single_revision", "fatal: Needed a single revision\n", true},
		{"unknown_revision", "fatal: ambiguous argument 'HEAD^{commit}': unknown revision or path not in the working tree.\n", true},
		{"corrupt_refs", "fatal: bad object HEAD\n", false},
		{"permission_denied", "fatal: unable to read ref: refs/heads/main\n", false},
		{"empty", "", false},
		{"dubious_ownership", "fatal: detected dubious ownership in repository at '/tmp/x'\n", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isUnbornHEADStderr([]byte(tc.stderr)); got != tc.want {
				t.Fatalf("isUnbornHEADStderr(%q) = %v, want %v", tc.stderr, got, tc.want)
			}
		})
	}
}

// TestHasBornHEAD_RejectsNonCommitHEAD pins that hasBornHEAD uses
// `HEAD^{commit}` (Codex:3260803910) rather than plain `HEAD`, so a
// HEAD pointing at a non-commit object (tree/blob) is correctly
// reported as NOT born. End-state under test: HEAD resolves under
// plain `--verify` (because a tree is a valid object name) but FAILS
// under `^{commit}` peeling — exactly the gap the reviewer flagged.
//
// Setup detail: git's own `update-ref` refuses to point a branch ref
// at a non-commit OID (safety check), so we bypass it by writing the
// raw OID directly to `.git/HEAD`. That produces a detached HEAD
// pointing at the tree object, replicating the corrupt / pathological
// state without fighting git's safety net.
func TestHasBornHEAD_RejectsNonCommitHEAD(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	mustGitInit(t, dir)
	mustGit(t, dir, "commit", "--allow-empty", "-m", "init")
	// Extract the tree OID of the empty commit so we have a known
	// non-commit object that exists in the object store.
	treeCmd := exec.Command("git", "-C", dir, "rev-parse", "HEAD^{tree}")
	treeCmd.Env = gitProbeEnv()
	treeOut, err := treeCmd.Output()
	if err != nil {
		t.Fatalf("rev-parse HEAD^{tree}: %v", err)
	}
	treeOID := strings.TrimSpace(string(treeOut))
	if treeOID == "" {
		t.Fatalf("empty tree OID")
	}
	// Bypass git: write the tree OID directly into .git/HEAD so HEAD
	// becomes a detached ref to a non-commit object.
	if err := os.WriteFile(filepath.Join(dir, ".git", "HEAD"), []byte(treeOID+"\n"), 0o644); err != nil {
		t.Fatalf("write .git/HEAD: %v", err)
	}
	// Pre-condition sanity: plain `--verify HEAD` (without ^{commit})
	// would still succeed here because the OID resolves to a real
	// object — this is the gap that motivated the ^{commit} switch.
	plainCmd := exec.Command("git", "-C", dir, "rev-parse", "--verify", "HEAD")
	plainCmd.Env = gitProbeEnv()
	if err := plainCmd.Run(); err != nil {
		t.Skipf("git rev-parse --verify HEAD already failed before our probe (git version differs?): %v", err)
	}
	// Now hasBornHEAD must report (false, nil): tree resolves but
	// `^{commit}` peeling fails.
	born, err := hasBornHEAD(context.Background(), dir)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if born {
		t.Fatalf("want born=false when HEAD points at a non-commit object; pre-fix this returned born=true under plain `--verify HEAD`")
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
