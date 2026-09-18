# `writable_paths_mode: prefer` — degrade-to-unjailed with a loud, recorded warning (#648)

**Status:** frozen contract, implemented; amended after security review rounds 2–3 (C6 tier + per-component no-symlink walk, C1 branch keys, §6/§7 additions, CI Landlock job) (written before implementation, per
[`security-pr-process.md`](../../architecture/security-pr-process.md) — this
change touches the jail boundary: `pipeline/handlers/codergen_jail.go`,
`backend_native.go`, the `codergen.go` refusal site).

Refs #272 (jail), #275 (out-of-process hole), #349 (FinalCommit scope guard),
#642 (refusal is a non-retryable routable fail), #648 (this).

## 1. Problem

`writable_paths` is fail-closed by design: on a host without Landlock ABI v3
(macOS, Linux < 6.2), on a non-native backend, or with malformed globs, the
node refuses to start. #642 made that refusal a routable `OutcomeFail`, but the
consequence for a cross-platform built-in is that a hardened node can never
*run* where the jail is unavailable — so `build_product.dip`'s `FinalCommit`
had to drop its #349 mechanical scope guard entirely (83a7c8b), losing it on
Linux too, where it works.

## 2. What this adds, in one sentence

A second enforcement mode, `writable_paths_mode: prefer`, under which a
**host-capability** refusal (and only that class) degrades the node to
**unjailed Bash** with a distinct event, a warning line, a diagnose suggestion,
a doctor warning, a run-manifest record and a trace marker — while authoring
and backend refusals still refuse exactly as today. `require` stays the
default and is byte-for-byte unchanged.

## 3. Security trade-off, stated plainly

`prefer` turns a *guarantee* into *best-effort*. On a host without Landlock the
node's **Bash subprocess has exactly the write reach it had before #272** — the
mode only makes the degradation loud and recorded. An author choosing `prefer`
accepts that the mechanical guard may be absent on some runs and that the
prompt / `commit_only` backstop is what remains. It also creates a mixed-fleet
asymmetry (Linux CI enforces, macOS dev doesn't), so a scope escape reproduces
only on the unjailed platform. **Operator copy must never describe a `prefer`
node as sandboxed.**

## 4. Frozen public API

| Surface | Shape | Notes |
|---|---|---|
| `.dip` attr | `writable_paths_mode: require \| prefer` (typed agent field since dippin-lang v0.75.0 / dippin-lang#307; the agent `params:` passthrough is still accepted) | default `require`; any other value is a load error naming the node |
| `pipeline.AgentNodeConfig.WritablePathsMode string` | `"require"` (absent/empty) or `"prefer"` | typed accessor via `Node.AgentConfig` |
| `pipeline.WritablePathsModeRequire` / `WritablePathsModePrefer` | string consts | the only two legal values |
| `pipeline.ValidateWritablePathsMode(raw string) error` | exact-match validator | used by the adapter and `validateGraph` |
| `agent.SessionConfig.WritablePathsMode string` | carried like `Backend` | consumed by `configureJail` |
| `pipeline.EventJailDegraded` = `"jail_degraded"` | `PipelineEvent.Jail *JailDegradedDetail` | one per node execution attempt that degraded |
| `pipeline.JailDegradedDetail{Mode, Reason, DeclaredGlobs}` | payload | `Reason` is the G3 probe error text |
| `pipeline.SessionStats.Jail string` (`json:"jail,omitempty"`) | `"degraded"` on the trace entry of a degraded node | absent otherwise (so `require` traces are unchanged) |
| `pipeline.RunManifest.JailDegradedNodes []string`, `NodeSummary.Jail string` | run.json | derived from `jail_degraded` log lines |
| activity.jsonl / `--json` fields | `jail_mode`, `jail_reason`, `jail_declared_globs` | additive, omitempty |
| `tracker.SuggestionJailDegraded` | diagnose suggestion kind | one per degraded node |
| `tracker doctor <pipeline>` | warning per `prefer` node when `ProbeLandlock` fails on this host | "will run UNJAILED here" |
| TUI | `MsgNodeWarning{NodeID, Message}` from `EventJailDegraded` | rendered as a `⚠ WARNING:` activity-log line |
| Native backend agent event | `agent.Event{Type: "jail_degraded", Text: reason}` emitted via `emit` **before** the session starts | internal transport from `NativeBackend.Run` to `CodergenHandler`; the handler converts it to the pipeline event and does not forward it |

## 5. Invariants (tests reference these IDs)

| ID | Invariant | Class | Proof |
|---|---|---|---|
| C1 | `writable_paths_mode` accepts exactly `require` and `prefer`. Absent ⇒ `require`. `Prefer`, `prefer ` (trailing space), `""` (present-but-empty), `preferred`, etc. ⇒ load-time error naming the node (adapter + `validateGraph`). The same check covers a parallel node's per-branch `branch.<n>.writable_paths_mode` override (the parallel handler copies `branch.<n>.*` onto the target's attrs, so an unvalidated bogus value there would silently read as `require`); a branch without an override inherits the target's mode. | fail-closed parse | `TestWritablePathsMode_C1_*` (rapid over random strings), adapter test, `TestWritablePathsMode_C1_BranchOverrideValidated` |
| C2 | `require` behaviour is byte-identical to pre-#648: same three gates, same order, same error strings, same `jailRefusedError` typing. | no regression | every pre-existing jail test is unchanged and still passes |
| C3 | `prefer` + all gates pass ⇒ jailed exactly as `require`: `CommandWrapper`, `WriteOpener`, `Remover` installed with the same closures; **no** `jail_degraded` event. | equivalence | `TestConfigureJail_C3_PreferJailsWhenLandlockAvailable` (Linux-only, skips elsewhere) |
| C4 | Refusal classes. G1 `ValidateWritablePaths` failure (bad `working_dir`, malformed globs, empty list) = **AUTHORING** → refuses in both modes. G2 backend ∈ {claude-code, acp, unknown} (both `configureJail` G2 and the dispatcher-layer `refuseWritablePathsOnUnsupportedBackend`) and a non-`*LocalEnvironment` env = **BACKEND** → refuses in both modes. G3 `ProbeLandlock` failure = **HOST-CAPABILITY** → refuses under `require`, degrades under `prefer`. | classification | `TestConfigureJail_C4_PreferStillRefusesAuthoring_Rapid`, `TestConfigureJail_C4_PreferStillRefusesBackend_Rapid`, `TestRefuseWritablePathsOnUnsupportedBackend_C4_PreferMatrix` |
| C5 | Degrade ⇒ Bash subprocess runs **unjailed** (no `CommandWrapper`); exactly one `EventJailDegraded{node, reason, declared_globs}` per node execution attempt, emitted **before** the session's first turn; the CLI/TUI warning line names the node and says UNJAILED. | observability | `TestNativeBackend_C5_PreferDegradesOnHostWithoutLandlock` (non-Linux or Landlock-less host), `TestCodergen_C5_JailDegradedEventOnce` |
| C6 | Degrade ⇒ in-process `Write`/`Edit`/`ApplyPatch` remain **policy-bounded** to the declared globs: `WriteOpener`/`Remover` are installed with the same `relPathForJail` + `matchWritablePath` check, backed by the **strongest available symlink-safe resolver** — the enforced tier's `openat2` closures when the kernel has openat2 but not Landlock v3 (Linux 5.6–6.1, `execpkg.ProbeOpenat2`), else `os.Root` (per-component resolution beneath the anchor; macOS, Linux < 5.6). A write/delete outside the globs is refused with `ErrPathNotAllowed`; a path that a pre-planted symlink (intermediate dir or leaf) would redirect outside the anchor is refused with `ErrPathEscape` and nothing lands outside. The tier used is recorded on `jail_reason` (`; in-process tier: openat2|os.Root`). | best-effort tier | `TestConfigureJail_C6_DegradedInProcessWritesStayBounded`, `TestConfigureJail_C6_DegradedRefusesSymlinkEscapes` (both run on macOS) |
| C7 | Degrade is **recorded** on every surface: activity.jsonl line `type=jail_degraded` with `jail_mode`/`jail_reason`/`jail_declared_globs`; `--json` StreamEvent same; `run.json` `jail_degraded_nodes` + `nodes[].jail="degraded"`; trace entry `stats.jail="degraded"`; `tracker diagnose` → `SuggestionJailDegraded`; `tracker doctor <pipeline>` warns per `prefer` node on a Landlock-less host. | audit trail | `TestRunManifest_C7_JailDegradedNodes`, `TestDiagnose_C7_JailDegradedSuggestion`, `TestDoctor_C7_PreferWarnsWithoutLandlock`, `TestRun_C7_PreferDegradesEndToEnd` (library e2e: run + log + run.json + trace), `TestAdaptPipelineEvent_JailDegraded` / `TestAgentLog_NodeWarningLine` (TUI), wire-shape test |
| C8 | Operator copy never calls a `prefer` node "sandboxed"/"jailed": every degrade message contains the literal `UNJAILED`; CHANGELOG/CLAUDE.md/docs carry the §3 trade-off. | copy | grep-pinned in `TestJailDegradedCopy_C8` |
| C9 | No code path degrades unless `mode == "prefer"` exactly. The degrade branch is gated on a positive comparison with `WritablePathsModePrefer`; `""`, `"require"`, and any other string take the refuse path. | fail-closed default | `TestConfigureJail_C9_OnlyExactPreferDegrades_Rapid` |
| C10 | Gate order G1 → G2 → G3 is preserved (G3 now runs after the pure `path.Clean` glob-normalization loop and anchor resolution, which have no side effects — behaviorally identical to before), so a `prefer` node with a malformed glob on a Landlock-less host **refuses** (authoring error precedes the host check) and never emits `jail_degraded`. | ordering | `TestConfigureJail_C10_AuthoringBeforeHost` |
| C11 | `refuseWritablePathsOnUnsupportedBackend` (dispatcher layer, by backend *type*) is mode-agnostic — the #275 hole stays closed under `prefer`. | #275 | `TestRefuseWritablePathsOnUnsupportedBackend_C4_PreferMatrix` |

## 6. What degrades vs what never degrades

| Component | `require` (default) | `prefer` on a Landlock host | `prefer` on a Landlock-less host |
|---|---|---|---|
| Bash + descendants (Landlock via `__jail-exec`) | enforced | enforced | **UNJAILED** (pre-#272 reach) |
| In-process `Write`/`Edit`/`ApplyPatch`/`generate_code`/`write_enriched_sprint` | glob policy + `openat2 RESOLVE_BENEATH` | same | glob policy + strongest available symlink-safe resolver (see row below) |
| `apply_patch` delete/move (`Remover`) | glob policy + `openat2` unlinkat | same | glob policy + `openat2` unlinkat or `os.Root.Remove` |
| Malformed glob / bad `working_dir` / empty list (G1) | refuse | refuse | **refuse** |
| `backend: claude-code` / `acp` / unknown (G2 + dispatcher) | refuse | refuse | **refuse** |
| Non-`*LocalEnvironment` exec env | refuse | refuse | **refuse** |
| G3 probe blocked by seccomp (`landlock_create_ruleset` → EPERM/ENOSYS) | refuse | refuse | **degrade** — the probe failing for *any* reason is host-capability class; the run records the probe error verbatim. *Not testable here — inferred from the C9 code path: `landlockUnavailable` keys only on the mode, never on the probe error's kind (the Blacksmith CI runner's ENOSYS is one live instance).* |
| Post-probe Landlock failure (`__jail-exec`: `landlock_restrict_self` blocked by seccomp / `RestrictPaths` error → exit 3, exec failure → exit 4) | **hard error** (the wrapped Bash command fails with that exit; never a degrade) | same | n/a — `CommandWrapper` is not installed, `__jail-exec` never runs. *Not testable here — inferred from the code path: the prefer branch exists only in `landlockUnavailable` (probe time); `RunJailExec` has no mode input and no degrade path.* |
| In-process resolver | `openat2` | `openat2` | openat2 where the kernel has it (Linux 5.6–6.1), else `os.Root` per-component resolution (macOS, Linux < 5.6) |
| `jail_degraded` event / warning / manifest / diagnose | never | never | once per attempt |

## 7. Threat-model delta and residual risks

- **New residual (by design):** on a Landlock-less host, a `prefer` node's Bash
  subprocess can write anywhere the tracker UID can. This is exactly the
  pre-#272 posture, now *declared and recorded* rather than silent. The
  `commit_only` prompt/system-prompt guard is the only remaining defence for
  Bash on that host.
- **Kept:** the #275 out-of-process hole stays closed (C11); authoring errors
  stay fail-closed (C4/C10); the in-process tier stays bounded (C6).
- **Degraded in-process tier resolver (review rounds 2–3):** the glob policy
  is evaluated on the lexical (`safePath`-cleaned) path, then the write/delete
  is performed by the strongest resolver the host has — the enforced tier's
  `openat2` closures on Linux 5.6–6.1, `os.Root` elsewhere. The `os.Root`
  tier additionally `Lstat`s every component of the path (all prefixes and,
  for writes, the leaf) and refuses a symlink at any of them
  (`rootRefuseSymlinks`), mirroring openat2's `RESOLVE_NO_SYMLINKS`: an
  absolute link out of the anchor is kernel-refused by `os.Root`, and a
  RELATIVE in-anchor link (`ok -> .`, `ok/leaf -> ../secret/t`) — which
  `os.Root` alone would follow, letting a glob-approved path land under a
  directory the glob never named — is refused by the walk
  (`TestConfigureJail_C6_DegradedRefusesSymlinkEscapes`,
  `TestConfigureJail_C6_DegradedRefusesInAnchorRelativeLinks`, both run on
  macOS). **Precise remaining residual of the `os.Root` tier:** a TOCTOU
  window between the `Lstat` walk and the `MkdirAll`/`OpenFile`/`Remove` —
  a same-UID process that races a symlink into a checked component after the
  walk can redirect the operation *within the anchor* (an out-of-anchor
  target is still refused by `os.Root`'s kernel-level resolution regardless
  of timing). The `openat2` tier has no such window (the kernel checks the
  chain atomically). Same-UID is already the accepted residual of the whole
  design (activity-log threat model). The tier used is recorded on the wire
  (`jail_reason` suffix `in-process tier: openat2|os.Root`).
- **Post-probe Landlock failure is never a degrade.** `prefer` only changes
  the disposition of the G3 *probe*. If the probe passes but `__jail-exec`
  later fails to apply the ruleset (`landlock_restrict_self` blocked by
  seccomp, `RestrictPaths` error) or to exec, the wrapped Bash command fails
  hard with `__jail-exec`'s exit code (3 / 4) in both modes — the node never
  silently continues unjailed. A probe that is itself blocked by seccomp is
  host-capability class and degrades under `prefer` (with the EPERM/ENOSYS
  text recorded).
- **`.git/**` includes `.git/hooks/` (both modes; FinalCommit).** The #349
  glob set lets a commit-only node write `.git/hooks/*` and `.git/config`
  (`core.hooksPath`), i.e. persist code that runs on the operator's next
  `git` invocation — inside the allowed path, so neither Landlock nor the
  in-process tier bounds it. This is the "anything inside an allowed path"
  residual made concrete. Follow-up (not this change): narrow FinalCommit to
  the objects/refs/index surface a commit actually needs — e.g.
  `.git/objects/**, .git/refs/**, .git/HEAD, .git/index, .git/logs/**,
  .git/COMMIT_EDITMSG, .git/ORIG_HEAD` (+ `.ai/**`) — after verifying
  `git add`/`git commit` need nothing else (lock files are created beside
  their targets, so `.git/index.lock` / `.git/HEAD.lock` must be covered too;
  `.git/*.lock` or listing them explicitly).
- **Mixed fleet:** Linux CI enforces, macOS dev does not. A scope escape
  reproduces only on the unjailed platform. Recorded in the manifest so a
  reviewer can tell which runs were unjailed.
- **Not covered (unchanged from #272):** network egress, reads/exfil-by-read,
  anything inside an allowed glob.

## 8. Audit-class sweep (`agent-tool-jail-checklist.md`)

| Class | Status for this PR |
|---|---|
| Every `agent/tools/` tool routes through `ExecutionEnvironment` | **not touched** — no tool code changes; `make tools-jail-check` still gates |
| `env==nil` fallback invariant (`generate_code`, `write_enriched_sprint`) | **addressed** — under degrade, `env` is still the fresh jailed `*LocalEnvironment` (non-nil) with `WriteOpener` installed, so both tools take the policy-bounded branch, never the `os.*` fallback |
| `__jail-exec` re-exec path | **not touched** — `CommandWrapper` is simply not installed under degrade (`WrapBashCmd` on a non-Linux build is already a passthrough; on a Linux < 6.2 host it must not be installed because `__jail-exec` would fail at ruleset creation) |
| Refuse-to-start gates G1/G2/G3 | **addressed** — same code, same order; only G3's *disposition* changes under `prefer` |
| Dispatcher-layer backend refusal (#275) | **addressed** — mode-agnostic (C11) |
| Out-of-process backends | **addressed** — still refused (C4) |
| Activity-log integrity | **not touched** — new event rides the existing sentinel-prefixed writer |
| Reads / network egress | **not applicable** — unchanged residuals |

## 9. Non-goals

- ~~No dippin-side lint yet (`writable_paths_mode` arrives via `params:`); a
  typed field is requested upstream.~~ Shipped: dippin-lang v0.75.0 (#307)
  types the field and lints it (DIP163–165); tracker adopted it in the
  v0.75.0 pin.
- No per-tool granularity (e.g. "prefer for Bash, require for Write") — a
  single mode per node.
- No attempt to jail out-of-process backends.
