// ABOUTME: End-to-end integration tests for v0.29.0 git preflight.
// ABOUTME: Confirms a `requires: git` workflow fails at preflight in a non-repo dir
// ABOUTME: with a copy-paste-ready remediation message, and proceeds after git init.
package tracker

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/2389-research/tracker/pipeline"
)

// requireGit skips the calling test when `git` is not on PATH. Used by the
// git-dependent preflight tests in this package so `go test ./...` remains
// portable on hosts without git installed. Matches the same-named helper in
// pipeline/git_artifacts_test.go.
func requireGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found in PATH, skipping git-dependent preflight test")
	}
}

// cleanGitEnv returns os.Environ() with git-internal repository pointers
// stripped. Tests in this package create throwaway git repos via `git init`
// in t.TempDir(); git honors an inherited GIT_DIR/GIT_INDEX_FILE over a
// command's working directory, so when `go test ./...` runs from inside a git
// hook (which exports those vars), an un-sanitized `git init` re-inits — and a
// follow-up `git add`/`commit` truncates — the OUTER repo's index instead of
// the temp dir. Set cmd.Env = cleanGitEnv() on every git subprocess in tests.
// Mirrors the same-named helper in pipeline/git_preflight_test.go.
func cleanGitEnv() []string {
	stripped := map[string]bool{
		"GIT_DIR":              true,
		"GIT_INDEX_FILE":       true,
		"GIT_WORK_TREE":        true,
		"GIT_OBJECT_DIRECTORY": true,
		"GIT_COMMON_DIR":       true,
	}
	src := os.Environ()
	out := make([]string, 0, len(src))
	for _, e := range src {
		name := strings.ToUpper(strings.SplitN(e, "=", 2)[0])
		if !stripped[name] {
			out = append(out, e)
		}
	}
	return out
}

const integrationFixtureRequiresGit = `workflow PreflightIntegration
  goal: "integration test"
  requires: git
  start: Start
  exit: Done

  agent Start
    label: Start

  agent Done
    label: Done

  edges
    Start -> Done
`

// TestIntegration_Preflight_NoRepoFailsBeforeNodeExecutes confirms a workflow
// declaring requires:git, run in a non-repo dir, fails at NewEngine time —
// no LLM client setup, no network, no node execution. The error message
// must include the user-facing remediation copy (git init / --git=off).
func TestIntegration_Preflight_NoRepoFailsBeforeNodeExecutes(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	cfg := Config{WorkingDir: dir}
	_, err := NewEngine(integrationFixtureRequiresGit, cfg)
	if err == nil {
		t.Fatalf("expected preflight failure")
	}
	if !errors.Is(err, pipeline.ErrGitWorkdirNotRepo) {
		t.Fatalf("want ErrGitWorkdirNotRepo, got %v", err)
	}
	msg := err.Error()
	for _, want := range []string{
		"git init",
		"--git=off",
		"tracker <workflow>",
		dir,
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("error message must include %q for the user remediation; got: %s", want, msg)
		}
	}
}

// TestIntegration_Preflight_GitInitClears confirms the same fixture proceeds
// past preflight once `git init` has been run. Downstream NewEngine may still
// fail for unrelated reasons (no API key) — the assertion is only that
// preflight is no longer the failure point.
func TestIntegration_Preflight_GitInitClears(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	cmd := exec.Command("git", "init", "-q")
	cmd.Dir = dir
	cmd.Env = cleanGitEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	cfg := Config{WorkingDir: dir}
	_, err := NewEngine(integrationFixtureRequiresGit, cfg)
	if err != nil && errors.Is(err, pipeline.ErrGitWorkdirNotRepo) {
		t.Fatalf("preflight should pass after git init, got %v", err)
	}
}

// TestIntegration_Preflight_AutoInitInTempDir exercises --git=init with the
// --allow-init latch. Confirms the side effect (a .git directory appears)
// happens at preflight time.
func TestIntegration_Preflight_AutoInitInTempDir(t *testing.T) {
	requireGit(t)
	dir := t.TempDir()
	cfg := Config{
		WorkingDir: dir,
		Git:        &GitConfig{Preflight: GitPreflightInit, AllowInit: true},
	}
	// NewEngine may fail downstream (no providers) — preflight runs first
	// and creates .git regardless.
	_, _ = NewEngine(integrationFixtureRequiresGit, cfg)
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		t.Fatalf("expected .git after auto-init: %v", err)
	}
}

// TestIntegration_Preflight_OffBypassesEverything confirms --git=off bypasses
// even when the workflow explicitly declares requires:git AND the workdir
// isn't a repo. Belt-and-suspenders escape hatch.
//
// Intentionally does NOT call requireGit: --git=off bypasses git checks
// entirely, so this test should pass even on hosts without git installed.
func TestIntegration_Preflight_OffBypassesEverything(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{
		WorkingDir: dir,
		Git:        &GitConfig{Preflight: GitPreflightOff},
	}
	_, err := NewEngine(integrationFixtureRequiresGit, cfg)
	if err != nil && errors.Is(err, pipeline.ErrGitWorkdirNotRepo) {
		t.Errorf("--git=off must bypass preflight even with source-level requires:git, got %v", err)
	}
}
