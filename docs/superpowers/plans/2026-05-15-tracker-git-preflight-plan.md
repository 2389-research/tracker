# Tracker Git Preflight (v0.29.0) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a `requires:` workflow header keyword plus a `--git=auto|off|warn|require|init` CLI flag so workflows that need git fail in seconds with a copy-pasteable remediation instead of burning $20–$100 of LLM spend before dying at the first `git commit`.

**Tracking issues:** tracker#234 (this work) blocked by dippin-lang#35 (upstream `requires:` IR field).

**Architecture:** A single `pipeline.Preflight(ctx, PreflightConfig)` function runs once at the library/CLI boundary before any node executes. It reads `graph.Attrs["requires"]` (populated by the dippin adapter from a new `*ir.Workflow.Requires` field) and the resolved policy (CLI flag > library config > workflow default `auto`). The function decides: error, warn, skip, or auto-init. Subgraph / manager_loop child engines do NOT re-run preflight — the parent run satisfies it.

**Tech Stack:** Go 1.24+, the dippin-lang IR (upstream PR required), the existing `exec.LookPath("git")` and `os/exec` pattern from `pipeline/git_artifacts.go`, the existing `tracker.CheckResult` shape from `tracker_doctor.go`, the existing `flag.FlagSet` style from `cmd/tracker/flags.go`.

**Non-goals (deferred to v0.30.0):** Engine-managed commits, push, pre-commit hook policy, clean-state requirement, `ChildRunContext.ManagedGit` propagation, checkpoint resume git reconciliation, removal of inline `git commit` instructions from agent prompts. See the spec's "Non-goals" and "Follow-on work pointer" sections.

---

> **Historical note (added post-PR-review):** Implementation snippets below
> show the original `checkGit` using `git rev-parse --git-dir`. PR review
> caught a bare-repo false positive in that approach (bare repos have no
> work tree, so `git commit`/`git merge` still fail) and the shipped
> implementation uses `git rev-parse --is-inside-work-tree` for the
> requires:git satisfaction probe. `safetyLatches` (the auto-init refusal
> path) keeps `--git-dir` because it asks a different question — "is this
> any kind of git context where `git init` would create a confusing
> duplicate?" — and `--git-dir` correctly catches bare repos, linked
> worktrees, and submodules. The CHANGELOG documents both probes; the
> snippets below are kept as a record of what the original plan proposed.
> Future agents following this plan should consult the shipped code in
> `pipeline/git_preflight.go` rather than mirror the snippets verbatim.

## Investigation findings (resolves the spec's Open Questions)

1. **dippin-lang `requires:` in `*ir.Workflow` — NOT present in v0.25.0.** Verified at `~/go/pkg/mod/github.com/2389-research/dippin-lang@v0.25.0/ir/ir.go:11-23` (no `Requires` field) and `parser/parser.go:97-108` (`dispatchWorkflowSimpleField` switches on `goal | start | exit | defaults | vars`; unknown identifiers emit `"unexpected top-level identifier: requires"` at parser/parser.go:141). **A dippin-lang bump is a blocker.** Phase 0 below sketches the upstream PR.

2. **Syntax — drop the bracket form `[git]` from the spec example; use bare comma list `requires: git`** (or `requires: git, docker, jq`). dippin-lang's lexer explicitly does not support bracket syntax (`/parser/lexer.go:29: "TokenLBracket — bracket syntax not supported; triggers a parse error"`), and existing list fields (`reads:`, `writes:`) use the comma form. Adding bracket syntax would be a larger upstream change; the comma form matches precedent and parses with the existing `readFieldValue` + `strings.Split` plumbing.

3. **`tracker workflows` display — cheap; include.** `tracker_workflows.go:68-97` already does a hand-rolled header scan (it doesn't parse the full IR). Extending it to capture a `requires:` line is ~10 LOC. Plan includes this.

4. **Parent-`.git` safety latch false positives — use `git rev-parse --git-dir` instead of walking parents.** Empirical test results (`/tmp/testgit` scratch runs):
   - **Bare repo:** A bare repo (`bare.git/`) has `HEAD`, `config`, etc. at the top — no `.git` directory inside. Walking parents looking for `.git` would MISS this. But `cd bare.git && git rev-parse --git-dir` returns `.` — git knows we're inside a (bare) repo.
   - **Worktree:** A linked worktree's `.git` is a FILE (~55 bytes containing `gitdir: ...`), not a directory. `os.Stat(".git").IsDir()` returns false but the user IS inside a git repo. Walking parents for a `.git` *directory* would miss this. `git rev-parse --git-dir` correctly returns the worktree pointer target.
   - **Submodule:** `submodule/.git` is also a FILE (~28 bytes containing `gitdir: ../.git/modules/sub`). Same issue.

   **Decision:** Replace the spec's "any parent dir has `.git`" check with `git -C <workDir> rev-parse --git-dir`. If it exits 0, we're inside some repo (regular, bare, worktree, or submodule) and `git init` here is nested-repo behavior — refuse. Also call `git -C <workDir> rev-parse --is-bare-repository` to distinguish "you're inside a bare repo's GIT_DIR" (refuse with a more specific message) from "you're inside a normal repo or worktree" (refuse as nested).

---

## File structure

| File | Responsibility |
|---|---|
| `pipeline/git_preflight.go` (NEW) | Pure preflight logic: `Preflight`, `PreflightConfig`, error sentinels, internal helpers `checkGit`, `runAutoInit`, `safetyLatches`. ~150 LOC. |
| `pipeline/git_preflight_test.go` (NEW) | Table-driven unit tests for every case in the spec's Test plan, plus the three false-positive cases (bare, worktree, submodule). |
| `pipeline/graph.go` (MODIFY) | Add `(*Graph).RequiredDeps() []string` method that reads `g.Attrs["requires"]`. |
| `pipeline/dippin_adapter.go` (MODIFY) | Add `extractRequires(workflow.Requires, g.Attrs)` called from `buildGraphFromWorkflow`. |
| `tracker.go` (MODIFY) | Add `GitPreflight` type, `GitConfig` struct, `Config.Git *GitConfig` field. Call `pipeline.Preflight` from `tracker.NewEngine` between `pipeline.Validate(graph)` and `resolveWorkDir`/LLM client setup. Add `ResolvePreflightPolicy`/`ResolveGitConfig` helper symmetric with `ResolveBudgetLimits`. |
| `tracker_workflows.go` (MODIFY) | Extend `WorkflowInfo` with `Requires []string`; extend `parseWorkflowHeader` to capture the `requires:` line. |
| `tracker_doctor.go` (MODIFY) | Add `checkGitRequires(ctx, cfg, graph)` and wire it into the existing check list when `cfg.PipelineFile != ""`. |
| `cmd/tracker/flags.go` (MODIFY) | Add `--git` and `--allow-init` flags to the run, doctor, and validate/simulate flag sets. Add validation. |
| `cmd/tracker/main.go` (MODIFY) | Add `git`/`allowInit` fields to `runConfig`. |
| `cmd/tracker/run.go` (MODIFY) | Call `pipeline.Preflight` after `applyRunParamOverrides` in both `run()` and `runTUI()`. Bail on error before any LLM client setup. |
| `cmd/tracker/commands.go` (MODIFY) | Extend `executeWorkflows` to display `requires:` for each workflow. |
| `cmd/tracker/doctor.go` (MODIFY) | Read `cfg.git` / `cfg.allowInit` and pass to `tracker.Doctor` via a new option `WithGitConfig`. |
| `workflows/build_product.dip` (MODIFY) | Add `requires: git`. |
| `workflows/build_product_with_superspec.dip` (MODIFY) | Add `requires: git`. |
| `workflows/ask_and_execute.dip` (MODIFY) | Audit — does this workflow assume git? Decide and document. |
| `workflows/deep_review.dip` (MODIFY) | Audit. |
| `CHANGELOG.md` (MODIFY) | Add v0.29.0 entry under Added. |
| `README.md` (MODIFY) | Single paragraph documenting `requires:` and `--git=`. |

**Upstream (dippin-lang) PR sketch** (Phase 0, blocker — see Task 1):

| File | Change |
|---|---|
| `ir/ir.go` | Add `Requires []string` to `Workflow` struct, after `Vars`. |
| `parser/parser.go` | Add `case "requires":` to `dispatchWorkflowSimpleField` that delegates to a new `parseWorkflowRequiresField` (reads comma list via existing `parseCommaList` pattern). |
| `formatter/format.go` | In `writeWorkflowHeader`, after `goal` and before `start/exit`, emit `requires: a, b, c` when `len(w.Requires) > 0`. |
| `parser/parser_test.go` + `formatter/format_test.go` + `roundtrip_test.go` | Add tests covering single-item and multi-item lists, roundtrip. |
| `docs/nodes.md` or workflow doc | Single sentence pointing at the new keyword. |
| `CHANGELOG.md` (dippin-lang) | "Added: workflow header `requires: <list>` for declaring environmental dependencies." |

Released as dippin-lang v0.26.0. Tracker's Phase 2 bumps to it.

---

## Task ordering and rationale

- **Phase 0** is upstream; pause once filed and merged.
- **Phase 1** (`pipeline.Preflight` primitive) lands first because it has no dippin dependency — it's pure logic exercised via direct test setup. This lets every later phase rely on it.
- **Phase 2** (dippin bump) gates everything that touches `workflow.Requires`.
- **Phases 3–8** (adapter, library Config, doctor, CLI flags, CLI wiring, workflows display) follow in dependency order.
- **Phase 9** (built-in workflows) lands last so that the entire toolchain — preflight + adapter + flags — is in place before any `.dip` file claims `requires: git`.
- **Phase 10** wraps with integration test, CHANGELOG, README, and the full verification gauntlet.

---

## Phase 0: dippin-lang upstream PR (blocker)

### Task 0.1: File the dippin-lang issue and PR

**This is upstream work in the dippin-lang repo, not tracker.** Skip to Phase 2 once the dippin-lang v0.26.0 tag is published.

- [x] **Step 1: File issue at github.com/2389-research/dippin-lang** — **DONE: dippin-lang#35**

Issue body and acceptance criteria are at https://github.com/2389-research/dippin-lang/issues/35. Tracker-side companion: tracker#234.

- [ ] **Step 2: Implement the upstream PR**

Branch: `requires-header`

File: `ir/ir.go` — add the field:

```go
type Workflow struct {
    Name       string
    Version    string
    Goal       string
    Start      string
    Exit       string
    Requires   []string          // NEW: environmental deps (e.g. ["git"])
    Defaults   WorkflowDefaults
    Vars       map[string]string
    Nodes      []*Node
    Edges      []*Edge
    Stylesheet []StylesheetRule
    SourceMap  *SourceMap
}
```

File: `parser/parser.go` — add the case in `dispatchWorkflowSimpleField`:

```go
func dispatchWorkflowSimpleField(p *Parser, t Token) bool {
    switch t.Value {
    case "goal", "start", "exit":
        p.parseWorkflowStringField(t)
    case "requires":
        p.parseWorkflowRequiresField(t)
    case "defaults":
        p.parseDefaults()
    case "vars":
        p.parseVars()
    default:
        return dispatchWorkflowTailField(p, t)
    }
    return true
}

// parseWorkflowRequiresField parses `requires: a, b, c` into Workflow.Requires.
// Bracket syntax is intentionally not supported — comma-list matches the
// existing reads:/writes: precedent and dippin's no-bracket lexer rule.
func (p *Parser) parseWorkflowRequiresField(t Token) {
    p.lexer.NextToken()              // consume the identifier
    p.expect(TokenColon)
    items := p.parseCommaList()
    var clean []string
    for _, it := range items {
        s := strings.TrimSpace(it)
        if s != "" {
            clean = append(clean, s)
        }
    }
    p.workflow.Requires = clean
}
```

File: `formatter/format.go` — emit it in `writeWorkflowHeader`:

```go
func writeWorkflowHeader(wr *writer, w *ir.Workflow) {
    wr.line("workflow %s", w.Name)
    wr.push()
    if w.Goal != "" {
        wr.line("goal: %s", quoteValue(w.Goal))
    }
    if len(w.Requires) > 0 {
        wr.line("requires: %s", strings.Join(w.Requires, ", "))
    }
    wr.line("start: %s", w.Start)
    wr.line("exit: %s", w.Exit)
}
```

File: `parser/parser_test.go` — add a test parsing `requires: git, docker` and asserting `workflow.Requires == []string{"git", "docker"}`.

File: `formatter/format_test.go` — add a test that an `ir.Workflow{Requires: []string{"git"}}` emits the expected line.

File: `roundtrip_test.go` — add `requires_simple.dip` fixture and confirm `parse → format → parse` is lossless.

- [ ] **Step 3: Run dippin-lang's test suite**

```bash
cd ~/code/dippin-lang  # or wherever the repo lives
go test ./...
```

Expected: all pass.

- [ ] **Step 4: Open PR, get it merged, request v0.26.0 release tag.**

- [ ] **Step 5: Once dippin-lang v0.26.0 is published, proceed to Phase 1.**

---

## Phase 1: pipeline.Preflight primitive (no dippin dependency)

This phase lands the pure-logic core. No adapter touch yet, so it can ship before the dippin-lang bump is finalized.

### Task 1.1: Define error sentinels

**Files:**
- Create: `pipeline/git_preflight.go`
- Test: `pipeline/git_preflight_test.go`

- [ ] **Step 1: Write the failing test (sentinel identity)**

Create `pipeline/git_preflight_test.go`:

```go
// ABOUTME: Tests for git preflight error sentinels and decision logic.
// ABOUTME: Covers happy path, hard-fail, warn-downgrade, auto-init, and safety latches.
package pipeline

import (
	"errors"
	"testing"
)

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
	// Sentinels must be distinct values.
	for i, a := range sentinels {
		for j, b := range sentinels {
			if i != j && errors.Is(a, b) {
				t.Errorf("sentinel collision: %v Is %v", a, b)
			}
		}
	}
}
```

- [ ] **Step 2: Run test, confirm it fails**

```bash
go test ./pipeline -run TestPreflightErrorSentinels -v
```

Expected: FAIL — `undefined: ErrGitNotInstalled`.

- [ ] **Step 3: Create `pipeline/git_preflight.go` with sentinels and config struct**

```go
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
// Mirrors tracker.GitPreflight (the library-API type), kept in pipeline
// so the engine package has no dependency on the tracker package.
type GitPreflight string

const (
	GitPreflightAuto    GitPreflight = ""
	GitPreflightOff     GitPreflight = "off"
	GitPreflightWarn    GitPreflight = "warn"
	GitPreflightRequire GitPreflight = "require"
	GitPreflightInit    GitPreflight = "init"
)

// ValidPreflight reports whether v is a recognized policy value.
// The empty string ("") is valid and resolves to auto.
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
	// Wrapped error in Preflight may be one of the above.
	ErrGitDependencyUnsatisfied = errors.New("workflow dependency not satisfied")
)

// PreflightConfig captures everything Preflight needs to make a decision.
// All fields are inputs only; no I/O happens until Preflight runs.
type PreflightConfig struct {
	WorkDir        string                            // absolute path; required
	Requires       []string                          // from graph.Attrs["requires"]
	Policy         GitPreflight                      // resolved from CLI > library > default ""
	AllowInit      bool                              // required when Policy == GitPreflightInit and !InteractiveTTY
	InteractiveTTY bool                              // when true, --git=init may prompt instead of needing --allow-init
	Warner         func(format string, args ...any)  // optional; defaults to a no-op
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
	// Empty implementation; filled in by subsequent tasks.
	_ = ctx
	_ = cfg
	return nil
}
```

- [ ] **Step 4: Run test, confirm it passes**

```bash
go test ./pipeline -run TestPreflightErrorSentinels -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pipeline/git_preflight.go pipeline/git_preflight_test.go
git commit -m "feat(pipeline): scaffold git preflight error sentinels and config (refs #234)

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 1.2: Implement `checkGit` helper

**Files:**
- Modify: `pipeline/git_preflight.go`
- Test: `pipeline/git_preflight_test.go`

- [ ] **Step 1: Write the failing test**

Add to `pipeline/git_preflight_test.go`:

```go
import (
	"os"
	"path/filepath"
	"testing"
)

func TestCheckGit_Installed(t *testing.T) {
	// Assumes git is installed in the test environment.
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

// mustGitInit creates a git repo at dir or fails the test.
func mustGitInit(t *testing.T, dir string) {
	t.Helper()
	cmd := exec.Command("git", "init", "-q")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init in %s: %v: %s", dir, err, out)
	}
}
```

(Add `"os/exec"` to the test file's imports.)

- [ ] **Step 2: Run tests, confirm they fail**

```bash
go test ./pipeline -run "TestCheckGit_" -v
```

Expected: FAIL — `undefined: checkGit`.

- [ ] **Step 3: Implement `checkGit`**

Add to `pipeline/git_preflight.go`:

```go
// checkGit runs two cheap probes:
//   1) `git --version` — does git exist on PATH?
//   2) `git -C <workDir> rev-parse --git-dir` — are we inside a repo?
// installed reports the first probe; isRepo reports the second.
// Returns an error only on unexpected I/O failure; "not installed" and
// "not a repo" are returned as installed=false / isRepo=false with err==nil.
func checkGit(workDir string) (installed bool, isRepo bool, err error) {
	if _, lerr := exec.LookPath("git"); lerr != nil {
		return false, false, nil
	}
	installed = true
	cmd := exec.Command("git", "-C", workDir, "rev-parse", "--git-dir")
	cmd.Env = gitSafeEnv() // re-use the existing sanitizer from git_artifacts.go
	if err := cmd.Run(); err == nil {
		isRepo = true
	}
	// rev-parse exits non-zero when not inside a repo — that is not an error.
	return installed, isRepo, nil
}
```

- [ ] **Step 4: Run tests, confirm they pass**

```bash
go test ./pipeline -run "TestCheckGit_" -v
```

Expected: PASS (all three).

- [ ] **Step 5: Commit**

```bash
git add pipeline/git_preflight.go pipeline/git_preflight_test.go
git commit -m "feat(pipeline): add checkGit helper using rev-parse --git-dir

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 1.3: Implement `safetyLatches` (home, root, nested)

**Files:**
- Modify: `pipeline/git_preflight.go`
- Test: `pipeline/git_preflight_test.go`

- [ ] **Step 1: Write failing tests**

Add to `pipeline/git_preflight_test.go`:

```go
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
```

- [ ] **Step 2: Run tests, confirm they fail**

```bash
go test ./pipeline -run "TestSafetyLatches_" -v
```

Expected: FAIL — `undefined: safetyLatches`.

- [ ] **Step 3: Implement `safetyLatches`**

Add to `pipeline/git_preflight.go`:

```go
// safetyLatches refuses `git init` for unsafe locations.
// Returns a wrapped ErrGitAutoInitRefused on refusal.
//
// Refusals:
//   - workDir is the user's $HOME
//   - workDir is the filesystem root ("/")
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
	// Nested-repo detection via git itself. exec.LookPath check is implicit:
	// if git is missing, the caller would have hit ErrGitNotInstalled before
	// reaching this point; but defend anyway and treat lookup failure as
	// "not nested" so we don't false-positive on a no-git host.
	if _, lerr := exec.LookPath("git"); lerr != nil {
		return nil
	}
	cmd := exec.Command("git", "-C", abs, "rev-parse", "--git-dir")
	cmd.Env = gitSafeEnv()
	if out, err := cmd.Output(); err == nil && len(out) > 0 {
		// Inside some kind of repo. Distinguish bare vs work-tree for a
		// clearer error message — the spec's error templates are different.
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
```

- [ ] **Step 4: Run tests, confirm they pass**

```bash
go test ./pipeline -run "TestSafetyLatches_" -v
```

Expected: PASS (all six).

- [ ] **Step 5: Commit**

```bash
git add pipeline/git_preflight.go pipeline/git_preflight_test.go
git commit -m "feat(pipeline): add safetyLatches with worktree/bare/submodule-correct nesting check

Uses 'git rev-parse --git-dir' instead of walking parents for a .git
directory, which would miss worktrees (.git is a file) and bare repos
(no .git at all). Covers the false-positive cases from the spec's
Open Question #3.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 1.4: Implement `runAutoInit`

**Files:**
- Modify: `pipeline/git_preflight.go`
- Test: `pipeline/git_preflight_test.go`

- [ ] **Step 1: Write failing tests**

Add to `pipeline/git_preflight_test.go`:

```go
func TestRunAutoInit_Success(t *testing.T) {
	dir := t.TempDir()
	if err := runAutoInit(dir, true, false, nil); err != nil {
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
	err := runAutoInit(child, true, false, nil)
	if !errors.Is(err, ErrGitAutoInitRefused) {
		t.Fatalf("want ErrGitAutoInitRefused, got %v", err)
	}
}

func TestRunAutoInit_NeedsAllowInit_NonInteractive(t *testing.T) {
	dir := t.TempDir()
	err := runAutoInit(dir, false /*allowInit*/, false /*interactive*/, nil)
	if !errors.Is(err, ErrGitAutoInitRefused) {
		t.Fatalf("want ErrGitAutoInitRefused, got %v", err)
	}
	if !strings.Contains(err.Error(), "--allow-init") {
		t.Fatalf("error must mention --allow-init: %v", err)
	}
}

func TestRunAutoInit_InteractiveYesAccepted(t *testing.T) {
	dir := t.TempDir()
	yes := func(string) bool { return true }
	if err := runAutoInit(dir, false /*allowInit*/, true /*interactive*/, yes); err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		t.Fatalf("expected .git after init: %v", err)
	}
}

func TestRunAutoInit_InteractiveNoRejected(t *testing.T) {
	dir := t.TempDir()
	no := func(string) bool { return false }
	err := runAutoInit(dir, false, true, no)
	if !errors.Is(err, ErrGitAutoInitRefused) {
		t.Fatalf("want ErrGitAutoInitRefused, got %v", err)
	}
}
```

- [ ] **Step 2: Run tests, confirm they fail**

```bash
go test ./pipeline -run "TestRunAutoInit_" -v
```

Expected: FAIL — `undefined: runAutoInit`.

- [ ] **Step 3: Implement `runAutoInit`**

Add to `pipeline/git_preflight.go`:

```go
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
	// Run `git init -q`. Reuse the sanitized environment.
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
```

- [ ] **Step 4: Run tests, confirm they pass**

```bash
go test ./pipeline -run "TestRunAutoInit_" -v
```

Expected: PASS (all five).

- [ ] **Step 5: Commit**

```bash
git add pipeline/git_preflight.go pipeline/git_preflight_test.go
git commit -m "feat(pipeline): add runAutoInit with --allow-init / interactive-prompt latches

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 1.5: Implement `Preflight` decision logic

**Files:**
- Modify: `pipeline/git_preflight.go`
- Test: `pipeline/git_preflight_test.go`

- [ ] **Step 1: Write the failing tests (table-driven, covers spec's Test plan)**

Add to `pipeline/git_preflight_test.go`:

```go
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
	if len(warnings) != 0 {
		t.Fatalf("--git=off must be silent, got: %v", warnings)
	}
}

func TestPreflight_RequireOverrideNoRequires(t *testing.T) {
	dir := t.TempDir()
	err := Preflight(context.Background(), PreflightConfig{
		WorkDir:  dir,
		Requires: nil, // workflow says nothing
		Policy:   GitPreflightRequire,
	})
	if !errors.Is(err, ErrGitWorkdirNotRepo) {
		t.Fatalf("want ErrGitWorkdirNotRepo (CLI override), got %v", err)
	}
}

func TestPreflight_AutoInit_Success(t *testing.T) {
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
```

Add `"context"` and `"fmt"` to test file imports if missing.

- [ ] **Step 2: Run tests, confirm they fail**

```bash
go test ./pipeline -run "TestPreflight_" -v
```

Expected: all FAIL — Preflight body still empty.

- [ ] **Step 3: Implement Preflight decision logic**

Replace the stub `func Preflight` in `pipeline/git_preflight.go`:

```go
func Preflight(ctx context.Context, cfg PreflightConfig) error {
	_ = ctx // reserved for future timeout/cancellation
	warn := cfg.Warner
	if warn == nil {
		warn = func(string, ...any) {}
	}

	if !ValidPreflight(cfg.Policy) {
		// Unknown policy is treated as auto rather than failing the run —
		// matches dippin-lang's tolerance for forward-declared values.
		warn("tracker: unknown --git policy %q; treating as auto", string(cfg.Policy))
		cfg.Policy = GitPreflightAuto
	}

	if cfg.Policy == GitPreflightOff {
		return nil
	}

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

	// --git=require forces the check even if the workflow doesn't declare it.
	// --git=init also implies the check.
	if cfg.Policy == GitPreflightRequire || cfg.Policy == GitPreflightInit {
		requiresGit = true
	}

	if !requiresGit {
		return nil
	}

	installed, isRepo, err := checkGit(cfg.WorkDir)
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
			if err := runAutoInit(cfg.WorkDir, cfg.AllowInit, cfg.InteractiveTTY, cfg.PromptYN); err != nil {
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
```

- [ ] **Step 4: Run tests, confirm they pass**

```bash
go test ./pipeline -run "TestPreflight_" -v
```

Expected: PASS (all nine).

- [ ] **Step 5: Run the full pipeline package tests to verify no regression**

```bash
go test ./pipeline -short
```

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add pipeline/git_preflight.go pipeline/git_preflight_test.go
git commit -m "feat(pipeline): implement Preflight decision logic with full policy matrix

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 1.6: Add `(*Graph).RequiredDeps()`

**Files:**
- Modify: `pipeline/graph.go`
- Test: `pipeline/graph_test.go` (or `pipeline/git_preflight_test.go` — same package)

- [ ] **Step 1: Write the failing test**

Add to `pipeline/git_preflight_test.go`:

```go
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
```

- [ ] **Step 2: Run tests, confirm they fail**

```bash
go test ./pipeline -run "TestGraph_RequiredDeps" -v
```

Expected: FAIL — `g.RequiredDeps undefined`.

- [ ] **Step 3: Implement RequiredDeps**

Add to `pipeline/graph.go` (place it near other Graph methods; find an existing accessor like `GraphParamAttrKey` or `Attrs` to land near):

```go
// RequiredDeps returns the parsed comma-separated list from
// g.Attrs["requires"]. Whitespace around each entry is trimmed; empty
// entries are dropped. Returns nil for empty/missing attrs.
//
// The "requires" attr is populated by the dippin adapter from the
// workflow header's `requires:` field (dippin-lang v0.26.0+).
func (g *Graph) RequiredDeps() []string {
	raw, ok := g.Attrs["requires"]
	if !ok || strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		s := strings.TrimSpace(p)
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}
```

(`strings` is almost certainly already imported in `graph.go`; if not, add it.)

- [ ] **Step 4: Run tests, confirm they pass**

```bash
go test ./pipeline -run "TestGraph_RequiredDeps" -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add pipeline/graph.go pipeline/git_preflight_test.go
git commit -m "feat(pipeline): add Graph.RequiredDeps() accessor

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Phase 2: Bump dippin-lang to v0.26.0

### Task 2.1: Update go.mod and adapter

**Files:**
- Modify: `go.mod`
- Modify: `go.sum` (regenerated by `go get`)
- Modify: `pipeline/dippin_adapter.go`
- Modify: `tracker_doctor.go` (PinnedDippinVersion constant)
- Test: existing pipeline tests must still pass

- [ ] **Step 1: Update the dippin-lang dependency**

```bash
go get github.com/2389-research/dippin-lang@v0.26.0
go mod tidy
```

Confirm `go.mod` now shows `v0.26.0`.

- [ ] **Step 2: Update `PinnedDippinVersion`**

Edit `tracker_doctor.go:25`:

```go
const PinnedDippinVersion = "v0.26.0"
```

- [ ] **Step 3: Build to confirm the type now exists**

```bash
go build ./...
```

Expected: success. If you see `workflow.Requires undefined`, the dippin-lang bump didn't land — re-check the upstream tag.

- [ ] **Step 4: Add `extractRequires` to the adapter**

In `pipeline/dippin_adapter.go`, immediately after the `extractWorkflowDefaults` function (around line 709), add:

```go
// extractRequires writes the workflow's requires list to graph.Attrs["requires"]
// as a comma-separated string. Empty / nil input is a no-op.
func extractRequires(requires []string, attrs map[string]string) {
	if len(requires) == 0 {
		return
	}
	cleaned := make([]string, 0, len(requires))
	for _, r := range requires {
		s := strings.TrimSpace(r)
		if s != "" {
			cleaned = append(cleaned, s)
		}
	}
	if len(cleaned) == 0 {
		return
	}
	attrs["requires"] = strings.Join(cleaned, ", ")
}
```

In `buildGraphFromWorkflow` (around line 97), add a call right after `extractWorkflowVars`:

```go
extractWorkflowDefaults(workflow.Defaults, g.Attrs)
extractWorkflowVars(workflow.Vars, g.Attrs)
extractRequires(workflow.Requires, g.Attrs)  // NEW
```

- [ ] **Step 5: Write the adapter test**

Add to `pipeline/dippin_adapter_test.go` (or wherever adapter tests live — search with `grep -l "TestFromDippinIR" pipeline/*_test.go`):

```go
func TestFromDippinIR_RequiresPopulatesGraphAttr(t *testing.T) {
	wf := &ir.Workflow{
		Name:     "TestWF",
		Start:    "Start",
		Exit:     "Exit",
		Requires: []string{"git", "docker"},
		Nodes: []*ir.Node{
			{ID: "Start", Kind: ir.NodeAgent},
			{ID: "Exit", Kind: ir.NodeAgent},
		},
	}
	g, err := FromDippinIR(wf)
	if err != nil {
		t.Fatal(err)
	}
	if got := g.Attrs["requires"]; got != "git, docker" {
		t.Errorf("requires attr: want %q, got %q", "git, docker", got)
	}
	deps := g.RequiredDeps()
	if len(deps) != 2 || deps[0] != "git" || deps[1] != "docker" {
		t.Errorf("RequiredDeps: want [git docker], got %v", deps)
	}
}

func TestFromDippinIR_RequiresEmpty(t *testing.T) {
	wf := &ir.Workflow{
		Name:  "TestWF",
		Start: "Start",
		Exit:  "Exit",
		Nodes: []*ir.Node{
			{ID: "Start", Kind: ir.NodeAgent},
			{ID: "Exit", Kind: ir.NodeAgent},
		},
	}
	g, err := FromDippinIR(wf)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := g.Attrs["requires"]; ok {
		t.Errorf("expected no 'requires' attr when workflow.Requires is empty")
	}
}
```

- [ ] **Step 6: Run the new tests**

```bash
go test ./pipeline -run "TestFromDippinIR_Requires" -v
```

Expected: PASS.

- [ ] **Step 7: Run the full short suite to confirm no regression**

```bash
go test ./... -short
```

Expected: PASS in all 17 packages.

- [ ] **Step 8: Commit**

```bash
git add go.mod go.sum pipeline/dippin_adapter.go pipeline/dippin_adapter_test.go tracker_doctor.go
git commit -m "feat(adapter): bump dippin-lang to v0.26.0, propagate requires: to graph.Attrs

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Phase 3: Library Config integration

### Task 3.1: Add `GitPreflight`, `GitConfig` to the tracker package

**Files:**
- Modify: `tracker.go`
- Test: `tracker_test.go`

- [ ] **Step 1: Write the failing test**

Find an existing `tracker_test.go` file (or create one at the package root) and add:

```go
func TestGitConfig_ZeroValueIsAuto(t *testing.T) {
	cfg := Config{}
	policy, allowInit := ResolveGitConfig(cfg)
	if policy != GitPreflightAuto {
		t.Errorf("want auto policy on zero Config, got %q", policy)
	}
	if allowInit {
		t.Errorf("want AllowInit=false on zero Config")
	}
}

func TestGitConfig_ExplicitWins(t *testing.T) {
	cfg := Config{Git: &GitConfig{Preflight: GitPreflightWarn, AllowInit: true}}
	policy, allowInit := ResolveGitConfig(cfg)
	if policy != GitPreflightWarn {
		t.Errorf("want warn, got %q", policy)
	}
	if !allowInit {
		t.Errorf("want AllowInit=true")
	}
}
```

- [ ] **Step 2: Run tests, confirm they fail**

```bash
go test . -run TestGitConfig_ -v
```

Expected: FAIL — `GitConfig undefined`.

- [ ] **Step 3: Add the types to `tracker.go`**

After the `WebhookGateConfig` struct (around line 83), add:

```go
// GitPreflight is the resolved preflight policy that controls the v0.29.0
// git environment check. Mirrors pipeline.GitPreflight; the library type
// is here so callers don't need to import the pipeline package.
type GitPreflight = pipeline.GitPreflight

// GitPreflight values (re-exported from pipeline for caller convenience).
const (
	GitPreflightAuto    = pipeline.GitPreflightAuto
	GitPreflightOff     = pipeline.GitPreflightOff
	GitPreflightWarn    = pipeline.GitPreflightWarn
	GitPreflightRequire = pipeline.GitPreflightRequire
	GitPreflightInit    = pipeline.GitPreflightInit
)

// GitConfig configures the git preflight check that runs before any node
// executes. Zero value (or nil *GitConfig on Config.Git) resolves to
// GitPreflightAuto, which respects the workflow's `requires:` block.
//
// AllowInit is required when Preflight == GitPreflightInit and stdin is
// not a TTY — it is the second safety latch on automatic `git init`.
type GitConfig struct {
	Preflight GitPreflight
	AllowInit bool
}

// ResolveGitConfig returns the (policy, allowInit) pair to apply for this
// run, considering Config.Git. The zero value resolves to (auto, false).
func ResolveGitConfig(cfg Config) (GitPreflight, bool) {
	if cfg.Git == nil {
		return GitPreflightAuto, false
	}
	return cfg.Git.Preflight, cfg.Git.AllowInit
}
```

Add the field to `Config`:

```go
type Config struct {
	// ... existing fields ...
	BundleIdentity string
	// Git configures the git preflight check (v0.29.0). Nil = auto.
	Git *GitConfig
}
```

- [ ] **Step 4: Run tests, confirm they pass**

```bash
go test . -run TestGitConfig_ -v
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add tracker.go tracker_test.go
git commit -m "feat(tracker): add Config.Git and GitConfig library type

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 3.2: Wire `Preflight` into `tracker.NewEngine`

**Files:**
- Modify: `tracker.go`
- Test: `tracker_test.go`

- [ ] **Step 1: Write the failing integration test**

Add to `tracker_test.go`:

```go
import (
	"context"
	"path/filepath"
	"testing"

	"github.com/2389-research/tracker/pipeline"
)

func TestNewEngine_PreflightFailsWhenRequiresGitMissing(t *testing.T) {
	// Synthetic .dip with requires: git, run in a non-repo tempdir.
	source := `workflow TestWF
  goal: "test"
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
	dir := t.TempDir()
	cfg := Config{WorkingDir: dir}
	_, err := NewEngine(source, cfg)
	if err == nil {
		t.Fatalf("expected preflight failure on non-repo workdir")
	}
	if !errors.Is(err, pipeline.ErrGitWorkdirNotRepo) {
		t.Errorf("want ErrGitWorkdirNotRepo, got %v", err)
	}
}

func TestNewEngine_PreflightBypassedWithGitOff(t *testing.T) {
	source := `workflow TestWF
  goal: "test"
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
	dir := t.TempDir()
	cfg := Config{
		WorkingDir: dir,
		Git:        &GitConfig{Preflight: GitPreflightOff},
	}
	// Should succeed (or fail for an unrelated reason like missing API keys),
	// but NOT with ErrGitWorkdirNotRepo.
	_, err := NewEngine(source, cfg)
	if err != nil && errors.Is(err, pipeline.ErrGitWorkdirNotRepo) {
		t.Errorf("--git=off should bypass preflight, got %v", err)
	}
	_ = filepath.Join // keep import; remove if unused
	_ = context.Background
}
```

Add `"errors"` to imports.

- [ ] **Step 2: Run tests, confirm they fail**

```bash
go test . -run TestNewEngine_Preflight -v
```

Expected: FAIL — preflight not yet invoked.

- [ ] **Step 3: Wire `Preflight` into `NewEngine`**

In `tracker.go`, modify `NewEngine` (around line 137) to call Preflight after `pipeline.Validate(graph)` and before `resolveCompleter`. Insert between lines 145 and 147:

```go
func NewEngine(source string, cfg Config) (*Engine, error) {
	graph, err := parsePipelineSource(source, cfg.Format)
	if err != nil {
		return nil, err
	}

	if err := pipeline.Validate(graph); err != nil {
		return nil, fmt.Errorf("validate graph: %w", err)
	}

	workDir, err := resolveWorkDir(cfg.WorkingDir)
	if err != nil {
		return nil, err
	}

	if err := runPreflight(graph, cfg, workDir); err != nil {
		return nil, err
	}

	if err := applyResumeRunID(&cfg, workDir); err != nil {
		return nil, err
	}

	client, completer, err := resolveCompleter(cfg)
	if err != nil {
		return nil, err
	}

	return buildEngine(graph, cfg, workDir, client, completer)
}

// runPreflight invokes pipeline.Preflight with the resolved policy from cfg.
// Returns nil if the workflow doesn't declare any deps or if the policy
// downgrades the check to a warning.
func runPreflight(graph *pipeline.Graph, cfg Config, workDir string) error {
	policy, allowInit := ResolveGitConfig(cfg)
	return pipeline.Preflight(context.Background(), pipeline.PreflightConfig{
		WorkDir:        workDir,
		Requires:       graph.RequiredDeps(),
		Policy:         policy,
		AllowInit:      allowInit,
		InteractiveTTY: false, // library callers default to non-interactive; CLI sets this to true
		Warner: func(format string, args ...any) {
			fmt.Fprintf(os.Stderr, "warning: "+format+"\n", args...)
		},
	})
}
```

Make sure `context` is imported.

- [ ] **Step 4: Run tests, confirm they pass**

```bash
go test . -run TestNewEngine_Preflight -v
```

Expected: PASS (both tests).

- [ ] **Step 5: Run full short suite**

```bash
go test ./... -short
```

Expected: PASS in all 17 packages. NOTE: tests in `cmd/tracker` may pass because the CLI doesn't go through `tracker.NewEngine` yet — Phase 5 wires the CLI side.

- [ ] **Step 6: Commit**

```bash
git add tracker.go tracker_test.go
git commit -m "feat(tracker): run git preflight in NewEngine before LLM setup

Library callers (tracker.Run, tracker.NewEngine) now get the preflight
check automatically. CLI integration follows in a subsequent commit.

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Phase 4: CLI flags

### Task 4.1: Add `--git` and `--allow-init` to runConfig

**Files:**
- Modify: `cmd/tracker/main.go`
- Modify: `cmd/tracker/flags.go`
- Test: `cmd/tracker/flags_test.go` (or wherever flag tests live)

- [ ] **Step 1: Write the failing tests**

Find or create `cmd/tracker/flags_test.go` and add:

```go
func TestParseFlags_GitPolicyValid(t *testing.T) {
	cases := []string{"off", "warn", "require", "init", "auto"}
	for _, v := range cases {
		t.Run(v, func(t *testing.T) {
			args := []string{"tracker", "run.dip", "--git=" + v}
			cfg, err := parseFlags(args)
			if err != nil {
				t.Fatalf("unexpected: %v", err)
			}
			want := v
			if v == "auto" {
				want = "" // resolves to GitPreflightAuto
			}
			if cfg.git != want {
				t.Errorf("git: want %q, got %q", want, cfg.git)
			}
		})
	}
}

func TestParseFlags_GitPolicyInvalid(t *testing.T) {
	args := []string{"tracker", "run.dip", "--git=bogus"}
	_, err := parseFlags(args)
	if err == nil {
		t.Fatalf("expected error on invalid --git value")
	}
	if !strings.Contains(err.Error(), "auto") || !strings.Contains(err.Error(), "off") {
		t.Errorf("error must list valid values, got %v", err)
	}
}

func TestParseFlags_AllowInit(t *testing.T) {
	args := []string{"tracker", "run.dip", "--git=init", "--allow-init"}
	cfg, err := parseFlags(args)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if cfg.git != "init" {
		t.Errorf("git: want %q, got %q", "init", cfg.git)
	}
	if !cfg.allowInit {
		t.Errorf("allowInit: want true")
	}
}
```

- [ ] **Step 2: Run tests, confirm they fail**

```bash
go test ./cmd/tracker -run TestParseFlags_Git -v
go test ./cmd/tracker -run TestParseFlags_AllowInit -v
```

Expected: FAIL — `cfg.git undefined`.

- [ ] **Step 3: Add fields to runConfig**

In `cmd/tracker/main.go` (around line 48), add to the `runConfig` struct:

```go
	// git preflight (v0.29.0). Empty = auto.
	git       string // "off" | "warn" | "require" | "init" | "" (auto)
	allowInit bool   // second latch for --git=init
```

- [ ] **Step 4: Wire flags in `newRunFlagSet`**

In `cmd/tracker/flags.go` `newRunFlagSet` (around line 196), add:

```go
fs.StringVar(&cfg.git, "git", "", "Git preflight: auto (default, respects workflow `requires:`) | off | warn | require | init")
fs.BoolVar(&cfg.allowInit, "allow-init", false, "Required latch for --git=init in non-interactive runs")
```

Add validation in `parseRunFlags` (after `validateBackend`):

```go
if err := validateGitFlag(cfg); err != nil {
	return cfg, err
}
```

Add the validator function:

```go
// validateGitFlag rejects invalid --git values up front so the user gets a
// clear error at flag-parse time rather than deep inside preflight.
func validateGitFlag(cfg runConfig) error {
	switch cfg.git {
	case "", "auto", "off", "warn", "require", "init":
		// OK
	default:
		return fmt.Errorf("invalid --git=%q: must be one of: auto, off, warn, require, init", cfg.git)
	}
	if cfg.git == "init" && !cfg.allowInit {
		// Non-interactive check happens at preflight time (the runtime can
		// see stdin); we don't fail at parse time so interactive runs work.
	}
	return nil
}
```

(Empty `cfg.git == "auto"` is treated as `""` downstream by `ResolveGitConfig` — the flag accepts both spellings.)

- [ ] **Step 5: Normalize the "auto" alias in parseRunFlags so test assertion is stable**

At the end of `parseRunFlags`, after validators, add:

```go
if cfg.git == "auto" {
	cfg.git = ""
}
```

- [ ] **Step 6: Wire flags into doctor's flag set too**

In `cmd/tracker/flags.go` `parseDoctorFlags` (around line 77), add:

```go
dfs.StringVar(&cfg.git, "git", "", "Git preflight policy (auto/off/warn/require/init) to evaluate")
dfs.BoolVar(&cfg.allowInit, "allow-init", false, "Required latch for --git=init")
```

And in the doctor path, also call `validateGitFlag`:

```go
if err := validateGitFlag(*cfg); err != nil {
	return *cfg, fmt.Errorf("doctor: %w", err)
}
```

- [ ] **Step 7: Update `printUsage`**

In `cmd/tracker/flags.go` `printUsage` (around line 360), add two lines after `--force-bundle-mismatch`:

```go
fmt.Fprintf(w, "  --git policy              Git preflight policy: auto (default), off, warn, require, init\n")
fmt.Fprintf(w, "  --allow-init              Required for --git=init in non-interactive runs\n")
```

- [ ] **Step 8: Run tests, confirm they pass**

```bash
go test ./cmd/tracker -run TestParseFlags_ -v
```

Expected: PASS.

- [ ] **Step 9: Run full cmd/tracker tests**

```bash
go test ./cmd/tracker -short
```

Expected: PASS.

- [ ] **Step 10: Commit**

```bash
git add cmd/tracker/main.go cmd/tracker/flags.go cmd/tracker/flags_test.go
git commit -m "feat(cli): add --git and --allow-init flags

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 4.2: Wire preflight into CLI run path (run() + runTUI())

**Files:**
- Modify: `cmd/tracker/run.go`
- Test: integration test added in Phase 6 — Task 6.1 covers this end-to-end.

- [ ] **Step 1: Add a helper for the CLI preflight call**

In `cmd/tracker/run.go`, near the top (after the existing helpers like `wireLLMTraceToLog`), add:

```go
// applyGitPreflight runs the v0.29.0 git preflight check. Called from both
// run() and runTUI() after the graph is parsed and params are applied,
// but before any LLM client setup or network activity. Bail on error so
// the user sees the actionable remediation instead of a deferred failure.
func applyGitPreflight(graph *pipeline.Graph, workdir string, cliCfg runConfig) error {
	return pipeline.Preflight(context.Background(), pipeline.PreflightConfig{
		WorkDir:        workdir,
		Requires:       graph.RequiredDeps(),
		Policy:         pipeline.GitPreflight(cliCfg.git),
		AllowInit:      cliCfg.allowInit,
		InteractiveTTY: isatty.IsTerminal(os.Stdin.Fd()),
		Warner: func(format string, args ...any) {
			fmt.Fprintf(os.Stderr, "warning: "+format+"\n", args...)
		},
	})
}
```

Confirm `context`, `isatty`, `os`, `pipeline` imports exist; add `context` if not.

- [ ] **Step 2: Plumb cliCfg through `run` and `runTUI`**

The current entry points `run(...)` and `runTUI(...)` take a fixed signature with `pipelineFile, workdir, checkpoint, format, backend string, verbose bool, jsonOut bool` — they don't receive the full `runConfig`. The simplest approach: pass the relevant fields explicitly without restructuring the signature, threading through the existing module-level `activeAutopilotCfg` / `activeArtifactDir` pattern.

Add module-level variables in `cmd/tracker/run.go`:

```go
// activeGitConfig holds the CLI git preflight settings, populated by main()
// before run/runTUI/doctor dispatch (parallels activeAutopilotCfg).
var activeGitConfig struct {
	policy    string
	allowInit bool
}
```

In `cmd/tracker/main.go` (where `activeAutopilotCfg` and other module-level state is wired — search for it; likely in the `dispatch` or `dispatchPipelineCommands` function), add a line setting `activeGitConfig` from `cfg.git` / `cfg.allowInit`:

```go
activeGitConfig.policy = cfg.git
activeGitConfig.allowInit = cfg.allowInit
```

Then in `run()` (cmd/tracker/run.go line 132) and `runTUI()` (line 399), immediately after `applyRunParamOverrides`:

```go
if err := applyGitPreflight(graph, workdir, runConfig{git: activeGitConfig.policy, allowInit: activeGitConfig.allowInit}); err != nil {
	return err
}
```

(Yes, using runConfig as a tiny carrier is ugly; consider refactoring `applyGitPreflight` to take just `(graph, workdir, policy, allowInit)` instead. The simpler form:)

```go
if err := pipeline.Preflight(context.Background(), pipeline.PreflightConfig{
	WorkDir:        workdir,
	Requires:       graph.RequiredDeps(),
	Policy:         pipeline.GitPreflight(activeGitConfig.policy),
	AllowInit:      activeGitConfig.allowInit,
	InteractiveTTY: isatty.IsTerminal(os.Stdin.Fd()),
	Warner: func(format string, args ...any) {
		fmt.Fprintf(os.Stderr, "warning: "+format+"\n", args...)
	},
}); err != nil {
	return err
}
```

Use this form — drop the `applyGitPreflight` helper that wraps a runConfig. Add inline in both `run()` and `runTUI()`.

- [ ] **Step 3: Build to confirm the CLI compiles**

```bash
go build ./...
```

Expected: success.

- [ ] **Step 4: Run cmd/tracker tests**

```bash
go test ./cmd/tracker -short
```

Expected: PASS.

- [ ] **Step 5: Smoke test manually (no commit yet)**

```bash
# Create a tempdir, run a built-in workflow that will get `requires: git` later.
TDIR=$(mktemp -d)
cd "$TDIR"
tracker workflows  # confirm flag plumbing didn't break command dispatch
cd -
rm -rf "$TDIR"
```

- [ ] **Step 6: Commit**

```bash
git add cmd/tracker/run.go cmd/tracker/main.go
git commit -m "feat(cli): invoke git preflight in run() and runTUI() before LLM setup

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Phase 5: `tracker doctor` integration

### Task 5.1: Add `checkGitRequires` to the Doctor

**Files:**
- Modify: `tracker_doctor.go`
- Test: `tracker_doctor_test.go`

- [ ] **Step 1: Write the failing tests**

In `tracker_doctor_test.go`:

```go
func TestDoctor_GitRequires_Satisfied(t *testing.T) {
	dir := t.TempDir()
	mustGitInit(t, dir)
	pipelineFile := filepath.Join(dir, "wf.dip")
	if err := os.WriteFile(pipelineFile, []byte(`workflow WF
  goal: "test"
  requires: git
  start: Start
  exit: Done
  agent Start
    label: Start
  agent Done
    label: Done
  edges
    Start -> Done
`), 0o644); err != nil {
		t.Fatal(err)
	}
	rep, err := Doctor(context.Background(), DoctorConfig{
		WorkDir:        dir,
		PipelineFile:   pipelineFile,
		ProbeProviders: false,
	}, WithGitConfig(GitPreflightAuto, false))
	if err != nil {
		t.Fatal(err)
	}
	gr := findCheck(rep, "Git Requires")
	if gr == nil {
		t.Fatal("no Git Requires check in report")
	}
	if gr.Status != CheckStatusOK {
		t.Errorf("want ok, got %s: %s", gr.Status, gr.Message)
	}
}

func TestDoctor_GitRequires_MissingHardFail(t *testing.T) {
	dir := t.TempDir()
	pipelineFile := filepath.Join(dir, "wf.dip")
	os.WriteFile(pipelineFile, []byte(`workflow WF
  goal: "test"
  requires: git
  start: Start
  exit: Done
  agent Start
    label: Start
  agent Done
    label: Done
  edges
    Start -> Done
`), 0o644)
	rep, _ := Doctor(context.Background(), DoctorConfig{
		WorkDir:      dir,
		PipelineFile: pipelineFile,
	}, WithGitConfig(GitPreflightAuto, false))
	gr := findCheck(rep, "Git Requires")
	if gr == nil || gr.Status != CheckStatusError {
		t.Fatalf("want error, got %v", gr)
	}
	if !strings.Contains(gr.Hint, "git init") {
		t.Errorf("hint must include 'git init': %v", gr.Hint)
	}
}

func TestDoctor_GitRequires_WarnPolicyDowngrade(t *testing.T) {
	dir := t.TempDir()
	pipelineFile := filepath.Join(dir, "wf.dip")
	os.WriteFile(pipelineFile, []byte(`workflow WF
  goal: "test"
  requires: git
  start: Start
  exit: Done
  agent Start
    label: Start
  agent Done
    label: Done
  edges
    Start -> Done
`), 0o644)
	rep, _ := Doctor(context.Background(), DoctorConfig{
		WorkDir:      dir,
		PipelineFile: pipelineFile,
	}, WithGitConfig(GitPreflightWarn, false))
	gr := findCheck(rep, "Git Requires")
	if gr == nil || gr.Status != CheckStatusWarn {
		t.Fatalf("want warn, got %v", gr)
	}
}

func TestDoctor_GitRequires_OffSkip(t *testing.T) {
	dir := t.TempDir()
	pipelineFile := filepath.Join(dir, "wf.dip")
	os.WriteFile(pipelineFile, []byte(`workflow WF
  goal: "test"
  requires: git
  start: Start
  exit: Done
  agent Start
    label: Start
  agent Done
    label: Done
  edges
    Start -> Done
`), 0o644)
	rep, _ := Doctor(context.Background(), DoctorConfig{
		WorkDir:      dir,
		PipelineFile: pipelineFile,
	}, WithGitConfig(GitPreflightOff, false))
	gr := findCheck(rep, "Git Requires")
	if gr == nil || gr.Status != CheckStatusSkip {
		t.Fatalf("want skip, got %v", gr)
	}
}

// findCheck returns the named CheckResult or nil.
func findCheck(rep *DoctorReport, name string) *CheckResult {
	for i := range rep.Checks {
		if rep.Checks[i].Name == name {
			return &rep.Checks[i]
		}
	}
	return nil
}

// mustGitInit is duplicated from pipeline/git_preflight_test.go to avoid
// cross-package test imports.
func mustGitInit(t *testing.T, dir string) {
	t.Helper()
	cmd := exec.Command("git", "init", "-q")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init in %s: %v: %s", dir, err, out)
	}
}
```

Add imports: `"os/exec"`, `"context"`, `"path/filepath"`, `"strings"`, `"os"`.

- [ ] **Step 2: Run tests, confirm they fail**

```bash
go test . -run TestDoctor_GitRequires -v
```

Expected: FAIL — `WithGitConfig undefined` and `Git Requires` check not present.

- [ ] **Step 3: Add `WithGitConfig` option and `gitCfg` field to DoctorConfig**

In `tracker_doctor.go` (around line 28), add a private field and a public option:

```go
type DoctorConfig struct {
	// ... existing fields ...
	versionInfo versionInfo
	gitCfg      doctorGitConfig
}

type doctorGitConfig struct {
	policy    GitPreflight
	allowInit bool
	set       bool
}

// WithGitConfig sets the git preflight policy considered by the Git Requires
// check. Library callers that don't set this get GitPreflightAuto behavior.
func WithGitConfig(policy GitPreflight, allowInit bool) DoctorOption {
	return func(c *DoctorConfig) {
		c.gitCfg = doctorGitConfig{policy: policy, allowInit: allowInit, set: true}
	}
}
```

- [ ] **Step 4: Add `checkGitRequires` and wire it into the check list**

Add the new check function (place it near `checkPipelineFile`, around line 814):

```go
// checkGitRequires evaluates the workflow's `requires:` list against the
// current environment and the resolved --git= policy. Runs only when a
// pipeline file is provided. The result mirrors what would happen at
// `tracker run` time, so users can preview the gate without burning spend.
func checkGitRequires(cfg DoctorConfig) CheckResult {
	out := CheckResult{Name: "Git Requires"}

	if cfg.PipelineFile == "" {
		out.Status = CheckStatusSkip
		out.Message = "no pipeline file provided"
		return out
	}

	fileBytes, err := os.ReadFile(cfg.PipelineFile)
	if err != nil {
		out.Status = CheckStatusSkip
		out.Message = fmt.Sprintf("cannot read %s: %v", cfg.PipelineFile, err)
		return out
	}
	graph, err := parsePipelineSource(string(fileBytes), detectSourceFormat(string(fileBytes)))
	if err != nil {
		out.Status = CheckStatusSkip
		out.Message = fmt.Sprintf("cannot parse %s: %v", cfg.PipelineFile, err)
		return out
	}

	deps := graph.RequiredDeps()
	policy := cfg.gitCfg.policy
	if policy == GitPreflightOff {
		out.Status = CheckStatusSkip
		out.Message = "--git=off; bypassing"
		return out
	}

	// Simulate the preflight by re-running its checks but without side
	// effects. We don't want `doctor` to auto-init; it's an inspection tool.
	requiresGit := false
	for _, d := range deps {
		if strings.EqualFold(strings.TrimSpace(d), "git") {
			requiresGit = true
			break
		}
	}
	if policy == GitPreflightRequire || policy == GitPreflightInit {
		requiresGit = true
	}
	if !requiresGit {
		out.Status = CheckStatusOK
		out.Message = "workflow does not require git"
		return out
	}

	installed, isRepo, err := pipelineCheckGitForDoctor(cfg.WorkDir)
	if err != nil {
		out.Status = CheckStatusError
		out.Message = fmt.Sprintf("git check failed: %v", err)
		return out
	}
	if !installed {
		out.Status = doctorStatusForPolicy(policy, CheckStatusError)
		out.Message = "workflow requires git, but git is not in PATH"
		out.Hint = "install git (brew install git / apt install git / https://git-scm.com)"
		return out
	}
	if !isRepo {
		out.Status = doctorStatusForPolicy(policy, CheckStatusError)
		out.Message = fmt.Sprintf("workflow requires a git repository; %s is not inside one", cfg.WorkDir)
		out.Hint = "run `git init` here, or `tracker <workflow> --git=init --allow-init`"
		return out
	}
	out.Status = CheckStatusOK
	out.Message = "workflow requires git; env satisfies it"
	return out
}

// doctorStatusForPolicy maps preflight policy to a CheckStatus, downgrading
// to warn when policy == warn.
func doctorStatusForPolicy(policy GitPreflight, hardStatus CheckStatus) CheckStatus {
	if policy == GitPreflightWarn {
		return CheckStatusWarn
	}
	return hardStatus
}

// pipelineCheckGitForDoctor is a thin wrapper so the doctor file doesn't
// reach into pipeline-internal symbols. Kept here to avoid exporting an
// otherwise-internal helper.
func pipelineCheckGitForDoctor(workDir string) (installed bool, isRepo bool, err error) {
	if _, lerr := exec.LookPath("git"); lerr != nil {
		return false, false, nil
	}
	cmd := exec.Command("git", "-C", workDir, "rev-parse", "--git-dir")
	if rerr := cmd.Run(); rerr == nil {
		return true, true, nil
	}
	return true, false, nil
}
```

Wire it into the check list inside `Doctor` (around line 138):

```go
r.Checks = append(r.Checks,
	checkEnvWarnings(),
	checkProviders(ctx, cfg.ProbeProviders),
	checkDippin(ctx),
	checkVersionCompat(ctx, cfg.versionInfo.version, cfg.versionInfo.commit),
	checkOtherBinaries(ctx, cfg.Backend),
	checkWorkdir(cfg.WorkDir),
	checkArtifactDirs(cfg.WorkDir),
	checkDiskSpace(cfg.WorkDir),
)
if cfg.PipelineFile != "" {
	r.Checks = append(r.Checks,
		checkPipelineFile(cfg.PipelineFile),
		checkGitRequires(cfg),  // NEW
	)
}
```

- [ ] **Step 5: Run tests, confirm they pass**

```bash
go test . -run TestDoctor_GitRequires -v
```

Expected: PASS (all four).

- [ ] **Step 6: Wire CLI doctor to pass --git/--allow-init through**

In `cmd/tracker/doctor.go`, find the call to `tracker.Doctor(...)` and add the option:

```go
report, err := tracker.Doctor(ctx, tracker.DoctorConfig{
	WorkDir:        cfg.workdir,
	Backend:        cfg.backend,
	ProbeProviders: cfg.probe,
	PipelineFile:   cfg.pipelineFile,
},
	tracker.WithVersionInfo(version, commit),
	tracker.WithGitConfig(tracker.GitPreflight(cfg.git), cfg.allowInit), // NEW
)
```

Find the actual call site by `grep -n 'tracker.Doctor(' cmd/tracker/doctor.go` and adapt to whatever shape it currently has.

- [ ] **Step 7: Run cmd/tracker tests**

```bash
go test ./cmd/tracker -short
```

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add tracker_doctor.go tracker_doctor_test.go cmd/tracker/doctor.go
git commit -m "feat(doctor): add Git Requires check; CLI passes --git/--allow-init through

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Phase 6: `tracker workflows` display + integration test

### Task 6.1: Show `requires:` in `tracker workflows`

**Files:**
- Modify: `tracker_workflows.go`
- Modify: `cmd/tracker/commands.go`
- Test: `tracker_workflows_test.go` (create if not present)

- [ ] **Step 1: Write the failing test**

If `tracker_workflows_test.go` doesn't exist, create it:

```go
// ABOUTME: Tests for the built-in workflow catalog header parsing.
package tracker

import "testing"

func TestParseWorkflowHeader_Requires(t *testing.T) {
	// Indirect test through the public Workflows() — the build_product
	// workflow will declare `requires: git` after Phase 8 lands. For now
	// use a controlled parse path.
	displayName, goal, requires := parseWorkflowHeaderForTest([]byte(`workflow Foo
  goal: "test"
  requires: git, docker
  start: Start
  exit: Done
`))
	if displayName != "Foo" {
		t.Errorf("displayName: want Foo, got %q", displayName)
	}
	if goal != "test" {
		t.Errorf("goal: want 'test', got %q", goal)
	}
	if len(requires) != 2 || requires[0] != "git" || requires[1] != "docker" {
		t.Errorf("requires: want [git docker], got %v", requires)
	}
}
```

- [ ] **Step 2: Run test, confirm it fails**

```bash
go test . -run TestParseWorkflowHeader_Requires -v
```

Expected: FAIL — `parseWorkflowHeaderForTest undefined`.

- [ ] **Step 3: Extend parseWorkflowHeader to capture requires**

In `tracker_workflows.go`:

```go
type WorkflowInfo struct {
	Name        string
	File        string
	DisplayName string
	Goal        string
	Requires    []string // NEW — parsed from `requires:` line if present
}

func parseWorkflowHeader(file string) (displayName, goal string, requires []string) {
	f, err := embeddedWorkflows.Open(file)
	if err != nil {
		return "", "", nil
	}
	defer f.Close()
	return parseWorkflowHeaderReader(f)
}

// parseWorkflowHeaderForTest exposes the parser to tests for fixture-based
// assertions without needing to bake test workflows into the embedded FS.
func parseWorkflowHeaderForTest(content []byte) (displayName, goal string, requires []string) {
	return parseWorkflowHeaderReader(bytes.NewReader(content))
}

func parseWorkflowHeaderReader(r io.Reader) (displayName, goal string, requires []string) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "workflow ") {
			displayName = strings.TrimSpace(strings.TrimPrefix(trimmed, "workflow "))
			continue
		}
		if strings.HasPrefix(trimmed, "goal:") {
			goal = strings.Trim(strings.TrimSpace(strings.TrimPrefix(trimmed, "goal:")), `"`)
			continue
		}
		if strings.HasPrefix(trimmed, "requires:") {
			raw := strings.TrimSpace(strings.TrimPrefix(trimmed, "requires:"))
			for _, part := range strings.Split(raw, ",") {
				s := strings.TrimSpace(part)
				if s != "" {
					requires = append(requires, s)
				}
			}
			continue
		}
		// Stop scanning once we hit start: — header section is over.
		if strings.HasPrefix(trimmed, "start:") {
			break
		}
	}
	return displayName, goal, requires
}
```

Update the `loadWorkflowCatalog` site to capture the new return value:

```go
displayName, goal, requires := parseWorkflowHeader(file)
info := WorkflowInfo{
	Name:        name,
	File:        file,
	DisplayName: displayName,
	Goal:        goal,
	Requires:    requires,
}
```

Add imports: `"bytes"`, `"io"`.

- [ ] **Step 4: Run test, confirm it passes**

```bash
go test . -run TestParseWorkflowHeader_Requires -v
```

Expected: PASS.

- [ ] **Step 5: Surface requires in `tracker workflows` output**

In `cmd/tracker/commands.go` `executeWorkflows` (around line 159), tweak the format:

```go
func executeWorkflows() error {
	workflows := listBuiltinWorkflows()
	if len(workflows) == 0 {
		fmt.Println("No built-in workflows available.")
		return nil
	}

	fmt.Println("\nBuilt-in workflows:")
	fmt.Println()
	fmt.Printf("  %-35s  %-12s  %s\n", "NAME", "REQUIRES", "DESCRIPTION")
	fmt.Printf("  %-35s  %-12s  %s\n", "────", "────────", "───────────")
	for _, wf := range workflows {
		goal := wf.Goal
		if len(goal) > 70 {
			goal = goal[:67] + "..."
		}
		req := strings.Join(wf.Requires, ", ")
		if req == "" {
			req = "—"
		}
		fmt.Printf("  %-35s  %-12s  %s\n", wf.Name+" ("+wf.DisplayName+")", req, goal)
	}
	fmt.Println()
	fmt.Println("  Run directly:     tracker <workflow_name>")
	fmt.Println("  Copy to edit:     tracker init <workflow_name>")
	fmt.Println("  Validate:         tracker validate <workflow_name>")
	fmt.Println()
	return nil
}
```

- [ ] **Step 6: Update embed_test.go if it asserts column structure**

If `cmd/tracker/embed_test.go` does any string-matching against the workflows output, update or skip — likely it just asserts `len(workflows) == 4`. Confirm with:

```bash
go test ./cmd/tracker -run TestListBuiltinWorkflows -v
```

- [ ] **Step 7: Commit**

```bash
git add tracker_workflows.go tracker_workflows_test.go cmd/tracker/commands.go
git commit -m "feat(workflows): show requires: per workflow in 'tracker workflows' output

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 6.2: End-to-end integration test

**Files:**
- Create: `tracker_git_preflight_integration_test.go`

- [ ] **Step 1: Write the integration test**

Create `tracker_git_preflight_integration_test.go`:

```go
// ABOUTME: End-to-end integration test for v0.29.0 git preflight.
// ABOUTME: Confirms a `requires: git` workflow fails at preflight in a non-repo dir,
// ABOUTME: and runs through after `git init`.
package tracker

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/2389-research/tracker/pipeline"
)

const fixtureWithRequiresGit = `workflow PreflightFixture
  goal: "test"
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

func TestIntegration_Preflight_NoRepoFailsBeforeNodeExecutes(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{WorkingDir: dir}
	_, err := NewEngine(fixtureWithRequiresGit, cfg)
	if err == nil {
		t.Fatalf("expected preflight failure")
	}
	if !errors.Is(err, pipeline.ErrGitWorkdirNotRepo) {
		t.Fatalf("want ErrGitWorkdirNotRepo, got %v", err)
	}
	// The remediation text MUST appear in the error.
	if got := err.Error(); !contains(got, "git init") {
		t.Errorf("error must include 'git init' remediation; got: %s", got)
	}
}

func TestIntegration_Preflight_GitInitClears(t *testing.T) {
	dir := t.TempDir()
	cmd := exec.Command("git", "init", "-q")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	cfg := Config{WorkingDir: dir}
	_, err := NewEngine(fixtureWithRequiresGit, cfg)
	// May fail for unrelated reasons (no API keys, etc.) but MUST NOT be ErrGitWorkdirNotRepo.
	if err != nil && errors.Is(err, pipeline.ErrGitWorkdirNotRepo) {
		t.Fatalf("preflight should pass after git init, got %v", err)
	}
}

func TestIntegration_Preflight_AutoInitInTempDir(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{
		WorkingDir: dir,
		Git:        &GitConfig{Preflight: GitPreflightInit, AllowInit: true},
	}
	// We use NewEngine just to trigger preflight; the engine itself may
	// or may not be usable depending on test environment LLM creds.
	_, _ = NewEngine(fixtureWithRequiresGit, cfg)
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		t.Fatalf("expected .git after auto-init: %v", err)
	}
}

func contains(haystack, needle string) bool {
	return indexOf(haystack, needle) >= 0
}

func indexOf(haystack, needle string) int {
	// Avoid importing strings here just for Contains — they're already in
	// scope elsewhere in the package; use the strings package import explicitly.
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}

func _suppressUnused() { _ = context.Background }
```

- [ ] **Step 2: Run the integration test**

```bash
go test . -run TestIntegration_Preflight -v
```

Expected: PASS (all three).

- [ ] **Step 3: Commit**

```bash
git add tracker_git_preflight_integration_test.go
git commit -m "test: end-to-end integration test for git preflight

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Phase 7: Update built-in workflows

### Task 7.1: Audit and tag built-ins

**Files:**
- Modify: `workflows/build_product.dip`
- Modify: `workflows/build_product_with_superspec.dip`
- Audit: `workflows/ask_and_execute.dip`
- Audit: `workflows/deep_review.dip`

- [ ] **Step 1: Audit which built-ins assume git**

Run:

```bash
grep -lE 'git (commit|add|push|tag|status|init|checkout|branch|stash|merge|rebase)' workflows/*.dip
```

Expected output indicates which workflows reference git commands in prompts or tool blocks. Compare against the spec's list. The spec calls out `build_product`, `build_product_with_superspec`, and (in tracker's installed name) potentially others.

Note any workflow that surfaces git but whose authors didn't intend a hard requirement — those get the comma-separated form anyway since `requires: git` is the gate.

- [ ] **Step 2: Add `requires: git` to build_product.dip**

Edit `workflows/build_product.dip` lines 1-4:

```dippin
workflow BuildProduct
  goal: "Read a SPEC.md, decompose into milestones, implement each with verification loops, cross-review the complete result, and verify full spec compliance."
  requires: git
  start: Start
  exit: Done
```

- [ ] **Step 3: Repeat for build_product_with_superspec.dip**

Same insertion, between `goal:` and `start:`.

- [ ] **Step 4: Audit ask_and_execute.dip**

Run:

```bash
grep -n "git " workflows/ask_and_execute.dip | head -20
```

If git is referenced in agent prompts (e.g. as a recommended verification step), decide:
- "Workflow strongly assumes git" → add `requires: git`
- "Workflow mentions git as one option, doesn't require it" → leave alone

For ask_and_execute, the workflow's purpose is "ask for a task, implement it" — git is implicit but not strict. **Decision:** leave the audit comment in this plan; the implementer should make the call based on what they read. Default to leaving it OUT unless prompts explicitly call `git commit`.

- [ ] **Step 5: Audit deep_review.dip**

Similar audit. `deep_review` is a code-review workflow that almost certainly inspects a git tree. **Probable decision:** add `requires: git`. Confirm by reading the file.

- [ ] **Step 6: Validate all built-ins still parse**

```bash
go test ./pipeline -run TestEmbeddedWorkflowsParse -v
```

(Or whichever existing test parses every embedded `.dip` file. Find with `grep -l "ParseDippin\|FromDippinIR" pipeline/*_test.go`.)

Expected: PASS.

- [ ] **Step 7: Run dippin doctor on examples**

```bash
dippin doctor workflows/build_product.dip workflows/build_product_with_superspec.dip workflows/ask_and_execute.dip workflows/deep_review.dip
```

Expected: all A-grade (matches the pre-commit requirement in CLAUDE.md).

- [ ] **Step 8: Commit**

```bash
git add workflows/build_product.dip workflows/build_product_with_superspec.dip
# Add others if your audit decided to include them
git commit -m "feat(workflows): declare requires: git on built-in workflows that commit mid-run

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

## Phase 8: CHANGELOG, README, final verification

### Task 8.1: Update CHANGELOG.md

**Files:**
- Modify: `CHANGELOG.md`

- [ ] **Step 1: Read the existing CHANGELOG for format**

```bash
head -60 CHANGELOG.md
```

- [ ] **Step 2: Add the v0.29.0 entry**

Insert at the top under `[Unreleased]` (or create the `v0.29.0` section if the project bumps before release):

```markdown
## [Unreleased]

### Added
- **Workflow header `requires: <list>` for environmental dependencies.** Workflows can now declare prerequisites at the top of the `.dip` file. v0.29.0 implements `git` (`requires: git` makes tracker verify git is installed and the workdir is a git repo before any LLM call). Unrecognized entries warn and continue so workflow authors can forward-declare dependencies that future tracker versions will check. Closes #234.
- **`--git=off|warn|require|init` CLI flag** to override the policy per run. Default `auto` respects the workflow's `requires:` block. `--git=init` (with mandatory `--allow-init` latch) auto-runs `git init` in the workdir, with safety refusals for `$HOME`, `/`, bare repos, linked worktrees, and submodules.
- **`tracker doctor` Git Requires check** previews what would happen at run start for the current dir + workflow + flags.
- **Built-in workflows** that commit mid-run (`build_product`, `build_product_with_superspec`) now declare `requires: git`. Running them in a non-git directory fails in seconds with a copy-paste remediation, instead of burning hours of LLM spend.

### Changed
- Dippin-lang bumped to v0.26.0 (adds `requires:` workflow header).
```

- [ ] **Step 3: Verify the version bump is reflected in the right places**

```bash
grep -n "v0.28\|v0.29" CHANGELOG.md tracker_doctor.go go.mod | head -20
```

`PinnedDippinVersion` in `tracker_doctor.go` should be `v0.26.0`. The tracker binary version itself bumps when the release PR is cut.

- [ ] **Step 4: Commit**

```bash
git add CHANGELOG.md
git commit -m "docs(changelog): v0.29.0 git preflight entry

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 8.2: Update README.md

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Find the workflow-authoring section in README**

```bash
grep -n "workflow\|requires\|defaults\|## " README.md | head -30
```

- [ ] **Step 2: Add a single paragraph documenting `requires:`**

Add after the existing workflow header documentation:

````markdown
### Declaring environmental dependencies

Workflows can declare what they need from the host environment with a
`requires:` line in the header:

```dippin
workflow BuildProduct
  goal: "..."
  requires: git
  start: Start
  exit: Done
```

`tracker run` checks these at startup. If the env doesn't satisfy them,
the run fails in seconds with a copy-paste remediation instead of burning
LLM spend before the first failure. Override per-run with `--git=auto|off|warn|require|init`.
v0.29.0 implements `git`; unrecognized entries warn and continue.
````

- [ ] **Step 3: Commit**

```bash
git add README.md
git commit -m "docs(readme): document requires: workflow header

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>"
```

---

### Task 8.3: Final verification gauntlet

**Files:** None — verification only.

- [ ] **Step 1: Run the full test suite (short)**

```bash
go test ./... -short
```

Expected: PASS in all 17 packages.

- [ ] **Step 2: Run the build**

```bash
go build ./...
```

Expected: success.

- [ ] **Step 3: Run linters / formatters (per CLAUDE.md pre-commit gate)**

```bash
gofmt -l .
go vet ./...
```

Expected: no output from `gofmt -l`, no warnings from `go vet`.

- [ ] **Step 4: Run dippin doctor on every example**

```bash
dippin doctor examples/ask_and_execute.dip examples/build_product.dip examples/build_product_with_superspec.dip
```

Expected: A-grade across the board (per CLAUDE.md).

- [ ] **Step 5: Run dippin simulate -all-paths on the core pipelines**

```bash
dippin simulate -all-paths examples/build_product.dip
dippin simulate -all-paths examples/build_product_with_superspec.dip
dippin simulate -all-paths examples/ask_and_execute.dip
```

Expected: no errors, no new warnings.

- [ ] **Step 6: Manual smoke test — happy path**

```bash
TDIR=$(mktemp -d)
cd "$TDIR"
git init -q
echo "test spec" > SPEC.md
tracker --git=auto build_product --help 2>&1 | head -5  # confirm flag is recognized; --help avoids LLM call
cd -
rm -rf "$TDIR"
```

- [ ] **Step 7: Manual smoke test — sad path**

```bash
TDIR=$(mktemp -d)
cd "$TDIR"
# No git init. Expect a clear remediation message.
tracker build_product 2>&1 | head -20
cd -
rm -rf "$TDIR"
```

Expected: clear error message including "git init", with non-zero exit code.

- [ ] **Step 8: Manual smoke test — `--git=init` auto-init**

```bash
TDIR=$(mktemp -d)
cd "$TDIR"
# Doctor is READ-ONLY: it previews what runtime preflight would do but
# does NOT mutate the workdir (no `git init` happens). We assert the
# preview message instead of checking for `.git`.
tracker doctor --git=init --allow-init <(echo 'workflow X
  goal: "x"
  requires: git
  start: S
  exit: E
  agent S
    label: S
  agent E
    label: E
  edges
    S -> E
') 2>&1 | head -20
# Expected: a "Git Requires" check with status ok and a message like
# "workflow requires git; --git=init --allow-init would auto-init <path>"
# plus the hint "tracker run will create .git here before the first
# node executes". No `.git` directory is actually created — that
# happens at `tracker run` time, not doctor.
ls -la "$TDIR"/.git 2>&1 || echo "(expected) no .git — doctor is read-only"
cd -
rm -rf "$TDIR"
```

Expected: the Git Requires check reports `ok` with an "auto-init would
... at run start" message. **No `.git` is created** — doctor previews,
runtime mutates.

- [ ] **Step 9: Verify the user-facing error from the integration test is readable**

```bash
go test . -run TestIntegration_Preflight_NoRepoFailsBeforeNodeExecutes -v
```

Read the captured stderr in the test output. The message should be:
- Multi-line
- Include `Working directory: ...`
- Include `git init`
- Include `--git=off` as an explicit escape hatch

- [ ] **Step 10: Run the test suite once more (verbose)**

```bash
go test ./... -short -v 2>&1 | tail -30
```

Expected: every package's `PASS` line in the tail.

---

## Self-review checklist (run after writing the plan, before handoff)

### Spec coverage

| Spec section | Task(s) covering it |
|---|---|
| Workflow header `requires:` | Phase 0 (upstream), Task 2.1 (adapter), Task 7.1 (built-ins) |
| `--git=auto\|off\|warn\|require\|init` flag | Task 4.1 |
| `--allow-init` latch | Task 4.1, Task 1.4 |
| Library `Config.Git` + `GitConfig` | Task 3.1, Task 3.2 |
| `tracker.Preflight` (in `pipeline/git_preflight.go`) | Tasks 1.1–1.5 |
| `checkGit` helper | Task 1.2 |
| `runAutoInit` helper | Task 1.4 |
| `safetyLatches` ($HOME, /, nested) | Task 1.3 |
| Engine hook placement (`tracker.NewEngine` path) | Task 3.2 |
| CLI hook placement (`run`, `runTUI`) | Task 4.2 |
| `Graph.RequiredDeps()` | Task 1.6 |
| Adapter `extractRequires` | Task 2.1 |
| Doctor integration `checkGitRequires` | Task 5.1 |
| Failure-mode error messages | Tasks 1.5 (Preflight), 5.1 (Doctor hint) |
| Unit tests (full matrix from spec) | Tasks 1.1–1.5, 5.1, 6.2 |
| Integration test | Task 6.2 |
| Built-in workflow audit & tag | Task 7.1 |
| CHANGELOG | Task 8.1 |
| README | Task 8.2 |
| Backward compatibility (additive change) | Task 8.3 step 1 (full short suite must still pass) |
| Bare repo / worktree / submodule false-positive coverage | Task 1.3 tests, Open Question #3 resolution above |

### Type consistency check

- `GitPreflight` is a type alias defined twice: once in `pipeline/git_preflight.go` (`pipeline.GitPreflight string`) and once in `tracker.go` as a type alias to the pipeline type. The library re-exports the values so callers don't import pipeline.
- `PreflightConfig.Policy` is `pipeline.GitPreflight`; `Config.Git.Preflight` is `tracker.GitPreflight` which is the same underlying type via alias. They are assignment-compatible.
- `runConfig.git` in `cmd/tracker/main.go` is `string` (the raw flag value); it's converted to `pipeline.GitPreflight` at call time.
- `ResolveGitConfig(Config) (GitPreflight, bool)` returns the alias type.
- `checkGit`, `safetyLatches`, `runAutoInit` are internal-only and unexported — no API-surface consistency concern.

### Placeholder scan

No `TBD`, `TODO`, "implement later", "add appropriate error handling" — every step shows the exact code or command.

The Phase 7 audit step ("Audit ask_and_execute.dip") gives explicit decision criteria rather than punting; the audit IS the task. The implementer should grep the file, apply the criteria, and either add `requires: git` or not.

---

## Execution handoff

Plan saved to `docs/superpowers/plans/2026-05-15-tracker-git-preflight-plan.md`. Two execution options:

**1. Subagent-Driven (recommended)** — dispatch a fresh subagent per task, review between tasks, fast iteration. Best when the dippin-lang upstream PR (Phase 0) is a real blocker — the subagent can fire-and-forget while you wait for the upstream merge.

**2. Inline Execution** — execute tasks in this session using executing-plans, batch execution with checkpoints. Best if Phase 0 is already done or can be skipped for now (Phase 1 tasks have no upstream dependency).

Pause before Phase 0: confirm with the user whether to drive the dippin-lang PR through this same session or hand it off.
