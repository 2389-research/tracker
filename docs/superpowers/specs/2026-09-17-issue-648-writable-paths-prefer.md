# `writable_paths_mode: prefer` — degrade-to-unjailed with a loud, recorded warning (#648)

**Status:** frozen contract (written before implementation, per
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
| `.dip` attr | `writable_paths_mode: require \| prefer` (agent `params:` passthrough until dippin grows a typed field) | default `require`; any other value is a load error naming the node |
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
| C1 | `writable_paths_mode` accepts exactly `require` and `prefer`. Absent ⇒ `require`. `Prefer`, `prefer ` (trailing space), `""` (present-but-empty), `preferred`, etc. ⇒ load-time error naming the node (adapter + `validateGraph`). | fail-closed parse | `TestWritablePathsMode_C1_*` (rapid over random strings), adapter test |
| C2 | `require` behaviour is byte-identical to pre-#648: same three gates, same order, same error strings, same `jailRefusedError` typing. | no regression | every pre-existing jail test is unchanged and still passes |
| C3 | `prefer` + all gates pass ⇒ jailed exactly as `require`: `CommandWrapper`, `WriteOpener`, `Remover` installed with the same closures; **no** `jail_degraded` event. | equivalence | `TestConfigureJail_C3_PreferJailsWhenLandlockAvailable` (Linux-only, skips elsewhere) |
| C4 | Refusal classes. G1 `ValidateWritablePaths` failure (bad `working_dir`, malformed globs, empty list) = **AUTHORING** → refuses in both modes. G2 backend ∈ {claude-code, acp, unknown} (both `configureJail` G2 and the dispatcher-layer `refuseWritablePathsOnUnsupportedBackend`) and a non-`*LocalEnvironment` env = **BACKEND** → refuses in both modes. G3 `ProbeLandlock` failure = **HOST-CAPABILITY** → refuses under `require`, degrades under `prefer`. | classification | `TestConfigureJail_C4_PreferStillRefusesAuthoring_Rapid`, `TestConfigureJail_C4_PreferStillRefusesBackend_Rapid`, `TestRefuseWritablePathsOnUnsupportedBackend_C4_PreferMatrix` |
| C5 | Degrade ⇒ Bash subprocess runs **unjailed** (no `CommandWrapper`); exactly one `EventJailDegraded{node, reason, declared_globs}` per node execution attempt, emitted **before** the session's first turn; the CLI/TUI warning line names the node and says UNJAILED. | observability | `TestNativeBackend_C5_PreferDegradesOnHostWithoutLandlock` (non-Linux or Landlock-less host), `TestCodergen_C5_JailDegradedEventOnce` |
| C6 | Degrade ⇒ in-process `Write`/`Edit`/`ApplyPatch` remain **policy-bounded** to the declared globs: `WriteOpener`/`Remover` are installed with the same `relPathForJail` + `matchWritablePath` check (best-effort tier). A write outside the globs is refused with `ErrPathNotAllowed`/`ErrPathEscape`. Residual: without `openat2` there is no kernel symlink-race defence (spec #272 D6) in degraded mode — lexical containment only. | best-effort tier | `TestConfigureJail_C6_DegradedInProcessWritesStayBounded` |
| C7 | Degrade is **recorded** on every surface: activity.jsonl line `type=jail_degraded` with `jail_mode`/`jail_reason`/`jail_declared_globs`; `--json` StreamEvent same; `run.json` `jail_degraded_nodes` + `nodes[].jail="degraded"`; trace entry `stats.jail="degraded"`; `tracker diagnose` → `SuggestionJailDegraded`; `tracker doctor <pipeline>` warns per `prefer` node on a Landlock-less host. | audit trail | `TestRunManifest_C7_JailDegradedNodes`, `TestDiagnose_C7_JailDegradedSuggestion`, `TestDoctor_C7_PreferWarnsWithoutLandlock`, wire-shape test |
| C8 | Operator copy never calls a `prefer` node "sandboxed"/"jailed": every degrade message contains the literal `UNJAILED`; CHANGELOG/CLAUDE.md/docs carry the §3 trade-off. | copy | grep-pinned in `TestJailDegradedCopy_C8` |
| C9 | No code path degrades unless `mode == "prefer"` exactly. The degrade branch is gated on a positive comparison with `WritablePathsModePrefer`; `""`, `"require"`, and any other string take the refuse path. | fail-closed default | `TestConfigureJail_C9_OnlyExactPreferDegrades_Rapid` |
| C10 | Gate order G1 → G2 → G3 is preserved, so a `prefer` node with a malformed glob on a Landlock-less host **refuses** (authoring error precedes the host check) and never emits `jail_degraded`. | ordering | `TestConfigureJail_C10_AuthoringBeforeHost` |
| C11 | `refuseWritablePathsOnUnsupportedBackend` (dispatcher layer, by backend *type*) is mode-agnostic — the #275 hole stays closed under `prefer`. | #275 | `TestRefuseWritablePathsOnUnsupportedBackend_C4_PreferMatrix` |

## 6. What degrades vs what never degrades

| Component | `require` (default) | `prefer` on a Landlock host | `prefer` on a Landlock-less host |
|---|---|---|---|
| Bash + descendants (Landlock via `__jail-exec`) | enforced | enforced | **UNJAILED** (pre-#272 reach) |
| In-process `Write`/`Edit`/`ApplyPatch`/`generate_code`/`write_enriched_sprint` | glob policy + `openat2 RESOLVE_BENEATH` | same | glob policy (lexical) — **no** kernel symlink defence |
| `apply_patch` delete/move (`Remover`) | glob policy + `openat2` unlinkat | same | glob policy (lexical) |
| Malformed glob / bad `working_dir` / empty list (G1) | refuse | refuse | **refuse** |
| `backend: claude-code` / `acp` / unknown (G2 + dispatcher) | refuse | refuse | **refuse** |
| Non-`*LocalEnvironment` exec env | refuse | refuse | **refuse** |
| `jail_degraded` event / warning / manifest / diagnose | never | never | once per attempt |

## 7. Threat-model delta and residual risks

- **New residual (by design):** on a Landlock-less host, a `prefer` node's Bash
  subprocess can write anywhere the tracker UID can. This is exactly the
  pre-#272 posture, now *declared and recorded* rather than silent. The
  `commit_only` prompt/system-prompt guard is the only remaining defence for
  Bash on that host.
- **Kept:** the #275 out-of-process hole stays closed (C11); authoring errors
  stay fail-closed (C4/C10); the in-process tier stays bounded (C6).
- **Degraded in-process tier is lexical:** `LocalEnvironment.safePath`
  containment + glob match, then `os.MkdirAll` + `O_NOFOLLOW` open of the leaf.
  A pre-created symlinked *intermediate* directory inside an allowed glob
  could redirect an in-process write outside the anchor on a Landlock-less
  host. Accepted: the Bash tier on that same host is already unbounded, so this
  does not widen the reach the operator has already accepted by choosing
  `prefer`.
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

- No dippin-side lint yet (`writable_paths_mode` arrives via `params:`); a
  typed field is requested upstream (draft in the PR report).
- No per-tool granularity (e.g. "prefer for Bash, require for Write") — a
  single mode per node.
- No attempt to jail out-of-process backends.
