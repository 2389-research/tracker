# Tracker Git Preflight — v0.29.0 Design

**Status:** Approved, ready for implementation plan
**Target release:** v0.29.0
**Follow-on work:** v0.30.0 will add engine-managed commits + push (separate spec)

## Goal

Workflows like `build_product.dip` instruct the LLM to `git commit` mid-run. Users frequently start a multi-hour, $20–$100 run in a directory that isn't a git repository or that lacks `git` in PATH. The pipeline burns the LLM spend and then dies at commit time. This spec adds a cheap, fast preflight check that fails the run *in seconds* instead of *hours*, with actionable remediation copy.

The spec also introduces a generic `requires:` workflow header keyword so workflows can declare their environmental dependencies in a way that scales beyond git (`requires: [docker, gh, jq]` are future extensions on the same mechanism).

## Non-goals (deferred to v0.30.0)

This spec is **preflight only**. The following are explicitly deferred to a future spec:

- Engine-managed commits at logical work boundaries
- Push at end of run
- Pre-commit hook policy (mutate-retry vs reject-abort)
- Working-dir clean-state requirement
- `ChildRunContext.ManagedGit` propagation to subgraph / manager_loop children
- Checkpoint-resume git reconciliation
- Removal of inline `git commit` instructions from built-in agent prompts

A separate brainstorming pass will design the managed-commits feature once preflight has shipped and matured.

## User-facing surface

### Workflow header: `requires:`

```dippin
workflow BuildProduct
  goal: "Build a feature end-to-end."
  start: Decompose
  exit: Done
  requires: [git]
  defaults:
    model: claude-opus-4-7
```

- The `requires:` list declares environmental dependencies the workflow expects to be satisfied at run start.
- v0.29.0 implements **only `git`**. Unrecognized entries (`docker`, `gh`, `jq`, etc.) parse successfully and emit a single warning per entry: `tracker: requires "<dep>" is not yet implemented; ignoring`. This lets workflow authors forward-declare without breaking on older tracker versions.
- The check runs once at the top of `tracker run`, before any node executes.

### CLI flags

```
--git=auto|off|warn|require|init   Preflight policy (default: auto)
--allow-init                       Second latch required by --git=init
```

`--git=auto` is equivalent to omitting the flag; both resolve to the `auto` policy.

| Flag value | Behavior |
|---|---|
| (omitted) | `auto`: respect the workflow's `requires:` block. Workflow says `requires: [git]` and env doesn't satisfy → hard-error. Workflow says nothing → no check. |
| `--git=off` | Bypass all git checks. Escape hatch for users running tracker on workflows whose authors haven't declared dependencies correctly. |
| `--git=warn` | Downgrade hard-fail to advisory warning. Workflow's `requires: [git]` becomes a warning instead of an error, run continues. |
| `--git=require` | Force hard-fail. Even workflows that don't declare `requires: [git]` get the check applied. |
| `--git=init` | Same as `require`, plus offer to auto-init the workdir as a git repo if missing. Requires `--allow-init` as a second latch. |
| `--allow-init` | Required when `--git=init` is set in a non-interactive run. Interactive runs (stdin is a TTY) can answer a `[Y/n]` prompt instead. |

CLI flags always win over the workflow's `requires:` block. Library `tracker.Config.Git` (see below) always wins over CLI.

### Library API

```go
type GitPreflight string

const (
    GitPreflightAuto    GitPreflight = ""        // default — respects requires:
    GitPreflightOff     GitPreflight = "off"
    GitPreflightWarn    GitPreflight = "warn"
    GitPreflightRequire GitPreflight = "require"
    GitPreflightInit    GitPreflight = "init"
)

type GitConfig struct {
    Preflight  GitPreflight
    AllowInit  bool // required when Preflight == GitPreflightInit and stdin is not a TTY
}
```

Added as `tracker.Config.Git *GitConfig`. Zero value (nil) is identical to `&GitConfig{Preflight: GitPreflightAuto}`.

### `tracker doctor` integration

`tracker doctor` learns a new check that resolves the workflow's `requires:` block against the current dir and CLI flags, and reports as `CheckStatusOK | Warn | Error | Skip`:

- **OK** — workflow doesn't require git, OR workflow requires git and the env satisfies it
- **Warn** — workflow requires git, env doesn't satisfy, AND `--git=warn` is set (or would be at run time)
- **Error** — workflow requires git, env doesn't satisfy, AND `--git=auto|require` is set
- **Skip** — `--git=off`

The check's `Suggestion` field carries the exact remediation command (`git init`, `Install git from https://git-scm.com`, etc.).

`tracker doctor` is documented in `cmd/tracker/doctor.go` already; this check slots into the existing `[]CheckResult` produced by `tracker.Doctor`.

## Implementation

### New file: `pipeline/git_preflight.go`

~120 LOC. Defines:

```go
// Preflight performs the git environment check declared by the workflow.
// Returns nil if checks pass or are bypassed; returns a typed error otherwise.
//
// Errors:
//   - ErrGitNotInstalled — git missing from PATH
//   - ErrGitWorkdirNotRepo — workdir is not inside a git repository
//   - ErrGitAutoInitRefused — --git=init was requested but a safety latch fired
//   - ErrGitDependencyUnsatisfied — workflow declares a dep we can't yet check
func Preflight(ctx context.Context, cfg PreflightConfig) error

type PreflightConfig struct {
    WorkDir       string
    Requires      []string       // from workflow header
    Policy        GitPreflight   // resolved from CLI / library / default
    AllowInit     bool           // from CLI / library
    InteractiveTTY bool          // for [Y/n] prompt fallback
    Warner        func(format string, args ...any) // structured warning sink
}
```

Internal helpers:
- `checkGit(workDir) (installed bool, isRepo bool, err error)` — runs `git --version` and `git -C <workDir> rev-parse --git-dir`
- `runAutoInit(workDir, allowInit, interactive bool) error` — performs `git init` after running safety latches
- `safetyLatches(workDir) error` — refuses init if `cwd == $HOME`, `cwd == /`, or any parent dir already contains `.git`

### Engine hook placement

`pipeline.NewEngine` is unchanged (no dependency on preflight). The preflight runs in `tracker.Run` (the library entry point), before the engine starts:

```go
// In tracker.go's Run() or buildEngine() flow:
if err := pipeline.Preflight(ctx, pipeline.PreflightConfig{
    WorkDir:        cfg.WorkingDir,
    Requires:       graph.RequiredDeps(),         // new method on Graph
    Policy:         resolvePreflightPolicy(cfg),  // CLI > Config > default
    AllowInit:      cfg.Git.AllowInit,
    InteractiveTTY: isTTY(os.Stdin),
    Warner:         logger.Warnf,
}); err != nil {
    return nil, err
}
```

This places preflight at the library API layer, not inside `pipeline.Engine`. The engine remains git-unaware. Subgraph and manager_loop child engines do NOT re-run preflight — the parent run satisfied it.

### Dependency: `Graph.RequiredDeps() []string`

New method on `pipeline.Graph` that reads `graph.Attrs["requires"]` (a comma-separated list set by the adapter). Returns the parsed slice.

### Adapter change: `pipeline/dippin_adapter.go`

`extractWorkflowDefaults` (or a sibling helper `extractRequires`) reads the new `requires:` field from `*ir.Workflow` and writes it to `graph.Attrs["requires"]` as a comma-separated string. Requires a dippin-lang version that surfaces `requires:` on `ir.Workflow`. If dippin-lang doesn't yet expose this field, the adapter falls back to `graph.Attrs["requires"]` being empty (no-op for now); a later dippin-lang bump activates the path.

**Action item before implementation:** confirm with dippin-lang maintainers whether `requires:` is in the IR for the version we're pinning to in v0.29.0. If not, file an issue and pin the dippin-lang bump as a blocker for this PR.

### CLI: `cmd/tracker/flags.go` + `run.go`

- Parse `--git` value into `cfg.Git.Preflight`
- Parse `--allow-init` boolean into `cfg.Git.AllowInit`
- `tracker --help` documents both
- `tracker doctor --git=...` accepted (matches `tracker run` flag surface)

### Doctor: `tracker_doctor.go`

A new `checkGitRequires(cfg DoctorConfig, graph *pipeline.Graph) CheckResult` function. Added to the existing check loop. Returns `CheckResult` with the same shape as other doctor checks.

## Failure modes & error messages

All error messages MUST follow the CLAUDE.md "must include actionable setup instructions" convention.

### `ErrGitNotInstalled`

```
tracker: this workflow requires git, but git was not found in PATH.

  Workflow: build_product
  Requires: [git]

  Install git:
    macOS:   brew install git
    Linux:   apt install git  (or your distro's equivalent)
    Windows: https://git-scm.com/download/win

  Or pass --git=off to bypass this check if you're sure git isn't needed.
```

### `ErrGitWorkdirNotRepo`

```
tracker: this workflow requires a git repository, but the current directory is not inside one.

  Workflow: build_product
  Working directory: /home/user/scratch
  Requires: [git]

  Initialize a repo here:
    git init

  Or have tracker do it:
    tracker run build_product --git=init --allow-init

  Or pass --git=off to bypass this check if you're sure git isn't needed.
```

### `ErrGitAutoInitRefused` (e.g., $HOME)

```
tracker: refusing to run `git init` in your home directory.

  Working directory: /home/user
  Reason: this is your home directory; initializing a git repo here would
          place every file under your home tree into the repo's tracking
          space. This is almost never what you want.

  Either:
    cd into a project subdirectory first, or
    explicitly run `git init` yourself if you know this is what you want.
```

### `ErrGitAutoInitRefused` (nested repo)

```
tracker: refusing to run `git init` here — a parent directory is already a git repository.

  Working directory: /home/user/project/subdir
  Parent repo:       /home/user/project

  Nested git repositories are almost always accidental and cause confusion
  about which repo `git` commands operate on.

  Either:
    cd into the parent repo and run from there, or
    pass --git=require (without =init) to fail without creating a nested repo.
```

## Test plan

### Unit tests (`pipeline/git_preflight_test.go`)

- **Happy path: git installed + workdir is repo + no `requires:` declared** → no error
- **Happy path: git installed + workdir is repo + `requires: [git]`** → no error
- **Happy path: git missing + no `requires:` declared + `--git=auto`** → no error (no opinion)
- **Hard fail: git missing + `requires: [git]` + `--git=auto`** → `ErrGitNotInstalled`
- **Hard fail: workdir not repo + `requires: [git]` + `--git=auto`** → `ErrGitWorkdirNotRepo`
- **Warn instead: workdir not repo + `requires: [git]` + `--git=warn`** → no error, warning emitted via `Warner`
- **CLI override: workdir not repo + no `requires:` + `--git=require`** → `ErrGitWorkdirNotRepo`
- **Auto-init success: workdir not repo + `--git=init --allow-init`** → no error, `.git/` created
- **Auto-init refused (home): cwd == $HOME + `--git=init --allow-init`** → `ErrGitAutoInitRefused`
- **Auto-init refused (nested): cwd inside existing repo's subdir + `--git=init --allow-init`** → `ErrGitAutoInitRefused`
- **Auto-init refused (no latch): `--git=init` without `--allow-init` and not interactive** → `ErrGitAutoInitRefused` with "pass --allow-init" message
- **Unrecognized requires: `requires: [docker]`** → no error, warning emitted ("not yet implemented")
- **Off bypass: `requires: [git]` + git missing + `--git=off`** → no error, no warning

### Doctor tests (`tracker_doctor_test.go`)

- `TestDoctor_GitRequires_Satisfied` — workflow declares git, env has it, returns `CheckStatusOK`
- `TestDoctor_GitRequires_Missing` — workflow declares git, env doesn't, returns `CheckStatusError` with remediation
- `TestDoctor_GitRequires_WarnPolicy` — `--git=warn` returns `CheckStatusWarn`
- `TestDoctor_GitRequires_OffPolicy` — `--git=off` returns `CheckStatusSkip`

### CLI tests (`cmd/tracker/flags_test.go`)

- `--git=off`, `--git=warn`, `--git=require`, `--git=init`, `--allow-init` all parse correctly
- `--git=bogus` returns an explicit error listing valid values
- `--git=init` without `--allow-init` in non-interactive mode is caught at flag-parse time with a friendly error (not deferred to preflight)

### Integration test

A fixture pipeline declaring `requires: [git]`, run end-to-end in a temp directory:

- with no git repo → fails at preflight, never invokes any node, surfaces the expected error message verbatim
- with `git init` first → runs through, no preflight error

## Migration & rollout

- **Built-in workflows updated in the same PR:**
  - `examples/build_product.dip` — add `requires: [git]`
  - `examples/build_product_with_superspec.dip` — add `requires: [git]`
  - `examples/dotpowers.dip` — add `requires: [git]`
  - Other built-ins audited: any that mention `git` in agent prompts or tool commands get the declaration
- **Backward compatibility:** the change is additive. Workflows that don't declare `requires:` get the same behavior as today. CLI flag default is `auto`, which is a no-op when no workflow declares `requires:`. No existing test should need to change.
- **CHANGELOG entry** (Added section):

  > - **Workflow header `requires: [...]` for environmental dependencies.** Workflows can now declare prerequisites at the top of the `.dip` file. v0.29.0 implements `git` (`requires: [git]` makes tracker check git is installed and the workdir is a git repo before any LLM call). Unrecognized entries warn and continue, so workflow authors can forward-declare deps that future tracker versions will check. Closes #<filed-during-implementation>.
  > - **`--git=off|warn|require|init` CLI flag** to override per-run. Default `auto` respects the workflow's `requires:` block. `--git=init` (with mandatory `--allow-init` latch) auto-runs `git init` in the workdir, with safety refusals for `$HOME` / `/` / nested repos.
  > - **`tracker doctor` git-requires check** shows what would happen at run start for the current dir + workflow + flags.
  > - **Built-in workflows** that use git (`build_product`, `build_product_with_superspec`, `dotpowers`) now declare `requires: [git]`. Running them in a non-git directory fails in seconds with a copy-paste remediation, instead of burning hours of LLM spend before failing at the first `git commit` instruction.

- **README** — single new paragraph in the workflow-authoring section pointing at `requires:`.
- **docs/architecture/** — no new doc needed; this is a small surface. A brief mention in `docs/architecture/adapter.md` describing the `requires:` IR field, if it gets one.

## Follow-on work pointer

v0.30.0 will design the managed-commits feature in a separate spec at `docs/superpowers/specs/<date>-tracker-managed-commits-design.md`. That spec will address the items listed in "Non-goals" above. The preflight surface designed here is forward-compatible with managed-commits — the `requires:` mechanism, the `--git=` flag, and the `GitConfig` library type all extend cleanly.

The known critical issues already identified by reviewer feedback for the v0.30.0 design (so the next brainstorming pass starts ahead):

- Parallel branch goroutines will race on `.git/index.lock`; commit hook must skip inside branch goroutines and only fire at `parallel.fan_in`
- `ChildRunContext` must carry the same `ManagedGitRunner` instance to subgraph + manager_loop children so commits serialize through one point
- Resume after mid-commit crash needs HEAD-SHA capture in checkpoint + pre-resume reconciliation
- Pre-commit hooks that mutate files (formatters) need re-stage + retry; hooks that reject abort the run
- Built-in agent prompts that say "git commit" need a coexistence story (engine commits become no-ops when LLM already committed; only remove the inline instructions after one release of dual-mode stability)
- Engine commit identity should NOT override local `user.name/email`; use trailers (`Tracker-Run-ID`, `Tracker-Node-ID`) instead of `Co-Authored-By`
- Working-dir contract must be explicit: commit on `cfg.WorkingDir`, not on per-node `working_dir` overrides

## Open questions

1. **Does dippin-lang currently expose `requires:` on `*ir.Workflow`?** If yes, adapter change is trivial. If no, file a dippin-lang issue first; we may need a `dippin-lang` bump as a blocker for this PR. Investigate during plan-writing.
2. **Should `tracker workflows` (the list command) show declared `requires:` per workflow?** Probably yes — fits the "what does this workflow need" question naturally. Low cost; include in the same PR.
3. **Does the safety-latch "any parent dir has `.git`" check have any false positives?** Bare repos, submodules, worktrees — confirm during plan-writing.
4. **Issue number** to file for the `requires:` feature in tracker's tracker (and the corresponding dippin-lang issue if needed). Placeholder `#<filed-during-implementation>` in the CHANGELOG copy gets filled in when the issue is filed.
