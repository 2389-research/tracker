# Issue #272 — `writable_paths` fs-jail enforcement (tracker side)

**Status:** Design v1

**Author:** Claude (Opus 4.7) + Clint Ecker

**Date:** 2026-06-01

**Closes:** [#272](https://github.com/2389-research/tracker/issues/272). Joint release with [dippin-lang #75 / PR #83](https://github.com/2389-research/dippin-lang/pull/83) (merged on `main`, untagged).

**Dippin spec (normative contract):** `docs/superpowers/specs/2026-05-29-issue-75-writable-paths-design.md` in `2389-research/dippin-lang`. § "Required tracker enforcement contract", § "Tracker-side tests", and § "Release coordination" are the source of truth.

**Likely release:** tracker minor — new `SessionConfig` semantics. Tag first, then dippin tags v0.35.0 pinning `requires tracker ≥ <this tag>` as a safety requirement.

---

## 1. Problem

dippin issue #75 introduces a `writable_paths:` author-facing primitive — a comma-separated glob list on agent nodes and per-branch parallel branches — bounding where the agent's file-mutation tools may write. **Dippin carries and lints; tracker enforces.** Shipping the authoring surface without enforcement is a silent fail-open on a safety field (the "lint-validated runtime-no-op safety field is worse than nothing" anti-pattern from dippin #41).

The motivating incident: `2389-research/pipelines` PR #25, `redecompose_single` agent. Its prompt restricted it to writing two YAML files. Instead it wrote arbitrary Rust source, ran `cargo build`/`cargo test`, and fabricated ledger state. Prompt discipline did not hold — the agent had `Write`/`Edit`/`Bash` and used them. A structural write-scope primitive enforced at the runtime boundary would have bounded *where* the agent could write.

This spec implements the tracker-side enforcement contract for the v1 native backend. claude-code/acp backends refuse-to-start when `writable_paths` is set (per the dippin spec § Backends).

## 2. Goals

- Implement the normative tracker enforcement contract (dippin spec § Required tracker enforcement contract) end-to-end:
  1. Write containment for ALL file mutations — `Write`/`Edit`/`ApplyPatch` AND `Bash` AND any process Bash spawns — bounded to writable_paths globs, resolved against an immutable session root.
  2. Symlink-chain resolution at write time, TOCTOU-safe at the kernel boundary.
  3. Immutable anchor — `working_dir` and any `Params` key MUST NOT relocate it.
  4. Fail-closed — empty/malformed/unrecognized → refuse-to-start.
  5. Bypass defense — Params keys cannot widen the write surface.
  6. Native-backend enforcement only in v1; claude-code/acp refuse-to-start.
- Cover the spec's tracker-side red-team test suite end-to-end.
- Document residual escape classes honestly: network, reads, content-within-an-allowed-path, hardlinks, inherited FDs, `/proc/self/*` re-entry.
- Pin a dippin-lang version that includes the IR field (`@latest` pseudo-version during dev; tagged version in the release PR).
- Coordinate the release sequence: tracker tag first, then dippin v0.35.0 pinning `requires tracker ≥ <tag>`.

## 3. Non-goals

- **Network sandboxing.** `writable_paths` says nothing about network; `bash("curl -d @secret …")` and `cargo`'s crate fetches remain permitted.
- **Read containment.** Bounds writes only; an agent can still read `.env` and exfiltrate it.
- **Content within an allowed path.** An agent with `writable_paths: workspace/**` can still poison `workspace/Cargo.toml` or fabricate state in a ledger that lives under `workspace/`. Narrow globs (single sentinel files, as in the motivating sites) are where the primitive is strongest.
- **Hardlinks / inherited FDs / `/proc/self/*` re-entry beyond what Landlock provides at the chosen ABI.** Landlock ABI v3 includes `FS_REFER` (hardlinks across rulesets) and `FS_TRUNCATE`. We document these as covered. Anything ABI v3 doesn't cover (e.g., FD inheritance) we name as residual.
- **In-tracker inherit-on-empty resolution.** dippin resolves branch-level inherit-on-empty at the IR layer; tracker treats the resolved field as authoritative. Adding a tracker-side resolution would let the two layers disagree.
- **macOS/Windows enforcement.** Refuse-to-start when `writable_paths` is set on a non-Linux host. Mirrors the claude-code/acp refuse-to-start posture; matches the spec.
- **`workable_paths` (lint-only).** Validation of the glob list happens in dippin's DIP141/DIP142 at pack time. Tracker is the runtime backstop.
- **Helper-binary distribution.** No `tracker-jail` artifact; the re-exec target is tracker itself (`/proc/self/exe __jail-exec`).

## 4. Decisions

| # | Decision | Rationale |
|---|----------|-----------|
| D1 | **Mechanism: Hybrid Landlock.** Linux Landlock LSM (ABI v3, kernel 6.7+) for the Bash subprocess via `tracker __jail-exec` self-re-exec; Go-level `openat2(RESOLVE_BENEATH \| RESOLVE_NO_SYMLINKS \| RESOLVE_NO_MAGICLINKS)` against a session-root FD for in-process tools. macOS/Windows/older Linux refuse-to-start when `writable_paths` is set. | 3 of 4 reviewers (security, Go maintainer, ops) picked this over bwrap or a uniform helper binary. Kernel-level enforcement on resolved paths, process-tree inherited (Bash → cargo → rustc all bounded), zero install footprint. In-process tools are tracker-controlled code; openat2 against the anchor FD is equivalent security. |
| D2 | **Two-tier glob semantic.** In-process tools (Write/Edit/ApplyPatch) enforce the *exact* `writable_paths` globs via openat2. Bash subprocess is bounded at the *directory* ancestor of each glob's static prefix (Landlock is path-prefix on resolved paths, not glob-aware). | Landlock has no glob support. For the 5 motivating sentinel-writer sites this is identical because their globs are already directory-scoped. Document the gap honestly; an author who wants exact-file Bash bounding strips Bash from `tool_access`. |
| D3 | **Self-re-exec via `tracker __jail-exec` documented subcommand.** Not a helper binary. Argv-based transport for the writable globs (not env vars). | `landlock_restrict_self` is irreversible on the calling thread; can't apply to the parent tracker process. Go's `os/exec` has no post-fork-pre-exec hook. `runtime.LockOSThread` doesn't help under TSYNC. Self-re-exec via `/proc/self/exe` is the canonical Go ecosystem pattern (verified against go-landlock library — no subprocess helper exists). Argv beats env vars because the bash child can't `echo $TRACKER_JAIL_WRITABLE` to enumerate the jail. |
| D4 | **Landlock ABI v3 floor; no `BestEffort`.** Refuse-to-start if kernel doesn't support ABI v3. | ABI v3 (kernel 6.7+, June 2023) adds `FS_REFER` (hardlinks) and `FS_TRUNCATE`. `BestEffort()` is the opposite of refuse-to-start — it silently degrades. Strict matches the spec's "deny-all or refuse" polarity. Modern CI (GHA ubuntu-latest, Ubuntu 24.04, RHEL 9.4+) covers v3. RHEL 8 / very old kernels: refuse-to-start. |
| D5 | **Eager `probeLandlock()` at session setup.** Not lazy at first Bash call. | Pipelines run for hours. Failing at minute 240 of a `build_product` run wastes the run AND consumes restart budget. Failing at session creation costs one `prctl` call. |
| D6 | **In-process tools use `openat2(RESOLVE_BENEATH \| RESOLVE_NO_SYMLINKS \| RESOLVE_NO_MAGICLINKS)`.** Not `filepath.EvalSymlinks` + `strings.HasPrefix`. | tracker's parallel handler (`pipeline/handlers/parallel.go:162`) dispatches branch goroutines sharing the workdir. Branch A's Bash can run `while true; do ln -sf /etc/shadow ./out/x; done` while Branch B's in-process Write races the EvalSymlinks → HasPrefix → `os.WriteFile` sequence. Kernel-atomic resolution closes the race; EvalSymlinks alone doesn't. |
| D7 | **One `SessionConfig.WorkingDir` field, validated for escape.** Not a separate `Anchor` field. | Reviewers debated whether `working_dir` could relocate the anchor. The relocation attack (`working_dir: /tmp/atk`) is real (skeptic walked it through), but a validator on `working_dir` (reject absolute paths and `..`-escapes from tracker's process cwd) catches it without adding a second field. Single source of truth in the operator's mental model. |
| D8 | **No `bypassKeys` denylist.** The adapter's existing typed-wins precedence (`pipeline/dippin_adapter.go:285-287`) makes Params-key overrides of typed fields no-ops. | Verified in code: `extractAgentAttrs` writes typed fields first, then spills `Params` only for keys that don't already exist. Re-implementing the precedence as a denylist would be dead code. The defense is the adapter's existing invariant — extend it to `writable_paths` by setting `attrs["writable_paths"]` from the typed IR field *before* the Params spill. |
| D9 | **Wrap openat2 inside `LocalEnvironment.WriteFile` via optional function fields.** Not inline in `agent/tools/{write,edit,apply_patch}.go`. | Adds `CommandWrapper func(*exec.Cmd) *exec.Cmd` and `WriteOpener func(path string, perm os.FileMode) (*os.File, error)` as optional fields on `LocalEnvironment`. Codergen handler sets them when `WritablePaths` is non-empty. Tool files unchanged. The env seam already exists for this kind of cross-cutting concern. |
| D10 | **Drop the LLM-mock red-team test fixture.** Test per-tool enforcement directly. | The "agent emits write+bash+bash in one response" scenario is a delivery-mechanism test, not an enforcement test. Direct invocation (`tool.Execute(input)`) covers the same boundary with less fixture scaffolding. |
| D11 | **Add `TestParallelBranchSymlinkRace`.** Two parallel branches; A's Bash forges symlinks in a loop, B's Write races. | The verified concurrency vector. Justifies the openat2 defense (D6). Was missing from the dippin spec's test enumeration. |

## 5. Architecture

### 5.1 Mechanism summary

Two enforcement paths, both consulting the same authoritative source (`SessionConfig.WorkingDir` as the immutable session root + `SessionConfig.WritablePaths` as the glob list):

- **In-process tools** (`Write`, `Edit`, `ApplyPatch`) → `openat2(anchorFD, relPath, RESOLVE_BENEATH | RESOLVE_NO_SYMLINKS | RESOLVE_NO_MAGICLINKS)`. Kernel binds path resolution to the anchor FD; symlink chains rejected at the syscall.
- **Bash subprocess** → `wrapBashCmd(cmd)` rewrites the command to `/proc/self/exe __jail-exec -- <anchor> <glob1> <glob2> ... -- sh -c <agentCmd>`. The `__jail-exec` child applies Landlock ABI v3 then `syscall.Exec`s into `sh -c <agentCmd>`. Landlock is preserved through exec and inherited by every descendant.

### 5.2 No separate Jail struct

Per reviewer convergence: keep functions as package-level helpers in `agent/exec/`, not a `jail/` sub-package. Helpers:

- `WrapBashCmd(cmd *exec.Cmd, anchor string, writable []string) *exec.Cmd` — rewrites argv to use `/proc/self/exe __jail-exec`. Pure function, testable without exec.
- `RunJailExec(args []string) int` — invoked from `cmd/tracker/main.go` when `os.Args[1] == "__jail-exec"`. Parses argv, applies Landlock, `syscall.Exec`s into the agent command.
- `ProbeLandlock() error` — eager session-setup probe. Returns `ErrLandlockUnavailable` on non-Linux or kernel without ABI v3.
- `ValidateWritablePaths(workingDir, globs []string, processCwd string) error` — validates working_dir doesn't escape tracker's process cwd; validates globs compile and don't escape working_dir; returns refusal errors.
- `OpenForWrite(anchorFD int, relPath string, perm os.FileMode) (*os.File, error)` — the `openat2` wrapper for in-process tools.

`LocalEnvironment` gains two optional function fields:
```go
type LocalEnvironment struct {
    // ... existing fields ...
    CommandWrapper func(*exec.Cmd) *exec.Cmd        // applied in ExecCommand
    WriteOpener    func(path string, perm os.FileMode) (*os.File, error) // applied in WriteFile
}
```
Both nil by default — tools fall through to the existing `os.WriteFile` / unwrapped exec path. The codergen handler populates them when `SessionConfig.WritablePaths` is non-empty.

## 6. In-process write enforcement (Write / Edit / ApplyPatch)

When `LocalEnvironment.WriteOpener` is non-nil, `LocalEnvironment.WriteFile` delegates to it. The implementation in `jail_linux.go`:

```
OpenForWrite(anchorFD, relPath, perm):
  Use openat2(2) with flags:
    RESOLVE_BENEATH       — resolution bound to anchorFD
    RESOLVE_NO_SYMLINKS   — reject any symlink in the resolution path
    RESOLVE_NO_MAGICLINKS — reject /proc/self/* magic links
  Returns the *os.File or a typed error:
    - ErrPathEscape (kernel returned EXDEV / ELOOP)
    - ErrPathNotAllowed (after openat2, glob check fails)
```

After openat2 has done the kernel-level resolution, a glob check against the cleaned relative path enforces the *exact* writable_paths globs (the closer-than-Landlock fidelity that satisfies the contract's "every file mutation" requirement for in-process tools).

**Reads stay un-bounded** per spec § Threat model. Read/Glob/Grep tools are unchanged.

## 7. Bash subprocess enforcement (Landlock via self-re-exec)

### 7.1 `tracker __jail-exec` subcommand

Registered as a regular Go function `RunJailExec(args []string) int` in `agent/exec/jail_linux.go`. Dispatched from `cmd/tracker/main.go`:

```go
// At main() entry, BEFORE cobra initialization:
if len(os.Args) > 1 && os.Args[1] == "__jail-exec" {
    os.Exit(exec.RunJailExec(os.Args[2:]))
}
```

Documented in CLAUDE.md under "Architecture Gotchas". NOT in `cli.html` — operators never invoke it directly; the `__` prefix signals "internal." Code comment at the dispatch site explains the re-exec mechanism.

### 7.2 `WrapBashCmd` argv shape

```
/proc/self/exe __jail-exec -- <anchor-absolute-path> <glob1> <glob2> ... -- sh -c <agentCmd>
```

The `--` separators are unambiguous boundaries: arg[0] = anchor, args between the two `--`s = globs, args after the second `--` = the original Bash command.

### 7.3 Inside `RunJailExec`

```
1. Parse argv: anchor, globs, command tail.
2. For each glob:
     prefix := pattern[:strings.IndexAny(pattern, "*?[{")]  // static prefix
     dir := filepath.Dir(prefix)                            // always widen to dir
     resolved := filepath.Join(anchor, dir)
     append to rwDirs
3. landlock.V3.RestrictPaths(landlock.RWDirs(rwDirs...))
     The go-landlock library sets PR_SET_NO_NEW_PRIVS automatically.
     STRICT mode (no BestEffort) — fail if ABI v3 not available.
4. syscall.Exec("/bin/sh", []string{"sh", "-c", agentCmd}, os.Environ())
     Replaces image. Go runtime exits. Landlock preserved through exec.
     Bash + all descendants (cargo, rustc, child shells) bounded.
```

### 7.4 Two-tier semantic

Documented in `site/static/skill.md` and a code comment on the helper:

> In-process tools (Write/Edit/ApplyPatch) enforce the *exact* `writable_paths` globs. Bash subprocess enforcement is bounded to the directory ancestors of each glob (Landlock is path-prefix on resolved paths, not glob-aware). For directory-scoped globs (`workspace/**`, `.ai/sprints/**`) the two are identical. For file-scoped globs (`workspace/out.md`) Bash can write any file under `workspace/` — strip Bash from `tool_access` for exact-file Bash bounding.

## 8. Anchor + bypass defense + fail-closed

### 8.1 One field — `SessionConfig.WorkingDir`

No separate `Anchor` field. The session's `WorkingDir` is the single source of truth for both writable_paths glob resolution AND execution cwd. Set once at session construction by `CodergenHandler.buildConfig`; never mutated.

### 8.2 `ValidateWritablePaths` (one function, three rejection paths)

```go
func ValidateWritablePaths(workingDir string, globs []string, processCwd string) error {
    // 1. WorkingDir escape — catches working_dir: /tmp/atk relocation
    if filepath.IsAbs(workingDir) || !isSubpathOf(workingDir, processCwd) {
        return fmt.Errorf("working_dir %q escapes tracker's process cwd %q", workingDir, processCwd)
    }
    // 2. Glob compile + escape — catches malformed and absolute/~/.. entries
    for _, g := range globs {
        if !globCompiles(g) {
            return fmt.Errorf("writable_paths glob %q does not compile", g)
        }
        if pathEscapes(g) {
            return fmt.Errorf("writable_paths glob %q escapes the workspace", g)
        }
    }
    // 3. Empty list — dippin parser rejects present-but-empty, but backstop here
    if len(globs) == 0 {
        return fmt.Errorf("writable_paths is empty (fail-closed)")
    }
    return nil
}
```

### 8.3 Bypass defense — no denylist; lean on the adapter's typed-wins precedence

`pipeline/dippin_adapter.go extractAgentAttrs` already spills `Params` into `node.Attrs` only for keys NOT already claimed by a typed field. Set `attrs["writable_paths"]` from the typed IR field BEFORE the Params spill. The existing precedence rule makes any `params.writable_paths` a no-op.

Same protection for `working_dir`: the existing typed `working_dir` field already wins over `params.working_dir`. No change needed.

### 8.4 Three refuse-to-start gates

| # | Condition | Source |
|---|-----------|--------|
| G1 | `ValidateWritablePaths` returns error (covers empty / malformed / escape) | `configureJail` in codergen |
| G2 | Backend ∈ {claude-code, acp} when `WritablePaths` is non-empty | `configureJail` in codergen |
| G3 | `ProbeLandlock()` fails (non-Linux, kernel < 6.7, syscall denied) | `configureJail` in codergen |

All three surface as `EventNodeFailed` with a clear, operator-facing reason. The pipeline halts before the agent's LLM tokens are spent.

### 8.5 Inherit-on-empty — explicitly tracker's non-responsibility

Tracker reads `branch.WritablePaths` from the IR as authoritative. dippin resolves branch-level inherit-on-empty at the IR layer (parent if non-empty else agent). **Do not add tracker-side inherit logic** — that would let two layers disagree silently.

## 9. Wiring (touch points + dippin pin)

### 9.1 Dippin pin strategy

Per user direction during brainstorming, dev work pins dippin to `@latest` (pseudo-version resolving to the latest commit on dippin's `main`, currently the merge of PR #83 / commit `792e6e6`):

```
go get github.com/2389-research/dippin-lang@latest
```

The release PR (after dippin tags v0.35.0) bumps to the tagged version AND bumps `PinnedDippinVersion` in `tracker_doctor.go` in lockstep. Standard release pattern.

### 9.2 Touch points

| File | Change |
|---|---|
| `go.mod` | bump `github.com/2389-research/dippin-lang` to `@latest` |
| `pipeline/node_config.go` | add `WritablePaths []string` to `AgentNodeConfig` + accessor (per CLAUDE.md typed-config convention) |
| `pipeline/dippin_adapter.go` | set `attrs["writable_paths"]` from typed IR field BEFORE Params spill (line 285-287 pattern) |
| `agent/config.go` | add `WritablePaths []string` to `SessionConfig` |
| `agent/exec/local.go` | add optional `CommandWrapper` and `WriteOpener` function fields; `ExecCommand` / `WriteFile` call them if set |
| `agent/exec/jail_linux.go` (new) | `WrapBashCmd`, `RunJailExec`, `ProbeLandlock`, `ValidateWritablePaths`, `OpenForWrite`; `//go:build linux` |
| `agent/exec/jail_other.go` (new) | passthrough stubs; `//go:build !linux` |
| `pipeline/handlers/codergen.go` | one new helper `configureJail(cfg, attrs, env) (bool, error)` called from `buildConfig`; collapses G1+G2+G3 into one error path; wires env's function fields |
| `cmd/tracker/main.go` | early `if os.Args[1] == "__jail-exec"` dispatch to `exec.RunJailExec(os.Args[2:])` |
| `CLAUDE.md` | one paragraph in "Architecture Gotchas" explaining the re-exec mechanism |
| `site/static/skill.md` | one paragraph: tracker-side enforcement contract, two-tier semantic, residual escape classes |
| `CHANGELOG.md` | `[Unreleased]` entry (see § 11) |

**9 source-code touch points + 3 doc touch points.** Zero changes to `agent/tools/{write,edit,apply_patch}.go` (the env seam absorbs the jail behavior).

## 10. Testing strategy

### 10.1 Six tests, table-driven where useful

| # | Test | Covers spec items |
|---|------|---|
| 1 | `TestWritablePathsEnforcement` (table, 3 rows) | spec test 1 (red-team direct) + 2 (symlink escape) |
| 2 | `TestWorkingDirRelocationRefused` | spec test 3 |
| 3 | `TestWritablePathsFailClosed` (table, 3 rows: empty/malformed/no-landlock) | spec test 4 (minus bypass row — see D8) |
| 4 | `TestBranchEnforcesResolvedPaths` | spec test 6 (branch inherit — tracker enforces what dippin gives it) |
| 5 | `TestParallelBranchSymlinkRace` | **new** — verified concurrency vector (D11), justifies D6 |
| 6 | `TestBackendRefuseToStart` | spec test 7 |

### 10.2 Test mechanics

- **`RunJailExec` unit tests** via the `TestMain` re-exec idiom: test binary dispatches to a test-only entrypoint when `TRACKER_TEST_JAIL_EXEC=1` is set; parent test invokes `cmd := exec.Command(os.Args[0], "-test.run=TestJailHelper")` and asserts on stdout/exit. No PATH lookup, no `tracker` binary needed.
- **Landlock integration tests** use a runtime `if !landlock.V3.Available() { t.Skip(...) }` guard. **No build tags.** GHA ubuntu-latest covers the real path.
- **Parallel-branch race test** sits in `pipeline/handlers/parallel_test.go` adjacent to existing branch-dispatch tests.

### 10.3 Test discipline

- Cuts the LLM-mock red-team fixture (D10). Test enforcement paths directly via `tool.Execute(input)`.
- Cuts the Params-precedence test (lives in adapter tests already; D8).
- Adds the parallel-branch race test (D11).

## 11. CHANGELOG entry shape

Under `[Unreleased]` in `CHANGELOG.md`:

```markdown
### Added

- **`writable_paths` fs-jail enforcement** (closes #272, paired with dippin v0.35.0 / #75). Agents with `writable_paths` declared in the workflow now have a runtime write jail bounding all file mutations (Write/Edit/ApplyPatch AND Bash + descendants) to the declared globs. **Linux-only** (kernel 6.7+ for Landlock ABI v3); macOS/Windows/older Linux refuse-to-start when `writable_paths` is set. claude-code/acp backends also refuse-to-start (out-of-process backends tracker cannot sandbox). In-process tools enforce the *exact* globs via `openat2(RESOLVE_BENEATH | RESOLVE_NO_SYMLINKS)`; Bash subprocess is bounded at the *directory ancestors* of each glob's static prefix via Linux Landlock — for the 5 motivating sentinel-writer adopters these are identical because their globs are directory-scoped.

### Library-API delta

- `pipeline.AgentNodeConfig` gains `WritablePaths []string` (typed accessor).
- `agent.SessionConfig` gains `WritablePaths []string`.
- `agent/exec.LocalEnvironment` gains optional `CommandWrapper` and `WriteOpener` function fields.
- New package functions in `agent/exec/`: `WrapBashCmd`, `RunJailExec`, `ProbeLandlock`, `ValidateWritablePaths`, `OpenForWrite`. Linux-only; non-Linux builds get passthrough stubs.

### Operator notes

- A new internal subcommand `tracker __jail-exec` exists for the runtime to re-exec into a Landlock-sandboxed child for Bash invocations under writable_paths. Operators MUST NOT invoke it directly; it's documented for transparency only.
- Residual escape classes (not bounded by writable_paths): network egress (`curl`, `cargo` crate fetches), reads / exfil-by-read, content within an allowed path (the agent can still poison `workspace/Cargo.toml` if `writable_paths: workspace/**`). Narrow globs are the strongest posture.

### Requires

- `dippin-lang vX.Y.Z` (the paired v0.35.0 release with the `WritablePaths` IR field). During dev, the worktree pins `@latest` against dippin's `main`; the release PR bumps to the tagged version and bumps `PinnedDippinVersion` in lockstep. **Without a tracker ≥ this tag, dippin v0.35.0's `writable_paths` field is unenforced — the paired tracker fail-closes on the field, so an unpinned/older tracker refuses to run rather than silently ignore.**
```

## 12. Verification gates

- `go build ./...` clean
- `go test ./... -short` — all packages pass
- `go test ./agent/exec -race` — no data races on jail wiring
- `go test ./pipeline/handlers -race` — covers the parallel-branch race test
- `dippin doctor` A grade on the four core example workflows (writable_paths is opt-in; existing workflows unaffected)
- Manual smoke: a `.dip` with `writable_paths: workspace/**` on an agent, paired with `--backend native`, attempts a write to `/tmp/x` and a `bash 'echo > /etc/y'` — both denied; the audit row records the rejection.
- Manual smoke (macOS): the same `.dip` refuses to start with `ErrLandlockUnavailable` and a clear message.

## 13. Release sequence

Per dippin spec § Release coordination (corrected version):

1. **This tracker PR lands on `main`, untagged.** Dippin pin is `@latest` during dev.
2. **Tracker tag first** (e.g., `v0.36.0` — minor bump for new `SessionConfig` semantics). Bumps `go.mod` dippin pin to the tagged dippin once it lands. Tag-push triggers GoReleaser.
3. **Dippin tags v0.35.0** in parallel, pinning `requires tracker ≥ <tracker tag>` in CHANGELOG/skill.md as a safety requirement.
4. **Version-skew safety statement** in tracker's CHANGELOG operator notes (above). The paired tracker fail-closes on the field; an unpinned/older tracker refuses rather than runs unbounded.

## 14. Out of scope (deferred)

- **Read containment** (issue #54 — `read_only`) — separate feature, separate spec.
- **Tool-name allowlist** (issue #55 — `allowed_tools` / `disallowed_tools`) — composes with writable_paths but neither blocks the other.
- **Defaults cascade** (issue #53 — `defaults` over `tool_access` and `writable_paths`) — needs its own spec; inherit-on-empty already composes cleanly.
- **Chain-attack mitigation** (issue #56) — content-flow analysis; the current contract bounds *location*, not *trustworthiness of content*.
- **claude-code / acp backend support.** Both refuse-to-start when `writable_paths` is set. Future: a backend-level sandbox primitive for those backends; not in scope here.
