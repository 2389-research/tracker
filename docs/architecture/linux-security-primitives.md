# Linux security primitives (writable_paths jail)

Reference for the kernel-level primitives the `writable_paths` filesystem jail
(issue [#272](https://github.com/2389-research/tracker/issues/272), hardened
across [#275](https://github.com/2389-research/tracker/pull/275)) relies on.
The semantics below were re-derived several times during #275's review rounds
and at least one was documented wrong along the way; this page pins them in
one place for future contributors extending the jail.

This is the *primitives* reference — the mechanism-by-mechanism "what each
syscall guards and why." For the seam invariant (every agent tool must route
through `exec.ExecutionEnvironment`) and the CI lint that enforces it, see
[`agent-tool-jail-checklist.md`](./agent-tool-jail-checklist.md). For the
end-to-end design (glob semantics, two-tier enforcement, refuse-to-start
gates) see the spec under
[`../superpowers/specs/2026-06-01-issue-272-writable-paths-enforcement-design.md`](../superpowers/specs/2026-06-01-issue-272-writable-paths-enforcement-design.md).

## What the jail is (and is not)

When an agent node declares `writable_paths`, tracker bounds the node's
filesystem writes to the declared globs, rooted at the session anchor (the
resolved `working_dir`). Enforcement is **two-tier**:

- **In-process tools** (`Write`, `Edit`, `ApplyPatch`) that route through
  `LocalEnvironment.WriteOpener` / `Remover` hit `openat2(2)` against a
  session-root directory fd — exact glob semantics, no TOCTOU window.
- **The Bash subprocess and all its descendants** are bounded by the Landlock
  LSM, applied in a re-exec'd child (`tracker __jail-exec`) before it
  `syscall.Exec`s into `sh -c <cmd>`. Landlock is a directory-prefix
  mechanism, so this tier is bounded at the *directory ancestor* of each
  glob's static prefix — coarser than the in-process tier by construction.

The jail **prevents** writes and deletes outside the declared directories from
both tiers. It does **not** prevent network egress, reads, or exfiltration by
reading — and it does **not** constrain what happens *inside* an allowed path.
See [Residual escape classes](#residual-escape-classes). Language matters here:
where this page says "prevents" it means a kernel mechanism refuses the
operation; where it says "detects" or "rejects fail-closed" it means a
userspace check refuses to proceed.

Every primitive below is Linux-only. On non-Linux builds the jail refuses to
start (`ProbeLandlock` returns `ErrLandlockUnavailable`); there is no macOS or
Windows equivalent wired in.

## 1. Landlock LSM (ABI v3)

**What it guards:** the Bash subprocess tier. Once
`landlock_restrict_self(2)` is applied, the restriction is inherited across
`execve` and by all descendants, and cannot be relaxed — so the agent's shell
and everything it spawns are bounded regardless of what they exec into.

**How tracker applies it:** in `RunJailExec`
([`../../agent/exec/jail_linux.go`](../../agent/exec/jail_linux.go)):

```go
landlock.V3.RestrictPaths(
    landlock.RODirs("/"),        // read-only access to the whole FS
    landlock.RWDirs(rwDirs...),  // read-write only under the writable roots
)
```

`RODirs("/")` grants read access everywhere (so the shell binary, shared
libraries, `/etc`, etc. remain readable); each `RWDirs` entry re-grants
read-write for one writable root, overriding the read-only rule for that
subtree. The `rwDirs` are computed by `landlockDirForGlob`, which takes each
glob's static prefix (everything before the first `*?[{` metachar) and returns
its directory ancestor joined to the anchor:

| anchor | glob | Landlock RW dir |
|---|---|---|
| `/run` | `workspace/**` | `/run/workspace` |
| `/run` | `workspace/out.md` | `/run/workspace` |
| `/run` | `.ai/sprints/**` | `/run/.ai/sprints` |
| `/run` | `x.md` | `/run` |

**Directory-prefix-only — why filename enforcement is not available here.**
Landlock rules attach to *directories* (or single files); the kernel matches
by path prefix on resolved paths, not by glob. It cannot express "only
`*.md` under this dir." That is why the Bash tier grants write on the whole
directory ancestor of a glob's prefix, and why the validator refuses globs
whose metachar placement would make the two tiers disagree (e.g. `work*/**`,
where the in-process matcher compares the literal prefix `work` but Landlock
would collapse to the whole anchor — see `validateDoubleStarPlacement` in
[`../../agent/exec/jail.go`](../../agent/exec/jail.go)). Filename-level
enforcement for the subprocess tier is fundamentally a kernel gap, tracked as
follow-up [#280](https://github.com/2389-research/tracker/issues/280).

**Kernel / version requirements:** Landlock ABI **v3**, first available in
Linux **6.7** (June 2023 kernel line; ABI v3 landed there). `ProbeLandlock`
([`jail_linux.go`](../../agent/exec/jail_linux.go)) verifies this eagerly at
session setup with a non-destructive probe:
`landlock_create_ruleset(NULL, 0, LANDLOCK_CREATE_RULESET_VERSION)` returns the
highest supported ABI as its return value (no ruleset fd is created, no side
effect on the caller). A result `< 3`, or any errno, yields
`ErrLandlockUnavailable`
([`jail_errors.go`](../../agent/exec/jail_errors.go)) and the session refuses
to start. The probe is **strict — no `BestEffort` fallback**: ABI v3 is
required because the jail's contract needs `LANDLOCK_ACCESS_FS_REFER`
(hardlink/rename across the ruleset) and `LANDLOCK_ACCESS_FS_TRUNCATE`, both of
which are ABI-v3 additions.

## 2. `openat2(2)` RESOLVE_* flags

**What they guard:** the in-process tool tier. `OpenForWrite`, `SafeMkdirAll`,
and `SafeRemove` (all in
[`jail_linux.go`](../../agent/exec/jail_linux.go)) resolve every path relative
to an `O_PATH | O_DIRECTORY` fd on the anchor, using `openat2` with **all
three** of these `Resolve` flags:

```go
Resolve: unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS
```

Each is necessary; they are not redundant:

- **`RESOLVE_BENEATH`** — the resolution must stay at or below the anchor
  dirfd. Any component that would ascend above it (`..` past the anchor, or an
  absolute reattachment) fails with `EXDEV`. This is the containment
  guarantee: the kernel binds resolution to the anchor, so there is no
  userspace check-then-open window an attacker can race.
- **`RESOLVE_NO_SYMLINKS`** — no symlink component is followed at all. Without
  it, an agent could pre-create `workspace` as a symlink to `/etc` and the
  write would land outside the anchor even though the *string* path looked
  contained. A symlink component fails with `ELOOP`. `RESOLVE_BENEATH` alone
  is not enough — a symlink can point to another same-anchor-looking subtree,
  or be swapped in a race; refusing symlinks outright closes the class.
- **`RESOLVE_NO_MAGICLINKS`** — a strict subset that `RESOLVE_NO_SYMLINKS`
  already implies, but stated explicitly for the two destructive helpers:
  procfs "magic links" (`/proc/self/fd/N`, `/proc/PID/root`, etc.) resolve to
  arbitrary open files / mount roots on traversal. Blocking them closes the
  `/proc`-reattachment escape. (This flag was missing from `SafeMkdirAll` /
  `SafeRemove` until #275 round 8 — treating the flag set as a copy-paste
  recipe rather than reasoning about each member is exactly the mistake this
  page exists to prevent.)

**Absolute paths are rejected before the syscall.** `OpenForWrite` returns
`ErrPathEscape` for an absolute `relPath` up front, because `RESOLVE_BENEATH`
governs *relative* resolution after the anchor fd — an absolute path resolves
to itself regardless of the anchor
([`jail_linux.go`](../../agent/exec/jail_linux.go)).

**Kernel requirement:** `openat2(2)` requires Linux **5.6+**. In practice the
Landlock ABI-v3 floor (6.7) dominates, so any host that passes `ProbeLandlock`
already has `openat2`.

## 3. `EACCES` vs `EXDEV` vs `ELOOP`

When `openat2(RESOLVE_BENEATH | RESOLVE_NO_SYMLINKS | RESOLVE_NO_MAGICLINKS)`
fails, the errno determines whether tracker treats it as an *escape* or an
*ordinary* error. Getting this wrong (misclassifying `EACCES` as an escape) was
a real bug fixed in #275 round 12.

| errno | Meaning | Tracker treatment |
|---|---|---|
| `EXDEV` | Resolution would leave the anchor (`RESOLVE_BENEATH` violated) | `ErrPathEscape` — the escape signal |
| `ELOOP` | A symlink / magic-link component was refused (`RESOLVE_NO_SYMLINKS` / `RESOLVE_NO_MAGICLINKS`) | `ErrPathEscape` — the escape signal |
| `EACCES` | Ordinary permission / mode / ownership denial | **Plain error, not an escape** — propagated as-is |

`OpenForWrite`, `SafeMkdirAll`, and `SafeRemove` all `switch` only on `EXDEV`
and `ELOOP` to wrap `ErrPathEscape`; everything else (including `EACCES` and
`ENOENT`) falls through to a plain wrapped error
([`jail_linux.go`](../../agent/exec/jail_linux.go)). `EACCES` is a routine
filesystem permission result and says nothing about a resolve-time escape
attempt; claiming otherwise would produce false "escape" alarms on ordinary
`0o000` files or root-owned paths.

`SafeMkdirAll` additionally treats `ENOENT` specially — a missing component is
created with `mkdirat`, then re-opened with the same `RESOLVE_NO_SYMLINKS`
flags so a symlink planted by a racing creator between the `ENOENT` and the
`mkdirat` is still rejected rather than followed
([`jail_linux.go`](../../agent/exec/jail_linux.go)).

## 4. `mkdirat` / `unlinkat` against an `openat2` dirfd

**What it guards:** the create/delete operations of the in-process tier,
against symlink races on intermediate components.

`SafeMkdirAll` and `SafeRemove`
([`jail_linux.go`](../../agent/exec/jail_linux.go)) never operate on a full
string path. They walk it component-by-component:

- **`SafeMkdirAll`** opens each existing component with `openat2` (the flags
  above) to advance a `parentFD`, and for a missing component calls
  `mkdirat(parentFD, comp, perm)` then re-opens it via `openat2`. Because the
  create and the subsequent open are both anchored to the already-resolved
  parent dirfd and both refuse symlinks, there is no window where a symlink
  swapped in at an intermediate path can redirect the creation outside the
  anchor.
- **`SafeRemove`** resolves the *parent directory* via `openat2` (symlink-free,
  beneath the anchor), then `unlinkat(parentFD, name, 0)` on the final
  component. It refuses an empty / `.` / `/` leaf up front.

This is the atomic-against-symlink-race pattern: resolution and mutation share
the same kernel-checked dirfd, so an agent-placed symlink cannot open a
check-then-act gap. These helpers exist because the earlier code path used
`os.MkdirAll` / `os.Remove` inside the `WriteOpener` closure, which would
follow an agent-placed symlink before `openat2` ever saw the leaf (#275 round
8; see the comments in
[`../../pipeline/handlers/codergen_jail.go`](../../pipeline/handlers/codergen_jail.go)).

## 5. `PR_SET_PDEATHSIG` (parent-death signal)

**What it guards:** orphan accumulation — not the filesystem boundary. This is
process-lifecycle hygiene, listed here because it is a Linux-specific
primitive the jail's subprocess machinery relies on.

`applyParentDeathSig`
([`../../agent/exec/parent_death_linux.go`](../../agent/exec/parent_death_linux.go))
sets `cmd.SysProcAttr.Pdeathsig = syscall.SIGKILL`, so the kernel SIGKILLs the
spawned child when its parent dies by any cause (panic, `SIGKILL`, interrupted
run). Without it, orphaned `__jail-exec` children reparent to init and keep
running; during #272 development this fork-bombed a dev box (132 live orphans,
load average 74 on 4 cores). `Pdeathsig` **persists across `execve`**, so it
survives `RunJailExec`'s `syscall.Exec` into `sh -c` and is inherited by the
agent's bash and all descendants. It is paired with `Setpgid` + `cmd.Cancel`
(process-group kill on timeout/cancel) in
[`../../agent/exec/local.go`](../../agent/exec/local.go) for layered defense.

**Kernel-version semantics (documented wrong once, corrected in #275 round 8):**
modern Linux delivers `PR_SET_PDEATHSIG` on parent **process** exit. An earlier
`parent_death_linux.go` comment (citing an outdated web result) claimed the
signal is scoped to the exit of the individual parent *thread*; that was wrong
for current kernels. The pre-2.6.27 kernel behavior *was* thread-scoped, but
tracker sets no minimum kernel beyond the Landlock 6.7 floor, so it does not
rely on either interpretation.

`tracker` still pins the spawning goroutine to its OS thread for the duration
of the spawn via `runtime.LockOSThread` (`pinCallingThreadForParentDeath` in
[`parent_death_linux.go`](../../agent/exec/parent_death_linux.go), called from
[`local.go`](../../agent/exec/local.go)). This is **defense in depth, not a
correctness requirement** under modern process-scoped semantics: it guards the
off chance of a thread-scoped kernel and gives the fork-exec a stable thread
identity. On non-Linux builds both helpers are no-ops
([`parent_death_other.go`](../../agent/exec/parent_death_other.go)).

## The `/proc/self/exe __jail-exec` re-exec pattern

Landlock must be applied to the process that will run the agent's shell, but
tracker itself needs unrestricted filesystem access. The jail resolves this by
re-execing tracker into a throwaway child that sandboxes *itself* and then
becomes the shell:

1. `WrapBashCmd`
   ([`jail_linux.go`](../../agent/exec/jail_linux.go)) rewrites the command's
   argv to
   `/proc/self/exe __jail-exec -- <anchor> <glob1..globN> -- <origArgs...>`.
   The two `--` separators are unambiguous boundaries. All other `*exec.Cmd`
   fields (`Dir`, `Env`, stdio, `SysProcAttr` including `Pdeathsig`/`Setpgid`)
   are preserved by in-place mutation.
2. `cmd/tracker/main.go` dispatches `__jail-exec` **before flag parsing** — the
   very first thing `main` does after argv inspection — and hands
   `os.Args[2:]` to `RunJailExec`
   ([`../../cmd/tracker/main.go`](../../cmd/tracker/main.go)). Early dispatch
   keeps `flag.Parse` from touching this argv and keeps the child's job
   minimal.
3. `RunJailExec` ([`jail_linux.go`](../../agent/exec/jail_linux.go)) parses the
   argv, pre-creates each writable root with `SafeMkdirAll` (Landlock refuses
   to add a rule for a non-existent dir, and once the ruleset is live the
   jailed shell can't create it under the read-only parent), `LookPath`s the
   command *before* Landlock is applied (so PATH resolution still has full
   FS access; `syscall.Exec` does no PATH lookup of its own), applies
   `landlock.V3.RestrictPaths`, then `syscall.Exec`s into the command tail.
   Landlock survives the exec.

Exit codes from the child: `2` = argv parse failure, `3` =
`landlock_restrict_self` failure, `4` = pre-create / PATH-resolve / exec
failure.

> Operators MUST NOT invoke `__jail-exec` directly. The `__` prefix is the
> "internal" signal; it exists only for tracker to re-exec itself.

## Refuse-to-start gates

The jail fails closed at session setup. `configureJail` and
`refuseWritablePathsOnUnsupportedBackend`
([`../../pipeline/handlers/codergen_jail.go`](../../pipeline/handlers/codergen_jail.go))
gate three ways before any agent token is spent:

- **G1 — bad paths.** `ValidateWritablePaths`
  ([`../../agent/exec/jail.go`](../../agent/exec/jail.go)) rejects a
  `working_dir` that escapes tracker's process cwd (string check *and*
  symlink-aware re-check against the deepest existing ancestor), an empty glob
  list, and malformed globs (absolute, `~`, any `..` segment, brace expansion,
  unsupported `**` placement, metachars before `**`, malformed character
  classes). Escape-flavored failures wrap `ErrPathEscape`; bad-shape failures
  are plain errors.
- **G2 — unsupported backend.** `writable_paths` is refused on `claude-code`
  and `acp` (they run out-of-process; tracker cannot apply Landlock to them)
  and on any unknown backend (fail-closed). The check runs both at the
  dispatcher (`refuseWritablePathsOnUnsupportedBackend`, by type assertion
  against `*NativeBackend`) and inside `configureJail` (by `cfg.Backend`
  string), because the non-native backends drop the `SessionConfig` signal
  before `configureJail` would ever run.
- **G3 — Landlock unavailable.** `ProbeLandlock` fails (non-Linux, kernel
  < 6.7, or the syscall is denied). Yields `ErrLandlockUnavailable`.

## Residual escape classes

The jail bounds *where writes and deletes land*. It does **not** bound
everything an agent can do. These classes are explicitly out of scope
(consistent with the CLAUDE.md "Tool node safety" / `__jail-exec` gotcha):

- **Network egress.** Landlock ABI v3 has no network rules; the jailed shell
  can open sockets and exfiltrate freely. (Later Landlock ABIs add TCP
  restrictions, but tracker targets v3 and does not use them.)
- **Reads / exfiltration by reading.** `RODirs("/")` grants read of the entire
  filesystem to the subprocess tier so the shell and its libraries work; the
  agent can read any file it has DAC permission for and ship the contents out
  over the network.
- **Anything inside an allowed path.** Within a declared writable directory the
  agent has full read-write. Overwriting files it was legitimately handed,
  planting scripts, filling the disk — none of that is constrained. The
  narrower the globs, the smaller this surface; **narrow globs are the
  strongest posture.**

None of these are bugs to be "fixed" in the jail — they are the deliberate
boundary of a *filesystem-write* sandbox. Do not describe the jail as
preventing them.

## Note on seccomp

Tracker does **not** install a seccomp-bpf syscall filter for the jail. The
containment model is Landlock (subprocess tier) plus `openat2` resolve flags
(in-process tier), not syscall filtering. If a future change adds one, document
it here as a new primitive.

## Primitive → guarantee summary

| Primitive | Tier | Guards against | Fail mode |
|---|---|---|---|
| Landlock ABI v3 (`RestrictPaths`) | Bash subprocess + descendants | Writes/deletes outside the writable directory ancestors | Refuse-to-start if ABI < 3 |
| `openat2` `RESOLVE_BENEATH` | In-process tools | Path ascending above the anchor | `EXDEV` → `ErrPathEscape` |
| `openat2` `RESOLVE_NO_SYMLINKS` | In-process tools | Symlink-component redirect | `ELOOP` → `ErrPathEscape` |
| `openat2` `RESOLVE_NO_MAGICLINKS` | In-process tools | `/proc` magic-link reattachment | `ELOOP` → `ErrPathEscape` |
| `mkdirat`/`unlinkat` on `openat2` dirfd | In-process tools | Symlink race on intermediate components | `ErrPathEscape` on a raced symlink |
| `PR_SET_PDEATHSIG` | Subprocess lifecycle | Orphan `__jail-exec` accumulation | SIGKILL on parent death |
| Refuse-to-start gates (G1/G2/G3) | Session setup | Bad paths, unsupported backend, no Landlock | Session never starts |
