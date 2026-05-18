// ABOUTME: Git environment preflight — runs before any node executes.
// ABOUTME: Honors workflow `requires:` declarations and the --git= policy flag.
package pipeline

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// GitPreflight is the resolved preflight policy passed to Preflight.
// The empty string ("") resolves to "auto".
type GitPreflight string

const (
	GitPreflightAuto    GitPreflight = ""
	GitPreflightOff     GitPreflight = "off"
	GitPreflightWarn    GitPreflight = "warn"
	GitPreflightRequire GitPreflight = "require"
	GitPreflightInit    GitPreflight = "init"
)

// ValidPreflight reports whether v is a recognized policy value.
// The empty string is valid and resolves to auto.
func ValidPreflight(v GitPreflight) bool {
	switch v {
	case GitPreflightAuto, GitPreflightOff, GitPreflightWarn,
		GitPreflightRequire, GitPreflightInit:
		return true
	}
	return false
}

var (
	// ErrGitNotInstalled — git missing from PATH and the workflow requires it.
	ErrGitNotInstalled = errors.New("git not installed")
	// ErrGitWorkdirNotRepo — workdir is not inside a git repository and the workflow requires it.
	ErrGitWorkdirNotRepo = errors.New("workdir is not a git repository")
	// ErrGitUnbornHEAD — workdir is inside a git work tree but HEAD is unborn
	// (no commits yet). Distinct from ErrGitWorkdirNotRepo: `git rev-parse
	// --is-inside-work-tree` returns true here, but `git worktree add ...
	// HEAD`, `git log`, `git merge` all fail until at least one commit
	// exists. Surfaced at preflight so requires:git workflows fail fast
	// instead of mid-run after burning LLM turns.
	ErrGitUnbornHEAD = errors.New("workdir is a git repository with no commits (unborn HEAD)")
	// ErrGitAutoInitRefused — --git=init requested but a safety latch fired (home, root, nested).
	ErrGitAutoInitRefused = errors.New("auto-init refused by safety latch")
)

// PreflightConfig captures everything Preflight needs to make a decision.
// All fields are inputs only; no I/O happens until Preflight runs.
type PreflightConfig struct {
	WorkDir        string                           // absolute path; required
	Requires       []string                         // from graph.Attrs["requires"]
	Policy         GitPreflight                     // resolved from CLI > library > default ""
	AllowInit      bool                             // required when Policy == GitPreflightInit and !InteractiveTTY
	InteractiveTTY bool                             // when true, --git=init may prompt instead of needing --allow-init
	Warner         func(format string, args ...any) // optional; defaults to a no-op
	// PromptYN is used by --git=init in interactive mode. Tests inject a stub.
	// When nil, the default reads from stdin.
	PromptYN func(prompt string) bool
}

// Preflight runs the dependency checks declared by the workflow header
// against the environment, honoring the resolved policy. Returns nil on
// pass / bypass / downgraded-to-warning. Returns a typed error on hard fail.
//
// Safe to call multiple times — only side effect is the optional `git init`
// triggered by --git=init.
func Preflight(ctx context.Context, cfg PreflightConfig) error {
	warn := cfg.Warner
	if warn == nil {
		warn = func(string, ...any) {}
	}

	if !ValidPreflight(cfg.Policy) {
		// Unknown policy is treated as auto rather than failing the run.
		warn("tracker: unknown --git policy %q; treating as auto", string(cfg.Policy))
		cfg.Policy = GitPreflightAuto
	}

	// Scan declared deps and warn on unrecognized entries BEFORE checking
	// the off bypass. `--git=off` disables git enforcement; it should not
	// silence diagnostic warnings about other forward-declared deps the
	// current tracker version doesn't yet implement (those warnings are
	// the whole reason the requires: keyword is forward-compatible).
	requiresGit := false
	for _, dep := range cfg.Requires {
		switch strings.ToLower(strings.TrimSpace(dep)) {
		case "":
			// empty entry; skip
		case "git":
			requiresGit = true
		default:
			warn("tracker: requires %q is not yet implemented; ignoring", dep)
		}
	}

	if cfg.Policy == GitPreflightOff {
		return nil
	}

	// --git=require forces the check even if the workflow doesn't declare it.
	// --git=init also implies the check.
	if cfg.Policy == GitPreflightRequire || cfg.Policy == GitPreflightInit {
		requiresGit = true
	}

	if !requiresGit {
		return nil
	}

	// Honor ctx cancellation before each subprocess so a canceled run aborts
	// instead of running git probes or — worse — performing the --git=init
	// side effect.
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("preflight cancelled: %w", err)
	}
	installed, isRepo, isBare, err := checkGit(ctx, cfg.WorkDir)
	if err != nil {
		return fmt.Errorf("git check: %w", err)
	}
	if !installed {
		msg := buildGitNotInstalledMessage(cfg.WorkDir)
		if cfg.Policy == GitPreflightWarn {
			warn("%s", msg)
			return nil
		}
		return fmt.Errorf("%w: %s", ErrGitNotInstalled, msg)
	}
	if !isRepo {
		// Bare-repo case (or inside a .git directory): users need to cd into
		// a checkout, NOT run `git init`. --git=init would create a nested
		// repo here, so we skip the auto-init branch entirely for isBare.
		if isBare {
			msg := buildInsideBareRepoMessage(cfg.WorkDir)
			if cfg.Policy == GitPreflightWarn {
				warn("%s", msg)
				return nil
			}
			return fmt.Errorf("%w: %s", ErrGitWorkdirNotRepo, msg)
		}
		if cfg.Policy == GitPreflightInit {
			if err := ctx.Err(); err != nil {
				return fmt.Errorf("preflight cancelled before auto-init: %w", err)
			}
			if err := runAutoInit(ctx, cfg.WorkDir, cfg.AllowInit, cfg.InteractiveTTY, cfg.PromptYN); err != nil {
				return err
			}
			return nil
		}
		msg := buildWorkdirNotRepoMessage(cfg.WorkDir)
		if cfg.Policy == GitPreflightWarn {
			warn("%s", msg)
			return nil
		}
		return fmt.Errorf("%w: %s", ErrGitWorkdirNotRepo, msg)
	}
	// At this point isRepo == true: workdir is inside a real work tree.
	// Verify HEAD is born — requires:git workflows that run `git worktree
	// add ... HEAD`, `git merge`, etc. all fail in an unborn repo, and
	// `--is-inside-work-tree` returns true regardless of HEAD state. Catch
	// it here rather than letting the workflow crash mid-run.
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("preflight cancelled: %w", err)
	}
	born, headErr := hasBornHEAD(ctx, cfg.WorkDir)
	if headErr != nil {
		return fmt.Errorf("git check (HEAD): %w", headErr)
	}
	if !born {
		msg := buildUnbornHEADMessage(cfg.WorkDir)
		if cfg.Policy == GitPreflightWarn {
			warn("%s", msg)
			return nil
		}
		return fmt.Errorf("%w: %s", ErrGitUnbornHEAD, msg)
	}
	return nil
}

func buildGitNotInstalledMessage(workDir string) string {
	return strings.Join([]string{
		"this workflow requires git, but git was not found in PATH.",
		"",
		"  Working directory: " + workDir,
		"",
		"  Install git:",
		"    macOS:   brew install git",
		"    Linux:   apt install git  (or your distro's equivalent)",
		"    Windows: https://git-scm.com/download/win",
		"",
		"  Or pass --git=off to bypass this check if you're sure git isn't needed.",
	}, "\n")
}

// buildInsideBareRepoMessage is the remediation copy for the
// `requires: git` + workdir-is-bare-repo case (or inside any .git dir
// without a work tree). `git init` here would create a nested repo;
// the right answer is to cd into a checkout/worktree. Distinguished
// from buildWorkdirNotRepoMessage so the user gets the correct fix
// rather than the generic "run git init" steer.
func buildInsideBareRepoMessage(workDir string) string {
	return strings.Join([]string{
		"this workflow requires a working tree, but the current directory has no work tree (bare git repository or inside a .git directory).",
		"",
		"  Working directory: " + workDir,
		"",
		"  Bare repositories have no checked-out files, so workflows that need to commit/branch/merge can't run here.",
		"",
		"  cd into a checkout of this repo:",
		"    git clone <bare-repo-url> /path/to/checkout && cd /path/to/checkout",
		"  or, if you have a worktree set up:",
		"    git -C <bare-repo> worktree list",
		"",
		"  Or pass --git=off to bypass this check if you're sure git isn't needed.",
	}, "\n")
}

func buildWorkdirNotRepoMessage(workDir string) string {
	return strings.Join([]string{
		"this workflow requires a git repository, but the current directory is not inside one.",
		"",
		"  Working directory: " + workDir,
		"",
		"  Initialize a repo here (the empty initial commit gives HEAD something",
		"  to point at so `git worktree add ... HEAD` and `git merge` work):",
		"    git init",
		"    git commit --allow-empty -m \"initial\"",
		"",
		"  Or, in an empty directory, have tracker do it:",
		"    tracker <workflow> --git=init --allow-init",
		"",
		"  Or pass --git=off to bypass this check if you're sure git isn't needed.",
	}, "\n")
}

// buildUnbornHEADMessage is the remediation copy for the
// `requires: git` + isRepo=true + no commits case. Mirrors the
// buildWorkdirNotRepoMessage shape so users get the same instructions
// whether they're starting from scratch or recovering from a bare
// `git init`. The `--allow-empty` initial commit is the canonical fix —
// any content the user wants in HEAD they should stage first.
func buildUnbornHEADMessage(workDir string) string {
	return strings.Join([]string{
		"this workflow requires a git repository with at least one commit, but the current repo has no commits (unborn HEAD).",
		"",
		"  Working directory: " + workDir,
		"",
		"  Workflows that use `git worktree add ... HEAD`, `git log`, `git merge`,",
		"  or `git diff` against HEAD can't run until HEAD points at a real commit.",
		"",
		"  Create an initial commit. To capture the existing workdir contents:",
		"    git add .",
		"    git commit -m \"initial\"",
		"",
		"  Or, to start with an empty baseline (workflow files commit later):",
		"    git commit --allow-empty -m \"initial\"",
		"",
		"  Or pass --git=off to bypass this check if you're sure git isn't needed.",
	}, "\n")
}

// resolveSymlinksOrFallback returns filepath.EvalSymlinks(p) on success,
// or p unchanged if EvalSymlinks fails (e.g. p doesn't exist yet, which
// is the common case for the --git=init auto-init path). The fallback
// keeps the latch from silently failing-open when the workdir hasn't
// been created — we'd rather compare unresolved paths than skip the
// $HOME check entirely.
func resolveSymlinksOrFallback(p string) string {
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return resolved
	}
	return p
}

// samePathForLatch reports whether two resolved paths refer to the same
// directory for safetyLatches' purposes. On case-insensitive filesystems
// (Windows by default, macOS APFS/HFS+ when not formatted
// case-sensitive) `C:\Users\Bob` and `c:\users\bob` denote the same
// directory but byte-equal comparison would miss. We approximate with
// runtime.GOOS rather than probing the FS so the check is deterministic
// and doesn't need an existing path. The Linux/strict-unix path stays
// case-sensitive — common Linux ext4/xfs setups are case-sensitive and
// allowing a fold here would be a real fail-open.
func samePathForLatch(a, b string) bool {
	if a == b {
		return true
	}
	// Darwin's default APFS is case-insensitive at the filesystem layer,
	// but Go's runtime doesn't expose that signal cheaply and case-
	// sensitive APFS volumes exist. The conservative call is to fold on
	// Windows only — macOS users who manage to spell $HOME differently
	// than os.UserHomeDir() returns it would still hit byte equality in
	// the common case via filepath.Clean + EvalSymlinks normalization.
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return false
}

// isNotARepoStderr reports whether stderr captured from a non-zero
// `git rev-parse` exit corresponds to the benign "not a git repository"
// case rather than a real failure (dubious ownership, safe.directory,
// permission/config error, etc.). Distinguishing the two is what keeps
// safetyLatches from failing open and running `git init` inside a repo
// that git refused to inspect.
//
// The phrase comes straight from setup.c's `not_a_git_repository_msg`
// in upstream git; it has been stable since at least git 1.5.0. The
// callers force LANG/LC_ALL=C via gitProbeEnv so a localized git
// installation can't translate the diagnostic and bypass this check —
// pre-fix this would have classified a plain non-repo as an unexpected
// probe failure on French/Japanese/etc. systems.
func isNotARepoStderr(stderr []byte) bool {
	return strings.Contains(string(stderr), "not a git repository")
}

// SafetyLatches returns a wrapped ErrGitAutoInitRefused when auto-init would
// be unsafe at workDir, or nil if it would proceed. Exported so the tracker
// doctor preview check can model --git=init --allow-init behavior without
// duplicating the latch logic. Doctor callers can pass context.Background()
// or a real ctx; cancellation aborts the git subprocess.
//
// Refusal cases — see safetyLatches for the full list. This is a thin
// public alias.
func SafetyLatches(ctx context.Context, workDir string) error {
	return safetyLatches(ctx, workDir)
}

// HasBornHEAD reports whether HEAD in workDir points at a real commit.
// Thin public alias of hasBornHEAD; exported so the doctor's git-requires
// preview can model the same born-HEAD check the runtime preflight does.
// Returns (false, nil) for an unborn repo — error is reserved for
// unexpected I/O.
func HasBornHEAD(ctx context.Context, workDir string) (bool, error) {
	return hasBornHEAD(ctx, workDir)
}

// WorkdirHasContent reports whether workDir contains any entry other
// than `.git`. Thin public alias of workdirHasContent; exported so the
// doctor's --git=init --allow-init preview can model the auto-init
// workdir-content latch without duplicating the rule.
func WorkdirHasContent(workDir string) (bool, error) {
	return workdirHasContent(workDir)
}

// runAutoInit performs `git init` after running safety latches, then
// ensures the freshly-initialized repo has a born HEAD by creating an
// empty initial commit. The initial commit matters because worktree
// workflows (ask_and_execute, build_product_with_superspec) run
// `git worktree add ... HEAD` early — that fails in a freshly-init'd
// repo with no commits. Without this step the advertised
// `--git=init --allow-init` remediation would pass preflight and the
// pipeline would then crash deep in setup after burning user / LLM
// turns. The commit uses `-c user.name -c user.email` rather than
// `git config` so we don't write identity into the user's repo config.
//
// Required latches:
//   - allowInit == true OR interactive prompt answered "yes"
//   - safetyLatches(workDir) passes
//   - workdir is empty (no files outside any pre-existing `.git`) —
//     see workdirHasContent for the rationale (we refuse rather than
//     auto-`git add -A` user files that might be secrets / artifacts)
//
// Returns a wrapped ErrGitAutoInitRefused if any latch fires.
func runAutoInit(ctx context.Context, workDir string, allowInit bool, interactive bool, promptYN func(prompt string) bool) error {
	// Latch 1: explicit consent. --allow-init is required in non-interactive
	// mode. In interactive mode, the [Y/n] prompt substitutes.
	if !allowInit {
		if !interactive {
			return fmt.Errorf("%w: --git=init requires --allow-init in non-interactive runs.\n\n  Pass --allow-init to acknowledge that tracker may run `git init` in this directory, or run `git init` manually first.", ErrGitAutoInitRefused)
		}
		if promptYN == nil {
			promptYN = defaultPromptYN
		}
		if !promptYN(fmt.Sprintf("Initialize a git repository in %s? [Y/n] ", workDir)) {
			return fmt.Errorf("%w: user declined interactive prompt.\n\n  Re-run with --git=off to bypass the check, --git=warn to downgrade to a warning, or run `git init` manually if this is the right working directory.", ErrGitAutoInitRefused)
		}
	}
	// Latch 2: location safety.
	if err := safetyLatches(ctx, workDir); err != nil {
		return err
	}
	// Latch 3: workdir baseline content. Auto-init creates an empty initial
	// commit so HEAD is born, but it does NOT stage user files. In a
	// non-empty workdir that leaves the user's content outside HEAD —
	// workflows that `git worktree add ... HEAD` get an empty worktree
	// instead of one containing SPEC.md / sources / etc., and only
	// discover the breakage mid-run after the workflow has already spent
	// LLM turns. Auto-`git add -A`-ing instead would silently capture
	// `.env`, build artifacts, and anything the user hadn't yet decided
	// to track. The safer call is to refuse here and tell the user to
	// stage their own initial commit. Empty workdir → fall through to
	// the empty-commit path below.
	hasContent, contentErr := workdirHasContent(workDir)
	if contentErr != nil {
		return fmt.Errorf("%w: scan workdir: %v", ErrGitAutoInitRefused, contentErr)
	}
	if hasContent {
		// Use %q for workDir so paths containing spaces / special chars
		// produce copy/pasteable commands (Copilot:3260796955,
		// CodeRabbit:3260803559). %s here would emit unquoted shell-broken
		// commands for any non-trivial workdir path on macOS/Windows.
		return fmt.Errorf("%w: workdir is not empty.\n\n  --git=init creates an empty initial commit; tracker will not auto-`git add` your existing files (they might be secrets, build artifacts, or content you haven't decided to track). Stage and commit them yourself so you control the baseline:\n\n    git -C %q init\n    git -C %q add .\n    git -C %q commit -m \"initial\"\n\n  Or empty the workdir first, then re-run with --git=init --allow-init.", ErrGitAutoInitRefused, workDir, workDir, workDir)
	}
	initCmd := exec.CommandContext(ctx, "git", "-C", workDir, "init", "-q")
	initCmd.Env = gitProbeEnv()
	if out, err := initCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git init failed: %w: %s", err, out)
	}
	// Empty initial commit so HEAD is born. Uses ephemeral `-c` identity
	// so we don't mutate the user's git config. We rely on the default
	// commit hook configuration (no --no-verify, no -c commit.gpgsign=false)
	// — if the user has gpg signing globally enforced and it fails, we
	// surface the underlying git error so they can fix the environment
	// rather than silently producing an unsigned commit they didn't
	// expect. The clean repo we just created has no hooks, so the only
	// failure mode is gpg.
	commitCmd := exec.CommandContext(ctx, "git", "-C", workDir,
		"-c", "user.name=tracker",
		"-c", "user.email=tracker@2389.ai",
		"commit", "--allow-empty", "-m", "tracker: initial empty commit (auto-init)")
	commitCmd.Env = gitProbeEnv()
	if out, err := commitCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git commit (initial) failed: %w: %s", err, out)
	}
	return nil
}

// defaultPromptYN reads a line from stdin and returns true unless the user
// types something starting with "n" or "N". Empty input (i.e. an actual
// empty line after a successful read) defaults to yes — matching the
// uppercase Y in the "[Y/n]" prompt.
//
// EOF / read error returns false: this prompt is the safety latch when
// --allow-init was not passed, and a closed/erroring stdin is NOT an
// affirmative answer. Treating Scan() == false as "yes" would let a
// piped script with no stdin satisfy the consent gate without the user
// ever typing anything, defeating the latch.
func defaultPromptYN(prompt string) bool {
	fmt.Fprint(os.Stderr, prompt)
	return readPromptYN(os.Stdin)
}

// readPromptYN is the testable inner half of defaultPromptYN — same
// contract, but the input source is injected.
func readPromptYN(r io.Reader) bool {
	scanner := bufio.NewScanner(r)
	if !scanner.Scan() {
		return false // EOF / read error → NOT consent (see defaultPromptYN comment)
	}
	answer := strings.TrimSpace(scanner.Text())
	if answer == "" {
		return true
	}
	return !strings.HasPrefix(strings.ToLower(answer), "n")
}

// safetyLatches refuses `git init` for unsafe locations.
// Returns a wrapped ErrGitAutoInitRefused on refusal.
//
// Refusals:
//   - workDir is the user's $HOME
//   - workDir is the filesystem root
//   - workDir is already inside any git repo, including bare repos and
//     linked worktrees (detected via `git -C workDir rev-parse --git-dir`
//     rather than walking parents for a `.git` directory — the directory
//     form misses worktrees (.git is a file) and bare repos (no .git at all))
func safetyLatches(ctx context.Context, workDir string) error {
	abs, err := filepath.Abs(workDir)
	if err != nil {
		return fmt.Errorf("%w: resolve absolute path: %v", ErrGitAutoInitRefused, err)
	}
	// Resolve symlinks for both workdir and $HOME / root before comparing.
	// Pre-fix, `~/symlink-to-home` would equal `~/symlink-to-home` after
	// filepath.Clean but not equal `$HOME` after symlink resolution —
	// `git -C ~/symlink-to-home init` would then initialize the home tree
	// despite the safety refusal. EvalSymlinks errors are tolerated (e.g.
	// the dir doesn't exist yet for the auto-init path) — we fall back to
	// the unresolved comparison in that case so a missing path doesn't
	// silently pass the latch.
	absResolved := resolveSymlinksOrFallback(abs)
	if home, err := os.UserHomeDir(); err == nil {
		homeResolved := resolveSymlinksOrFallback(filepath.Clean(home))
		if samePathForLatch(absResolved, homeResolved) {
			return fmt.Errorf("%w: workdir equals $HOME (%s).\n\n  Initializing a repo at $HOME would place every file under your home tree into the repo's tracking space. cd into a project subdirectory first, or run `git init` here manually if this really is what you want.", ErrGitAutoInitRefused, home)
		}
	}
	// Filesystem-root refusal must be volume-aware for Windows. On Unix,
	// filepath.VolumeName("/") returns "" and the equality reduces to
	// abs == "/". On Windows, filepath.Abs("C:\\") returns "C:\" and the
	// equality compares against "C:\\" (VolumeName "C:" + separator "\\"),
	// matching the documented "/" refusal across platforms. Use the
	// case-aware comparison so a Windows caller spelling the drive
	// letter `c:\\` vs `C:\\` still triggers the refusal.
	if samePathForLatch(absResolved, filepath.VolumeName(absResolved)+string(filepath.Separator)) {
		return fmt.Errorf("%w: workdir is filesystem root (%s).\n\n  cd into a project subdirectory first.", ErrGitAutoInitRefused, absResolved)
	}
	// Use safetyLatches's `--git-dir` check (NOT `--is-inside-work-tree` like
	// the requires:git satisfaction probe in checkGit) because we want to
	// catch bare repos, linked worktrees, and submodules — all three resolve
	// a git dir even though work-tree-only operations would later fail.
	// Different questions: checkGit asks "would `git commit` work here?"
	// (work-tree required), safetyLatches asks "is this any kind of nested
	// git context where running `git init` would create a confusing
	// duplicate?" (any git-dir resolution counts).
	if _, lerr := exec.LookPath("git"); lerr != nil {
		// "git not found" is a benign reason to skip — caller should already
		// have surfaced ErrGitNotInstalled at the checkGit layer if it
		// matters. Other LookPath failures (permission denied, IO error,
		// PATH search short-circuit) are unexpected and propagated so
		// safetyLatches doesn't silently report "safe to init" when it
		// actually can't make the determination.
		if errors.Is(lerr, exec.ErrNotFound) {
			return nil
		}
		return fmt.Errorf("%w: resolve git in PATH: %v", ErrGitAutoInitRefused, lerr)
	}
	cmd := exec.CommandContext(ctx, "git", "-C", abs, "rev-parse", "--git-dir")
	cmd.Env = gitProbeEnv()
	out, runErr := cmd.Output()
	if runErr != nil {
		// Four cases:
		//   - ctx cancellation → propagate so callers can abort cleanly
		//   - ExitError with "fatal: not a git repository" stderr → safe to
		//     init, return nil (the documented "clean dir" outcome)
		//   - ExitError with any other stderr (dubious ownership / safe.directory
		//     / permission / config errors) → propagate so we don't fail open
		//     and run `git init` inside a repo git refused to inspect
		//   - non-ExitError (signal, I/O) → propagate as unexpected
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("%w: %w", ErrGitAutoInitRefused, ctxErr)
		}
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			if isNotARepoStderr(exitErr.Stderr) {
				return nil // expected — workdir is not a repo, safe to init
			}
			return fmt.Errorf("%w: git rev-parse --git-dir refused: %s", ErrGitAutoInitRefused, strings.TrimSpace(string(exitErr.Stderr)))
		}
		return fmt.Errorf("%w: git rev-parse --git-dir: %v", ErrGitAutoInitRefused, runErr)
	}
	if len(out) > 0 {
		// Inside some kind of repo. Distinguish bare vs work-tree for a
		// clearer error message.
		bareCmd := exec.CommandContext(ctx, "git", "-C", abs, "rev-parse", "--is-bare-repository")
		bareCmd.Env = gitProbeEnv()
		bareOut, bareErr := bareCmd.Output()
		if bareErr != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return fmt.Errorf("%w: %w", ErrGitAutoInitRefused, ctxErr)
			}
			// bareErr is an ExitError → couldn't determine bare-ness; default
			// to the non-bare message which is still accurate ("inside a repo").
		}
		if strings.TrimSpace(string(bareOut)) == "true" {
			return fmt.Errorf("%w: workdir is inside a bare git repository.\n\n  Bare repositories have no working tree, so workflows that need to commit/branch/merge can't run here. cd into a checkout of this repo (e.g. clone or worktree) and run from there.", ErrGitAutoInitRefused)
		}
		return fmt.Errorf("%w: workdir is inside a parent git repository", ErrGitAutoInitRefused)
	}
	return nil
}

// workdirHasContent reports whether workDir has any directory entry
// other than ".git". A freshly-created `.git` directory is the one
// thing auto-init is allowed to add; anything else means the user has
// existing content and the empty-initial-commit auto-init path would
// leave that content outside HEAD. See the Latch 3 comment in
// runAutoInit for the full rationale.
//
// We treat any non-`.git` entry — including dotfiles like `.gitignore`
// or `.env` — as content. A user with a `.gitignore` they want in HEAD
// has the same answer as a user with source files: make the initial
// commit yourself so you decide what's tracked.
//
// The `.git` exemption requires the entry to be a real directory
// (CodeRabbit:3260803525). A stray FILE or SYMLINK named `.git` is
// user-managed content, not repo metadata — exempting it here would let
// runAutoInit fall through to `git init` against a workdir that already
// has user-visible files, exactly the trap Latch 3 exists to prevent.
func workdirHasContent(workDir string) (bool, error) {
	entries, err := os.ReadDir(workDir)
	if err != nil {
		return false, err
	}
	for _, e := range entries {
		if e.Name() == ".git" && e.IsDir() {
			continue
		}
		return true, nil
	}
	return false, nil
}

// isUnbornHEADStderr reports whether stderr from `git rev-parse --verify
// HEAD^{commit}` corresponds to the benign unborn-HEAD case rather than a
// real failure (corrupt refs, permission error, etc.). The two phrases
// it matches are both from upstream git and have been stable across
// recent versions:
//
//   - "Needed a single revision" — emitted by older git (and by plain
//     `--verify HEAD` in newer git) when HEAD is unborn
//   - "unknown revision or path not in the working tree" — emitted by
//     newer git's `^{commit}` peeling code path when HEAD doesn't
//     resolve to a commit (covers both unborn HEAD AND dangling /
//     non-commit OIDs at HEAD)
//
// Distinguishing the two from other ExitError outputs keeps hasBornHEAD
// from collapsing every git failure into "unborn" — corruption-class
// failures need to surface as errors so callers can fix the underlying
// problem (or rerun under `--git=warn`) rather than getting the
// "create an initial commit" remediation steer.
//
// Callers force LANG/LC_ALL=C via gitProbeEnv so a localized git
// installation can't translate either phrase and bypass this check.
func isUnbornHEADStderr(stderr []byte) bool {
	s := string(stderr)
	return strings.Contains(s, "Needed a single revision") ||
		strings.Contains(s, "unknown revision or path not in the working tree")
}

// hasBornHEAD reports whether HEAD points at a real commit in workDir.
// Returns (false, nil) for an unborn HEAD (newly-init'd repo with no
// commits) — that case is expected and not an error. Returns an error
// for unexpected failures (corrupt refs, permission denied, etc.) so
// callers don't silently report "unborn, please git commit --allow-empty"
// when the actual problem is something `git fsck` would diagnose.
//
// Caller is responsible for having already established that workDir is
// inside a work tree (via checkGit); this probe is the second-stage
// check that catches the gap between `--is-inside-work-tree` (which
// says yes for unborn repos) and what requires:git workflows actually
// need (HEAD must point at a commit).
//
// Probe choice: `git rev-parse --verify HEAD^{commit}` rather than
// plain `--verify HEAD` (Codex:3260803910). `^{commit}` forces commit
// peeling, so a HEAD pointing at a dangling OID or a non-commit object
// (tree/blob in a pathological repo) fails the probe instead of
// masquerading as born. Plain `--verify HEAD` would say yes for any
// resolvable OID, including non-commit objects that `git worktree add
// HEAD` / `git log` would still fail on.
//
// Stderr inspection: pre-fix this function used `cmd.Run()` and treated
// every `*exec.ExitError` as `(false, nil)`. Go's `os/exec` package
// does NOT populate `ExitError.Stderr` for `Run()` — only `Output()` /
// `CombinedOutput()` capture stderr (Copilot:3260797018,
// CodeRabbit:3260803531). So the previous code couldn't distinguish
// unborn HEAD from corrupt refs even though the doc comment claimed it
// could. Switched to `CombinedOutput()` + `isUnbornHEADStderr` so
// unborn classifies as `(false, nil)` and anything else surfaces as an
// error.
func hasBornHEAD(ctx context.Context, workDir string) (bool, error) {
	if _, lerr := exec.LookPath("git"); lerr != nil {
		if errors.Is(lerr, exec.ErrNotFound) {
			// Caller's checkGit step would already have surfaced this as
			// ErrGitNotInstalled. If we got here git was found earlier but
			// disappeared from PATH between probes — surface it.
			return false, fmt.Errorf("locate git in PATH: %w", lerr)
		}
		return false, fmt.Errorf("locate git in PATH: %w", lerr)
	}
	cmd := exec.CommandContext(ctx, "git", "-C", workDir, "rev-parse", "--verify", "HEAD^{commit}")
	cmd.Env = gitProbeEnv()
	out, err := cmd.CombinedOutput()
	if err == nil {
		return true, nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return false, ctxErr
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if isUnbornHEADStderr(out) {
			return false, nil // expected — unborn HEAD or non-commit at HEAD
		}
		return false, fmt.Errorf("git rev-parse --verify HEAD^{commit} refused: %s", strings.TrimSpace(string(out)))
	}
	return false, fmt.Errorf("git rev-parse --verify HEAD^{commit}: %w", err)
}

// checkGit runs two cheap probes:
//  1. `git --version` — does git exist on PATH?
//  2. `git -C <workDir> rev-parse --is-inside-work-tree` — are we inside a
//     repo with a work tree?
//
// Returns (installed, isRepo, isBare, err). Returns an error only on
// unexpected I/O failure; "not installed" and "not a repo" surface as
// non-error states. isBare reports "inside a bare repository's GIT_DIR"
// (or, more generally, inside any git context that has no work tree) —
// distinct from isRepo, which is true only inside a real work tree.
// Callers can use isBare to emit a more accurate remediation ("cd into a
// checkout") instead of the generic "run git init" message.
//
// We use `--is-inside-work-tree`, NOT `--git-dir`, so bare repositories
// don't count as a "repo" for `requires: git` purposes: workflows declare
// requires:git because they need `git commit` / `git merge`, both of
// which require a work tree and fail in a bare repo. Reporting isRepo=true
// for a bare repo would defer that failure to mid-run instead of catching
// it here, which is the bug the preflight is meant to prevent.
//
// The not-a-repo case is distinguished from other ExitError failures by
// inspecting stderr for "not a git repository" — the upstream-stable
// phrase from setup.c. Dubious-ownership / safe.directory / permission
// errors all exit 128 but with distinct stderr; they propagate as real
// errors rather than fail-open as "not in a repo."
func checkGit(ctx context.Context, workDir string) (installed bool, isRepo bool, isBare bool, err error) {
	if _, lerr := exec.LookPath("git"); lerr != nil {
		if errors.Is(lerr, exec.ErrNotFound) {
			return false, false, false, nil
		}
		return false, false, false, fmt.Errorf("locate git in PATH: %w", lerr)
	}
	installed = true
	cmd := exec.CommandContext(ctx, "git", "-C", workDir, "rev-parse", "--is-inside-work-tree")
	cmd.Env = gitProbeEnv()
	out, runErr := cmd.Output()
	if runErr == nil {
		// Exit 0 outcomes:
		//   stdout="true"  → inside a real work tree (normal repo or linked
		//                    worktree). isRepo=true.
		//   stdout="false" → inside a bare repo or inside a .git directory.
		//                    isRepo=false (no work tree); isBare=true so
		//                    callers can give a better remediation.
		stdout := strings.TrimSpace(string(out))
		switch stdout {
		case "true":
			return installed, true, false, nil
		case "false":
			return installed, false, true, nil
		default:
			// Defensive: git contract says "true" or "false"; anything else
			// is unexpected. Surface rather than guess.
			return installed, false, false, fmt.Errorf("git rev-parse --is-inside-work-tree: unexpected output %q", stdout)
		}
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return installed, false, false, ctxErr
	}
	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		if isNotARepoStderr(exitErr.Stderr) {
			return installed, false, false, nil // expected — not in any repo
		}
		return installed, false, false, fmt.Errorf("git rev-parse --is-inside-work-tree refused: %s", strings.TrimSpace(string(exitErr.Stderr)))
	}
	return installed, false, false, fmt.Errorf("git rev-parse --is-inside-work-tree: %w", runErr)
}
