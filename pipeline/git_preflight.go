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
	// ErrGitDependencyUnsatisfied — a `requires:` entry is recognized but the env check failed.
	ErrGitDependencyUnsatisfied = errors.New("workflow dependency not satisfied")
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
	// Filled in by a later task.
	_ = ctx
	_ = cfg
	return nil
}

// runAutoInit performs `git init` after running safety latches.
//
// Required latches:
//   - allowInit == true OR interactive prompt answered "yes"
//   - safetyLatches(workDir) passes
//
// Returns a wrapped ErrGitAutoInitRefused if any latch fires.
func runAutoInit(workDir string, allowInit bool, interactive bool, promptYN func(prompt string) bool) error {
	// Latch 1: explicit consent. --allow-init is required in non-interactive
	// mode. In interactive mode, the [Y/n] prompt substitutes.
	if !allowInit {
		if !interactive {
			return fmt.Errorf("%w: --git=init requires --allow-init in non-interactive runs", ErrGitAutoInitRefused)
		}
		if promptYN == nil {
			promptYN = defaultPromptYN
		}
		if !promptYN(fmt.Sprintf("Initialize a git repository in %s? [Y/n] ", workDir)) {
			return fmt.Errorf("%w: user declined interactive prompt", ErrGitAutoInitRefused)
		}
	}
	// Latch 2: location safety.
	if err := safetyLatches(workDir); err != nil {
		return err
	}
	cmd := exec.Command("git", "-C", workDir, "init", "-q")
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
func safetyLatches(workDir string) error {
	abs, err := filepath.Abs(workDir)
	if err != nil {
		return fmt.Errorf("%w: resolve absolute path: %v", ErrGitAutoInitRefused, err)
	}
	if home, err := os.UserHomeDir(); err == nil && abs == filepath.Clean(home) {
		return fmt.Errorf("%w: workdir equals $HOME (%s)", ErrGitAutoInitRefused, home)
	}
	if abs == string(filepath.Separator) {
		return fmt.Errorf("%w: workdir is filesystem root", ErrGitAutoInitRefused)
	}
	// Nested-repo detection via git itself. If git is missing the caller would
	// have hit ErrGitNotInstalled before reaching this point; defend anyway
	// and treat lookup failure as "not nested" so we don't false-positive
	// on a no-git host.
	if _, lerr := exec.LookPath("git"); lerr != nil {
		return nil
	}
	cmd := exec.Command("git", "-C", abs, "rev-parse", "--git-dir")
	cmd.Env = gitSafeEnv()
	if out, err := cmd.Output(); err == nil && len(out) > 0 {
		// Inside some kind of repo. Distinguish bare vs work-tree for a
		// clearer error message.
		bareCmd := exec.Command("git", "-C", abs, "rev-parse", "--is-bare-repository")
		bareCmd.Env = gitSafeEnv()
		bareOut, _ := bareCmd.Output()
		if strings.TrimSpace(string(bareOut)) == "true" {
			return fmt.Errorf("%w: workdir is inside a bare git repository", ErrGitAutoInitRefused)
		}
		return fmt.Errorf("%w: workdir is inside a parent git repository", ErrGitAutoInitRefused)
	}
	return nil
}

// checkGit runs two cheap probes:
//  1. `git --version` — does git exist on PATH?
//  2. `git -C <workDir> rev-parse --git-dir` — are we inside a repo?
//
// installed reports the first probe; isRepo reports the second. Returns an
// error only on unexpected I/O failure; "not installed" and "not a repo"
// are returned as installed=false / isRepo=false with err==nil. rev-parse
// exits non-zero when not inside a repo, which is not an error condition.
func checkGit(workDir string) (installed bool, isRepo bool, err error) {
	if _, lerr := exec.LookPath("git"); lerr != nil {
		return false, false, nil
	}
	installed = true
	cmd := exec.Command("git", "-C", workDir, "rev-parse", "--git-dir")
	cmd.Env = gitSafeEnv()
	if runErr := cmd.Run(); runErr == nil {
		isRepo = true
	}
	return installed, isRepo, nil
}
