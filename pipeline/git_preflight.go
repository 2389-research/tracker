// ABOUTME: Git environment preflight — runs before any node executes.
// ABOUTME: Honors workflow `requires:` declarations and the --git= policy flag.
package pipeline

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
	installed, isRepo, err := checkGit(ctx, cfg.WorkDir)
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

func buildWorkdirNotRepoMessage(workDir string) string {
	return strings.Join([]string{
		"this workflow requires a git repository, but the current directory is not inside one.",
		"",
		"  Working directory: " + workDir,
		"",
		"  Initialize a repo here:",
		"    git init",
		"",
		"  Or have tracker do it:",
		"    tracker <workflow> --git=init --allow-init",
		"",
		"  Or pass --git=off to bypass this check if you're sure git isn't needed.",
	}, "\n")
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

// runAutoInit performs `git init` after running safety latches.
//
// Required latches:
//   - allowInit == true OR interactive prompt answered "yes"
//   - safetyLatches(workDir) passes
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
	cmd := exec.CommandContext(ctx, "git", "-C", workDir, "init", "-q")
	cmd.Env = gitSafeEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git init failed: %w: %s", err, out)
	}
	return nil
}

// defaultPromptYN reads a line from stdin and returns true unless the user
// types something starting with "n" or "N". Empty input defaults to yes.
func defaultPromptYN(prompt string) bool {
	fmt.Fprint(os.Stderr, prompt)
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		return true // EOF → default yes
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
	if home, err := os.UserHomeDir(); err == nil && abs == filepath.Clean(home) {
		return fmt.Errorf("%w: workdir equals $HOME (%s).\n\n  Initializing a repo at $HOME would place every file under your home tree into the repo's tracking space. cd into a project subdirectory first, or run `git init` here manually if this really is what you want.", ErrGitAutoInitRefused, home)
	}
	// Filesystem-root refusal must be volume-aware for Windows. On Unix,
	// filepath.VolumeName("/") returns "" and the equality reduces to
	// abs == "/". On Windows, filepath.Abs("C:\\") returns "C:\" and the
	// equality compares against "C:\\" (VolumeName "C:" + separator "\\"),
	// matching the documented "/" refusal across platforms.
	if abs == filepath.VolumeName(abs)+string(filepath.Separator) {
		return fmt.Errorf("%w: workdir is filesystem root (%s).\n\n  cd into a project subdirectory first.", ErrGitAutoInitRefused, abs)
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
		return nil
	}
	cmd := exec.CommandContext(ctx, "git", "-C", abs, "rev-parse", "--git-dir")
	cmd.Env = gitSafeEnv()
	if out, err := cmd.Output(); err == nil && len(out) > 0 {
		// Inside some kind of repo. Distinguish bare vs work-tree for a
		// clearer error message.
		bareCmd := exec.CommandContext(ctx, "git", "-C", abs, "rev-parse", "--is-bare-repository")
		bareCmd.Env = gitSafeEnv()
		bareOut, _ := bareCmd.Output()
		if strings.TrimSpace(string(bareOut)) == "true" {
			return fmt.Errorf("%w: workdir is inside a bare git repository.\n\n  Bare repositories have no working tree, so workflows that need to commit/branch/merge can't run here. cd into a checkout of this repo (e.g. clone or worktree) and run from there.", ErrGitAutoInitRefused)
		}
		return fmt.Errorf("%w: workdir is inside a parent git repository", ErrGitAutoInitRefused)
	}
	return nil
}

// checkGit runs two cheap probes:
//  1. `git --version` — does git exist on PATH?
//  2. `git -C <workDir> rev-parse --is-inside-work-tree` — are we inside a
//     repo with a work tree?
//
// installed reports the first probe; isRepo reports the second. Returns an
// error only on unexpected I/O failure; "not installed" and "not a repo"
// are returned as installed=false / isRepo=false with err==nil.
//
// We use `--is-inside-work-tree`, NOT `--git-dir`, so bare repositories
// don't count as a "repo" for preflight purposes: workflows that declare
// `requires: git` need to run `git commit` / `git merge`, both of which
// require a work tree and would fail in a bare repo with `fatal: this
// operation must be run in a work tree`. Reporting isRepo=true for a bare
// repo would defer that failure to mid-run instead of catching it here,
// which is the bug the preflight is meant to prevent.
func checkGit(ctx context.Context, workDir string) (installed bool, isRepo bool, err error) {
	if _, lerr := exec.LookPath("git"); lerr != nil {
		return false, false, nil
	}
	installed = true
	cmd := exec.CommandContext(ctx, "git", "-C", workDir, "rev-parse", "--is-inside-work-tree")
	cmd.Env = gitSafeEnv()
	out, runErr := cmd.Output()
	// Exits non-zero (and writes to stderr) when not inside a repo. Inside a
	// bare repo, exits 0 but prints "false". Inside a normal repo or linked
	// worktree, exits 0 and prints "true".
	if runErr == nil && strings.TrimSpace(string(out)) == "true" {
		isRepo = true
	}
	return installed, isRepo, nil
}
