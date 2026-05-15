// ABOUTME: Git environment preflight — runs before any node executes.
// ABOUTME: Honors workflow `requires:` declarations and the --git= policy flag.
package pipeline

import (
	"context"
	"errors"
	"os/exec"
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
