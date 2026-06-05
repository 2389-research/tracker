# Changelog

All notable changes to tracker will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- **engine: commit-WIP to a recoverable ref before routing a failed/exhausted
  node** (closes #302, refs epic #308 Phase 1). The engine now preserves an
  agent node's dirty (possibly green) working tree to a named, recoverable git
  ref **before** it routes away from — or halts at — a failure/exhaustion, so
  green-but-uncommitted work is no longer silently discarded (the loss in the
  `code-goblin` run `7b6e08c9e2b2`, where `Implement` was green at turn 48 but
  uncommitted when the turn budget breached). A new `gitArtifactRepo.CommitWIP`
  method commits the dirty tree (additions, modifications, **and** removals via
  `git add -A`) and points the lightweight tag `tracker/wip/<runID>/<nodeID>` at
  it, mirroring the existing `TagCheckpoint` precedent; the ref is recorded in
  both the checkpoint (`Checkpoint.WIPRefs`, additive/backward-compatible) and
  the trace (`TraceEntry.WIPRef`). A single engine helper
  (`commitWIPBeforeRouting`) is wired into every fail/exhaust routing path — the
  strict-failure path (`checkStrictFailure`, covering both the `fallback_target`
  escalation and the terminal halt), the retry-exhausted path
  (`handleRetryExhausted`), the exit-node fail path (`handleExitNode`), and the
  terminal handler-error path (`processActiveNode`, e.g. a node that wrote files
  then returned an error or was cancelled mid-write) — and always runs
  **before** the routing/halt decision. A clean tree is a no-op (no empty
  commit, no ref); when git artifacts are disabled it logs an `EventWarning` and
  skips (it never touches the user's real working repo); a WIP-commit failure is
  surfaced as a warning and never masks the original failure or changes routing.
  This completes #297's deferred exhaustion-path persistence (`CommitIfDirty`
  covered only the success path) and is the persistence layer that #303's
  verify-then-commit-on-breach builds on.
- **build_product: commit-first / stop-when-green discipline + `CommitIfDirty`
  checkpoint node** (closes #297, refs epic #308 Phase 1, builds on #296). Two
  `.dip`-only changes guard against green-but-uncommitted work being lost. (1) A
  terminal stop-when-green-and-committed rule appended to both the `Implement`
  and `FixMilestone` prompts: the agent emits `DONE` and stops the instant the
  milestone verify passes **and** it has committed — committing is the
  precondition for stopping, so turns aren't burned on redundant re-verification
  that ends with an uncommitted tree (the proximate cause of the `code-goblin`
  run `7b6e08c9e2b2` failure). (2) A new `tool CommitIfDirty` node on the
  `Implement` **success** path — the success edge changed from
  `Implement -> TestMilestone` to `Implement -> CommitIfDirty`, with a new
  `CommitIfDirty -> TestMilestone` edge — commits the working tree iff dirty so
  a success-with-dirty-tree is persisted before `TestMilestone` runs. The commit
  passes an explicit git identity and does **not** swallow failures (no
  `|| true`, per CLAUDE.md "never silently swallow errors"). `CommitIfDirty` does
  **not** run on the turn-**exhaustion** path: `Implement` failure routes
  straight to `EscalateMilestone` via the #296 catch-all / `fallback_target`
  (left unchanged), and persisting green work on the exhaustion path is the
  engine's job, tracked in #302.
- **Agent-tool jail threat model + jail-bypass lint** (closes #283, refs
  #275/#272). New doc `docs/architecture/agent-tool-jail-checklist.md`: a
  per-tool threat-model table (every `agent/tools/` tool — what it touches and
  which `ExecutionEnvironment` seam it routes through, each row verified against
  the code), the rule that any filesystem mutation **or subprocess** MUST route
  through `exec.ExecutionEnvironment`, a new-tool checklist, and the documented
  env==nil-fallback invariant. New CI gate `make tools-jail-check` (a `go/ast`
  analyzer under `tools/jailcheck/`) flags any reference to a watched mutating
  function in `agent/tools/*.go` (excluding `_test.go`) across **`os`,
  `os/exec`, `io/ioutil`, and `syscall`** — covering both jail seams (filesystem
  writes and subprocess spawn). It resolves aliased imports, matches selector
  references (so function-value capture / callback passes can't dodge it), and
  flags dot-imports of watched packages. The one legal exception — a fallback
  reachable only when `env == nil`, where no jail can be active — is whitelisted
  by a `//jail:allow-unjailed-fallback` marker on the function. Wired into the
  `ci:` target and the CI "Quality Gates" job. The two existing audited
  fallbacks (`generate_code`, `write_enriched_sprint`) carry the marker; no
  other product code changed.
- **Property/invariant tests for the `writable_paths` jail public surface**
  (closes #282, refs #275/#272). New `agent/exec/jail_property_test.go` and
  `pipeline/handlers/codergen_jail_property_test.go`, plus property additions to
  `agent/exec/jail_linux_test.go` and `agent/exec/jail_other_test.go`, all using
  the existing `pgregory.net/rapid` dev dep (no new deps). These pin the
  first-principles properties the #275 review surfaced round-by-round (~30
  findings over 13 rounds) so a regression fails fast with a shrunk
  counter-example: the escape-vs-malformed sentinel partition of
  `ValidateWritablePaths`/`validateGlobEntry`; the supported doublestar shapes
  of `matchOneGlob`/`matchWritablePath` and the matcher↔`landlockDirForGlob`
  never-escapes-anchor invariant; `OpenForWrite`/`SafeMkdirAll`/`SafeRemove`
  containment (absolute/parent/symlink-at-any-depth refusal, EACCES-vs-escape
  classification, EEXIST/ENOENT tolerance); `relPathForJail` containment incl.
  the bare-`..name` allow case; the `configureJail` G1/G2 refuse-to-start gates
  and the `WriteOpener`/`Remover` closures (glob accept/`ErrPathNotAllowed`/
  `ErrPathEscape` classes, mkdir-after-glob ordering); and the non-Linux stubs'
  input-independent fail-closed contract. Test-only — no product code changed.
- **`TRACKER_GATEWAY_KIND` routing dispatch** (refs #274, closes #276). New env
  var, `--gateway-kind` CLI flag, and `Config.GatewayKind` library option select
  the per-provider URL suffix convention used with `TRACKER_GATEWAY_URL`:
  - `cf-aig` (empty/default, backcompat) — Cloudflare AI Gateway path
    conventions: `/anthropic`, `/openai`, `/google-ai-studio`, `/compat`.
  - `bedrock` — the 2389 bedrock-gateway Worker: empty suffix for Anthropic
    (the SDK appends `/v1/messages`), `/v1` for OpenAI and Gemini.
    `openai-compat` is unsupported (the bedrock gateway has no `/compat`
    equivalent) and the resolver refuses to route (fail-closed).

  Unknown kind values refuse to route rather than silently falling through
  to `cf-aig`. Per-provider `<PROVIDER>_BASE_URL` env vars still win
  unconditionally as the surgical override.

  **Fail-closed enforcement:** when a gateway URL is configured but the
  (kind, provider) pair is unsupported (e.g. `bedrock` + `openai-compat`)
  or the kind is unknown, adapter construction fails with a wrapped
  `tracker.ErrGatewayRouteRefused`. This prevents silent fallback to the
  provider SDK's default endpoint (which would otherwise leak the gateway
  token to public hosts like `openrouter.ai`, `api.openai.com`,
  `api.anthropic.com`). The new strict resolver is exposed as
  `tracker.ResolveProviderBaseURLStrict`; the legacy
  `tracker.ResolveProviderBaseURL` is preserved for back-compat and
  returns `""` for both "no gateway" and "refuse" without distinction.

  `--gateway-kind` is validated at flag-parse time: unsupported values
  fail fast with a clear CLI error instead of propagating to
  `TRACKER_GATEWAY_KIND` and surfacing late as a routing refusal.

  Existing CF AIG callers see zero behavior change — the default behavior
  matches the prior hard-coded suffix map. Operator-facing docs + doctor
  preflight notes ship in follow-up PRs (#277, #278).

- **`tracker doctor` gateway routing notes** (refs #274, closes #277). When a
  gateway is configured (`TRACKER_GATEWAY_URL` or `TRACKER_GATEWAY_KIND` set),
  doctor adds a "Gateway Routing" check that surfaces two setup-time caveats
  as informational notes (never warnings or errors):
  - **Bedrock masquerade** — when OpenAI traffic actually traverses the
    bedrock gateway (`TRACKER_GATEWAY_KIND=bedrock` + a gateway URL +
    `OPENAI_API_KEY`, with no `OPENAI_BASE_URL` override), `gpt-*` / `o*-*`
    model strings route to Claude Sonnet 4.6 today (AWS Bedrock has no
    OpenAI models yet); the gateway re-routes automatically with no tracker
    change once AWS adds them.
  - **Per-provider precedence** — when `TRACKER_GATEWAY_URL` and one or more
    `<PROVIDER>_BASE_URL` are both set, the per-provider overrides that win
    over the gateway are listed by name. The check is omitted entirely when
    no gateway is configured, so default runs are unchanged.

- **Gateway routing operator docs** (refs #274, closes #278). New
  [`docs/architecture/bedrock-gateway.md`](docs/architecture/bedrock-gateway.md)
  setup guide covering both gateway kinds (`cf-aig` default and `bedrock`),
  their per-provider suffix conventions, `<PROVIDER>_BASE_URL` precedence, and
  the bedrock-kind caveats (OpenAI→Claude masquerade, synthesized streaming,
  `openai-compat` fail-closed). Linked from the architecture docs index and
  `llm.md`.

### Fixed

- **Unhandled agent failure no longer dead-stops the pipeline** (closes #295,
  refs epic #308, pairs with #296). When an agent node returns `OutcomeFail`
  (including turn-limit exhaustion) and has only unconditional outgoing edges,
  the strict-failure guard now consults `findFallbackTarget` before halting:
  if a node-level or graph-level `fallback_target`/`fallback_retry_target`
  resolves, the run routes there (one-shot per node, guarded by
  `Checkpoint.FallbackTaken` and persisted across resume) instead of skipping
  every downstream safety node. A single graph-level `fallback_target` thus
  becomes a catch-all escalation route for any unhandled failure. Behavior is
  unchanged when no fallback is declared — same terminal `OutcomeFail` status
  and the same error string as before. The fallback advance applies the same
  post-node `BudgetGuard` check as the normal edge-advance path, so a failed
  node that already breached a hard token/cost ceiling halts on budget instead
  of spending more on the fallback node.
- **`build_product.dip`: every agent node now routes failure/turn-exhaustion to
  an escalation gate instead of dead-stopping** (closes #296, refs epic #308,
  pairs with the #295 engine catch-all). Six agent nodes had exactly one
  unconditional outgoing edge and no fallback, so on `max_turns` exhaustion (the
  proximate cause of the code-goblin run `7b6e08c9e2b2`, where GREEN-but-
  uncommitted work halted the pipeline before the cross-review lanes and
  FinalSpecCheck ever ran) the engine's strict-failure rule halted the whole
  run. Mirroring the `FixMilestone` precedent: `Implement` gains
  `fallback_target: EscalateMilestone`; `ApplyReviewFixes` and `FinalCommit`
  gain `fallback_target: EscalateReview`. For `Implement` and `ApplyReviewFixes`
  the primary edge is now conditional on `ctx.outcome = success` with an
  unconditional belt-and-suspenders catch-all to the escalation gate;
  `FinalCommit` relies on `fallback_target` alone because a catch-all edge there
  would close an unconditional `Cleanup → FinalCommit → EscalateReview` cycle
  (DIP005). The three cross-review reviewers run as **parallel branches**, which
  execute via `ParallelHandler` and bypass the engine's strict-failure path — a
  per-branch `fallback_target` would be inert — so their failure is routed on the
  aggregating `ReviewParallel` node with a conditional fail edge to
  `EscalateReview`. Because `aggregateStatus` is success-if-any, this fires when
  **all three** reviewers fail (previously a silent dead-stop); a single
  reviewer failing is still masked by `aggregateStatus` and is tracked as an
  engine follow-up (#313). New `pipeline/build_product_failure_routing_test.go` pins the
  invariant — every codergen node has a conditional edge or a resolvable
  fallback, with parallel branches checked against their parent's route — as the
  in-repo counterpart to dippin-lang#93's author-time lint.

## [0.35.1] - 2026-06-02

### Changed

- **Joint-release closeout for v0.35.0 ↔ dippin v0.35.0.** `go.mod` swaps the
  joint-release-window pseudo-version
  (`v0.34.1-0.20260601154018-792e6e644e9f`) for the published
  `github.com/2389-research/dippin-lang v0.35.0` tag. `PinnedDippinVersion`
  in `tracker_doctor.go` updated in lockstep. **No functional changes vs
  v0.35.0** — the underlying dippin code is byte-identical (`792e6e6` is
  the commit dippin v0.35.0 tagged). Joint-release loop closed: tracker
  v0.35.0 ships against dippin's `main` HEAD, dippin v0.35.0 tags pinning
  tracker v0.35.0, tracker v0.35.1 swaps the SHA for the tag. Same shape
  as the v0.31.0 → v0.32.0 closeout.

## [0.35.0] - 2026-06-02

### Added

- **`writable_paths` fs-jail enforcement** (closes #272, paired with dippin
  v0.35.0 / #75). Agents with `writable_paths` declared in the workflow now have
  a runtime write jail bounding all file mutations (Write/Edit/ApplyPatch AND
  Bash + descendants) to the declared globs. **Linux-only** (kernel 6.7+ for
  Landlock ABI v3); macOS/Windows/older Linux refuse-to-start when `writable_paths`
  is set. `claude-code` and `acp` backends also refuse-to-start (out-of-process
  backends tracker cannot sandbox). In-process tools enforce the **exact** globs
  via `openat2(RESOLVE_BENEATH | RESOLVE_NO_SYMLINKS | RESOLVE_NO_MAGICLINKS)`;
  Bash subprocess is bounded at the **directory ancestors** of each glob's
  static prefix via Linux Landlock — for the sentinel-writer adopters these are
  identical because their globs are directory-scoped (e.g. `workspace/**`).

  **Operator notes:**
  - A new internal subcommand `tracker __jail-exec` exists for the runtime to
    re-exec into a Landlock-sandboxed child. Operators MUST NOT invoke it
    directly; documented for transparency only.
  - Residual escape classes (not bounded by writable_paths): network egress
    (`curl`, `cargo` crate fetches), reads / exfil-by-read, content within an
    allowed path. The agent can still poison `workspace/Cargo.toml` if
    `writable_paths: workspace/**`. Narrow globs are the strongest posture.
  - **Requires**: paired dippin-lang release with the `WritablePaths` IR field
    (tracked via #75). During cross-repo dev tracker pins `@latest` against
    dippin's `main`; the release PR bumps to the tagged version. Without a
    matching tracker, dippin's `writable_paths` field is unenforced — tracker
    fail-closes on the field, so an older tracker refuses to run rather than
    silently ignoring.

  **Library-API delta** (for embedded integrations):
  - `pipeline.AgentNodeConfig` gains `WritablePaths []string` + `WritablePathsSet bool` (typed accessor).
  - `agent.SessionConfig` gains `Backend string`, `WritablePaths []string`, `WritablePathsSet bool`.
  - `agent/exec.LocalEnvironment` gains optional `CommandWrapper`, `WriteOpener`, and `Remover` function fields.
  - `agent/exec.ExecutionEnvironment` interface gains a `RemoveFile` method (the writable_paths jail intercepts delete + apply_patch move-cleanup paths through this seam).
  - New `agent/exec` package functions: `WrapBashCmd`, `RunJailExec`, `ProbeLandlock`, `ValidateWritablePaths`, `OpenForWrite`. Linux-only; non-Linux builds get passthrough stubs that refuse-to-start.

- **New terminal `EngineResult.Status` value `validation_overridden`.** Runs that traverse a `wait.human` gate edge marked `override: true` in their `.dip` workflow now terminate with `Status == "validation_overridden"` instead of `"success"`, recording in the audit trail that a non-workflow decision (a human at the TUI, an autopilot persona, or a webhook callback) accepted a result the automated checks flagged as failed. The `Override:` line in `tracker audit` traces the routing; the `--json` output carries a stable `status_class: succeeded|failed` companion field for downstream consumers. Closes Gap 5.2 of [#233](https://github.com/2389-research/tracker/issues/233) ([#271](https://github.com/2389-research/tracker/issues/271)).
- **New edge attribute `override: true`** in `.dip` syntax, valid only on edges from `wait.human` nodes. Adapter rejects misuse at compile-time. See the `validation_overridden` design doc for the per-edge guidance.
- **New CLI flag `--fail-on-override`** (and env var `TRACKER_FAIL_ON_OVERRIDE=1`, strict-`=1` parsing matching the existing `TRACKER_PASS_*` convention). Causes `tracker run` to exit code 2 (distinguishable from generic-fail exit 1) when a run terminates as `validation_overridden`. Default unchanged: exit 0.
- **New `dippin doctor` lint rule TRK102** (warn-level): fires on `wait.human` edges with label `accept` / `mark done` / `approve` (case-insensitive) that route to a forward-progress target, are upstream-reachable from an `outcome = fail` edge, and lack `override: true`. Surfaces the migration opportunity without false-positive on plan-approval gates.
- **TUI live override rendering**: `MsgPipelineCompleted` gains a typed `Status` field; the TUI completion row renders the amber override badge in real-time. No more identical green badges for `success` and `validation_overridden`.

### Changed

- **Joint-release coordination**: `go.mod` pins `github.com/2389-research/dippin-lang` to the pseudo-version `v0.34.1-0.20260601154018-792e6e644e9f` (dippin's `main` commit `792e6e6`), which contains the matching `WritablePaths` and `Override` IR fields. dippin v0.35.0 is not yet tagged; the loop closes when dippin tags v0.35.0 pinning tracker v0.35.0 and tracker then publishes v0.35.1 swapping to the tagged dippin version (zero functional change). Same pattern as the v0.31.0 → v0.32.0 closeout.
- **Library-API delta**: `EngineResult.Status`, `tracker.Result.Status`, `tracker.AuditReport.Status`, `tracker.RunSummary.Status` are re-typed from `string` to `pipeline.TerminalStatus` (a named string type). Existing literal-string comparisons (`result.Status == "success"`) continue to compile and produce the same answer. Assignments from a `string` variable into a `Status` field require an explicit cast — this is the one breaking change in the re-typing. Embedded library callers should migrate to the new `TerminalStatus.IsSuccess()` helper for forward-compat across future status additions.
- **New exported symbols** (additive): `pipeline.TerminalStatus` (type), `pipeline.OutcomeValidationOverridden`, `pipeline.OverrideDetail`, `pipeline.Actor` (type), `pipeline.ActorHuman` / `ActorAutopilot` / `ActorWebhook` / `ActorUnknown`, `pipeline.Edge.Override`, `pipeline.Outcome.ChildOverride`, `pipeline.Outcome.OverrideActor`, `pipeline.EngineResult.ValidationOverrides`, `pipeline.Checkpoint.ValidationOverrides`, `pipeline.EventValidationOverridden`, `pipeline.PipelineEvent.Override`, `pipeline.ErrValidationOverridden`. Plus mirrored fields on `tracker.Result`, `tracker.AuditReport`, `tracker.RunSummary`, `tracker.DiagnoseReport`.
- `tracker_audit.AuditReport.Recommendations` is no longer alphabetically sorted — entries appear in priority order (override notes first, then per-override entries chronologically, then retry/budget notes). Downstream tools that sort on receipt are unaffected; tools that displayed in receive-order will see a different order.

### Fixed

- **`tracker_audit.classifyStatus` previously collapsed `budget_exceeded` events to `"fail"`**, so `tracker audit` and `tracker list` reported budget-halted runs as failures. Audit surfaces now surface `budget_exceeded` correctly. **User-visible behavior change**: scripts filtering on `status == "fail"` will see budget-halted runs move out of the `fail` bucket into a new `budget_exceeded` bucket. Update filters to `status_class == "failed"` (introduced in this release) for a stable bucket that survives future enum extensions. `EngineResult.Status` on budget-halted runs was already `budget_exceeded` since `OutcomeBudgetExceeded` shipped — this fix aligns the audit surface with the engine surface, closing a long-standing reporting gap.

### Requires

- `dippin-lang vX.Y.Z` (a real tag, not a pseudo-version pin — see release sequence in the design doc §16). `PinnedDippinVersion` bumped in lockstep with `go.mod` in the same commit.

### Migration for embedded library callers

```go
// Before:
if result.Status == pipeline.OutcomeSuccess {
    deploy()
}

// After (treats validation_overridden as success — opt into the new audit-positive semantic):
if result.Status.IsSuccess() {
    deploy()
}

// To gate deploy on overrides explicitly:
if result.Status.IsSuccess() && len(result.ValidationOverrides) == 0 {
    deploy()
}
```

If your code only ever cared about "completed cleanly without further action," no change is needed — `IsSuccess()` returns the same value for `OutcomeSuccess` as the old string comparison did. Override runs now classify as `IsSuccess() == true` (audit-positive) rather than as failures.

### Operator notes (monitoring & CI integrations)

Two surfaces silently change behavior on upgrade:

1. **Monitoring dashboards filtering JSON output on `status == "success"` will silently undercount completed runs.** Override runs surface as `status == "validation_overridden"` — update filters to `status_class == "succeeded"` (the new stable open-enum-tolerant companion field) or to the union `status IN ("success", "validation_overridden")`.
2. **Scripts counting failed runs via `status == "fail"` will see budget-halted runs disappear from the failure bucket** (the `classifyStatus` fix). Update to `status_class == "failed"` for a stable bucket.

For CI integrations that should NOT auto-deploy on overrides:

```bash
tracker run --fail-on-override workflow.dip && deploy.sh
```

Without `--fail-on-override`, override runs exit 0 and `deploy.sh` will fire — this is the intended default (an override is a deliberate operator decision), but CI integrators should opt in to strict mode.

## [0.34.0] - 2026-05-27

### Changed

- **Collapsed `workflows/` mirror into `examples/` via explicit-file `go:embed`** ([#256](https://github.com/2389-research/tracker/issues/256)). The repo previously kept four byte-identical copies of the built-in workflow .dip files under `workflows/` and `examples/`, synchronized by a Makefile target (`make sync-workflows` / `make check-workflows`), a pre-commit gate, and a CI step. This drifted three times despite the guardrails. The `//go:embed workflows/*.dip` glob is replaced with four explicit `//go:embed examples/<name>.dip` lines pointing directly at the canonical copies. The `workflows/` directory and all four sync-infrastructure pieces (Makefile targets, pre-commit gate #9, CI `Embedded workflows in sync` step) are deleted. `WorkflowInfo.File` (library API) now reports paths with the `examples/` prefix instead of `workflows/` — the only externally visible delta. No functional change for CLI users: `tracker workflows`, `tracker init`, and bare-name resolution behave identically. Closes [#256](https://github.com/2389-research/tracker/issues/256).

## [0.33.0] - 2026-05-27

### Changed

- **`build_product` re-runs reviewers after `ApplyReviewFixes` with a one-shot budget** ([#233](https://github.com/2389-research/tracker/issues/233) Gap 5.3). The original `build_product` audit observed that `ApplyReviewFixes` ran once and routed directly to `FinalBuild`, so any W4/W5/W13 regressions introduced by the fix commit (zero-assertion stubs, wrong-target tests, DI bypass in the patch) reached `Done` unchecked — `FinalSpecCheck` deliberately scopes to W17 sleep-fence + interface reachability + `SPEC.md` compliance per Gap 8 v5 and explicitly delegates W4/W5/W13 to reviewer rubric point 3, which only runs pre-fix. Fix: new tool node `CheckReviewFixBudget` increments `.ai/build/review_fix_attempts` and exits non-zero once `MAX_ATTEMPTS=1` is exceeded; new edges `ApplyReviewFixes -> CheckReviewFixBudget` then `CheckReviewFixBudget -> ReviewParallel when ctx.outcome = success restart: true` (one re-review pass) or `CheckReviewFixBudget -> EscalateReview when ctx.outcome = fail` (budget exhausted → human gate). Pattern mirrors the existing `TestMilestone` `fix_attempts` file. `FinalSpecCheck` allowlist updated to include `review_fix_attempts` as an intentional `.ai/build/` artifact. A new `ResetReviewBudget` tool node sits on the `EscalateReview "retry" -> Decompose` edge so the counter is cleared when the human picks retry — otherwise the stale counter from the prior build would immediately fail-close the retry's first re-review pass (caught by Codex / Copilot during PR review). `dippin simulate -all-paths` enumerates paths cleanly, all terminating; `dippin doctor` A grade. Closes Gap 5.3 of the #233 audit recap. Gap 5.2 (`OutcomeHumanOverride`) remains as a separate follow-up — touching `pipeline.Outcome` has wide blast radius and should be its own issue.
- **`parseAutoStatus` tolerates markdown-emphasis on STATUS lines** ([#233](https://github.com/2389-research/tracker/issues/233) Gap 5.1). The original `build_product` audit observed a `FinalSpecCheck` run where the agent emitted `**STATUS: fail**` (bold) and the parser silently fell back to the default `success` because `HasPrefix("**STATUS:", "STATUS:")` returns false. LLMs commonly bold/italicize STATUS lines when they want the verdict to draw the eye — the parser was treating these as no-STATUS-found and the inverted Gap 7 contract was the only thing preventing fail-open on a real bug. Fix: `parseStatusLine` now `strings.Trim`s the line and value with the `"*_"` cutset before the prefix check + switch, so `**STATUS: fail**` / `*STATUS: fail*` / `STATUS: **fail**` / `__STATUS: success__` / etc. all parse correctly. Locked semantics from `TestParseAutoStatus_V3FailFirstContract` are unchanged: last-line-wins + default-success-on-empty. New regression coverage in `TestParseAutoStatus_Gap5_1_AuditedShapes` (11 sub-tests) — six previously RED, now GREEN; five pin existing behavior (uppercase value, whitespace trimming, last-wins under emphasis, code-fence skip). Closes Gap 5.1 of the #233 audit recap. Gap 5.2 (`OutcomeHumanOverride`) remains as a separate follow-up.

## [0.32.0] - 2026-05-27

### Changed

- **`dippin-lang` dependency bumped to v0.32.0 tag** ([#258](https://github.com/2389-research/tracker/issues/258), joint-release follow-up). v0.31.0 shipped with a transient pseudo-version pin (`v0.31.1-0.20260526211025-53c24f13a4d0`, the dippin-lang#41 merge SHA) during the joint-release window — at the time tracker v0.31.0 was tagged, dippin v0.32.0 didn't exist yet, but tracker's `tool_access: none` enforcement code depended on the `tool_access` IR field from that unreleased dippin work. With dippin v0.32.0 now tagged, this release swaps the SHA pin for the proper tag and updates `PinnedDippinVersion` in `tracker_doctor.go`. No functional changes vs v0.31.0; the `tool_access` enforcement behavior is identical because the underlying dippin code is identical (the pseudo-version pointed at the same commit that became the v0.32.0 tag). This release closes the joint-release coordination loop: tracker v0.32.0's go.mod pins dippin v0.32.0; dippin v0.32.0's go.mod pins tracker v0.31.0; both tags are now mutually pinned and published.

## [0.31.0] - 2026-05-27

### Added

- **`tool_access: none` runtime enforcement on agent nodes** ([#258](https://github.com/2389-research/tracker/issues/258), joint with [dippin-lang#41](https://github.com/2389-research/dippin-lang/issues/41)). Bounds the v0.28.2 single-agent multi-tool-call vector: when an LLM emits multiple tool calls in one response, tracker would dispatch all of them before `max_turns` checked the cap. With `tool_access: none` set on an agent node, tracker now hands the LLM **zero tools** so the response comes back as plain text. Implementation:
  - `agent.SessionConfig.ToolAccess` (`string`) — populated from the dippin adapter; canonical case-insensitive, whitespace-trimmed. Any non-empty value disables tools (fail-closed for typos — `noen`, `None`, `off`, `x` all trip the restriction so a lint-skipped misspelling can't ship full tools). Helper: `SessionConfig.IsToolAccessRestricted()`.
  - `agent.builtInToolsForConfig` returns `nil` when restricted (no built-in tools registered).
  - `agent.NewSession` clears the tool registry after all options apply — catches `WithTools(...)` bypass. Defense in depth: built-ins gate + post-WithTools clear + executeToolCalls early-exit means three independent paths block dispatch.
  - `agent.Session.doLLMCall` sets `request.Tools = nil` and `request.ToolChoice = ToolChoiceNone()` so the API itself blocks tool invocation.
  - `agent.Session.initConversation` swaps the default `"File tool arguments (read, write, edit, glob, grep_search) MUST use paths relative..."` prefix for a tool-free variant when restricted. Scope: only the built-in prefix is scrubbed — a caller-supplied `SessionConfig.SystemPrompt` is still appended verbatim. The registry-empty + ToolChoice=none + dispatch-shortcircuit defenses do NOT depend on the prompt scrub; the scrub is defense-in-depth, not load-bearing.
  - `agent.Session.executeToolCalls` short-circuits when restricted — if the LLM emits tool calls despite `ToolChoice=none` (mock, retry, provider that ignored the signal), zero of them execute. Emits an error event for visibility.
  - **Params-bypass defense** at `pipeline/handlers/codergen.go`: `applyToolLists` and `applyPermissionMode` early-return when `tool_access` is set, so `allowed_tools`/`disallowed_tools`/`permission_mode=bypassPermissions` Params keys can't re-enable the tools the directive intends to deny.
  - **Backend compatibility:**
    - **Native backend** — full honor via `agent.SessionConfig.ToolAccess`.
    - **claude-code backend** — best-effort enumeration: `applyClaudeCodeToolAccess` populates `DisallowedTools` with the canonical Claude Code tool names (`Bash`, `Edit`, `Glob`, `Grep`, `NotebookEdit`, `Read`, `Task`, `TodoWrite`, `WebFetch`, `WebSearch`, `Write`) so the CLI denies the surface.
    - **ACP backend** — refuses session creation with a clear error pointing to #258. ACP's deny-equivalent spelling hasn't been verified against each upstream agent (claude-code-acp, codex-acp, gemini); per the spec's "fallback unsupported → refuse" rule, refusing is safer than shipping a soft-no that silently allows execution.
  - **Tests** (`agent/tool_access_test.go`, `pipeline/handlers/tool_access_test.go`):
    - `TestSessionToolAccess_RedTeamMultiToolCall` — the dispositive v0.28.2 test. Mock LLM returns a single response containing three tool calls (`bash`, `write`, `bash`). Assert: zero tool executions, `result.TotalToolCalls() == 0`, request had no tools, `ToolChoice.Mode == "none"`.
    - `TestSessionToolAccess_RestrictedRegistry_EmptyAfterWithTools` — registry is empty even when `WithTools(read, write, bash)` is called.
    - `TestSessionToolAccess_FailClosedOnTypo` — `noen`, `None`, `  none  `, `NONE`, `off`, `x` all disable tools.
    - `TestSessionToolAccess_EmptyMeansUnrestricted` — empty string leaves tools registered normally.
    - `TestSessionToolAccess_SystemPromptScrub` — assembled system prompt contains no standalone case-insensitive `read`/`write`/`edit`/`glob`/`grep_search`/`bash`/`apply_patch` for the built-in prefix path (i.e. when SystemPrompt is either empty or doesn't name tools itself).
    - `TestApplyToolLists_BypassDefense` + `TestApplyPermissionMode_BypassDefense` — Params keys ignored under `tool_access: none`.
    - `TestApplyClaudeCodeToolAccess` — DisallowedTools populated with canonical names.
    - `TestACPBackend_RefusesToolAccess` — ACP returns error referencing #258 and the directive value.
  - **Joint-release coordination:** this lands on tracker `main` but is **not** tagged. The dippin-lang #41 PR will bump `go.mod` to the new tracker tag, then tag dippin v0.32.0 immediately after. No advisory window — the dippin field doesn't ship in a tagged release until tracker enforcement is also tagged.

### Changed

- **`build_product` workflow: closed Gap 7 from the #233 audit (interface-method reachability)** ([#233](https://github.com/2389-research/tracker/issues/233)). The audit caught three Go interface methods defined and unit-tested but never called from production code: `AuthStatus(ctx) error` (Appendix A I9), `IsRebaseInProgress() bool` (I10), `DiffStat` (similar shape). Tests passed because the same agent wrote impl and tests; the workflow had no check that defined interface methods have a non-test caller. New mechanism:
  - **`Setup` now writes `.ai/build/iface-reachability-rubric.md`** — a shared discipline file mirroring PR #246's `ci-probe.sh` pattern. Contains language detection (10 static-interface languages with skip-vacuously rule for Ruby / plain JS / Elixir / Zig / C / shell), per-language enumeration grep patterns, caller-discipline rules (call-syntax targeting, common-name receiver context, broad test-file exclusion glob, generated-code-counts-as-production), stdlib/framework satisfaction principle, single-sentence waiver discipline, library-API carve-out via `.ai/decisions/library_api.md`, and known-limitation skips (Rust `dyn Trait`, Haskell typeclasses, TS bracket-notation, Swift extension conformances, etc.) with named-reason discipline.
  - **`FinalSpecCheck` owns the check** — repo-wide sweep, single owner, fires once per run at the goal-gate immediately before Cleanup/FinalCommit/Done. The prompt opens with an inverted STATUS contract: agent emits `STATUS:fail` as the FIRST line of its response, then enumerates, then emits `STATUS:success` as the LAST line only if every check passes. This defends against the `parseAutoStatus` default-to-success-on-empty fail-open shape that is exactly the original Gap 7 bug — under last-line-wins parsing, a truncated response preserves the early fail. The pre-existing `STATUS:fail with specific list of gaps` line was also fixed (parser requires the STATUS value to be exactly `success`/`fail`/`retry` — trailing prose on the same line is silently discarded). Output is prose enumeration (no markdown table — that introduced parser-fragility risk because STATUS tokens inside table cells aren't parsed).
  - **Reviewer rubric point 2 strengthened** in `ReviewClaude`, `ReviewCodex`, `ReviewGemini` — from 3-line "name a caller" to 9-line "show the grep command, paste the output, cite file:line — same show-your-work standard as SPEC LITERALS at point 1." The heavy discipline lives in the shared rubric file the reviewers reference, keeping rubric balance with the other four points (per PR #249's design).
  - **`VerifyMilestone` is unchanged.** Per-milestone iface checks were considered and rejected (squad review): the bug is a property of the terminal state not the build process, per-milestone scoping creates a cross-milestone leak shape that would have required LLM-managed `.ai/pending_wiring.md` bookkeeping (~17% reliability after 5 milestones per parser-pragmatist analysis), and the workflow is fully automated so "catch early" has no operational value when no human is debugging mid-run.
  - **New regression test:** `TestParseAutoStatus_V3FailFirstContract` in `pipeline/handlers/codergen_test.go` pins the parser's last-line-wins / default-success-on-empty semantics that the new FinalSpecCheck STATUS contract relies on. Three subtests: terminal-success-wins, mid-check-fail-remains, no-STATUS-default-success.
  - Workflow score on `dippin doctor examples/build_product.dip` stays **A / 100/100**, no new lint warnings. Gap 8 (`TestQuality` step) is closed in the next bullet below; Gap 5 (engine-level `auto_status` audit) remains the last Chunk C item.

- **`build_product` workflow: closed Gap 8 from the #233 audit (test-quality smells)** ([#233](https://github.com/2389-research/tracker/issues/233)). The audit caught five test-quality regressions that shipped green when the same agent wrote impl and tests: zero-assertion (`goblin.start` test logs an empty SHA without asserting on it — W4), wrong-target (`TestRun_SignalHandling` tests stdlib `signal.NotifyContext` not the daemon's handler — W5), DI bypass (tests call `time.Now()` directly when a `Clock` seam exists — W13), sleep-as-fence (`TestLoop_BusyDrops` uses three `time.Sleep` calls between phases — W17), subname collision (`bytes_trimSpace` shadows `bytes.TrimSpace` under Go's subtest case-fold — W21). The audit's failure mode was reviewers handwaving past prose-only checks; this PR mitigates by adding shown-work demands at the layer that catches each smell most reliably:
  - **`FinalSpecCheck` grows a TEST QUALITY section for sleep-as-fence only** — one new ~30-line block between INTERFACE REACHABILITY (Gap 7) and SPEC.md compliance. Per-language sleep-class greps (Go `time.Sleep` / `<-time.After`, Python `(time|asyncio|trio|anyio|gevent).sleep`, JS/TS `await sleep` / `setTimeout` / `waitForTimeout` / `cy.wait`, Rust `thread::sleep` / `tokio::time::sleep` / `async_std::task::sleep`, Ruby `sleep` / `Kernel.sleep`, Java/Kotlin `Thread.sleep` / `delay` / `Mono.delay` via portable `find ... -exec grep ... {} +` (POSIX-portable; avoids the GNU-only `xargs -r` flag flagged by Codex review)). Each hit needs disposition: (a) the sleep IS the SUT — cite the SPEC.md timing-contract section file:line, OR (b) replaced by a deterministic primitive — cite the primitive's introduction, OR (c) waiver per `.ai/decisions/*.md` naming the test with smell-specific rationale citing a SPEC.md section. "Intentional timing test" without a spec citation is blanket — FAIL. Covers W17.
  - **Reviewer rubric point 3 strengthened across all three reviewers** (`ReviewClaude`, `ReviewCodex`, `ReviewGemini`). Two changes: (1) The Gemini-only sentence "Tests that only validate standard-library or third-party-library behavior instead of the project's own logic are FAIL" is **promoted to ReviewClaude and ReviewCodex** — covers W5 across all three lanes. (2) Each reviewer's point 3 now demands shown-work grep evidence for two semantic smells the audit specifically found: (a) zero-assertion test bodies, (b) DI bypass to `time.Now` / `rand.Read` / stdlib-IO when production code defines a Clock/Random/IO seam. Cite the grep command and its output for each hit AND for the empty-result case (same shown-work standard as point 1 SPEC LITERALS, per PR #249). Reviewer also audits `.ai/decisions/*.md` waivers and verifies cited SPEC.md sections actually contain content supporting the waiver's rationale (defeats the "cite a real section that doesn't actually relate" forgery shape). Covers W4 and W13.
  - **Legacy `FinalSpecCheck` STATUS tail fixed.** The pre-PR-#254 framing at the bottom of FinalSpecCheck (`If fully compliant: STATUS:success / If not: emit STATUS:fail`) contradicted PR #254's inverted contract opening — under last-line-wins parsing, a passing SPEC.md section reached mid-survey could override the early `STATUS:fail` for INTERFACE REACHABILITY or TEST QUALITY. The tail is rewritten to require all three sections to pass before emitting terminal `STATUS:success`, with single-source-of-truth reference to the existing allowlist above (not inline re-enumeration).
  - **What's intentionally NOT in this PR.** W21 (Go subtest case-fold collision) is explicit non-goal — Go-specific lexical bug, golangci-lint territory. No chmod+sha tripwire on Gap 7's rubric file. No integrity preamble. No SPEC.md SHA verification. No DI-bypass cross-check folded into Gap 7's rubric heredoc. No inline TEST QUALITY rubric enumerating Smell 1 (zero-assertion) at FinalSpecCheck. The audit found honest LLM oversight, not adversarial mutation — defense is sized to the observed threat model. Three squad-review rounds taught us what to trim; the shipped PR is ~83 net prompt lines (40 sleep-as-fence block + 33 reviewer-rubric appends + 6 Gemini-sentence promotion + 4 STATUS tail rewrite) — well below v4's ~165-200 and v1's ~330.
  - Spec: `docs/superpowers/specs/2026-05-26-gap8-test-quality-design.md` (v5). Plan: `docs/superpowers/plans/2026-05-26-gap8-test-quality-plan.md`.
  - With Gap 8 landed, seven of eight #233 audit gaps are closed (1, 2, 3, 4, 6, 7, 8). Gap 5 (engine-level `auto_status` audit + `OutcomeHumanOverride` + re-run `FinalSpecCheck` after `ApplyReviewFixes`) remains as the final Chunk C work before `release: v0.31.0` closes out #233.

## [0.30.0] - 2026-05-19

### Changed

- **`build_product` workflow: closed Gaps 2 + 4 from the #233 audit (reviewer rubric overhaul + `VerifyMilestone` reads SPEC.md)** ([#233](https://github.com/2389-research/tracker/issues/233)). PR #246 shipped the cheap trio (Gaps 1, 3, 6); this PR is the next chunk. Additional audit findings from #233 Appendix A are now caught upfront by these two gaps, in addition to what PR #246 already catches (see per-gap Appendix A maps below — these overlap with the #246 set on B1, B3, B5).
  - **Gap 2 — Cross-review prompts (`ReviewClaude`, `ReviewCodex`, `ReviewGemini`, `SynthesizeReviews`) overhauled.** Pre-#233 the three reviewer prompts were 4-8 lines each of free-form focus areas. On the offending run `ReviewGemini` returned "faithful and high-quality realization — PASS / PASS / PASS / PASS" because it read the spec's own ✅ markers as evidence, missing the API-shape blocker (B1), the off-by-one retries (B3), the `fingerprints` scope creep (B4), the red `make lint` (B6), and the dead interface methods (I9, I10). `SynthesizeReviews` then weighted findings by vote count, so a 2-vote PASS could drown a 1-vote FAIL with concrete evidence — synthesis missed ~33 of the 38 real audit findings. Four changes:
    - **All three reviewers now carry the same 5-point structured rubric** (spec literals grep, interface reachability, test-verifies-contract, scope, architecture & leftovers). Each rubric question requires concrete evidence (grep output, file:line, snippet) for any FAIL — free-form lane-specific focus areas come AFTER the rubric, not instead of it.
    - **"Do not trust spec ✅ markers" warning** at the top of every reviewer prompt. Single sentence; would alone have fixed the Gemini failure mode on the offending run.
    - **`ReviewGemini` retargeted to explicit adversarial / steel-man role.** Pre-#233 Gemini's lane was "intent vs letter / advice respected / performance / UX" — the same vague focus that produced the PASS / PASS / PASS / PASS report. Post-#233 Gemini's job is to find what's wrong even when everything looks fine; "faithful and high-quality realization" is a forbidden phrase. Claude stays generalist (missing requirements, architectural violations, leftover artifacts), Codex stays quality-focused (test coverage, edge cases, regression risk). Each reviewer contributes a distinct angle but none can skip the rubric.
    - **`SynthesizeReviews` weights by evidence, not vote count.** A single reviewer with grep / file:line evidence of a contract-level FAIL now wins over two evidence-free PASSes — STATUS:fail flips on one evidence-backed finding, not on majority. The synthesis document now has a dedicated "Evidence-backed findings" section listing single-reviewer concrete-evidence flags so they can't be silently dropped into "Disputed".
    - Catches B1, B3, I3, I9, I10, W3, W6, W7, W8, W10, W14, W16, W18, W19, W20, W22 from #233 Appendix A — every contract-level finding that needed grep / file:line evidence to surface.
  - **Gap 4 — `VerifyMilestone` reads SPEC.md, runs explicit grep checks, applies the test-asserts-contract check.** Pre-#233 the verifier only read `.ai/milestones/current.md` and accepted "tests pass" as evidence of completion — inheriting the implementing agent's blind spot. If `Decompose` dropped a spec requirement during milestone planning, the verifier had no path to discover the gap. New responsibilities:
    1. **Read SPEC.md directly** (not just the milestone notes). The verifier now cross-checks every SPEC.md bullet intersecting this milestone's file list, whether or not the milestone notes mention it.
    2. **Run spec-literal greps inline.** For every literal value in the spec sections this milestone covers, grep the implementation and paste the command + result into the verification report. Missing literals are FAIL.
    3. **Apply the test-asserts-contract check.** For each test touched by the milestone, the verifier asks: "If the production code were deleted and rewritten differently but spec-conformantly, would this assertion still pass for the right reason?" The off-by-one `attempts == 2` pattern is called out by name as a FAIL pattern. Tests asserting fields marked "DO NOT implement" in the milestone (Gap 6 affordances) are FAIL.
    4. **Confirms project CI + tests both passed** by reading `${ctx.tool_stdout}` (TestMilestone already ran both per Gap 1; the verifier does NOT re-run them, just gates on the evidence).
    5. **Walks the diff for out-of-scope work** — files / functions / fields the milestone didn't ask for.
    - Catches B1, B5, I1, I2, I4, I5, W11, W12 from #233 Appendix A — every contract-level finding that should have been caught BEFORE cross-review.
  - Workflow score on `dippin doctor examples/build_product.dip` stays **A / 100/100**, 25 nodes, 49 edges, no new lint warnings. The remaining #233 gaps (5 engine-level `auto_status` audit, 7 interface reachability, 8 `TestQuality` step) are queued for a follow-up PR.

- **dippin-lang dependency bumped v0.28.0 → v0.29.0** ([#250](https://github.com/2389-research/tracker/pull/250)). Picks up the three tool-routing follow-ups deferred from #247 (closes [dippin-lang#42](https://github.com/2389-research/dippin-lang/issues/42), [#43](https://github.com/2389-research/dippin-lang/issues/43), [#44](https://github.com/2389-research/dippin-lang/issues/44)):
  - **dippin#42 (DIP138 / lint suppression):** dippin-lang now suppresses DIP101 / DIP102 coverage warnings on tool nodes that declare `marker_grep:` — the typed routing channel via `ctx.tool_marker` is now recognized as exhaustive in the same way `ctx.outcome = success/fail` pairs are. Workflows that route on `_TRACKER_ROUTE=<marker>` no longer get false-positive grade drops on `dippin doctor`. Optional new advisory DIP138 fires when a tool node uses conditional edges on `ctx.tool_stdout` but declares neither `marker_grep:` nor `outputs:`, pointing authors at the typed routing primitive. Pure dippin-internal change; no tracker code needed.
  - **dippin#43 (parseBoolAttr normalization):** `goal_gate:`, `auto_status:`, `cache_tools:`, and `route_required:` now accept canonical truthy/falsy forms (`true`/`false`, `1`/`0`, `yes`/`no`, `on`/`off`, case-insensitive). Pre-v0.29.0 only `"true"` parsed as true; `yes` / `1` / `TRUE` silently coerced to `false` — a foot-gun on `route_required` especially, where false-negative parsing silently disabled the runtime safety check. Anything outside the accepted set emits a parse diagnostic. Pure dippin-internal change; no tracker code needed.
  - **dippin#44 (Outputs DOT round-trip + adapter passthrough):** `ir.ToolConfig.Outputs` (the comma-separated `outputs:` field declaring the tool's possible stdout values for coverage analysis) is now emitted to DOT (`outputs="pass,fail"`) and parsed back by `dippin migrate`. Tracker's adapter (`pipeline/dippin_adapter.go::extractToolAttrs`) gains the matching passthrough so the field reaches `node.Attrs["outputs"]` for both `.dip` and DOT inputs. The runtime doesn't consume `outputs` yet — this PR is plumbing only, paving the way for future output-set validation. Wire contract matches dippin's `applyToolOutputsAttrs`: `strings.Join(cfg.Outputs, ",")`, omitted when empty. Two new tests (`TestExtractToolAttrs_OutputsForwarded` unit and `TestFromDippinIR_ToolConfigOutputs` end-to-end) follow the v0.28.0 pattern from PR #248.
  - `PinnedDippinVersion` in `tracker_doctor.go` bumped in lockstep so `TestPinnedDippinVersionMatchesGoMod` passes and `tracker doctor`'s dippin-version check matches. All three example pipelines (`ask_and_execute`, `build_product`, `build_product_with_superspec`) remain A grade on `dippin doctor`.

- **dippin-lang dependency bumped v0.27.0 → v0.28.0** ([#247](https://github.com/2389-research/tracker/issues/247)). Picks up the new `ir.ToolConfig` fields `MarkerGrep`, `RouteRequired`, and `OutputLimit` (closes [dippin-lang#39](https://github.com/2389-research/dippin-lang/issues/39)). `pipeline/dippin_adapter.go::extractToolAttrs` now forwards all three to `node.Attrs` using the same wire-contract names dippin-lang's DOT exporter emits (`marker_grep`, `route_required="true"`, `output_limit=<int>`), so DOT ⇄ Dippin IR round-trips stay stable. The tracker runtime already consumes these attrs (`pipeline/node_config.go` for read, `pipeline/engine_run.go` for `EventToolMarkerMissing` / `EventToolRouteMissing` emission) — this PR is the missing adapter passthrough. Pure pass-through: no runtime behavior change, fully backward compatible (workflows that don't declare the new fields produce identical adapter output). `PinnedDippinVersion` in `tracker_doctor.go` bumped in lockstep so `tracker doctor`'s dippin-version check matches.

- **`build_product` workflow: closed three gaps from the #233 audit (Gaps 1, 3, 6 — the "cheap trio")** ([#233](https://github.com/2389-research/tracker/issues/233)). The audit ran `build_product` end-to-end on a real Phase 1 spec and found 38 issues the workflow declared "Done" on — including red CI, a wrong-shape OpenAI request body, an off-by-one retry count, and a Phase-6 feature shipped in Phase 1 with green tests pinning the wrong behavior. This change addresses the three lowest-cost, highest-leverage gaps in `examples/build_product.dip`; the remaining five gaps (reviewer rubric overhaul, `VerifyMilestone` reading SPEC.md, engine-level `auto_status` audit, interface-method reachability, and a `TestQuality` step) are queued for a follow-up.
  - **Gap 1 — project CI is now part of the gate.** `TestMilestone` and `FinalBuild` previously only ran the language-stack default (`go build && go test`, `npm test`, etc.) and silently passed even when the project's own `make lint` / `golangci-lint` was red. Both nodes now probe for a `Makefile` (also `makefile`, `GNUmakefile`) and run the first target that's defined out of `ci` / `check` / `lint`, gating on its exit. Detection parses the Makefile directly via `sed s/#.*//` (strip comments) piped to an `awk` that: (a) skips tab-prefixed recipe lines, (b) skips variable assignments by checking whether the first `:` is part of `:=` / `::=` / `:::=` via `^:+=`, then (c) tokenizes everything before the first `:` and looks for TARGET as a whitespace-delimited token — so `ci:`, `ci check lint:`, and `ci: VAR := overridden` are all correctly detected while `ci := value` and substring collisions like `build-ci:` are not. The initial draft of this PR used `make -n <target>` as the existence probe, but PR #246 review (Codex P1, CodeRabbit Major, four rounds of Copilot) flagged in succession: (1) GNU Make returns 0 when the target name happens to match a file or directory on disk (so a `ci/` or `lint/` directory would false-positive), (2) a missing `make` binary or a Makefile parse error would collapse silently to "target absent" — letting CI-red projects through anyway, (3) the grep replacement missed multi-target rules like `ci check lint:`, and (4) it false-positived on `ci := value` variable assignments. The current sed+awk parser sidesteps all four. When a Makefile is present but `make` isn't installed, the probe now fails loud instead of skipping the gate. When no Makefile or no matching target exists, the node emits an `INFO: no project CI target in <Makefile>` (or `INFO: no Makefile present`) line so operators reading `tracker diagnose` can see the gap rather than assuming CI passed. `FinalBuild`'s timeout bumped from 300s → 600s to accommodate the additional lint pass on real projects. Trade-off accepted: the parser misses targets defined only in included files; CI/lint/check are by convention declared in the root Makefile.
  - **Gap 3 — `Implement` prompt now anchors on spec literals and test quality.** Added three rule blocks. (a) "Spec literals are contracts": for every literal value in the spec section (exact command strings, JSON keys, header names, integer constants, log keys), grep the implementation byte-for-byte before committing — silent paraphrase (`--head <branch>` vs spec's `--head <owner>:<branch>`) is the most common way milestones ship wrong. (b) "Tests verify the contract, not your code": every assertion must still pass for the right reason if the production code is deleted and rewritten spec-conformantly. The canonical failure mode is asserting `attempts == 2` when the spec says "max 2 retries" = 3 attempts — the test green-lights the off-by-one because it was written from the code. (c) "Snapshot tests must be hand-verified": the FIRST version of every golden file must be anchored to the spec, not regenerated from current output; only use UPDATE_GOLDEN after that.
  - **Gap 6 — `Decompose` now produces an explicit "DO NOT implement" list per milestone, and `Implement` reads it.** Added a new milestone field `**DO NOT implement**:` that names Phase 2+ features the spec defers but whose supporting types/functions live in this milestone's file list. `Implement` reads `.ai/milestones/current.md`'s DO NOT lines before writing in any file and leaves those affordances inert (no wiring into call sites, no populating fields the spec marks as empty in this phase, no non-default return values from deferred helpers). Closes the entire "wrote-future-phase-with-green-tests" failure class — e.g., the `fingerprints` field that the audit found populated in Phase 1 despite the spec deferring it to Phase 6 would now appear as a DO NOT line in any Phase 1 milestone touching the trailer file.
  - Workflow score on `dippin doctor examples/build_product.dip` is still **A / 100/100**, no new lint warnings; 25 nodes, all reachable.

### Fixed

- **`tracker validate` no longer prints every DIP1XX lint warning twice** ([#244](https://github.com/2389-research/tracker/issues/244)). Two parallel emission paths fed the same `validator.Lint()` diagnostics through the CLI: `loadDippinPipeline` printed each diagnostic in long form (with `--> file:line:col` location and `= help: ...` suggestion) to stderr, and `pipeline/validate.go`'s `validateGraph` folded the pre-formatted single-line `Graph.LintWarnings` strings into `ValidationError.Warnings`, which `printValidationResult` then re-emitted on stdout. The summary count was correctly de-duplicated (it counted via `lintResult.Diagnostics`, not the concatenation) but the printed list was not — a workflow with 5 DIP1XX warnings showed 10 warning lines. Fix is a print-time dedup at the CLI layer (issue's Option 3): `printValidationResult` builds a set from `graph.LintWarnings` and skips matching entries when iterating `result.Warnings`, leaving only tracker-side semantic warnings (e.g. `validateConditionalFailEdges`, `validateEdgeLabelConsistency`) on stdout. The long-form stderr diagnostic from the loader is the sole user-visible copy. The fix is deliberately NOT a removal of `ve.Warnings = append(ve.Warnings, g.LintWarnings...)` in `validateGraph`: non-CLI consumers of `pipeline.ValidateAll` / `ValidateAllWithLint` (`tracker.ValidateSource`, `tracker_doctor.go::checkPipelineFile` + `checkPipelineBundle`, `cmd/tracker-conformance`) rely on `ve.Warnings` as the single source of pipeline warnings and would silently lose DIP1XX signal otherwise (caught in PR #245 review by Codex P2). Two new regression tests: `TestValidateNoDuplicateLintWarnings` captures both stdout and stderr (via an `os.Pipe`-redirected `os.Stderr` with a single-pass cleanup path and a goroutine that closes its read end and surfaces `io.ReadAll` errors via `t.Fatalf`) and asserts DIP warnings appear on stderr and are not re-emitted on stdout; `TestValidateLintWarningsStillInWarningsChannel` pins the API contract — `pipeline.ValidateAll` must continue to expose DIP1XX warnings via `ValidationError.Warnings` so non-CLI consumers keep seeing them.

## [0.29.2] - 2026-05-18

### Changed

- **dippin-lang dependency bumped v0.26.0 → v0.27.0** ([#242](https://github.com/2389-research/tracker/pull/242)). Picks up the 2026-05-18 model/pricing catalog refresh and the grok-4-1-fast-* redirect fix (callable model IDs survive their target's rename). No IR or adapter changes — drop-in. `PinnedDippinVersion` in `tracker_doctor.go` updated in lockstep so `tracker doctor`'s dippin-version check matches. Combined with v0.29.1's lint deduplication, DIP108 now covers the full current catalog — workflows using `gemini-3-flash-preview`, redirected grok IDs, and other recent models validate clean.

## [0.29.1] - 2026-05-18

### Changed

- **Defer all DIP-coded lint to dippin-lang** ([#239](https://github.com/2389-research/tracker/issues/239), [#240](https://github.com/2389-research/tracker/pull/240)). Deleted `pipeline/lint_dippin.go` + `pipeline/lint_dippin_extra.go` (660+ lines covering DIP101–DIP112, DIP120, DIP121) — every one of those checks was already implemented in `dippin-lang/validator.Lint()`, which tracker has been calling at `.dip` / `.dipx` load time since v0.16. The duplicates had drifted: tracker's `knownProviderModels` catalog hadn't been updated past Gemini 2.5, so any pipeline using `gemini-3-flash-preview` or other current model names produced a false-positive DIP108 warning from `tracker validate` even though `dippin doctor` accepted the same file cleanly. Tracker's local DIP120/DIP121 also semantically collided with dippin-lang's DIP120/DIP121 (different checks under the same code numbers). `validateNodeAttributes` (which re-checked typed IR fields like `max_retries`, `cache_tool_results`, `context_compaction`, `context_compaction_threshold`) is now gated behind `!g.DippinValidated`, matching the existing pattern for DIP001–DIP009 structural checks — so for `.dip` sources tracker fully defers to dippin's typed IR + DIP116 lint instead of re-validating with different (and stricter) semantics. After this change, dippin-lang is the sole authority for DIP-coded lint and adding a new model to the catalog only requires one edit. Tracker keeps `LintTrackerRules` (TRK1XX) — those encode tracker-runtime concerns (64KB tool-output cap, tail-window routing-marker pitfalls) that don't belong upstream. Lint warnings still surface in `tracker validate` / `simulate` / `doctor` output via the new `Graph.LintWarnings` field, which `LoadDippinWorkflowFromIR` populates from `validator.Lint()` and `ValidateAll` appends to its warnings channel — so the user-visible "Validation Warnings" section is unchanged except that the warnings now come from the current dippin-lang catalog instead of tracker's stale copy.

## [0.29.0] - 2026-05-18

### Added

- **Workflow header `requires: <list>` for environmental dependencies** ([#234](https://github.com/2389-research/tracker/issues/234)). Workflows can now declare prerequisites at the top of the `.dip` file via a comma-separated list (e.g. `requires: git`). v0.29.0 implements `git`: when a workflow declares `requires: git`, tracker verifies `git` is installed AND the working directory is a git repository before any node executes. Unrecognized entries (`docker`, `gh`, `jq`, etc.) warn and continue, so workflow authors can forward-declare dependencies that future tracker versions will check. The mechanism lives at the library + CLI boundary, not inside the engine — `pipeline.Preflight` is invoked once at run start; subgraph and `manager_loop` children inherit the parent's check. Requires dippin-lang v0.26.0 ([dippin-lang#35](https://github.com/2389-research/dippin-lang/issues/35), [#36](https://github.com/2389-research/dippin-lang/pull/36)).

- **`--git=auto|off|warn|require|init` CLI flag** to override the policy per run. Default `auto` respects the workflow's `requires:` declaration. `--git=off` bypasses all git checks (escape hatch). `--git=warn` downgrades a hard failure to a warning and continues. `--git=require` forces the check even when the workflow doesn't declare it. `--git=init` (with mandatory `--allow-init` latch in non-interactive runs, or a `[Y/n]` prompt in interactive runs) auto-runs `git init` in the workdir followed by an empty initial commit (ephemeral `-c user.name=tracker -c user.email=tracker@2389.ai`, so the user's git config is not mutated). The initial commit means the resulting repo is immediately worktree-ready: the built-in workflows that run `git worktree add ... HEAD` early on (`ask_and_execute`, `build_product_with_superspec`) would otherwise pass preflight against an unborn-HEAD repo and crash deep in setup after burning user / LLM steps. Safety refusals fire for `$HOME`, `/`, and nested repos — including linked worktrees (where `.git` is a file, not a directory), submodules (same), and bare repos (no `.git` at all). The `$HOME` and root refusals use a case-aware comparison (case-insensitive on Windows so `C:\Users\Bob` and `c:\users\bob` both trip the latch). The repo-satisfaction probe (`checkGit`, "would `git commit` work here?") uses `git -C <dir> rev-parse --is-inside-work-tree`, so bare repos correctly classify as not-a-repo for `requires: git` purposes — work-tree-only operations would fail in a bare repo. The nested-repo safety latch (`safetyLatches`, "is this any kind of git context where `git init` would create a confusing duplicate?") uses `git -C <dir> rev-parse --git-dir`, which catches bare repos, linked worktrees, AND submodules. Two different probes for two different questions, neither a parent-directory walk for `.git`. All git probes run with `LANG=C LC_ALL=C LANGUAGE=C` (`pipeline.GitProbeEnv()`) so the `"not a git repository"` stderr classifier is stable on localized git installations.

- **`tracker doctor` Git Requires check** previews what would happen at run start for the current dir + workflow + flags. Status maps to the policy: `OK` (workflow satisfied, OR auto-init would succeed under `--git=init --allow-init`), `Error` (hard-fail under auto/require/init), `Warn` (downgrade under `--git=warn`), `Skip` (under `--git=off`). The check's `Hint` carries the exact remediation command (`git init`, `tracker <workflow> --git=init --allow-init`, install instructions).

- **Library API: `tracker.Config.Git *GitConfig`** for embedded callers. Zero value resolves to `GitPreflightAuto`. The `GitPreflight` constants are re-exported on the `tracker` package as type aliases of `pipeline.GitPreflight` so consumers don't need to import the pipeline package. `tracker.WithGitConfig(policy, allowInit)` is the equivalent functional option for `tracker.Doctor`. `pipeline.SafetyLatches(ctx context.Context, workDir string) error` is also exported so callers can preview auto-init outcomes without depending on `runAutoInit`; ctx threads into the underlying git subprocesses so a canceled caller aborts cleanly. New `tracker.NewEngineWithContext(ctx, source, cfg)` is the context-aware constructor — `tracker.Run(ctx, ...)` and other ctx-aware callers should use it to get end-to-end cancellation coverage of the v0.29.0 git preflight, including the `--git=init` side effect.

- **`tracker workflows` now shows a REQUIRES column** per built-in workflow.

- **Built-in workflows that commit / branch / merge mid-run declare `requires: git`** — `ask_and_execute`, `build_product`, and `build_product_with_superspec`. Running them in a non-git directory now fails in seconds with a copy-paste remediation message (`git init`, `--git=off`, `tracker <workflow> --git=init --allow-init`), instead of burning $20–$100 of LLM spend before failing at the first git operation.

### Changed

- **dippin-lang dependency bumped v0.25.0 → v0.26.0**. Picks up `ir.Workflow.Requires []string` and the parser / formatter support for the `requires:` workflow header keyword.

### Fixed

- **`manager_loop` now propagates ctx-cancellation errors instead of silently returning `(OutcomeFail, nil)`** ([#227](https://github.com/2389-research/tracker/pull/227)). When the parent context was cancelled, `ManagerLoopHandler.Execute`'s poll loop had a select race between `<-ctx.Done()` and `<-resultCh`: the child engine's handler returned `ctx.Err()`, the engine wrapped it via `fmt.Errorf("handler error at node %q: %w", ...)` (preserving `errors.Is(..., context.Canceled)`), and sent `{result: &EngineResult{Status: OutcomeFail}, err: <wrapped-cancellation>}` to `resultCh` — making both select arms ready. When `<-resultCh` won, `handleChildResult`'s `msg.result != nil` non-success branch silently discarded `msg.err` (designed for strict-failure-edges informational errors) and returned `(OutcomeFail, nil)` — visually indistinguishable from a normal child failure that conditional edges could route on. Surfaced as a `TestManagerLoopHandler_CtxCancellation` flake (3/5 runs failed under `go test ./... -short -count=1` parallelism) but the underlying bug affected production: a `tracker run` with a `manager_loop` node + Ctrl+C during the first poll cycle could see the handler return a clean "fail" outcome and route through conditional failure edges before the parent engine's next-loop `ctx.Err()` check fired. Fix: targeted cancellation guard at the top of `handleChildResult` — scoped on `ctx.Err() != nil` (i.e. the manager_loop's own ctx was canceled), NOT on the shape of `msg.err`, so a child handler's own `context.WithTimeout` firing while the manager_loop ctx is alive still routes through normal failure edges as an ordinary child-internal timeout. Companion fix in `executeNode` captures `outcome.ChildUsage` into the trace entry even when the handler returns a non-nil error, so cancelled child runs contribute their accumulated spend to the parent's `AggregateUsage` and `BudgetGuard` rollup. Non-cancellation `msg.err` (strict-failure-edges) still drops through to the existing path unchanged. Cancellation event audit message normalized to `ctx.Err()` (matches the `<-ctx.Done()` arm; pre-fix the two paths emitted different lines for the same observable event). Four deterministic unit tests pin the contract: parent-ctx cancellation, parent-ctx deadline, child-internal `DeadlineExceeded` with parent-ctx alive (P1 regression guard), and non-cancellation `msg.err` pass-through.

- **`executeNode` now captures `ChildUsage` from handler outcome even on handler error**. Previously, when a handler returned both a non-nil `ChildUsage` and a non-nil error (e.g. the `manager_loop` cancellation path), the engine's error branch set `traceEntry.Status = "error"` and added the entry without setting `traceEntry.ChildUsage`, silently dropping the child's token/cost data from `AggregateUsage` and `BudgetGuard` rollups.

- **Preflight + Doctor now catch unborn HEAD (no commits) up front** (PR #235 round 7, Copilot:3260568737; probe choice refined in PR #237 round 8, Copilot:3260797018 + CodeRabbit:3260803531 + Codex:3260803910). `git rev-parse --is-inside-work-tree` returns true for a `git init`'d repo that has no commits, so pre-fix a `requires: git` workflow could pass preflight against an unborn-HEAD repo and crash mid-run on `git worktree add ... HEAD`, `git merge`, or `git log` after burning LLM turns. The new `pipeline.HasBornHEAD` probe runs `git rev-parse --verify HEAD^{commit}` after `--is-inside-work-tree` succeeds — `^{commit}` forces commit peeling so a HEAD pointing at a non-commit OID (dangling/corrupt) doesn't masquerade as born. Stderr inspection via `isUnbornHEADStderr` matches the two upstream-stable phrases (`"Needed a single revision"`, `"unknown revision or path not in the working tree"`) to distinguish the benign unborn case from real failures (corrupt refs, permissions); corruption-class errors surface as wrapped errors rather than collapsing to "unborn." On unborn HEAD Preflight returns `ErrGitUnbornHEAD` and Doctor reports Error with copy-paste remediation (`git commit --allow-empty -m initial` for an empty baseline, or `git add . && git commit -m initial` to capture existing files). The manual not-a-repo remediation in `buildWorkdirNotRepoMessage` offers both paths explicitly so users with files already in the workdir don't end up with a born-but-empty HEAD that re-trips the worktree workflows. `--git=warn` downgrades to a warning as before.

- **Auto-init refuses in non-empty workdirs** (PR #235 round 7, Copilot:3260568814). `--git=init --allow-init` creates an empty initial commit so HEAD is born, but it does NOT stage user files — in a non-empty workdir that left user content outside HEAD, and worktrees created from HEAD by workflows like `build_product_with_superspec` were silently empty (missing `SPEC.md`, `.ai/decisions/execution-plan.md`, etc.). New Latch 3 in `runAutoInit`: if the workdir contains any entry other than `.git`, refuse with `ErrGitAutoInitRefused` and tell the user to stage their own initial commit (`git init && git add . && git commit -m initial`) so they control what lands in HEAD. The refusal fires **before** `git init` runs — the workdir is unchanged on refusal. We don't auto-`git add -A` because user content can include secrets (`.env`), build artifacts, or anything else they hadn't yet decided to track. `pipeline.WorkdirHasContent` is exported so the Doctor preview can model the same latch and avoid the false-OK case where Doctor reported success but the runtime would refuse.

- **Auto-init `[Y/n]` prompt no longer treats EOF as consent** (PR #235 round 7, Copilot:3260568794). `defaultPromptYN` returned `true` when `bufio.Scanner.Scan()` returned `false` (EOF or read error), so a piped run with no stdin could satisfy the consent latch without the user typing anything. Now `readPromptYN` (the testable inner half) returns `false` on `Scan() == false`; only an actual empty line — successful read of the user pressing Enter — defaults to yes, matching the uppercase Y in `[Y/n]`. The test matrix pins all six outcomes (`eof_refuses`, `blank_line_accepts`, `yes_lower`, `yes_upper`, `no_lower`, `no_upper`).

- **Spec test-plan corrected** (PR #235 round 7, Copilot:3260568849). `docs/superpowers/specs/2026-05-15-tracker-git-preflight-design.md` line 280 previously claimed `--git=init` without `--allow-init` is caught at flag-parse time. It isn't — the `--allow-init` requirement is a preflight-time latch (`pipeline.Preflight` → `runAutoInit`), because interactive (TTY) runs may satisfy it via the `[Y/n]` prompt. Updated to describe the actual behavior and point at the existing `TestRunAutoInit_NeedsAllowInit_NonInteractive` test.

## [0.28.2] - 2026-05-14

Patch release fixing a runaway-agent bug in three of the four built-in workflows. No engine changes.

### Fixed

- **Built-in workflows no longer run an unconstrained agent in `Start` / `Done`** ([#230](https://github.com/2389-research/tracker/issues/230)). `workflows/ask_and_execute.dip`, `workflows/build_product.dip`, and `workflows/build_product_with_superspec.dip` (and their `examples/` mirrors) defined Start/Done as `agent` nodes with `prompt: Initialize pipeline.` / `prompt: Pipeline complete.`. Because the prompt attribute was present, `ensureStartExitNodes` skipped the passthrough handler and these nodes became real codergen sessions — system message limited to the file-path reminder, full tool access (read/write/bash/glob/edit/grep_search), no per-node turn cap. A real `build_product` run was observed spending ~10 minutes and ~39k output tokens inside `Start`, implementing an entire separate Go project from a SPEC.md found on disk, before getting `context canceled` and being classified `outcome: retry`. Dropping the prompt lines makes Start/Done passthroughs (matching `deep_review.dip`, which was already correct). `dippin doctor` scores went A → 100/100 on both `build_product` files; `ask_and_execute` stays at 95 (unrelated warning). The broader engine policy gaps surfaced by this incident — `outcome: retry` on cancellation, no default `max_turns` cap, runaway nodes invisible to `tracker diagnose`, missing tool-call args in `activity.jsonl`, suspect per-node token accounting — are tracked in [#230](https://github.com/2389-research/tracker/issues/230) for separate follow-up.

## [0.28.1] - 2026-05-14

Maintenance release picking up dippin-lang v0.25.0's bundle-load fixes. No tracker-side feature changes; no breaking changes.

### Changed

- **dippin-lang dependency bumped v0.24.0 → v0.25.0**. Picks up the v1.1 `.dipx` format clarifications and three bug fixes that affect tracker's bundle-load path: cycle detection now walks every manifest-listed workflow (was: only entry-reachable, could miss cycles in unreachable workflows that `parseAllWorkflows` had already loaded); `dipx.Open` enriches `ErrManifestInvalid` / `ErrUnsupportedFormatVersion` errors with the bundle path; `dipx.Pack` correctly classifies subgraph parse failures as `ErrSubgraphParse` instead of `ErrEntryParse`. Also adds context-cancellation checks through Open/Pack hot paths. `Source.Workflow` gained a `ctx` parameter (breaking for `Source`-interface consumers); tracker uses `Bundle.Entry()` / `Bundle.Lookup()` directly so no code change needed at the call sites. `PinnedDippinVersion` constant updated to match.

## [0.28.0] - 2026-05-13

This release closes the five-issue follow-up arc from the [#208](https://github.com/2389-research/tracker/issues/208) design review. All additions are backward-compatible: the activity-log relocation (#213) falls back to the legacy path for archived runs, and the new lint (#211) / routing channels (#210, #212) are opt-in.

### Added

- **Activity log integrity hardening** (closes [#213](https://github.com/2389-research/tracker/issues/213)). The audit log used to live at `<workDir>/.tracker/runs/<runID>/activity.jsonl` mode `0o644`, reachable via relative path from any tool subprocess running with `cmd.Dir = workDir` — opening the door to injected `decision_edge` lines, truncated `tool_output_truncated` events, and forged `pipeline_completed` records. Two-part fix: **(A)** live writes now go to `$XDG_STATE_HOME/tracker/runs/<runID>/activity.jsonl` (default `$HOME/.local/state/tracker/`, mode `0o600`; `TRACKER_AUDIT_DIR` override; `%LOCALAPPDATA%` fallback on Windows) — outside any tool subprocess's `cmd.Dir`, so the most common LLM-tool-mistake attack vectors (shell redirection from project root, `find . -name activity.jsonl`) no longer reach it. **(B)** Every line the runtime writes is prefixed with `\x1f\x1e` (`pipeline.ActivityLogSentinel`); `tracker diagnose` and `tracker.Audit` validate the sentinel and surface non-sentinel lines as `SuggestionAuditLogInjection`. On `JSONLEventHandler.Close()` a sentinel-stripped snapshot is written to the legacy run-dir path (mode `0o644`, `O_NOFOLLOW` on unix) so bundle export and git_artifacts still find a readable JSONL file in the run dir. Pre-#213 runs and archived runs without the secure file fall through to the legacy path via `tracker.ResolveActivityLogPath` and parse unchanged — backward compatible. The sentinel scheme is **detection, not authentication**: an attacker who reads tracker's source can emit the bytes, by design. Per-line HMAC (option C in the issue) is explicitly out of scope; the key-management cost is too high for the marginal gain. The threat model is documented in CLAUDE.md under "Activity log integrity."

- **`_TRACKER_ROUTE=` reserved sentinel for convention-based routing** (closes [#212](https://github.com/2389-research/tracker/issues/212)). Complement to `marker_grep:` (#210) for tools that can't change schema or want to opt into typed routing without a node attribute. The runtime scans every tool node's captured stdout for lines matching `^\s*_TRACKER_ROUTE=(.+?)\s*$`, takes the LAST match's captured value, and populates `ctx.tool_route`. Anchored on both ends so an arbitrary `_TRACKER_ROUTE` substring inside other text doesn't match; CRLF-tolerant per-line. Author pattern: emit `printf '_TRACKER_ROUTE=tests-pass\n'` from the tool once the routing decision is known, then route via `when ctx.tool_route = tests-pass`. New optional `route_required: true` node attribute opts in to strict mode — when set AND no sentinel was emitted, the node fails with `OutcomeFail` and emits `EventToolRouteMissing` (with the captured stdout tail for diagnosis) rather than silently falling through. `ctx.tool_route` is LLM-origin (the subprocess emitted it), so it is **not** in the `tool_command` safe-key allowlist and **cannot** be declared as a `writes:` target (the runtime owns it). `tracker diagnose` surfaces a `SuggestionToolRouteMissing` with the recommended fix copy.

- **`TRK101` validate-time lint for risky tool-stdout routing** (closes [#211](https://github.com/2389-research/tracker/issues/211)). New tracker-specific lint rule (TRK1XX namespace, distinct from dippin's DIP1XX) that surfaces the #208 foot-gun shape at `tracker validate` and `tracker doctor` time — before a pipeline ships. Fires on a tool node when ALL of: (1) routes on `ctx.tool_stdout` via exactly one conditional edge, (2) has an unconditional fallback edge, (3) has no `marker_grep:` declared, (4) has no explicit `output_limit:`, (5) command body emits volume (`tee` or `2>&1`). Suggests `marker_grep` (the #210 structural fix) as the primary remediation, then `output_limit:`, then splitting the volume-emitting body from the routing-signal printf, then enumerating every expected marker as its own conditional edge. Heuristics tuned for low false-positive rate via sweep across `examples/*.dip`: skips nodes that also route on `ctx.outcome` (exit code primary signal) and nodes with 2+ conditional edges on `tool_stdout` (exhaustive enumeration is the safely-structured pattern, as in `parallel-ralph-dev.dip`'s `ContractCheck` / `IntegrationTest` validators). Eight unit tests pin the positive case and each skip condition individually.

- **`marker_grep:` node attribute on tool nodes** (closes [#210](https://github.com/2389-research/tracker/issues/210)). Typed routing channel separate from `ctx.tool_stdout`: the runtime applies the declared regex line-by-line to captured stdout, last match wins, and `ctx.tool_marker` is populated with capture group 1 (or the full match if the regex has no groups). With `marker_grep: '^tests-(pass|fail)$'`, routing reads `when ctx.tool_marker = pass` (the captured group), not the whole line — explicit intent, not "whatever the tool happened to print last." If you want the full token, drop the group: `marker_grep: '^tests-pass$|^tests-fail$'` then `when ctx.tool_marker = tests-pass`. If the regex matches nothing, the node fails with `OutcomeFail` and emits `EventToolMarkerMissing` (with the configured pattern + the last 256 bytes of captured stdout for diagnosis) rather than silently falling through to an unconditional edge — the foot-gun removal that's the whole point. Bad regex on the node surfaces via `ctx.tool_marker_error` plus a node fail. `ctx.tool_marker` is LLM-origin (the subprocess emitted it), so it is **not** in the `tool_command` safe-key allowlist — conditions can read it, but tool_command interpolation cannot. Compatible with the existing `output_limit` tail-window: the regex runs over the captured tail, so an end-of-output routing marker survives by construction.

- **Property-based tests for `tailBuffer`** (closes [#214](https://github.com/2389-research/tracker/issues/214)). New dev dep `pgregory.net/rapid` v1.3.0 and `agent/exec/tail_buffer_property_test.go` cover the tail-window invariant across arbitrary write sequences: for any sequence of `Write` calls with total `N` bytes and `limit` `L`, `tb.String()` equals the last `min(N, L)` bytes of the concatenation. A second property pins `Truncated()` and `BytesDropped()` against the same invariant. Generalizes the ~12 hand-rolled example-based boundary tests in `tail_buffer_test.go` to the full state space — catches off-by-one boundary errors, write-boundary state corruption, and ring-buffer wrap-around bugs (the class of bugs PR #215 went through several iterations to get right). 100 random cases per property; fast (< 2ms per property).

## [0.27.0] - 2026-05-13

### Fixed

- **Tool stdout/stderr truncation now keeps the tail, not the head** (closes
  [#208](https://github.com/2389-research/tracker/issues/208)). Pre-fix, the
  per-stream 64KB cap in `agent/exec/local.go` kept the *first* 64KB of
  output, which silently dropped routing markers (`printf 'tests-pass'`)
  past the boundary — pipeline routing then fell through the unconditional
  fallback edge and could ship broken code as if it had passed. The
  notebook_smoke pipeline reproduced this twice in one day. New
  `tailBuffer` ring-buffer keeps the trailing `limit` bytes (O(1) amortized
  per-byte cost, single `limit`-sized allocation, exact tail match
  regardless of write boundaries). `CommandResult` gains structured
  `StdoutTruncated` / `StdoutBytesDropped` / `StderrTruncated` /
  `StderrBytesDropped` fields so callers no longer have to pattern-match
  on an in-band sentinel string. Symmetric for stderr — closes a
  pre-existing zero-stderr-truncation-tests coverage gap. The in-band
  `"...(output truncated at N bytes)"` suffix is gone; consumers must
  read the new flags (or the new `EventToolOutputTruncated` event,
  below) to detect truncation. Drops the unintended-defense head pattern
  surfaced by the security reviewer (head-keep accidentally defended
  against a different attack — see follow-up issue
  [#212](https://github.com/2389-research/tracker/issues/212) for the
  reserved routing-sentinel hardening that closes the new threat-model
  delta).

### Added

- **`EventToolOutputTruncated` activity event** ([#208](https://github.com/2389-research/tracker/issues/208) Tier 1).
  Emitted once per truncated stream after each tool node, with
  `TruncationDetail{Stream, Limit, CapturedBytes, DroppedBytes,
  TotalBytes}`. Written to `activity.jsonl` so `tracker diagnose`,
  `tracker.Audit`, and NDJSON consumers can detect truncation
  retrospectively. `tracker diagnose` surfaces a
  `SuggestionToolOutputTruncated` suggestion explaining the elision,
  pointing at `output_limit` as the escape hatch, and noting the
  tail-window preserves trailing routing markers by construction.

- **`EventConditionalFallthrough` activity event** ([#208](https://github.com/2389-research/tracker/issues/208) Tier 2).
  Fires when at least one conditional outgoing edge from a node was
  evaluated, all evaluated false, and routing fell through to a
  fallback (`label`, `suggested`, or `weight`). Carries the list of
  `ConditionEval{EdgeTo, Condition}` entries that missed. Does NOT
  fire on intentional all-unconditional routing — distinguishes
  "stated routing intent missed" from "fallback is the only option."
  `tracker diagnose` correlates this with `EventToolOutputTruncated`
  on the same node and surfaces a combined suggestion when both
  fire — the canonical diagnostic narrative for the #208 failure
  shape ("your routing marker may have been dropped").

- **Five follow-up issues filed for the broader hardening surface**
  ([#210](https://github.com/2389-research/tracker/issues/210) marker_grep
  primitive · [#211](https://github.com/2389-research/tracker/issues/211)
  validate-time lint for risky stdout-routing patterns ·
  [#212](https://github.com/2389-research/tracker/issues/212) `_TRACKER_ROUTE`
  reserved sentinel · [#213](https://github.com/2389-research/tracker/issues/213)
  activity.jsonl integrity ·
  [#214](https://github.com/2389-research/tracker/issues/214) property tests
  via `pgregory.net/rapid`). Each came out of the 6-expert design panel that
  reviewed the #208 proposed fixes.
## [0.26.0] - 2026-05-12

### Added

- **Native `.dipx` bundle support** (closes the `docs/requests/native-dipx-bundle-support.md` request from the pipelines team). Tracker now accepts content-addressed `.dipx` bundles (produced by `dippin pack`) anywhere it accepts a pipeline file: `tracker validate`, `tracker simulate`, `tracker run`, `tracker doctor`, and `tracker -r <runID>` resume. Pre-fix, tracker read the bundle's ZIP bytes as `.dip` source and failed with bogus `DIP001`/`DIP002` validation errors — the runtime didn't share dippin's understanding of the format, so the integrity guarantees, single-artifact distribution, and audit-trail provenance value of `.dipx` only landed at lint time. New `pipeline.LoadDipxBundle` opens the bundle via `dipx.Open` (SHA-256 verifies every file in `manifest.json` before any content reaches the parser), uses the bundle's pre-parsed `*ir.Workflow` directly (no re-parse of bundled sources), and bypasses the filesystem subgraph walker entirely since dipx already verifies ref closure + acyclicity on `Open`. The bundle's content-addressed identity (`sha256:<hex>`) is stamped onto every line of `activity.jsonl` (engine emissions, parallel/manager_loop emissions that bypass the engine's emit chokepoint, and agent/llm JSONL writes that bypass both — three composable layers so every line of audit output carries provenance), persisted into `checkpoint.json` for resume verification, and surfaced in `tracker list` (new `Bundle` column) and `tracker audit` (new `Bundle:` header line). Bundle identity is exposed on `tracker.Result.BundleIdentity` and `tracker.RunSummary.BundleIdentity` for embedded library callers. Resume against a `.dipx` strictly verifies the stored identity matches the one being resumed — mismatch aborts with both hashes shown so the operator can pick the right artifact; `--force-bundle-mismatch` is the escape hatch (loud warning to stderr). Bare-name resolution (`tracker build_product`) still resolves `.dip` first, then file, then built-in — `.dipx` is dispatched explicitly by extension on full paths. Because the identity is computed deterministically over manifest bytes and verified on every `Open`, a `tracker validate` pass on a CI bundle gives the same answer as the production run.

### Changed

- **dippin-lang dependency bumped v0.23.0 → v0.24.0** for the new `dipx` package (`Open`, `Bundle.Workflow`, `Bundle.Identity`). `PinnedDippinVersion` in `tracker_doctor.go` updated to match so `tracker doctor`'s version-mismatch check reflects the new pin.
- **`pipeline.LoadDipxBundle` now returns diagnostics instead of writing to `os.Stderr`.** The library API no longer prints to the process-global stderr; the signature gains a `[]validator.Diagnostic` return so embedded callers can route them through their own logger. CLI callers (`cmd/tracker/loadDipxPipeline`, `tracker doctor`'s bundle check) print to stderr as before. Mirrors the existing `pipeline.LoadDippinWorkflow` contract for the `.dip` path.

## [0.25.1] - 2026-05-11

### Changed

- **Gemini SSE parser coalesces split finish + usage chunks into a single
  `EventFinish`.** Follow-up polish to the earlier trailing-usage fix:
  when an upstream emits the finish reason and the `usageMetadata` in
  two separate chunks (the 2389 Bedrock Gateway does this; real Google
  can too), the parser now buffers the finish reason in
  `geminiStreamState.pendingFinish` instead of emitting it immediately.
  When the trailing usage chunk arrives, both are emitted together as
  one event. A `flushPendingFinish` helper on `*geminiStreamState`
  guarantees the buffered reason is emitted before every early-return
  path — clean stream exit, scanner error, and JSON parse error — so
  partial-failure streams still produce a terminal `EventFinish` ahead
  of the `EventError`, preserving the prior behavior for accumulator
  bookkeeping. The combined-chunk path also defensively clears
  `pendingFinish` to guard against a hypothetical split-then-combined
  upstream emitting a duplicate finish at stream end. Net effect: the
  `llm finish` trace line now prints exactly once per turn regardless of
  upstream chunking shape, fixing the duplicate-line cosmetic artifact
  called out in the Fixed entry below. Four new regression tests pin the
  behavior end to end (`TestAdapterStreamTrailingUsageChunkEmitsSingleFinish`
  for the split case; `TestAdapterStreamFinishWithoutUsageChunk` for the
  no-trailing-usage case; `TestAdapterStreamCombinedAfterSplitClearsPending`
  for the defensive pending-clear; `TestAdapterStreamParseErrorFlushesPendingFinish`
  for the parse-error flush ordering). Also extracts a `usageFromMeta`
  helper since the same `geminiUsageMeta` → `*llm.Usage` conversion now
  happens at three call sites.

- **Bedrock Gateway integration guide refreshed** for upstream gateway fixes
  [#4](https://github.com/2389-research/gateway/issues/4) and
  [#5](https://github.com/2389-research/gateway/issues/5) (closed
  2026-04-30). The gateway now accepts both Cloudflare AI Gateway native
  routing prefixes (`/anthropic`, `/openai`, `/google-ai-studio`,
  `/compat`) and Gemini's `/v1beta/models/...` paths, so tracker's
  `--gateway-url` flag works end-to-end against
  `https://bedrock-gateway.2389-research-inc.workers.dev` and
  `provider: gemini` is no longer broken. Smoke-tested with a
  single-agent dip pipeline: `provider: anthropic` and `provider: gemini`
  both completed against the live gateway. `docs/bedrock-gateway.md`
  rewritten to lead with the recommended `--gateway-url` recipe; the old
  "Why not `--gateway-url`?" section removed; the compatibility matrix
  flips Gemini to working; the "404 on every request" and "Gemini
  `/v1beta` 404" troubleshooting entries dropped. The `provider: openai`
  (Responses API) row stays as broken pending gateway
  [#3](https://github.com/2389-research/gateway/issues/3), which was
  reopened after we discovered it had been auto-closed by an unrelated
  commit's "Fix #3" wording referring to a bot-review item, not the
  GitHub issue.

### Fixed

- **Gemini token usage no longer reports 0 when the upstream emits
  `usageMetadata` as a standalone trailing SSE chunk.** Tracker's
  `llm/google/adapter.go` SSE parser bailed on any chunk with no
  `candidates` array, which dropped trailing usage-only chunks on the
  floor — so `StreamAccumulator` only saw the candidate chunks (with no
  usage attached) and the final `Usage{}` came out empty. Surfaced while
  smoke-testing tracker against the [2389 Bedrock Gateway](https://github.com/2389-research/gateway)
  where the gateway's `:streamGenerateContent?alt=sse` reply is three
  chunks: text → `finishReason:"STOP"` → `usageMetadata`. The accumulator
  contract already supports `processFinish` being called twice (first
  sets `finishReason`, second updates `usage` without overwriting
  reason), so the fix is a 10-line patch in `processSSELine`: when a
  candidate-less chunk carries `UsageMetadata`, emit a usage-only
  `EventFinish`. End-to-end verified against the live bedrock gateway —
  a single-agent `provider: gemini` smoke run now reports
  `1,408 in / 4 out` instead of `0 in / 0 out`, and tracker's
  per-provider cost rollup is correct (no double-counting because
  `AggregateUsage` folds per-node `SessionStats`, not per `TraceEvent`).
  Net visible artifact: the `llm finish` trace line now prints twice on
  affected gateways — first with `reason=stop` and no tokens, second
  with `tokens=N/N` and no reason — but the final accumulated state is
  correct. New regression test `TestAdapterStreamTrailingUsageChunk`
  pins the trailing-chunk case end-to-end through
  `StreamAccumulator.Response()`.

## [0.25.0] - 2026-05-05

### Added

- **Architect-side machinery for local codegen** (PR #198). New agent-tool primitive `TerminalTool` lets a tool flag itself as the terminal step of an agent session — the runtime breaks the loop the moment it succeeds (after the same turn's tool batch, but before the next LLM call), avoiding wasted post-dispatch turns. New `agent/tools/dispatch_sprints` reads a `{path, description}` JSONL plan and runs the per-sprint author+audit pipeline once per line via a deterministic in-tool loop with bounded retry+backoff for retryable provider errors (5xx / rate-limit / timeout / network); non-retryable errors bubble out so the agent can react. New `agent/tools/write_enriched_sprint` calls a mid-tier LLM (Sonnet by default) once per sprint with a 4-strategy SEARCH/REPLACE matcher (exact → indent-preserving → whitespace-insensitive → fuzzy with Levenshtein ratio ≥ 0.9), partial-apply semantics that distinguish `PATCHED-PARTIAL` from clean `PATCHED`, and a tolerant audit-verdict parser that handles `AUDIT-VERDICT:` anywhere in the first 10 non-empty lines (markdown decoration, leading prose, fence-wrapped output all tolerated). Companion `agent/tools/generate_code` calls a cheap/fast model (default `gpt-4o-mini`, override via `TRACKER_CODEGEN_MODEL`) to expand a contract into one or more files. All four tools land via env-gated registration in `pipeline/handlers/backend_native.go` keyed on `TRACKER_SPRINT_WRITER_MODEL` / `TRACKER_CODEGEN_MODEL`. Validated end-to-end on Notebook synthetic (41/41 pytest passing, ~$2, 28min) and NIFB architect-only (16 sprints, Pattern B autonomously, ~$5, 47min). Includes path-traversal guards (new `resolveUnderRoot` helper with symlink evaluation) covering both write paths and contract-file reads, and uniform reservation of the `Completer` interface across the agent and tools packages via a type alias to prevent silent divergence.

- **Self-healing JSON extraction cascade for declared writes** (PR #201). When an LLM responds with prose instead of valid JSON for a node with `writes:`, the runner now attempts: (1) direct JSON parse; (2) extraction of any ```...``` fenced block whose content parses as a JSON object — iterating fences via a strict-shape regex so a `text`/`bash` preamble doesn't block discovery of a later `json` fence and stray inline backticks in prose don't kick off extraction; (3) balanced-brace scan for the first top-level `{…}` span that parses as an object (handles prose with stray brace pairs around real JSON without picking the wrong span; `{` inside JSON-string values and inside `[…]` arrays are correctly skipped via state tracking); (4) single-key fallback to the raw response with a `writes_warning` so the pipeline survives. Multi-key writes still hard-fail since prose can't be distributed. The fallback is gated on "no extractable JSON found" — a model that returned valid JSON missing the declared key gets a hard contract failure with a specific error, not a silent fallback. Fallback values are capped at 8 KiB to keep large tool stdout out of `status.json` / `activity.jsonl` / checkpoints. Driven by an `analyze_spec` failure on the NIFB run where the agent wrote `.ai/spec_analysis.md` but responded "Done — …" in prose; the runner used to hard-fail on the first character of the response, now heals and surfaces a warning.

- **Bedrock Gateway integration guide** (PR #200). New `docs/bedrock-gateway.md` walks through pointing tracker at the [2389 Bedrock Gateway](https://github.com/2389-research/gateway) Cloudflare Worker — per-provider `*_BASE_URL` recipes, a provider compatibility matrix (anthropic and openai-compat work; openai's Responses API and gemini's `/v1beta` paths don't, with workarounds), authentication via Cloudflare AI Gateway tokens, and verification guidance pointing at the CF AI Gateway dashboard rather than `tracker doctor` (which doesn't echo the resolved base URL).

### Changed

- **`writes:` declarations are rejected when they collide with reserved key names** (PR #201). Two reserved sets: (a) the `tool_command` safe-key allowlist (`outcome`, `preferred_label`, `human_response`, `interview_answers`), exposed via the new `pipeline.IsToolCommandSafeCtxKey` accessor — letting a workflow declare `writes: outcome` would funnel LLM-controlled content into a reserved name and bypass the sanitization that keeps LLM output out of shell input; (b) the writes-signal keys (`writes_error`, `writes_warning`) — runtime observability that `tracker diagnose` and `when ctx.writes_error != ""` edges branch on; allowing a workflow to set them via writes would let an LLM spoof failure/healed signals. Collision rejection runs before any value is written and fails the node. No existing pipelines used these collisions.

### Fixed

- **`tracker doctor` provider probe restored to 16-token max output** (PR #199, mdagost). The probe had been using `maxTok := 1`, but OpenAI's Responses API requires `max_output_tokens >= 16` and returns HTTP 400 (`Invalid 'max_output_tokens': integer below minimum`) below that — breaking `tracker doctor` for OpenAI keys entirely.

## [0.24.2] - 2026-05-03

### Fixed

- **ACP `CreateTerminal` now validates commands against the built-in denylist and constrains `cwd` to the working directory** (PR #197). Previously an LLM-directed ACP agent could execute arbitrary commands via `CreateTerminal`, completely bypassing the denylist/allowlist that protects `tool_command`. Bare denylisted commands (e.g. `eval` with no args) are also blocked. Error code corrected to `-32602` (Invalid Params) matching `ReadTextFile`/`WriteTextFile`.
- **Claude Code backend kills subprocess process group on pipeline cancellation** (PR #197). Added `SysProcAttr.Setpgid`, `cmd.Cancel` (SIGKILL to process group), and `WaitDelay` to prevent orphaned `claude` subprocesses consuming API credits after ctrl-C or budget breach.
- **`TRACKER_PASS_API_KEYS` now requires `=1`** instead of any non-empty value (PR #197). Previously `TRACKER_PASS_API_KEYS=false` or `=0` silently leaked all API keys to the claude subprocess. `tracker doctor` env warning updated to match.
- **Engine fails on unknown outcome status instead of treating as success** (PR #197). The `default:` case in `handleOutcomeStatus` previously called `MarkCompleted`, silently promoting handler bugs to success. Now emits `EventStageFailed` and sets `OutcomeFail`.
- **Pipeline goroutine panic recovery** (PR #197). `runPipelineAsync` now has `defer/recover` so a handler panic produces a clean error instead of crashing the TUI without checkpoint save.
- **`PinnedDippinVersion` updated to `v0.23.0`** to match `go.mod` (PR #197). `tracker doctor` was telling users to install v0.21.0.
- **`DefaultModel` updated to `claude-sonnet-4-6`** (PR #197). Was still `claude-sonnet-4-5`.
- **Autopilot LLM calls now respect pipeline context cancellation** (PR #197). All call sites used `context.Background()` — pipeline cancellation had no effect on in-flight autopilot requests. New `ContextSetter` interface threads the pipeline context without changing the `LabeledFreeformInterviewer` contract.
- **Example `manager_loop_child.dip` updated for `steer.*` namespace** (PR #197). References `${ctx.steer.hint}` instead of the broken `${ctx.hint}` after PR #196's rename.
- **`escapeOsascript` now escapes newlines** to prevent injection in macOS notification strings (PR #197).
- **Removed stale comment in `human.go`** that incorrectly claimed CLAUDE.md was wrong about `questions_key` (PR #197).

### Changed

- **`stack.manager_loop` `steer_context` keys are now namespaced under `steer.*`** (closes #177). Previously a manager_loop's `steer_context: { outcome: "fail" }` injected a bare `outcome` key into the running child's `PipelineContext`, which collided with the four safe-allowlisted bare ctx keys (`outcome`, `preferred_label`, `human_response`, `interview_answers`) that `tool_command` variable expansion permits. The threat: today `steer_context` is static at `.dip` parse time so collisions are author-controlled, but if a future feature lets steer values come from LLM output an attacker-controlled value could reach a shell command via `${ctx.outcome}`. Fix is option B from the issue: a new `namespaceSteerKeys` helper in `pipeline/handlers/manager_loop.go` rewrites every parsed key with the `SteerContextKeyPrefix = "steer."` prefix before it lands in `cfg.steerKeys`, so the collision is impossible by construction — bare safe-allowlist keys stay reserved for legitimate node-level outcomes, steered values flow through `steer.*` and are blocked from tool_command expansion (the namespace isn't on the allowlist). The transform is idempotent (already-namespaced keys aren't double-prefixed) and applies uniformly via `parseManagerLoopConfig`. Authors who want to read steered values in prompts / conditions / `--max-cost` lookups now reference `${ctx.steer.<key>}`. **Behavior change:** any pipeline that today reads a steer-injected value via the bare-key form (e.g. `${ctx.hint}` after `steer_context: { hint: "..." }`) needs updating to `${ctx.steer.hint}`. Mixed-form input (`hint=a,steer.hint=b` in the same `steer_context`) is rejected at parse time with `ErrAmbiguousSteerKey` rather than picked nondeterministically by Go map iteration order. Five regression tests pin (a) bare keys get prefixed, (b) the transform is idempotent and nil-safe, (c) attempting to steer one of the four safe-allowlist keys (`outcome`, `preferred_label`, `human_response`, `interview_answers`) lands as `steer.<safekey>` so the bypass is closed end-to-end, and (d) the bare/prefixed collision case is rejected loudly.

## [0.24.1] - 2026-04-24

### Fixed

- **Claude Code backend now reports cache-token usage from the NDJSON result envelope** (closes #185 Track A). The Claude CLI already emits `cache_read_input_tokens` and `cache_creation_input_tokens` in its `result` NDJSON message, but `storeResult` was silently dropping them — so `llm.EstimateCost` priced every input token at the fresh rate. For the canonical heavy-cache workload (Sonnet 4.5 + CLAUDE.md injection on every turn with stable prompt caching, typically 60–90% cache-read by input token count) that resulted in a ~3× overcount on the input side of per-node cost. Fix: `ndjsonUsage` gains `CacheReadInputTokens` + `CacheCreationInputTokens` JSON fields; `storeResult` populates the matching `*int` pointers on `llm.Usage` when non-zero so `EstimateCost` prices cache reads at 10% and cache writes at 25% of the input rate (Anthropic pricing convention). `TotalTokens` stays fresh-input + output to match the convention in `llm/anthropic/translate_response.go` — cache tokens are tracked separately, priced independently, and deliberately kept out of the token total so `BudgetGuard`'s `--max-tokens` semantics stay consistent across backends. Two new regression tests pin the populated-from-NDJSON case and the back-compat case (no cache fields → nil pointers, unchanged total).

### Added

- **`TRACKER_ACP_CACHE_READ_RATIO` env var for ACP cost-estimate tuning** (closes #185 Track B). The ACP protocol doesn't report cache tokens and the tracker-side heuristic can't observe them, so estimated ACP input was priced entirely as fresh — conservative (never under-reports) but up to ~3× high for workloads where the bridge keeps a stable context cached. Setting `TRACKER_ACP_CACHE_READ_RATIO` to a value in `(0, 1]` tells `estimateACPUsage` what fraction of the estimated input tokens to route to `CacheReadTokens` (priced at 10% of the input rate) instead of `InputTokens`. Typical values: `0.5`–`0.8` for stable-context Claude workloads. Default (unset or out-of-range) keeps the conservative behavior. Out-of-range values log a one-time warning and are ignored. Seven regression tests pin the split math across unset, sub-1, exactly-1, negative, >1, and non-numeric inputs.
- **`--tool-denylist-add <glob>` CLI flag + `tool_denylist_add` graph attribute** (closes #168; completes the deferred `WorkflowDefaults.ToolDenylistAdd` adapter wiring from v0.24.0 #181). Operators and workflow authors can now extend the built-in tool-command denylist (eval, pipe-to-shell, curl|sh, etc.) with additional glob patterns for defense in depth — previously the only way to block a new pattern without forking tracker was to restrict via `--tool-allowlist`, which inverts the default. `CheckToolCommand` now takes an extra-deny-patterns arg that checks alongside the built-ins. Interaction rules: user-added patterns cannot remove any built-in, `--bypass-denylist` still disables everything (built-in + user-added — it's the all-or-nothing escape hatch), and user-added patterns are evaluated before the allowlist so a command must pass both gates. Plumbing mirrors the allowlist exactly: repeatable CLI flag with comma-separated value support, `handlers.GraphAttrToolDenylistAdd = "tool_denylist_add"` constant, `mergeToolDenylistAdd` union-with-dedup of CLI + graph patterns, adapter-side wiring from `ir.WorkflowDefaults.ToolDenylistAdd` into `graph.Attrs["tool_denylist_add"]`, `parseGraphCommaList` shared parser factored out so the allowlist and denylist-add paths can't drift on whitespace/trim semantics. Help text + preamble logging note the security posture (additive block for defense in depth; `--bypass-denylist` still disables).
- **Estimated-usage flag plumbed from ACP backend through trace → CLI → TUI → NDJSON** (closes #186). The `ACPUsageMarker` introduced in v0.24.0 was written into `llm.Usage.Raw` but had no downstream readers — `llm.Usage.Add` and `buildSessionStats` both dropped `Usage.Raw`, so the CLI summary, TUI header, and NDJSON cost events saw a single dollar figure with no way to distinguish heuristic ACP spend from metered native/claude-code spend. Fix: `pipeline.SessionStats` gains `Estimated bool` + `EstimateSource string`; `pipeline.ProviderUsage` and `pipeline.UsageSummary` gain `Estimated bool`; `pipeline.CostSnapshot` gains `Estimated bool`. `buildSessionStats` calls a new `extractEstimateMarker` helper to populate `Estimated`/`EstimateSource` from `Usage.Raw` before the value is lost. `Trace.AggregateUsage` OR-propagates the flag across sessions and child-usage rollups — a single estimated session taints both its per-provider bucket and the summary-level flag, so a mixed native+ACP run is correctly labeled as "not fully metered". Surfaces: CLI "Tokens by Provider" table suffixes estimated providers with `(estimated)` and renders total cost as `~$X.XXXX (estimated — heuristic spend on at least one provider)`; `printTotalTokens` now emits `~$X.XX usage` whenever any session was heuristic (not just the pre-existing Max-subscription-only case); TUI header's cost badge prefixes with `~` for estimated runs; NDJSON `cost_updated` and `budget_exceeded` events carry `CostSnapshot.Estimated`. Three new test suites cover the propagation — `TestBuildSessionStats_PropagatesACPEstimatedMarker` in transcript_test.go, `TestTraceAggregateUsage_EstimatedPropagation` in trace_test.go (4 sub-tests), and `TestPrintTotalTokens_*` in cmd/tracker (3 tests). Not in scope (per the issue): changes to `llm.Usage.Add`'s `Raw` handling — the flag is now carried by `SessionStats` forward; `Usage.Raw` remains an implementation detail only read by `extractEstimateMarker` at the single point where `agent.SessionResult` is consumed.

## [0.24.0] - 2026-04-24

### Added

- **ACP estimator counts reasoning chunks and tool-call payloads** (closes #184). Previously `estimateACPUsage` only saw the collected assistant text (`handler.textParts`), so multi-turn tool-heavy sessions systematically under-reported usage — often by 10–100× for the canonical coding-agent workload (extended-thinking models, repeated tool loops). `acpClientHandler` now tracks three additional rune counters advanced at event time: `reasoningRunes` (advanced by `handleThoughtChunk`), `toolArgRunes` (advanced by `handleToolCallStart` from the JSON-formatted `RawInput`), and `toolResultRunes` (advanced by `handleToolCallUpdate` on completed or failed status from the tool's content + `RawOutput`). Counters are `int` — we store sums, not the underlying text — so memory cost is O(1) per channel regardless of output volume. `estimateACPUsage` folds them in: reasoning + tool-args contribute to `Usage.OutputTokens` (matching how providers price extended thinking today), tool-results contribute to `Usage.InputTokens` (the bridge re-sends tool output as next-turn input context), and reasoning additionally populates `Usage.ReasoningTokens` for future catalog-level per-reasoning pricing. The remaining intrinsic undercount — bridge-injected system prompt + tool-schema definitions — is documented in `docs/architecture/backends.md` and requires a bridge-specific `Meta` extension we don't have.
- **ACP backend surfaces approximate per-prompt token usage** (closes #167). The Agent Client Protocol spec (github.com/coder/acp-go-sdk v0.6.x) has no usage surface — `PromptResponse` carries only `StopReason`+`Meta`, and no `SessionUpdate` subtype reports tokens — so ACP-backed nodes previously returned `SessionResult.Usage` zero-valued. `CodergenHandler.trackExternalBackendUsage` routes ACP usage to `llm.TokenTracker.AddUsage("acp", ..., model)` (the model arg is new this release — see the `claude-code`/`acp` Provider-wiring bullet below). `estimateACPUsage` synthesizes `llm.Usage` from rune counts (UTF-8 aware via `unicode/utf8`; `ceil(runes/4)` applied per side) and populates `EstimatedCost` via `llm.EstimateCost`. The estimator's channel coverage is described in full in the #184 entry above; the initial cut counted only the assistant text stream and the PR #189 follow-up extended it to reasoning + tool-call argument/result payloads. Remaining intrinsic undercount: the bridge's own injected system prompt + tool schemas are invisible to the heuristic (they never flow through `cfg.Prompt`/`cfg.SystemPrompt`). A one-time log line per `ACPBackend` instance announces that ACP token/cost numbers are estimates. `--max-tokens` now enforces against ACP sessions; `--max-cost` enforces when `cfg.Model` is a catalog-known ID (see `EstimateCost` warning below). `Usage.Raw` is tagged with `ACPUsageMarker{Estimated:true, Source:"acp-chars-heuristic", Ratio:4}` for consumers that inspect `SessionResult.Usage` directly, but `llm.Usage.Add` and `pipeline/handlers/transcript.go:buildSessionStats` both drop `Usage.Raw`, so the marker is currently write-only from the trace/CLI/TUI perspective — plumbing an explicit "estimated" flag through `SessionStats`/`ProviderUsage`/the TUI header is tracked as a follow-up.
- **`Provider` field now set on `SessionResult` for `claude-code` and `acp` backends.** Previously `backend_claudecode_ndjson.storeResult` and `buildACPResult` left `SessionResult.Provider` empty, which caused `pipeline.Trace.AggregateUsage` to bucket their usage under the `"unknown"` provider in per-provider rollups and CLI summaries. Set to `"claude-code"` / `"acp"` respectively, matching what `trackExternalBackendUsage` already uses as the `TokenTracker` provider key. Dashboards and library consumers reading `EngineResult.Usage.ProviderTotals` will now see a populated `"claude-code"` / `"acp"` bucket instead of everything collapsing into `"unknown"`.
- **`trackExternalBackendUsage` now threads `cfg.Model` into `TokenTracker.AddUsage`** for the `claude-code` and `acp` backends. Previously the model arg was omitted, so `TokenTracker.CostByProvider`'s resolver fell back to `graph.Attrs["llm_model"]` (often empty for workflows that set models per-node) and priced at $0. As a result, library consumers reading `tracker.Result.Cost.ByProvider["claude-code"|"acp"]` saw `$0.00` even when the session computed a nonzero `EstimatedCost`, and `BudgetGuard`'s `--max-cost` ceiling was silently non-binding for those backends. Both paths now price correctly against the model the node actually ran under.
- **`llm.EstimateCost` logs a one-time warning per unknown model** when `GetModelInfo` returns nil and usage is non-zero. Previously returned `$0` silently, which violates the project's "never silently swallow errors" rule (CLAUDE.md) and hid the real consequence: `--max-cost` ceilings can't apply to usage priced under a model that isn't in the catalog. The warning names the unknown model once and spells out the budget implication.
- **Built-in example pipelines for `stack.manager_loop`** (closes #175). `examples/manager_loop_demo.dip` + `examples/subgraphs/manager_loop_child.dip` exercise the full `subgraph_ref` + poll interval + steering path against a real child pipeline. Both grade A via `dippin doctor`, and the Makefile doctor target runs them so adapter-path regressions on the new v0.22.0 IR attrs trip CI instead of silently rotting.
- **Diagnostic warning when both unprefixed + legacy `manager.*` attrs are set** on the same manager_loop node (closes #176). Surfaces accidental shadowing (author migrates some attrs to the v0.22.0 unprefixed contract but leaves the legacy form in place) without changing the unprefixed-wins precedence.
- **`warnUnknownStackChildKeys` diagnostic** on `stop_condition` and `steer_condition` expressions (closes #176). Scans for `stack.child.<word>` references and warns when the subkey isn't one of the three tracker actually publishes (`status`, `cycles`, `exit_status`). Catches typos that would silently evaluate to empty.

### Changed

- **dippin-lang dependency bumped v0.22.0 → v0.23.0**. Upstream ships [DIP28 tool-safety defaults](https://github.com/2389-research/dippin-lang/releases/tag/v0.23.0): `ir.WorkflowDefaults` now exposes `ToolCommandsAllow` and `ToolDenylistAdd` fields so `.dip` authors can declare tool-safety constraints at the workflow level instead of reaching for DOT or the library API. `extractWorkflowDefaults` in `pipeline/dippin_adapter.go` wires `WorkflowDefaults.ToolCommandsAllow` → `graph.Attrs["tool_commands_allow"]` (the consumer side has been ready since #164). Closes the adapter-side follow-up noted in v0.23.0's own #164 entry. `ToolDenylistAdd` wiring is deferred until the matching `--tool-denylist-add` CLI flag lands (#168).
- **Docs relocated under `docs/architecture/`** (closes #165). `docs/pipeline-context-flow.md` → `docs/architecture/context-flow.md` and `docs/manager-loop.md` → `docs/architecture/handlers/manager-loop.md`. Every inbound link in `README.md`, `ARCHITECTURE.md`, `CLAUDE.md`, `CHANGELOG.md`, and the `docs/architecture/` tree is updated; the `handlers.md` "tracked in #165 for a later PR" placeholder is removed and `architecture/README.md`'s "may move under `architecture/` in a later PR" note is retired.

### Fixed

- **`stack.manager_loop` nodes no longer bypass `--max-tokens` / `--max-cost` budgets** (closes #188). Same shape of bug as #183 / PR #187 fixed for the subgraph handler: `ManagerLoopHandler.Execute` was constructing its child engine without `WithBudgetGuard` + `WithBaselineUsage`, and the handler's `Outcome` returned no `ChildUsage`. Operator-configured token and cost ceilings were therefore silently non-binding for any work nested in a manager_loop supervisor — the canonical place where long-running token piles form, since manager_loop is specifically designed for cycle-heavy async supervision (Attractor spec 4.11). Fix mirrors PR #187: `Execute` now reads `pipeline.ChildRunContextFromContext(ctx)` and threads the parent's `BudgetGuard` + baseline usage into the child engine, and `handleChildResult` sets `Outcome.ChildUsage = result.Usage` on every return path (success, fail, budget-exceeded). A child-side `OutcomeBudgetExceeded` is mapped to parent `OutcomeSuccess` (with ChildUsage attached) — the same strict-failure-edges avoidance reasoning as the subgraph fix. Three new regression tests mirror the subgraph suite's coverage: usage rollup into parent `ProviderTotals`, delayed parent-halt after the manager_loop overspends, and mid-loop child-guard halt via baseline + partial trace exceeding the ceiling.
- **Subgraph nodes no longer bypass `--max-tokens` / `--max-cost` budgets** (closes #183). Pre-fix, a pipeline author could place cost-intensive nodes inside a subgraph and both the token and cost ceilings became silently non-binding: the child `pipeline.Engine` was constructed without `WithBudgetGuard`, so its between-node checks were no-ops; and `SubgraphHandler.Execute` returned an `Outcome` with no usage rollup, so the parent trace's `AggregateUsage` missed all child spend, preventing the parent's guard from firing either. Fix: (a) `Outcome` and `TraceEntry` gain a new `ChildUsage *UsageSummary` field; `Trace.AggregateUsage` folds it into both the running totals and per-provider buckets so parent-level rollups see child spend; (b) the engine stashes its `BudgetGuard` plus a snapshot of already-consumed `UsageSummary` on `ctx` via `ChildRunContextFromContext` (only when a guard is configured — no overhead for unbudgeted runs), so handlers that launch child runs can propagate them; (c) the engine gains `WithBaselineUsage(*UsageSummary)`, which folds an external baseline into the child's `checkBudgetAfterEmit` snapshot — child guards now evaluate `parent-consumed + child-trace` against the limits, matching the operator's intent; (d) `SubgraphHandler.Execute` wires its child engine with `WithBudgetGuard` + `WithBaselineUsage` from the ctx, and returns `Outcome.ChildUsage = result.Usage` regardless of child outcome. A child-side `OutcomeBudgetExceeded` is propagated to the parent as `OutcomeSuccess` with child usage attached so the parent's own guard fires on the next between-node check (returning `OutcomeFail` here would trip the strict-failure-edges rule before the budget check could run). Four regression tests pin the three enforcement paths (parent-level rollup, late parent-halt after subgraph overspends, mid-subgraph child halt via baseline) and a two-level-nested case. Not yet addressed: mid-stream enforcement inside a single `Prompt()` call — the guard still fires only between nodes; and `manager_loop` handler has the same shape and likely needs the same treatment (filing as a follow-up).
- **CLAUDE.md `questions_key` default matches code** (closes #163). CLAUDE.md § Interview mode now accurately states `questions_key` defaults to `interview_questions` with `last_response` as a read-time fallback inside `resolveAgentOutput`. Previously claimed `last_response` as the primary default, which contradicted `resolveInterviewKeys` in `pipeline/handlers/human.go`. The drift-note block in `docs/architecture/handlers/human.md` flagging this mismatch is removed.
- **"Escalation" terminology reconciled across docs** (closes #166). CLAUDE.md § Claude Code backend no longer lists `escalate` as a pipeline outcome (actual outcomes: `success`, `fail`, `retry`, plus engine-level `budget_exceeded`) and cross-links to `docs/architecture/engine.md#escalate` for the routing-convention framing. The outcome table in `context-flow.md` is updated to match. This completes the audit started in `engine.md:370` which already had the canonical "not a distinct outcome status" framing.
- **`steer_context` keys with `:` rejected at adapter time** (closes #171). Dippin-lang's block-form formatter writes entries as `key: value`, so a colon in a `steer_context` key breaks `.dip → IR → .dip` round-trip; the upstream parser drops such keys with a diagnostic. `flattenSteerContext` in `pipeline/dippin_adapter.go` now returns `ErrInvalidSteerContextKey` so authors fail loudly at graph-build time instead of silently losing keys downstream.
- **`manager_loop` nodes with nil `ir.ManagerLoopConfig` fail at graph-build time** (closes #174). `convertNode` previously let a nil Config flow through `extractNodeAttrs` as a no-op, producing a graph node without `subgraph_ref` that only surfaced at Execute-time as a vague "subgraph not found" error. Returns `ErrMissingManagerLoopCfg` instead. Scoped to `manager_loop` only; same pattern may extend to other kinds in follow-ups.
- **Adapter rejects Parsed-only conditions that format to parenthesized expressions**. The pipeline edge evaluator tokenizes on plain `strings.Split("||")` / `strings.Split("&&")` and does not support parens — `a || (b && c)` silently mis-evaluates as unknown variables with empty-string results. `convertEdge` now returns `ErrParenthesizedParsedCondition` at adapter time so authors get a hard error up front; workaround is to populate `Condition.Raw` with a flat form (`a=1 || b=2 || c=3`) or simplify the Parsed tree to not emit parens.

## [0.23.0] - 2026-04-22

### Fixed

- **`formatManagerLoopConditionExpr` now emits `&&` / `||` instead of English `and` / `or`** (PR #170 round-2 review; closes part of #172). The formatter is called when an `ir.Condition` has only `Parsed` populated (Raw empty), producing the text that flows into `pipeline.EvaluateCondition`. The evaluator only recognizes Go-style boolean operators, so a Parsed-only fallback was silently mis-evaluated as a single opaque clause. Programmatically-built IR workflows that didn't populate `Raw` are now correctly evaluated. `CondNot` continues to emit `not ` (the evaluator's native negation). New test `TestFormatManagerLoopCondition_EvaluatorCompatibility` pins the formatter→evaluator round-trip for `CondAnd`, `CondOr`, and `CondNot`.
- **`managerAttr` uses comma-ok lookup** so an explicit empty string on the unprefixed key wins over a non-empty legacy `manager.*` value (closes #173). The previous zero-value check (`if v := attrs[key]; v != ""`) silently fell through to the legacy prefix, letting authors accidentally resurrect values they thought they had cleared. New test `TestManagerAttr_EmptyStringPrecedence` pins all four combinations (explicit empty, missing, legacy-only, unprefixed-wins).
- **`parseManagerLoopConfig` distinguishes "empty" from "invalid" `steer_context`** (PR #170 round-2 review). When `steer_condition` is set and `steer_context` parses to zero entries, the error now reports "steer_context %q is invalid" with the raw value if it was non-empty, and "steer_context is empty — nothing to inject" only when truly unset.
- **`tool_commands_allow` graph attribute is now wired into the tool handler allowlist** (closes #164). CLAUDE.md documented this path ("`--tool-allowlist` CLI flag or `tool_commands_allow` graph attr"), but the graph-attr side was never plumbed. `registerToolHandler` now reads `graph.Attrs["tool_commands_allow"]` (comma-separated glob patterns, whitespace tolerant), unions it with the CLI-supplied `--tool-allowlist` patterns, and passes the combined list to `NewToolHandlerWithConfig`. Authors can set the attr via DOT (`graph [tool_commands_allow="git *,make *"]`) or programmatically on `Graph.Attrs`; denylist-wins invariant is preserved (a graph attr of `*` does NOT unblock `eval` or `curl | sh`). Dippin-lang IR does not yet expose this field — `.dip` authors must use DOT or the library API until upstream ships `ir.WorkflowDefaults.ToolCommandsAllow`.

### Added

- **`ir.NodeManagerLoop` adapter support + dippin-lang v0.22.0 bump** (closes #162). `.dip` authors can now declare `stack.manager_loop` supervisors directly via the new IR kind. `pipeline/dippin_adapter.go` maps `ir.NodeManagerLoop` → `shape=house` → `handler=stack.manager_loop` and flattens `ir.ManagerLoopConfig` into the six unprefixed DOT attrs the handler consumes: `subgraph_ref`, `poll_interval`, `max_cycles`, `stop_condition`, `steer_condition`, `steer_context`. `steer_context` uses canonical sorted `k=v,k=v` with percent-encoding for the three reserved chars (`,` → `%2C`, `=` → `%3D`, `%` → `%25`) — mirrors dippin-lang v0.22.0 `export.flattenSteerContext` exactly so DOT round-trips (adapter ↔ dippin-lang migrator) stay lossless. When a manager_loop is the workflow's Start or Exit, `ensureStartExitNodes` overrides the shape to `Mdiamond`/`Msquare` but the handler (`stack.manager_loop`) and flat attrs are preserved so the supervisor still executes. The `ManagerLoopHandler` now accepts both the unprefixed v0.22.0 contract names and the legacy `manager.*` prefixed variants for backward compatibility; unprefixed wins when both are set. `parseSteerContext` percent-decodes reserved chars so lossless round-trips complete through the handler. Semantic note: `PollInterval == 0` and `MaxCycles == 0` in the IR degrade to tracker's handler defaults (45s / 1000) rather than the IR-documented "event-driven" / "unbounded" modes; tracker has no such modes today. Partial steering configs (`steer_condition` without `steer_context`, or vice versa) are now rejected at parse time — previously one half of the pair would silently render the supervisor inert.
- **`--bypass-denylist`, `--tool-allowlist`, `--max-output-limit` CLI flags for tool command sandboxing.** The underlying denylist, allowlist, and per-stream output ceiling were already enforced by `pipeline/handlers/tool_safety.go` and `ToolHandlerConfig`, but only via node-attr and library APIs — the CLI paths were missing. `--bypass-denylist` (bool, default `false`) disables the built-in denylist and prints a loud stderr warning on startup; use only in sandboxed environments where dangerous patterns (eval, pipe-to-shell, curl|sh) are intentional. `--tool-allowlist <pattern>` is repeatable and accepts comma-separated glob patterns; every tool command statement must match at least one allowlist entry when the flag is set. Allowlist entries are additive with any `tool_commands_allow` graph attr and never override the denylist. `--max-output-limit <bytes>` sets the hard ceiling (default 10MB) applied to per-node `output_limit:` attrs. Node-attr and graph-attr paths remain unchanged; these flags are additive CLI surface.

## [0.22.0] - 2026-04-22

### Added

- **`tracker-swebench analyze <results-dir>` subcommand** (closes #141). Bulk-triage tool for completed SWE-bench runs: reads `predictions.jsonl`, `logs/*.log`, and the optional empty-patch diagnostic files from PR #150, then emits a structured report covering (1) overall resolved/unresolved/empty/error counts with percentages, (2) per-repo breakdown matching the #116 baseline table, (3) top-10 empty-patch instances with termination reason and final-message snippets from #139 diagnostics, (4) top-10 longest unresolved instances sorted by turns and elapsed time, and (5) error class distribution consuming the setup/patch/harness split from #140. Auto-detects a SWE-bench evaluator JSON report (`resolved_ids` field) to distinguish resolved from unresolved; gracefully degrades to "patched but unverified" classification when no evaluator report is present. Gracefully degrades on missing empty-patch diagnostics with a one-line note pointing to the PR #150 runtime. `--json` emits the structured `AnalyzeReport` for downstream tools. Pure artifact analysis — does not require access to the SWE-bench dataset.
- **Typed `NodeConfig` accessors on `*pipeline.Node`** (closes #142, #143, #144; partial #19). New methods `AgentConfig(graphAttrs)`, `ToolConfig()`, `HumanConfig()`, `ParallelConfig()`, and `RetryConfig(graphAttrs)` return typed structs parsed from `Node.Attrs` with the graph-default-then-node-override merge centralized. Numeric parse failures are lenient (zero-value, no panic) to preserve existing permissive behavior. Three-state booleans (e.g. `ReflectOnError`, `VerifyAfterEdit`, `PlanBeforeExecute`, `CacheToolResults`) expose companion `*Set` flags so callers can distinguish "explicitly configured" from "absent".

### Changed

- **Codergen handler now consumes `AgentNodeConfig`** instead of calling 8 separate `apply*` methods that each re-parsed `Node.Attrs` directly. Graph→node override resolution happens once in the accessor; `buildConfig` just copies typed fields into `agent.SessionConfig`. Replaces `applyModelProvider`, `applySessionLimits`, `applyReasoningEffort`, `applyResponseFormat`, `applyCacheAndCompaction`, `applyReflectOnError`, `applyVerifyConfig`, and `applyPlanningConfig` with a single typed consumer. No behavior change; existing codergen tests pass unchanged.
- **`Engine.maxRetries` uses the typed `RetryConfig` accessor** instead of duplicating `strconv.Atoi` over `node.Attrs["max_retries"]` → `graph.Attrs["default_max_retry"]`. The fallback default (3) is unchanged.
- **Human, tool, and parallel handlers now consume typed configs** (closes #145; finishes #19). `human.go` (12 → 3 `node.Attrs[...]` reads), `tool.go` (4 → 2), and `parallel.go` (5 → 0) route through `HumanConfig()`, `ToolConfig()`, and `ParallelConfig()` accessors. The remaining direct reads are semantically distinct: `tool.parseTimeout` / `parseOutputLimit` return errors on malformed values that the silent-default accessor can't express, and the three `human.go` holdouts (`default` vs `default_choice` disambiguation) each have an inline comment explaining why the typed accessor's unified `DefaultChoice` can't be used in that specific call site. `parseBranchOverrides` in `parallel.go` still receives the full `Attrs` map by design because it scans for a `branch.N.*` key prefix rather than specific fields.
- **`HumanNodeConfig.DefaultChoice`** now resolves `default_choice` first, then falls back to `default` — centralizes a two-key lookup that was duplicated across the human handler.
- **`ToolNodeConfig` gains `Timeout time.Duration`**; **`ParallelNodeConfig` gains `JoinID string`, `MaxConcurrency int`, `BranchTimeout time.Duration`** so the remaining tool and parallel reads can go through the typed accessor.
- **Tool node `timeout` attribute now errors when the tool node executes if set to a zero or negative duration** (closes #151). This is a behavior change. Previously such values reached `context.WithTimeout` and caused immediate cancellation with a confusing "command timed out" error; `ToolHandler.parseTimeout` now returns `node %q has non-positive timeout %q: must be > 0` instead. Validation runs inside `ToolHandler.Execute` (before the command is dispatched), not at workflow load time. Pipelines that wrote `timeout: "0"` (unlikely but possible) will now error when the run reaches that tool node — configure a positive duration or omit the attr to use the handler default.

## [0.21.0] - 2026-04-21

### Added

- **Declarative `writes:` / `reads:` unified structured output** (closes #85). Agent, human, and tool nodes can now declare the keys they produce and consume. Declared writes are extracted from handler output into the pipeline context and validated — missing required fields fail the node. `reads:` pins fidelity for the keys a node consumes so downstream nodes see consistent data. New helpers: `pipeline/context_writes.go`, `pipeline/handlers/declared_writes.go`. Replaces node-type-specific workarounds previously needed to thread typed outputs through.
- **`tracker.SimulateGraph(ctx, graph)`** (closes #108) — graph-in variant of `Simulate` that accepts a pre-parsed `*pipeline.Graph` and returns a `SimulateReport`. Lets callers that already parsed the pipeline (CLI flows that also run `ValidateSource`, tooling that builds a graph programmatically) avoid a second parse. `Simulate(ctx, source)` is now a thin wrapper over `parsePipelineSource` + `SimulateGraph`; signature and behavior unchanged.
- **Repository localization pre-processing** (agent, closes #95): optional pre-processing phase that scans the working directory for files relevant to the task prompt and injects a structured context block before the first LLM turn. Pure text analysis + filesystem scan — zero LLM calls. Opt-in via `SessionConfig.Localize` (default `false`). Extracts file paths, camelCase/snake_case identifiers, quoted phrases, and error-line excerpts from the prompt; capped at 10 files / ~2KB injected context with 5-line snippets. Reduces wasted turns on `glob`/`grep` for repository-level tasks.
- **Agent episodic memory across retries/resumes** (closes #96): native codergen sessions now record a structured per-tool episode log (`tool`, args, success/fail, summary), publish `episode_summary` and rolling `episode_summaries` context keys at session end, and inject prior summaries into subsequent retry/resume attempts so the model can avoid repeating failed approaches.
- **Plan-before-execute phase** (agent, closes #97): optional single planning LLM call before the main execution loop. Opt-in via `SessionConfig.PlanBeforeExecute` (default `false`) or codergen node attrs (`plan_before_execute: "true"` or `plan: "true"`). The generated plan is retained in conversation context for subsequent execution turns.
- **Library API godoc, stability policy, and runnable examples** (closes #110). Package-level `doc.go` now documents pre-1.0 API stability expectations; README gains a stability callout; `tracker_examples_test.go` ships runnable `ExampleDiagnose` / `ExampleAudit` / `ExampleDoctor` examples that double as godoc content.
- **Test coverage close-out for `Diagnose` / `Audit` / `Doctor`** (closes #107). Covers `DiagnoseMostRecent`, `MostRecentRunID`, `ResolveRunDir` no-match path, corrupted `status.json` warning, `Audit` error paths (missing / malformed / empty run dir), `Doctor` warnings sentinel, and `checkArtifactDirs` non-ENOENT stat errors.

### Changed

- **`tracker simulate` output is now deterministic** (closes #111). Graph-level attributes in the simulate header are now sorted alphabetically; orphan/unreachable nodes in the node table are appended in sorted order. Previously both depended on Go's random map iteration order, producing different diffs on each run.
- **`MostRecentRunID` no longer writes to `os.Stderr`** from library paths (#107 follow-up). Parse warnings now route through `DiagnoseConfig.LogWriter` so library callers aren't surprised by stray stderr.

### Fixed

- **`tracker simulate` now parses the pipeline source exactly once** (closes #108). Previously `runSimulateCmd` parsed twice — once for the validation-warnings section, again inside `tracker.Simulate`. That risked a TOCTOU mismatch between the two views, duplicated dippin-lang parser side effects (lint warnings printed twice), and burned extra CPU on large `.dip` files. The CLI now reads the source once, calls `tracker.ValidateSource` for `{Graph, Errors, Warnings}`, and hands the same graph to `tracker.SimulateGraph`. CLI stdout is byte-identical to before; only the duplicated parser-logging lines are gone.
- **Cost accounting and reporting are now consistent across runtime and CLI summaries** (closes #128):
  - CLI run summaries now read token/cost totals from `EngineResult.Usage` (trace aggregate) instead of `TokenTracker.TotalUsage().EstimatedCost`, so cost is shown correctly.
  - Repair turns now apply the same `EstimateCost` compensation path used by normal turns when providers omit `EstimatedCost`.
  - OpenAI SSE `response.completed` now preserves `ReasoningTokens` in finish usage events.
  - Gemini adapter now falls back to the requested model when `modelVersion` is absent in API responses.
  - Trace usage aggregation now attributes missing providers to `unknown` instead of dropping those sessions from per-provider totals.
  - External backend usage tracking now records sessions with non-zero input/output tokens even when `TotalTokens` is zero.

## [0.20.0] - 2026-04-21

### Added

- **`stack.manager_loop` handler — async child-pipeline supervision** (PR #126, Attractor spec 4.11). A supervisor node that launches a child pipeline in a goroutine, polls at a configurable interval, and optionally steers the running child by injecting context mid-execution. New attributes: `subgraph_ref`, `manager.poll_interval`, `manager.max_cycles`, `manager.stop_condition`, `manager.steer_condition`, `manager.steer_context`. Exposes `stack.child.status` / `stack.child.cycles` / `stack.child.exit_status` to parent context. Emits `EventStageStarted` on launch, `EventManagerCycleTick` per poll cycle, and `EventStageCompleted` / `EventStageFailed` on terminal outcomes (success, child fail, child crash, max_cycles exceeded, cancellation, stop/steer condition invalid). Bounded `childJoinGrace` (30 s) protects against non-context-aware child handlers hanging the manager. See `docs/architecture/handlers/manager-loop.md`.
- **Engine steering channel** (PR #126): new `pipeline.WithSteeringChan(chan map[string]string)` engine option. Between node executions, the engine drains the channel and merges updates into the run's `PipelineContext`. Used by `manager_loop` to inject context into running children; available to any supervisor. Non-blocking drain; nil channel is a no-op.
- **`PipelineContext.MergeWithoutDirty`** (PR #126): writes updates without marking keys as dirty, so externally-injected values never leak into any node's per-node scope. Used by the engine's steering drain so injected keys stay in the global/bare namespace.
- **Accurate cost estimation via catalog + cache token pricing** (PRs #127, #128): `EstimateCost` now resolves prices through the model catalog (`GetModelInfo`) instead of a duplicated hardcoded map. Adds cache token pricing: cache reads at 10% of input rate, cache writes at 25%. `TokenTracker` now records the observed model per provider (`AddUsage` takes an optional model arg, normalized through the catalog to match `WrapComplete`) so per-provider cost estimates use the right rate sheet instead of a global fallback.
- **Model catalog April 2026 refresh** (PR #128): adds `claude-opus-4-7`, `gpt-5.4-mini` / `gpt-5.4-nano`, `gpt-4.1` family, `o3`, `o4-mini`, GA Gemini 2.5 models, and `gemini-3.1-pro-preview` (replaces the shut-down `gemini-3-pro-preview`). Fixes `claude-opus-4-6` pricing (was incorrectly $15/$75; now $5/$25). Context windows for Sonnet/Opus 4.6 bumped to 1M. `claude-sonnet` / `claude-opus` aliases now point at the latest 4.7 entries. `claude-haiku-4-5`, `gpt-4o`, and `gpt-4o-mini` added (they were in the old pricing map but not the catalog).
- **`docs/architecture/handlers/manager-loop.md`**: user-facing documentation for the manager-loop handler — lifecycle diagram, configuration reference, context outputs, event semantics, steering contract, and tuning guidance.
- **`tracker-swebench` now captures the active provider base-URL override** in `run_meta.json` (`BaseURLOverride`). Derived from `${PROVIDER}_BASE_URL` with hyphens normalized to underscores, so `--provider openai-compat` maps to `OPENAI_COMPAT_BASE_URL` consistently with `ResolveProviderBaseURL`. Useful for reproducing SWE-bench runs that routed through a Cloudflare AI Gateway or custom endpoint.

### Fixed

- **ACP path validation rejects `..` path segments before symlink resolution** (PR #126, security hardening). Previously, a symlink pointing outside the work dir plus a `..` in the target path could escape the sandbox: symlink resolution occurred before the check, and `..` in the resolved path was not filtered. `validatePathInWorkDir` now splits on both `/` and `\` so Windows paths are also protected.
- **Manager loop: poll timer vs. child-completion race** (PR #126 review): when `pollTimer.C` and `resultCh` are both ready, Go's `select` is nondeterministic. The timer path could trigger `max_cycles` failure even though the child had already finished. The timer case now does a non-blocking drain of `resultCh` first and dispatches to the child-result handler if the child is done.
- **Manager loop: crash path always returns a non-nil error** (PR #126 review). If the child goroutine delivered neither a result nor an error, the handler synthesizes `"manager_loop: child exited with no result and no error"` so callers never see `(OutcomeFail, nil)`.
- **Manager loop: config validation now hard-fails on malformed values** (PR #126 review). `manager.poll_interval` and `manager.max_cycles` with invalid or non-positive values now error at parse time instead of silently falling back to defaults (previously: `time.ParseDuration` error swallowed, zero/negative values ignored).
- **Manager loop: `EvaluateCondition` errors surface for both `stop_condition` and `steer_condition`** (PR #126 review). A malformed expression now fails the loop with a clear error plus an `EventStageFailed` emission, instead of being treated as "never match" until `max_cycles`.
- **Manager loop: emit `EventStageFailed` on context cancellation and condition-parse errors** (PR #126 review). Parity with other terminal failure paths (max_cycles, child fail, child crash) so the TUI surfaces every failure mode.
- **Manager loop: `handleChildResult` returns `OutcomeFail` on child failure** (PR #126 review). Handler-level outcome values must be from the handler set (`success`/`fail`/`retry`); engine-level statuses like `OutcomeBudgetExceeded` would have fallen through the outcome switch and been silently treated as success. The real child status remains available via `pctx.Set("stack.child.exit_status", ...)`.

## [0.19.0] - 2026-04-20

### Added

- **Library API hardening for v1.0** (#102, #103, #104, #106, #109):
  - Typed enum-like strings for `CheckStatus` and `SuggestionKind` so consumers can switch-exhaust. Existing constants (`SuggestionRetryPattern`, etc.) retain their underlying string values.
  - `tracker.WithVersionInfo(version, commit)` functional option replaces the CLI-only `DoctorConfig.TrackerVersion` / `TrackerCommit` fields.
  - `DiagnoseConfig.LogWriter` / `AuditConfig.LogWriter` — optional `io.Writer` for non-fatal parse warnings. Nil is treated as `io.Discard` so library callers no longer see stray warnings on `os.Stderr`. The `tracker` CLI sets this to `io.Discard` for user-facing commands. `Doctor` has no warnings to suppress so it deliberately does not carry a `LogWriter` field.
  - `Doctor`, `Diagnose`, `DiagnoseMostRecent`, `Audit`, `Simulate` now accept `context.Context`, honored by provider probes and binary version lookups. `getBinaryVersion` now uses `exec.CommandContext` with a 5-second timeout, matching `getDippinVersion`.
  - Provider probe error bodies are now sanitized (API keys and bearer tokens stripped) before they land in `CheckDetail.Message`.
  - `NDJSON` handler closures (pipeline, agent, LLM trace) now `recover()` from panics in the underlying writer so a misbehaving sink cannot crash the caller goroutine. Panic suppression is per-`NDJSONWriter` instance (not package-level), so one misbehaving sink cannot silence unrelated writers in the same process.
  - `Diagnose` now streams `activity.jsonl` with `bufio.Scanner` instead of `os.ReadFile` → `strings.Split`, matching `LoadActivityLog` and avoiding a memory spike on large runs. Scanner errors (1 MB line-length overflow, I/O) and `ctx.Err()` now propagate out of `Diagnose` as a real error — partial reports are never returned as success, so automation with deadlines can distinguish complete from truncated analysis.
- **Workflow params via `${params.*}` with CLI/library overrides** (closes #81): top-level Dippin `vars` now map to graph attrs under `params.<key>`, making them available in agent prompts, tool commands, and edge conditions through `${params.key}` interpolation. Added repeatable `--param key=value` on the CLI plus `tracker.Config.Params` for library callers; overrides hard-fail on unknown keys at startup and run summaries print effective overridden params. New lint rules DIP120 (undeclared `${params.*}` reference) and DIP121 (declared but unused var).
- **Per-human-gate timeout / timeout_action in `.dip`** (closes #112): the dippin-lang v0.21.0 IR exposes `HumanConfig.Timeout` and `HumanConfig.TimeoutAction`; the adapter copies them into `node.Attrs["timeout"]` / `node.Attrs["timeout_action"]` where `pipeline/handlers/human.go` already consumed them. The `examples/human_gate_test_suite.dip` Makefile lint skip is removed.
- **Workflow-level budget ceilings from `.dip`** (closes #67): dippin-lang v0.21.0 adds `WorkflowDefaults.MaxTotalTokens`, `WorkflowDefaults.MaxCostCents`, and `WorkflowDefaults.MaxWallTime`. The adapter now maps them to `graph.Attrs["max_total_tokens"]` / `["max_cost_cents"]` / `["max_wall_time"]`, and `tracker.ResolveBudgetLimits` uses them as a fallback when `Config.Budget` and the matching `--max-*` CLI flags are zero. Explicit config values still win. Wired through both the library engine builder and the CLI's console/TUI engine builders.
- **TUI pre-populates subgraph children in the sidebar** (closes #118): subgraph reference nodes previously appeared as opaque single rows until child `stage_started` events arrived. `buildNodeList` now accepts the `subgraphs` map and recursively flattens child graphs with prefixed IDs (`Parent/Child/...`), preserving user-set labels and parallel/fan-in flags. Lazy insertion remains as a fallback with a cycle guard for self-referential subgraph maps.
- **Agent quality-of-life improvements from SWE-bench work**:
  - **Turn-budget checkpoints**: optional guidance messages injected at configurable fractions of the turn budget (50%, 75%) to reduce thrashing on hard instances.
  - **Two-phase verify-after-edit**: focused test first, broad regression test second, with a configurable repair retry budget. Models the pattern top SWE-bench agents use.
  - **Tool polish**: `grep` gets context lines, noise-dir filtering, and truncated-match count; `read` gets `offset`/`limit` for paged access; `edit` shows nearby context on a miss.
  - **Process safety**: tool subprocess groups are killed after the shell command completes, preventing orphan zombies on timeouts.
  - **SWE-bench harness**: agent event logging + transcript capture; checkpoint and verify config threaded into `agent-runner`.
  - **Config defaults promoted**: `DefaultConfig()` now uses `MaxTokens: 16384`, auto-continue on truncation, and `LoopDetectionThreshold: 4` — values measured effective in SWE-bench Lite (59.0% → 70.3% baseline shift).
  - New CLI flag `--artifact-dir` overrides the node state directory.

### Changed

- **dippin-lang dependency bumped from `v0.20.0` → `v0.21.0`.** Picks up three upstream fixes tracked as dippin-lang#18/#20/#21 (PRs #22/#23) plus release issue #25. `PinnedDippinVersion` constant updated to match. Closes tracker#75 transitively — dippin lint now recognizes `${ctx.node.<id>.*}` scoped reads as valid without tracker-side changes.
- **BREAKING** (library):
  - `tracker.Doctor(cfg)` → `tracker.Doctor(ctx, cfg, opts...)`.
  - `tracker.Diagnose(runDir)` → `tracker.Diagnose(ctx, runDir, opts...)`.
  - `tracker.DiagnoseMostRecent(workdir)` → `tracker.DiagnoseMostRecent(ctx, workdir, opts...)`.
  - `tracker.Audit(runDir)` → `tracker.Audit(ctx, runDir)`. (No config struct — Audit emits no suppressible warnings. Use `ListRuns` + `AuditConfig{LogWriter}` for bulk enumeration.)
  - `tracker.Simulate(source)` → `tracker.Simulate(ctx, source)`.
  - `tracker.ListRuns(workdir)` now accepts optional `...AuditConfig`.
  - `tracker.NDJSONEvent` → `tracker.StreamEvent`. Wire-format JSON tags unchanged.
  - `NDJSONWriter.Write` now returns `error` so callers can detect a broken stream. First failure is still logged to `os.Stderr` once (unchanged behavior); subsequent failures are surfaced via the return value.
  - `DoctorConfig.TrackerVersion` and `DoctorConfig.TrackerCommit` removed — use `tracker.WithVersionInfo(version, commit)` instead.
  - `CheckResult.Status` and `CheckDetail.Status` are now typed as `tracker.CheckStatus` (underlying string). Untyped string literal comparisons (`status == "ok"`) keep working.
  - `Suggestion.Kind` is now typed as `tracker.SuggestionKind` (underlying string).
- `tracker diagnose` suggestion order is now deterministic (alphabetical by node ID). Previously suggestions printed in Go map-iteration order, which varied between runs.

### Fixed

- **OpenAI Responses API: `function_call_output` and `function_call` items now always serialize required fields** (closes #114). Previously the shared `openaiInput` struct used `omitempty` on every field, so a tool returning an empty-string result produced `{"type":"function_call_output","call_id":"..."}` with no `output` field, and a no-argument tool call produced `function_call` with no `arguments`. OpenAI's endpoint tolerated this, but OpenRouter's strict Zod validator rejected the requests with `invalid_prompt` / `invalid_union` errors, symptomatic on GLM, Qwen, and Kimi via OpenRouter. Fixed by replacing the `omitempty`-tagged single struct with a `MarshalJSON` method that emits only fields valid per item type, with required fields always present. Reported by @Nopik.

## [0.18.0] - 2026-04-17

### Added

- **CLI↔library feature parity — Phase 1 (NDJSON) + Phase 2** (#76, PR #101). Four CLI commands (`diagnose`, `audit`, `doctor`, `simulate`) and the NDJSON event writer are now public library APIs. Library consumers can reuse the CLI's behavior without shelling to a binary and parsing printed output.
  - `tracker.NewNDJSONWriter(io.Writer)` — public NDJSON event writer producing the same wire format as `tracker --json`. Factory methods `PipelineHandler`, `AgentHandler`, `TraceObserver` return handlers that plug into `Config.EventHandler`, `Config.AgentEvents`, and the LLM trace hook. Closes Phase 1.
  - `tracker.Diagnose(runDir)` / `tracker.DiagnoseMostRecent(workDir)` — structured `*DiagnoseReport` with node failures, budget halt, and typed suggestions (`Kind: "retry_pattern" | "escalate_limit" | "no_output" | "shell_command" | "go_test" | "suspicious_timing" | "budget"`).
  - `tracker.Audit(runDir)` — structured `*AuditReport` with timeline, retries, errors, and recommendations.
  - `tracker.ListRuns(workDir)` — sorted `[]RunSummary` for enumerating past runs (newest first).
  - `tracker.Doctor(cfg)` — structured `*DoctorReport` for preflight health checks. `ProbeProviders` defaults to false; set true to make real API calls for auth verification. `CheckDetail.Status` has four values: `"ok"`, `"warn"`, `"error"`, and `"hint"` (informational sub-items such as optional providers not configured).
  - `tracker.PinnedDippinVersion` — exported constant exposing the dippin-lang version pinned in `go.mod`.
  - `tracker.Simulate(source)` — structured `*SimulateReport` with nodes, edges, execution plan, graph attributes, and unreachable-node list.
  - `tracker.ResolveRunDir(workDir, runID)` / `tracker.MostRecentRunID(workDir)` — exposed run-directory resolution helpers.
  - `tracker.ActivityEntry` / `tracker.LoadActivityLog(runDir)` / `tracker.ParseActivityLine(line)` / `tracker.SortActivityByTime(entries)` — shared activity.jsonl parsing used by CLI and library.

- **SWE-bench harness (`cmd/tracker-swebench`)**: a new orchestrator binary that evaluates tracker's agent against the SWE-bench dataset. Includes a Dockerfile and build script for the base image, container lifecycle management with SIGTERM handling and orphan cleanup, dataset JSONL parsing, results writer with resumability, container resource limits (CPU/memory) and `--platform` pinning, secure `--env-file` for API keys (replacing `-e` flags), instance-ID validation + scoped container names, integration test for the dataset-to-results pipeline, and an in-container `agent-runner` binary that captures all changes via `git diff` (including new files).

- **`WithExtraHeaders` option for Anthropic and OpenAI adapters**: injects custom HTTP headers (e.g., `cf-aig-token`) for gateway auth. Used by the swebench harness to forward `CF_AIG_TOKEN` from the host through the container to the agent-runner.

### Fixed

- `classifyStatus` now correctly returns `"fail"` for budget-halted runs (runs with a `budget_exceeded` activity event were previously mis-classified as `"success"`).
- `NDJSONWriter.AgentHandler` now preserves the original `agent.Event.Timestamp` instead of re-stamping with `time.Now()`, preventing event reordering in the NDJSON stream.
- `simBFSNodeOrder` now sorts orphan nodes by ID before appending, making `SimulateReport.Nodes` ordering deterministic.
- `ResolveRunDir` now always returns an absolute path via `filepath.Abs`, matching its documented contract.
- `MostRecentRunID` no longer writes to `os.Stderr` from a library function; invalid checkpoint directories are silently skipped.
- `checkWorkdirLib` now correctly propagates `warn` details to the section-level `Status` field.
- `checkProvidersLib` now propagates individual provider `error` details to the section-level `Status` (was always `"ok"` when any provider was configured).
- `getDippinVersion` now uses `exec.CommandContext` with a 5-second timeout to prevent hangs on unresponsive dippin binaries.
- `PinnedDippinVersion` constant updated to `v0.20.0` to match the `go.mod` requirement.
- `checkPipelineFileLib` no longer warns when the pipeline file has a `.dot` extension (both `.dip` and `.dot` are valid input formats).
- Fixed ineffectual assignment to `suffix` in `cmd/tracker/doctor.go` `maybeFixGitignore`.
- `checkDiskSpaceLib` moved to platform-specific files (`tracker_doctor_unix.go` / `tracker_doctor_windows.go`) to avoid a Windows build failure from `syscall.Statfs`.
- `enrichFromEntryNF` and `updateFailureTimingNF` now guard against zero timestamps to prevent incorrect duration calculations in `DiagnoseReport`.
- `claude-sonnet-4-6` added to the LLM model catalog — the model was in `pricing.go` but missing from `catalog.go`, causing `GetModelInfo` to return nil and cost reporting to show `$0.00` for the swebench harness default model.
- ACP backend: `validatePathInWorkDir` now resolves symlinks on both `path` and `workDir`. On macOS `/var` is a symlink to `/private/var`, which was causing path validation to reject files inside `t.TempDir()`.

### Changed

- `cmd/tracker/diagnose.go`, `audit.go`, `doctor.go`, `simulate.go` are now thin printers over the new library APIs. CLI stdout and `--json` wire format are byte-identical. Closes Phase 2 of #76.
- `dippin-lang` dependency bumped from `v0.19.1` → `v0.20.0`. CI installs the matching CLI version (was stale at `v0.10.0`). `examples/human_gate_test_suite.dip` renamed `default_choice:` → `default:` to match the IR field. The file is temporarily skipped from `make lint` because v0.20.0's stricter parser rejects `timeout:` / `timeout_action:` on human nodes — tracker supports those attrs at the node level but dippin-lang's `HumanConfig` IR doesn't expose them yet. Tracked upstream at dippin-lang#18.

- **Structured reflection prompt on tool failure** (issue #93): when a tool call returns an error, the agent session now automatically injects a user-role reflection message before the next LLM turn. The prompt asks the model to identify what went wrong, what assumption was incorrect, and what minimal change will fix it — matching the pattern used by top SWE-bench agents (~10-15% recovery improvement). The feature is enabled by default (`ReflectOnError: true` in `DefaultConfig()`) and capped at three consecutive reflection turns to prevent infinite loops; the counter resets after any clean (no-error) turn. Pipeline authors can opt individual nodes out via `reflect_on_error: false` in their `.dip` file.
- **Verify-after-edit loop with auto-test** (closes #94): agent sessions can now automatically run tests after any turn that includes file-edit tool calls (`write`, `edit`, `apply_patch`, `notebook_edit`). Modelled on top SWE-bench agent behaviour (~15-20% improvement on benchmark), this transparent inner loop catches regressions before the LLM moves on.
  - `SessionConfig.VerifyAfterEdit bool` — opt-in flag (default: false).
  - `SessionConfig.VerifyCommand string` — explicit command; if empty, auto-detection runs: `go.mod` → `go test ./...`, `Cargo.toml` → `cargo test`, `package.json` → `npm test`, `Makefile` with `test:` target → `make test`, `pytest.ini`/`pyproject.toml[tool.pytest]` → `pytest`.
  - `SessionConfig.MaxVerifyRetries int` — max verify→repair cycles per edit turn (default: 2). After exhaustion the session proceeds without blocking.
  - Repair turns do NOT count toward `MaxTurns` — they are a transparent sub-loop.
  - Verification output is capped at 4 KB (tail kept — most relevant errors appear at the end).
  - Pipeline nodes wire the feature via `verify_after_edit`, `verify_command`, and `max_verify_retries` node attributes. `verify_command` can also be set at graph level as a default for all nodes.
  - New file `agent/verify.go`; 8 new tests in `agent/verify_test.go` and `agent/session_test.go`.

## [0.17.0] - 2026-04-16

### Added

- **Library API for workflow catalog and resolution** (partial #76 — Phase 1): library consumers can now list, open, and resolve built-in workflows without shelling out to the CLI.
  - `tracker.Workflows() []WorkflowInfo` returns every embedded workflow sorted by name.
  - `tracker.LookupWorkflow(name) (WorkflowInfo, bool)` looks up a single built-in by bare name.
  - `tracker.OpenWorkflow(name) ([]byte, WorkflowInfo, error)` returns the raw `.dip` source for a built-in.
  - `tracker.ResolveSource(name, workDir) (source, WorkflowInfo, error)` mirrors the CLI's bare-name resolution — filesystem first, then embedded — and returns the actual source bytes.
  - `tracker.ResolveCheckpoint(workDir, runID) (path, error)` resolves a run ID (or unique prefix) to its `checkpoint.json` path under `.tracker/runs/<runID>/`.
  - `tracker.Config.ResumeRunID` lets library consumers set `cfg.ResumeRunID = "abc123"` and `NewEngine` resolves it to `CheckpointDir` automatically — equivalent to the CLI's `-r/--resume` flag. An explicit `CheckpointDir` on the same config still wins as a manual override.
  - Embedded workflow files moved from `cmd/tracker/workflows/` to top-level `workflows/` so they can be shared by both the tracker library and the CLI binary. The CLI continues to embed them via thin wrappers over the library functions.

- **`ExportBundle(runDir, outPath string) error` library API and `--export-bundle` CLI flag** (issue #77, Layer 2): after a run completes, `ExportBundle` calls `git bundle create <outPath> --all` against the artifact run directory to produce a single portable `.bundle` file capturing every commit and tag (including `checkpoint/*` tags) produced by `WithGitArtifacts`. The bundle can be cloned on any machine with `git clone <bundle>` and inspected with `git log`. `Result.ArtifactRunDir` is now populated when `Config.ArtifactDir` is set, giving callers a direct path to the run directory. `Result.BundlePath` is available for callers that populate it after calling `ExportBundle`. The CLI `--export-bundle <path>` flag invokes `ExportBundle` as a post-run step; failures print a warning and do not affect the run's exit code. No new dependencies — implemented with `os/exec`.
- **`WithGitArtifacts(bool)` engine option** (issue #77, Layer 1): when enabled alongside `WithArtifactDir`, the artifact run directory is initialized as a (non-bare) git repository at run start and a commit is created after every terminal-outcome node — including success, fail, retry-exhausted, goal-gate fallback, and goal-gate unsatisfied paths. Commits carry a structured message (`node(<id>): <handler> outcome=<status>`) plus duration, edge, and token/cost metadata. `git log` gives a human-readable audit trail of execution order. Successful node advances also create lightweight checkpoint tags (`checkpoint/<runID>/<nodeID>`) enabling future replay support. On checkpoint resume, `Init()` detects an existing HEAD and skips the "run started" commit so replay doesn't add noise. All git operations are best-effort — git failures emit `EventWarning` events and do not crash the engine. Requires `git` in PATH; silently no-ops if `artifactDir` is unset or git is missing.

### Fixed

- **`tracker doctor` robustness fixes** (PR #83 review round 2):
  - Writability probes now use `os.CreateTemp` instead of fixed filenames (`.tracker_test_write`, `.tracker_write_probe`) — probes can't collide with real user files and are always cleaned up.
  - `checkProviders` no longer emits ✗ lines for unconfigured providers when at least one provider is already configured. Missing providers are shown as an informational hint line (e.g. "not configured: OpenAI, Gemini (optional)"). The ✗ lines appear only when zero providers are configured.
  - `checkGitignore` parses the `.gitignore` file line-by-line with exact (trimmed, slash-stripped) comparison instead of `strings.Contains` to prevent false positives (`runsheet` → `runs`, `my.tracker.bak` → `.tracker`).
  - Removed spurious `TRACKER_ARTIFACT_DIR` check — that env var is not wired into any CLI code path; checking it was misleading.
  - Disk space threshold confirmed at 10 GB (was already correct in code and CHANGELOG; the initial PR description saying 100 MB was wrong and has been corrected).
  - `resolveProviderBaseURL` in `doctor.go` was a duplicate of the canonical function. The duplicate is removed; `doctor.go` now calls the exported `tracker.ResolveProviderBaseURL`. The Gemini gateway suffix is corrected to `/google-ai-studio` (was `/gemini`).
  - `parseDoctorFlags` now validates `--backend` against the allowed set (`native`, `claude-code`, `acp`), consistent with `parseRunFlags`.

- **Per-node backend selection now overrides global `--backend` flag** (issue #70): A node with `backend: native` always uses the native LLM client even when `--backend claude-code` is set globally, enabling mixed-backend pipelines (e.g. some nodes on claude-code subscription, others on OpenAI native API). The `selectBackend` priority is now documented: per-node attr > global flag > default native. The registry also registers the CodergenHandler when per-node backend attrs are present in the graph, even if the global default is native and no `--backend` flag is passed. Error messages for missing native client when using `--backend claude-code` now include actionable guidance.
- **Start/exit node handler overwrite broadened fix**: `ensureStartExitNodes` previously checked only the `prompt` attribute to decide whether to preserve a node's handler, which meant tool nodes (`tool_command`) and human nodes (`mode`) designated as start/exit would still have their handlers silently overwritten. The helper now bases the decision on the resolved `Handler` field: any handler other than `codergen` is always preserved; only a bare `codergen` node with no `prompt` gets the passthrough. This fixes cases like `parallel` with `parallel_targets`, `parallel.fan_in` with `fan_in_sources`, `conditional`, `subgraph`, `stack.manager_loop`, and `wait.human` nodes used as start/exit. Closes #69.

### Added

- **Cloudflare AI Gateway support** (`TRACKER_GATEWAY_URL` env var, `--gateway-url` CLI flag): set one gateway root URL and tracker routes every provider through Cloudflare's AI Gateway — Anthropic, OpenAI, Gemini, OpenAI-compat — avoiding 429 rate limits and enabling gateway-side analytics, caching, and model routing. The new `ResolveProviderBaseURL(provider)` helper resolves the per-provider base URL with priority `<PROVIDER>_BASE_URL` > `TRACKER_GATEWAY_URL` + provider suffix > empty (SDK default), so per-provider env var overrides still work. Closes #64.
- **`tracker doctor` comprehensive preflight checks** (closes #61): `tracker doctor` now runs a structured series of checks with clear pass/warn/fail status, actionable fix messages, and documented exit codes (0=all pass, 1=any failure, 2=warnings only). New checks include:
  - Per-provider API key validation with format hints (key prefix, length)
  - `--probe` flag for live auth validation (makes a minimal 1-token API call per configured provider; offline-safe by default). The probe adapters honor `<PROVIDER>_BASE_URL` env vars (and `TRACKER_GATEWAY_URL`) so probing through a Cloudflare gateway works.
  - `dippin` binary version detection; `checkVersionCompat` compares the installed CLI's major.minor against the `go.mod`-pinned version (`v0.18.0`) and warns on divergence.
  - `.ai/` subdirectory writability check (note: `TRACKER_ARTIFACT_DIR` env var is not checked — it is not wired into the CLI and was removed to avoid misleading output)
  - Disk space warning (warn if < 10 GB free — threshold confirmed in code; the initial PR description that said 100 MB was incorrect)
  - `.gitignore` check for `.tracker/`, `runs/`, and `.ai/` entries (line-by-line exact match — no more false positives from substrings like `runsheet`)
  - Environment variable warnings for dangerous override keys (`TRACKER_PASS_ENV`, `TRACKER_PASS_API_KEYS`)
  - `--backend claude-code` awareness: hard-fails (exit 1) if the `claude` CLI is not found; without `--backend` the missing binary is a warning only.
  - `tracker doctor [pipeline.dip]`: optional positional arg validates the pipeline file with full lint (same as `tracker validate`)
  - Human-readable composite result lines per check group (providers, binaries, dirs)
  - `-w/--workdir` and `--backend` flags on `tracker doctor` so `tracker -w /path doctor` and `tracker --backend claude-code doctor` work as expected.
  - OpenAI-Compat provider now has a real `--probe` implementation (previously silently skipped).
  - Probe default models updated to current catalog entries: Anthropic → `claude-haiku-4-5`, Gemini → `gemini-2.0-flash`.
  - Exit code 2 is emitted when doctor finishes with warnings but no hard failures (was always 0). `DoctorWarningsError` sentinel returned from `runDoctorWithConfig`; `main.go` maps it to `os.Exit(2)`.

- **Webhook-based human gates for headless execution** (Closes #63, Closes #86): new `tracker.Config.WebhookGate` library field and matching CLI flags wire a `WebhookInterviewer` that POSTs gate prompts to a user-configured webhook URL and blocks on a callback. The interviewer starts a local HTTP server on a configurable address, tracks pending gates by UUID with per-gate shared-secret tokens (`X-Tracker-Gate-Token`) to authenticate inbound callbacks (mismatches return 401), supports a per-gate timeout with configurable action (`fail` / `success`), optional `Authorization` header for outbound requests, server-side HTTP timeouts (`ReadHeaderTimeout` 10s / `ReadTimeout` 30s / `WriteTimeout` 30s / `IdleTimeout` 60s), 64 KB callback body cap via `http.MaxBytesReader`, wildcard-address rewrite (`0.0.0.0` / `[::]` → `127.0.0.1`) so the outbound payload carries a dialable callback URL, and an explicit `Cancel()` that closes the server and unblocks pending gates. Implements both `FreeformInterviewer` and `LabeledFreeformInterviewer` so it drops into existing pipeline flows unchanged. CLI flags added: `--webhook-url` (required to enable), `--gate-callback-addr` (default `:8789`), `--gate-timeout` (default `10m`), `--gate-timeout-action` (`fail`/`success`), `--webhook-auth` (outbound `Authorization` header). Mutual exclusion with `--autopilot` and `--auto-approve` is enforced at parse time. Validation rejects invalid `--gate-timeout-action` values at parse time.
- **Per-node context scoping** (`PipelineContext.ScopeToNode`): after each node's handler completes, the engine copies every key written during that node's execution into a `node.<nodeID>.<key>` namespace. Downstream nodes can read `node.MyAgent.last_response` to get a specific upstream node's output without being affected by later writes to the bare `last_response` key. Bare keys retain their last-writer-wins global semantics for full backward compatibility. New convenience method `GetScoped(nodeID, key)`. Closes #32.
- `pipeline.ContextKeyNodePrefix` constant (`"node."`), the namespace prefix for per-node scoped keys.

- `Result.Cost` on the library API with per-provider rollup (`map[string]llm.ProviderCost`) and `TotalUSD`. Populated from the `llm.TokenTracker` middleware and priced via `llm.EstimateCost`. Closes #62.
- `pipeline.BudgetGuard` enforcing `MaxTotalTokens`, `MaxCostCents`, and `MaxWallTime` limits. Halts the run with `pipeline.OutcomeBudgetExceeded` when any dimension trips. Closes #17.
- New `tracker.Config.Budget` field (type `pipeline.BudgetLimits`) for library consumers.
- New CLI flags on `tracker run`: `--max-tokens`, `--max-cost` (cents), `--max-wall-time`.
- New pipeline events `cost_updated` (streaming per-node cost snapshots) and `budget_exceeded` (fired on halt). Both carry a `CostSnapshot` payload with `TotalTokens`, `TotalCostUSD`, `ProviderTotals`, and `WallElapsed`.
- `tracker diagnose` surfaces a "Budget halt detected" section when a run halts on budget.
- `UsageSummary.ProviderTotals` (per-provider token and cost rollup) on `pipeline.Trace.AggregateUsage()` output.

### Notes

- Reading budget limits from `.dip` workflow attrs is blocked on dippin-lang IR support; tracked in #67.

## [0.16.4] - 2026-04-09

### Fixed

- **Turn-limit exhaustion treated as success**: Agents that exhausted their turn limit (or entered a tool call loop) were silently treated as `OutcomeSuccess`, allowing pipelines to advance past nodes that wrote zero files. Now returns `OutcomeFail` so the engine routes through explicit `when ctx.outcome = fail` edges (or stops via strict-failure-edge when no failure edge exists).
- **Loop detection produces distinct diagnostic**: `turn_limit_msg` context key now distinguishes "agent entered tool call loop" from "agent exhausted turn limit" for clearer `tracker diagnose` output.

### Added

- **`ContextKeyTurnLimitMsg` constant**: New `pipeline.ContextKeyTurnLimitMsg` context key for turn-limit and loop-detection diagnostics. Added to `reservedContextKeys()` for linter recognition.
- **Turn-limit and loop-detection tests**: `TestCodergenHandlerMaxTurnsExhaustedIsFail`, `TestCodergenHandlerMaxTurnsWithAutoStatusSuccess`, `TestCodergenHandlerMaxTurnsWithAutoStatusFail`, `TestCodergenHandlerLoopDetectedMessage`.

## [0.16.3] - 2026-04-06

### Fixed

- **Thinking signature dropped in streaming**: The Anthropic SSE handler now captures `signature_delta` events. Previously, thinking block signatures were silently lost during streaming, causing multi-turn sessions with extended thinking (Opus 4.6) to crash with `messages.N.content: Input should be a valid list` when the API rejected the signature-less thinking block on the next turn.
- **Redacted thinking blocks dropped in streaming**: The SSE handler now captures `redacted_thinking` content blocks and round-trips them through the `StreamAccumulator`. Previously, these opaque blocks were silently dropped, breaking conversation continuity.
- **Nil message content serialized as `null`**: `translateMessage` now initializes content as an empty slice so JSON serializes to `[]` instead of `null` when all content parts are skipped.

## [0.16.2] - 2026-04-05

### Added

- **Comprehensive human gate test suite**: `examples/human_gate_test_suite.dip` exercises all 4 gate modes (choice, yes_no, freeform, interview) plus timeout, default_choice, ctx.outcome routing, hybrid freeform, and interview cancel. 100 simulated paths, all reaching Exit.
- **Backend selection precedence test**: Verifies node attr overrides global `--backend` CLI flag.

### Changed

- **dippin-lang v0.18.0**: Updated from v0.17.0. Adds `flatten` package for inlining subgraph refs into a single flat workflow.

### Fixed

- **human_gate_showcase.dip**: EchoFreeform agent no longer asks follow-up questions that conflict with the next gate's choices.

## [0.16.1] - 2026-04-04

### Fixed

- **`mode: yes_no` human gate outcome mapping**: Yes now returns `OutcomeSuccess`, No returns `OutcomeFail`. Previously, `yes_no` fell through to choice mode which always returned `OutcomeSuccess` regardless of selection, causing `ctx.outcome = fail` conditions to never match. Pipelines using `mode: yes_no` with `ctx.outcome` edge conditions now route correctly.

### Added

- **`executeYesNo` handler**: Dedicated handler for `mode: yes_no` human gates. Presents fixed "Yes"/"No" choices and maps selection to outcome status. Comprehensive test coverage for all four human gate modes (choice, yes_no, freeform, interview).

## [0.16.0] - 2026-04-04

### Added

- **ACP (Agent Client Protocol) backend**: Third execution backend alongside native and claude-code. Spawns ACP-compatible coding agents as subprocesses via JSON-RPC 2.0 over stdio using `github.com/coder/acp-go-sdk`. Per-node selection via `backend: acp` + `acp_agent` params in .dip files, global override via `--backend acp` CLI flag.
- **ACP agent routing**: Provider-based binary mapping (`anthropic` → `claude-agent-acp`, `openai` → `codex-acp`, `gemini` → `gemini --acp`). The `acp_agent` node attribute overrides provider-based selection.
- **ACP model bridging**: `mapModelToBridge` maps tracker model names (e.g. `claude-sonnet-4-6`) to bridge model IDs via substring matching against `NewSession` advertised models.
- **ACP environment scoping**: API keys and base URLs stripped from subprocess environment by default so agents use native auth (subscription/OAuth). Override with `TRACKER_PASS_API_KEYS=1`.
- **ACP terminal management**: Full `CreateTerminal`, `TerminalOutput`, `KillTerminalCommand`, `ReleaseTerminal` implementation with process group isolation (`Setpgid`) and goroutine-safe output buffering.
- **ACP file operations**: `ReadTextFile` and `WriteTextFile` handlers scoped to the node's working directory.
- **`ACPConfig` type**: Backend-specific config carrying explicit agent binary name, extracted from `params.acp_agent` in .dip files.
- **`--backend acp` CLI flag**: Routes all agent nodes through ACP without per-node attrs.

### Fixed

- **ACP data race on empty response check**: `handler.mu` now locked before reading `textParts`/`toolCount` after prompt completes.
- **ACP terminal output data race**: Replaced `bytes.Buffer` with `syncBuffer` (mutex-protected writer) for subprocess stdout/stderr.
- **ACP protocol version validation**: `InitializeResponse.ProtocolVersion` checked against `ProtocolVersionNumber` with warning on mismatch.
- **ACP empty Cwd fallback**: `os.Getwd()` used when `WorkingDir` is empty, preventing ACP SDK validation failure.
- **ACP process kill safety**: `Pid > 0` guard before `syscall.Kill(-pid, SIGKILL)` at all 3 call sites to prevent killing pid 0 process group.
- **`TRACKER_PASS_API_KEYS` truthiness**: Changed from `!= ""` to `== "1"` so `"false"` and `"0"` correctly strip keys.

## [0.15.0] - 2026-04-03

### Added

- **Per-node response context keys**: Codergen and human handlers now write `response.<nodeID>` alongside `last_response`/`human_response`, enabling downstream nodes to reference specific upstream outputs instead of only the most recent. (#24)
- **Parallel concurrency limits**: `max_concurrency` attr on parallel nodes limits concurrent branch goroutines via semaphore. Context-aware acquisition aborts on cancellation. (#27)
- **Parallel branch timeout**: `branch_timeout` attr on parallel nodes sets per-branch context deadline. Slow branches fail without blocking fan-in. (#27)
- **Human gate timeout**: `timeout` attr on human nodes with `timeout_action` (default/fail) and `default_choice` fallback. Applied to freeform, choice, and interview modes. (#30)
- **Edge adjacency indexes**: `OutgoingEdges`/`IncomingEdges` now use O(1) map lookup via adjacency indexes built by `AddEdge`, with O(E) fallback for graphs built without `AddEdge`. Returns defensive copies. (#31)
- **Format constants**: `FormatDip` and `FormatDOT` typed constants for pipeline format identification. (#9)
- **Pipeline package documentation**: `pipeline/doc.go` with package overview and dual-format documentation. (#12)

### Fixed

- **P0: Goal-gate infinite fallback loop**: `FallbackTaken` guard persisted in checkpoint prevents one-shot fallback/escalation from looping. Separate fallback routing path in `handleExitNode` doesn't increment retry counts. (#15)
- **P0: Parallel branch context loss on fan-in**: `PipelineContext.DiffFrom()` captures side effects from parallel branches. (#20)
- **Adapter nil pointer guards**: Nil checks for IR nodes, edges, and all 6 pointer config types in `extractNodeAttrs`. Also guards in `synthesizeImplicitEdges` and `buildFanInSourceMap`. (#38)
- **Adapter sentinel errors**: `ErrNilWorkflow`, `ErrMissingStart`, `ErrMissingExit`, `ErrUnknownNodeKind`, `ErrUnknownConfig` with `%w` wrapping for `errors.Is` support. (#33)
- **Deterministic map iteration**: `extractSubgraphAttrs` and `serializeStylesheet` sort keys before iteration via `slices.Sorted(maps.Keys(...))`. (#8)
- **Workflow.Version mapping**: `ir.Workflow.Version` now mapped to `g.Attrs["version"]`. (#25)
- **Validation bypass removed**: Deleted `DippinValidated` field — all 5 structural validation checks always run for defense-in-depth. (#4)
- **Library stderr cleanup**: Replaced `fmt.Fprintf(os.Stderr, ...)` with `log.Printf(...)` in library code (tracker.go, condition.go, autopilot handlers). (#7)
- **Case-insensitive auto_status**: `parseAutoStatus` now matches STATUS prefix case-insensitively and skips STATUS lines inside code fences. (#23)
- **Word-boundary fidelity truncation**: `truncateAtWordBoundary` cuts at whitespace (unicode.IsSpace) instead of mid-word, with `...` suffix and named `DefaultTruncateLimit` constant. (#34)
- **Condition parser hardening**: Support `==` operator (space-delimited), strip surrounding double quotes from values in `=`/`==`/`!=` comparisons. (#21)
- **Consensus pipeline parallelized**: `consensus_task.dip` now uses parallel fan-out/fan-in for DoD, Planning, and Review phases. (#26)
- **CLI format detection default**: Unknown extensions now default to `.dip` instead of `.dot`, with case-insensitive extension matching. (#9)
- **Empty API response retry**: Empty API responses (0 output tokens, 0 tool calls) now trigger `OutcomeRetry` instead of hard-failing. (#23)
- **POSIX build constraint**: `//go:build !windows` on `agent/exec/local.go`. (#28)
- **ConsoleInterviewer IsYesNo priority**: Yes/no check now runs before option list check, matching TUI behavior. (#48 review)
- **Test rename**: `TestListBuiltinWorkflowsReturnsThree` → `ReturnsFour`. (#48 review)

### Changed

- **Retry backoff jitter**: `ExponentialBackoff` and `LinearBackoff` now apply ±25% random jitter to prevent thundering herd when multiple pipelines retry simultaneously. (#29)
- **Code cleanup**: Unexported `NodeKindToShape`, removed `make([]*Edge, 0)`, replaced custom `contains` helper with `strings.Contains`, replaced bubble sort with `slices.SortFunc`. (#10)

### Deprecated

- **DOT format support**: `ParseDOT` is deprecated. Use `.dip` format with `FromDippinIR` instead. DOT support will be removed in v1.0. (#12)

## [0.14.0] - 2026-03-31

### Added

- **Interview mode for human gates**: New `mode: interview` on human nodes enables structured multi-field form collection. An upstream agent generates markdown questions; the interview handler parses them into individual fields (select with inline options, yes/no confirm, freeform textarea). Answers are stored as JSON at a configurable context key and as a markdown summary at `human_response`. Supports retry pre-fill, cancellation with partial answers, and 0-question fallback to freeform.
- **Interview question parser**: `ParseQuestions()` extracts structured questions from agent markdown — numbered items, bulleted questions, imperative prompts. Trailing parentheticals like `(option1, option2)` become select field options. Yes/no patterns auto-detected. Fenced code blocks skipped.
- **TUI interview modal**: Fullscreen one-question-at-a-time form with progress bar, answered summary, selection feedback (filled dot + checkmark), elaboration textareas (Tab), submit (Ctrl+S), cancel (Esc), and PgUp/PgDn jump navigation. Pre-fills from previous answers on retry.
- **Interview autopilot support**: `AutopilotInterviewer`, `ClaudeCodeAutopilotInterviewer`, and `AutopilotTUIInterviewer` all implement `AskInterview`. LLM-backed autopilot sends all questions in a single prompt, parses JSON response, retries once on parse failure, hard-fails on double failure.
- **Console interview support**: `ConsoleInterviewer.AskInterview` presents questions one at a time with option selection by name or number, blank-line skip, and previous-answer hints on retry.
- **`deep_review` built-in workflow**: Interview-driven codebase review pipeline with 3 structured interview gates (scope, findings, priority), parallel analysis (correctness, security, design), and remediation plan generation. Run with `tracker deep_review`.
- **`interview-loop.dip` subgraph**: Reusable interview loop pattern (ask → answer → assess → loop) in `examples/subgraphs/`. Parameterized with `topic` and `focus` for embedding via `subgraph` nodes.
- **Structured JSON question format**: `ParseStructuredQuestions()` parses JSON questions from agent output with validation. Handles code fences, preamble text, and extracts `{"questions": [...]}` objects. Falls back to markdown heuristic parsing. "Other" option variants are auto-filtered since the UI always provides its own.
- **One-question-at-a-time TUI**: Interview form shows one question with full context, progress bar, answered summary, and remaining count. Selection feedback with filled dot and checkmark. Enter confirms and advances.
- **`response_format` support**: Agent nodes can set `response_format: json_object` or `response_format: json_schema` with `response_schema:` to force structured output at the LLM API level. Plumbed from `.dip` files through dippin IR → adapter → codergen → agent session → all three providers (Anthropic, OpenAI, Gemini).
- **Agent `params` map**: Generic key-value pass-through from `.dip` files via `AgentConfig.Params` (dippin-lang v0.16.0). Enables runtime features like `backend: claude-code` without IR schema changes.
- **Empty API response diagnostics**: Anthropic adapter logs raw response body, HTTP status, stop_reason, model, and request-id when API returns 0 output tokens. Session layer retries completely empty responses with diagnostic event emission.
- **EngineResult.Usage**: Pipeline runs now expose aggregated token counts and cost via `EngineResult.Usage` (`*UsageSummary`). Downstream consumers can read `TotalInputTokens`, `TotalOutputTokens`, `TotalTokens`, `TotalCostUSD`, and `SessionCount` directly from the result.
- **Per-node token tracking in SessionStats**: `InputTokens`, `OutputTokens`, `TotalTokens`, `CostUSD`, `ReasoningTokens`, `CacheReadTokens`, `CacheWriteTokens` fields on `SessionStats` in trace entries.
- **Parallel branch stats aggregation**: Parallel handler now collects and aggregates `SessionStats` from branch outcomes into its own trace entry.
- **Consistent JSON tags**: All fields on `SessionStats`, `TraceEntry`, and `Trace` now have `json:"snake_case"` tags for consistent serialization.

### Fixed

- **Interview cancellation returns OutcomeFail**: Canceled interviews now return `fail` status instead of `success`, allowing pipeline edges to route canceled interviews differently from completed ones.
- **ClaudeCode autopilot hard-fails on parse error**: `ClaudeCodeAutopilotInterviewer.AskInterview` now retries once on JSON parse failure and hard-fails on double failure, matching the native autopilot behavior. Previously silently fell back to first-option defaults.
- **SerializeInterviewResult enforced**: Panics on marshal failure instead of silently returning empty string, preventing downstream deserialization corruption.
- **Goroutine leak in autopilot flash**: `flashDecision` goroutine now exits immediately when the caller unblocks via a `done` channel, instead of sleeping for the full 2-second timer. Includes `defer/recover` for panic safety per CLAUDE.md.
- **Mode 1 tea.Cmd propagation**: All three TUI runner types (choice, freeform, interview) now propagate `tea.Cmd` from `content.Update()` instead of discarding it.
- **Context leak in retry loop**: `ClaudeCodeAutopilotInterviewer.AskInterview` uses explicit `cancel()` calls instead of `defer cancel()` inside a for loop, preventing context timer goroutine leaks on retry.
- **Empty API response guard**: Agent sessions that receive completely empty responses (0 content parts, 0 output tokens, no prior tool calls) now retry with a continuation prompt instead of silently succeeding with empty `last_response`. Codergen handler also fails the node when the session produces empty text with zero tool calls.
- **Start/exit agent nodes preserved**: `ensureStartExitNodes` no longer overwrites the `codergen` handler on agent nodes designated as start or exit. Agent start/exit nodes now execute their LLM prompts instead of being silently replaced with no-op passthroughs. (Closes #42)
- **DecisionDetail token mapping**: `TokenInput`/`TokenOutput` in pipeline events now correctly map from `InputTokens`/`OutputTokens` instead of `CacheHits`/`CacheMisses`.
- **Native backend double-counting**: Token usage from the native backend is no longer reported twice to the `TokenTracker`.
- **Cancel/fail EndTime**: Cancelled and retry-exhausted runs now set `trace.EndTime` so the run summary shows duration.
- **failResult atomicity**: `failResult()` now accepts a `*Trace` parameter and sets both `Trace` and `Usage` internally, preventing silent data loss.
- **Built-in pipeline prompts**: Removed trivial placeholder prompts from Start/Done nodes in built-in workflows that were causing unnecessary LLM calls.

## [0.13.0] - 2026-03-28

### Added

- **TUI: Progress bar with ETA**: Amber ASCII bar (`━━━──────`) in the status bar shows completed/total nodes. ETA appears after 2+ real LLM nodes complete, based on rolling average of node durations.
- **TUI: Desktop notification**: Fires OS-native notification on pipeline completion (macOS `osascript`, Linux `notify-send`). Disable with `TRACKER_NO_NOTIFY=1`.
- **TUI: Log verbosity cycling (`v`)**: Cycle through All → Tools → Errors → Reasoning. View-level filter only — all lines always stored (append-only per CLAUDE.md).
- **TUI: Zen mode (`z`)**: Hide sidebar, agent log gets full terminal width. Status bar and modal gates still work.
- **TUI: Help overlay (`?`)**: Modal showing all keyboard shortcuts in a styled two-column table.
- **TUI: Agent log search (`/`)**: Inline search bar with real-time highlighting. `n`/`N` jump between matches. Search intersects with verbosity filter.
- **TUI: Per-node cost tracking**: Shows cost badge on completed nodes in the sidebar. Uses delta snapshots from `TokenTracker`. Parallel branches show `~` prefix (approximate). Max subscription shows "usage" not "cost".
- **TUI: Node drill-down (`Enter`)**: Arrow keys navigate the node list, Enter focuses the log on that node, Esc returns to full view.
- **TUI: Copy to clipboard (`y`)**: Copies visible (filtered) log text. Uses `pbcopy`/`xclip`. Error message includes diagnostic on failure.
- **TUI: Status bar flash**: "Copied!" confirmation that auto-clears after 2 seconds.
- **Claude-code autopilot**: New `ClaudeCodeAutopilotInterviewer` routes autopilot gate decisions through the `claude` CLI subprocess instead of direct API calls. No API key needed for `--autopilot` with `--backend claude-code`.
- **`--auto-approve` works with TUI**: No longer forces `--no-tui`. Gates auto-dismiss in the dashboard.

### Changed

- **Claude-code env: API keys stripped**: `buildEnv()` strips `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `GEMINI_API_KEY` from the subprocess environment so the `claude` CLI uses Max/Pro subscription auth instead of consuming API credits. Override with `TRACKER_PASS_API_KEYS=1`.
- **Lazy LLM client**: `buildLLMClient()` failure is non-fatal with `--backend claude-code`. The native client is only required when something actually needs it (native backend nodes, native autopilot).
- **Claude-code backend handles all providers**: With `--backend claude-code`, nodes with `provider: openai` or `provider: gemini` also route through the claude CLI. Non-Anthropic model names are stripped so the CLI uses its default.
- **Max subscription cost labeling**: Header, sidebar, and exit summary show "~$X.XX usage" instead of "$X.XX" when all usage is from `claude-code` provider. Exit summary adds "(Max subscription — no actual charge)".
- **Strict failure edges**: When a node's outcome is "fail" and all outgoing edges are unconditional, the pipeline now stops instead of silently continuing. Pipelines that intentionally handle failure must use explicit `when ctx.outcome = fail` edges.
- **Status bar hints**: Updated to show all new shortcuts (`v filter  z zen  / search  ? help  q quit`).

### Fixed

- **TUI: Sidebar connector alignment**: Connectors (`│`) now align with node lamps when selection mode is active.
- **TUI: Scroll follows selection**: Up/Down navigation scrolls the node list viewport to keep the selected node visible.
- **Search: `formatMatchStatus` bug**: Rune arithmetic broke for 10+ matches. Now uses `fmt.Sprintf`.
- **Search: Match consistency with filters**: Search matches against the filtered view, not the full line buffer.
- **Verbosity: Separators preserved**: Node separator lines pass through all verbosity filters for structural context.
- **Zen mode: `relayout()` fix**: Terminal resize in zen mode now gives the agent log full width.
- **Exit hang**: `runTUI()` waits at most 5 seconds for the pipeline goroutine after the TUI closes.
- **Notification zombie**: `SendNotification` uses `cmd.Run()` in a goroutine instead of `cmd.Start()` without `Wait()`.

## [0.12.1] - 2026-03-27

### Fixed

- **Claude Code subprocess killed after 10 seconds**: `exec.CommandContext` + `WaitDelay` created a race where Go's process management sent SIGKILL to the Claude Code subprocess after exactly 10 seconds, despite no context cancellation. Switched to plain `exec.Command`.
- **Claude Code auth failure from stripped environment**: The minimal env allowlist prevented Claude Code from finding its OAuth token / config directory. Now passes the full parent environment.
- **NDJSON unmarshal error on subagent results**: Claude Code's subagent tool results return `content` as an array of blocks, not a string. The parser now handles both formats.

### Added

- **Autopilot runs inside the TUI**: `--autopilot` no longer forces `--no-tui`. Gate decisions flash in a modal for 2 seconds showing "AUTOPILOT" header, the prompt, and the chosen option in green. Press Enter to dismiss early.
- **Backend and autopilot tags in TUI header**: Orange tag for `claude-code`, purple tag for autopilot persona — always visible next to the pipeline name.
- **"Agent backend:" startup message**: Prints the active backend before the TUI starts (visible in `--no-tui` mode).

## [0.12.0] - 2026-03-27

### Added

- **Claude Code backend**: Pluggable `AgentBackend` interface with `--backend claude-code` flag. Spawns the `claude` CLI as a subprocess, parses NDJSON output, and maps exit codes to pipeline outcomes. Per-node via `backend: claude-code` in `.dip` files, or global via CLI flag. Includes environment scoping, token tracking, and retryable init.
- **`tracker update`**: Self-update command downloads the latest GitHub release, verifies SHA256 checksum, extracts the binary, smoke-tests it, and atomically replaces the current binary with a `.bak` rollback. Detects install method (Homebrew → advises `brew upgrade`, go install → advises `go install @latest`, binary → self-replaces).
- **Non-blocking update check**: On every `tracker run`, a background goroutine checks for new releases (24h file-based cache). Prints a one-line hint to stderr if an update is available. Disabled in CI (`CI` env) or with `TRACKER_NO_UPDATE_CHECK`.

### Changed

- Upgraded dippin-lang dependency v0.10.0 → v0.12.0 (preferred_label fix, immediately_after assertions, tool command lint, subgraph validation, test coverage)
- Tightened 5 dippin test assertions with `immediately_after` for stricter edge verification

## [0.11.2] - 2026-03-27

### Fixed

- **PickNextMilestone silent skip**: Flexible milestone header matching now handles `## Milestone 1: Title`, `### Milestone 1 — Setup`, and other LLM formatting variations. Fails loudly if no milestones found or extraction produces an empty file.
- **Removed `eval` of LLM-generated verify commands**: TestMilestone no longer evals commands extracted from milestone specs — this was arbitrary code execution from free-form LLM text. Verification is now the Implement agent's responsibility.
- **TestMilestone known_failures parsing**: Strip comments and blank lines, use `go test -skip` instead of unsupported `(?!` negative lookahead.
- **PickBest winner parsing hardened**: Uses `grep -ioE 'claude|codex|gemini'` regardless of markdown formatting.

## [0.11.1] - 2026-03-27

### Fixed

- Provider errors hard-fail per CLAUDE.md (autopilot review fixes)
- Default autopilot model picks cheapest from configured provider
- Autopilot forces `--no-tui`, `matchChoice` uses longest-match, `decide()` returns errors

## [0.11.0] - 2026-03-26

### Added

- **`--autopilot <persona>`**: Replace all human gates with LLM-backed decisions. Four personas encode different risk tolerances:
  - **lax**: Bias toward forward progress. Approves plans, marks done on escalation, accepts reviews.
  - **mid**: Balanced engineering judgment. The default persona if none specified.
  - **hard**: High quality bar. Pushes back on gaps, demands fixes, retries before accepting.
  - **mentor**: Approves forward progress but writes detailed constructive feedback.
- **`--auto-approve`**: Deterministic auto-approval of all human gates. No LLM calls — always picks the default or first option. For testing pipeline flow and CI.
- Uses the pipeline's existing LLM client with low temperature (0.1) for consistent decisions. Structured JSON output with fallback-to-default on error.

## [0.10.3] - 2026-03-26

### Fixed

- **Signature collision in retry detection**: Failure signatures now use null byte separator instead of pipe, preventing false "identical" matches when error strings contain `|`.
- **Duration label clarity**: Shows "Duration (last):" instead of "Duration:" when a node had multiple retries, so users know the value is the last attempt's duration, not total.

## [0.10.2] - 2026-03-26

### Added

- **Deterministic failure detection in `tracker diagnose`**: When a tool node fails multiple times with identical errors, diagnose now flags it as a deterministic bug — "Failed 5 times with identical errors — this is a deterministic bug in the command, not a transient failure. Retrying won't help. Fix the tool command in the .dip file and re-run." Distinguishes deterministic failures (same error every time) from flaky failures (varying errors across retries).
- **Retry count in diagnose output**: Failed nodes now show "Attempts: N failures (all identical — deterministic)" in the diagnosis, so the retry pattern is visible at a glance without reading suggestions.

## [0.10.1] - 2026-03-26

### Changed

- **README rewritten**: Added v0.10.0 features (workflows, init, bare names), mermaid diagrams for build_product milestone loop and architecture layers, full CLI reference section, development section with `dippin test`.
- **CLAUDE.md updated**: Fixed stale `EscalateToHuman` reference in edge routing rules, added `tracker workflows`/`tracker init` docs and bare name resolution section.

### Fixed

- **`suggested_next_nodes` string literal**: Extracted `ContextKeySuggestedNextNodes` constant in `pipeline/context.go`, eliminating 6 scattered string literals across engine and handler code.
- **`enrichFromActivity` cognitive complexity (34 → 18)**: Extracted `enrichFromEntry()` helper for per-line processing.
- **`printDiagnoseSuggestions` cyclomatic complexity (16 → 8)**: Extracted `suggestionsForFailure()` helper. All functions now pass complexity thresholds.

## [0.10.0] - 2026-03-26

### Added

- **Embedded built-in workflows**: The 3 flagship pipelines (`ask_and_execute`, `build_product`, `build_product_with_superspec`) are now embedded in the binary via `go:embed`. Users who install via `brew` or `go install` can run them without cloning the repo.
- **`tracker workflows`**: Lists all built-in workflows with their display names and goals.
- **`tracker init <workflow>`**: Copies a built-in workflow to the current directory for customization. Refuses to overwrite existing files.
- **Bare name resolution**: `tracker build_product`, `tracker validate build_product`, and `tracker simulate build_product` all work with bare workflow names. Local `.dip` files always take precedence over built-ins.
- **`make sync-workflows` / `make check-workflows`**: Makefile targets to keep embedded copies in sync with `examples/`. CI enforces sync.

### Changed

- **Split `EscalateToHuman` into two context-specific gates** in `build_product.dip`:
  - `EscalateMilestone` (mid-build): offers **mark done** (override test, continue to next milestone), **retry** (re-implement from scratch), **accept** (skip to cleanup), **abandon**. Defaults to "mark done".
  - `EscalateReview` (post-build): offers **accept** (ship it), **retry** (back to Decompose), **abandon**. Defaults to "accept".
- **Escalation gates now have `prompt:` blocks** with rich context explaining each option (requires dippin-lang v0.9.0+).

### Fixed

- **TestMilestone early-exit bug**: Previously, the attempt counter was checked *before* running tests. A milestone that was genuinely fixed on attempt 4 would escalate instead of succeeding. Tests now run first; the counter is only checked on failure.
- **Milestone escalation was a dead end**: `EscalateToHuman` had no edge back into the build loop. Choosing "accept" ended the entire build instead of continuing to the next milestone. `EscalateMilestone -> MarkMilestoneDone` now enables "mark done and move on."

### Tests

- **23 dippin simulation tests** for `build_product.dip` covering every edge from both escalation gates, all human gate label selections, fix loop mechanics, and cross-review routing. Uses dippin-lang v0.9.0 features: `preferred_label`, `immediately_after`, and `prompt:` blocks on human gates.
- **18 Go unit tests** for the embedded workflow system: catalog lookup, resolution order (filesystem > local .dip > embedded > error), flag parsing for `workflows`/`init`, init file creation and overwrite protection.

## [0.9.2] - 2026-03-26

### Added

- **`tracker diagnose [runID]`**: Deep failure analysis for pipeline runs. Reads per-node status files and activity logs to surface tool stdout/stderr, error messages, and timing anomalies. Provides actionable suggestions (e.g., stale fix_attempts counter, suspiciously fast execution, missing tools). Without a run ID, analyzes the most recent run.
- **`tracker doctor`**: Preflight health check verifying LLM provider API keys (masked in output), dippin binary availability, and working directory access. Shows actionable hints for every failure.
- **Provider status in `tracker version`**: Shows which LLM providers have API keys configured, or prompts `tracker setup` if none are found.
- **VCS-aware local builds**: `go install` builds now show the git commit hash and build timestamp via Go's embedded VCS metadata, instead of `unknown`. GoReleaser ldflags still take precedence for release builds.
- **Freeform "other" option in review hybrid**: ReviewHybridContent now includes an "other (provide feedback)" option with a textarea, so users can provide custom retry instructions at labeled escalation gates — not just pick from predefined labels.
- **Runtime error surfacing in TUI**: The activity log now shows `FAILED:` and `RETRYING:` messages inline when nodes fail or retry. Previously, tool node failures only updated the sidebar icon with no details visible.

### Fixed

- **ReviewHybridContent phantom cursor**: `totalOptions()` returned `len(labels)+1` creating an unreachable dead-end cursor position. Now correctly bounded to label count + 1 (for "other").
- **Glamour rendering in review hybrid**: The prompt label portion was rendered with plain lipgloss bold, bypassing glamour. Now the full prompt (label + context) goes through glamour so headings, code blocks, and lists render correctly in the viewport.
- **Actionable "no providers" error**: The bare `error: create LLM client: no providers configured` message is replaced with specific env var names and a `tracker setup` hint.

## [0.9.1] - 2026-03-25

### Fixed

- **ReviewHybridContent phantom cursor position**: `totalOptions()` returned `len(labels)+1` creating an unreachable "other" slot with no textarea — cursor could land on a dead-end position that couldn't be submitted. Now correctly bounded to label count only.
- **RadioHeight off-by-one in review hybrid**: Viewport height calculation reserved space for a non-existent "other" option line, wasting a terminal row.

## [0.9.0] - 2026-03-25

### Added

- **Subgraph Loading**: CLI now loads and executes subgraph references from `.dip` files. Path resolution tries relative to parent file, with `.dip` extension auto-appended, recursive loading with cycle detection
- **Hybrid Radio+Freeform Gate**: Human gates with labeled outgoing edges present a radio list of labels plus an "other" option for custom freeform feedback
- **Split-Pane Review View**: Long human gate prompts (20+ lines) use a fullscreen split-pane with glamour-rendered scrollable viewport and textarea
- **Upfront Subgraph Validation**: Every subgraph node is validated at load time — missing refs, empty refs, and circular refs all fail immediately with clear messages

### Fixed

- **Subgraph handler was never wired**: The CLI had SubgraphHandler and WithSubgraphs but never called either — subgraph nodes always failed at runtime with "subgraph not found"
- **Child registry used wrong graph for human gates**: RegistryFactory now overrides WithInterviewer with the child graph so human gates inside subgraphs see the correct edge labels
- **Circular subgraph refs caused runtime stack overflow**: Now detected at load time via absolute-path cycle detection
- **Concurrent subgraph executions shared mutable state**: InjectParamsIntoGraph now deep-clones Attrs, Edges, and NodeOrder instead of sharing pointers
- **Gate deadlocks on cancel**: Ctrl+C and Esc close reply channels on all gate types (Choice, Freeform, Hybrid, Review)
- **Labels hidden by long prompt**: Labeled gates always use hybrid radio view regardless of prompt length
- **Activity log indicator pushed off viewport**: Fixed terminal row budget calculation
- **67 root-level analysis markdown files removed**: Cleaned repo of stale LLM analysis artifacts

## [0.8.0] - 2026-03-25

### Added

- **Decision Audit Trail**: Engine emits structured decision events to activity.jsonl
  - `decision_edge`: which edge was selected, at what priority level, with context snapshot
  - `decision_condition`: every condition evaluated with match result and context values
  - `decision_outcome`: node outcome status, context updates, token counts
  - `decision_restart`: restart count, cleared nodes, context snapshot
- **Skipped Node State**: Unvisited nodes show ⊘ (dim) when pipeline completes
- **Topological Node Ordering**: TUI sidebar uses execution order (Kahn's algorithm), not declaration order or BFS
- **Complexity Enforcement**: Makefile targets and pre-commit hooks enforce cyclomatic ≤ 15, cognitive ≤ 25, file size ≤ 500 LOC
- **Pre-commit Quality Gates**: Format, vet, build, test, race detector, coverage, dippin lint — all enforced on every commit
- **Pipeline Test Scenarios**: `.test.json` files for all three core pipelines with happy path and failure scenarios
- **CLAUDE.md**: Project rules, versioning policy, and architecture gotchas for AI-assisted development
- **Subgraph Event Propagation**: Child pipeline engines emit events visible to the parent TUI
- **Per-Branch Parallel Config**: Parallel fan-out nodes can override target node attributes per branch
- **Per-Node Working Directory**: `working_dir` attribute on agent and tool nodes for git worktree isolation
- **Variable Interpolation**: Full `${namespace.key}` syntax — `ctx.*`, `params.*`, `graph.*` namespaces
- **Pipeline Examples**: `ask_and_execute.dip`, `build_product.dip`, `build_product_with_superspec.dip`

### Changed

- **Major complexity refactoring**: 35 cyclomatic violations → 0, 30 cognitive violations → 0, 7 oversized files → 0
  - `engine.go` (1002 lines, cyclomatic 61) → 4 files, max cyclomatic 12
  - `main.go` (1228 lines) → 8 focused files, max 378 lines
  - All 3 LLM adapters, codergen handler, parallel handler, condition evaluator, dippin adapter decomposed
- **dippin-lang upgraded** to v0.8.0 (explain, unused, graph, test commands; DIP121/DIP122 lint rules; exhaustive condition detection; model catalog with verified pricing)
- **GoReleaser**: quality gates in before hooks, grouped changelog (Features/Fixes/Other)
- **CI workflow**: full gate suite (format, vet, build, test, race, coverage, lint, doctor, complexity)
- **TUI activity log**: rewritten — per-node streams, line-level styling (no glamour), append-only with 10k line cap
- **TUI human input**: bubbles/textarea with wrapping, multiline, Ctrl+S submit, Esc cancel
- **Build product pipeline**: opus fix agent with 50 turns, per-milestone circuit breaker (3 attempts then escalate), known test failures support

### Fixed

- **OpenAI SSE error handling**: `error` and `response.failed` events parsed and surfaced as typed errors (was silently dropped)
- **Non-retryable provider errors**: quota, auth, model not found now crash immediately (was `OutcomeRetry`)
- **Empty agent responses**: zero-output sessions return `OutcomeFail` (was `OutcomeSuccess`)
- **Parallel handler**: navigates to join node via `suggested_next_nodes`; dispatches only branch targets; panic recovery in goroutines; emits stage events per branch
- **Condition evaluator**: resolves `ctx.*`, `context.*`, `internal.*` prefixes; handles infix negation; warns on unresolved variables
- **Variable expansion**: single-pass prevents infinite loops; malformed tokens skipped instead of stopping all expansion
- **Freeform human gates**: match response text against edge labels for routing
- **Thinking spinner**: emitted from agent events (with nodeID) not global LLM trace
- **Activity log viewport**: counts terminal rows, reserves indicator line, stable rendering
- **Pipeline routing**: removed unconditional fallbacks that caused infinite loops; merge conflicts escalate to human; ReadSpec/Decompose gated on success
- **Provider naming**: `gemini` not `google` everywhere
- **Checkpoint**: save failures use correct event type; per-node edge selections for deterministic resume
- **All 25 example pipelines**: grade A on `dippin doctor` (was 10 F's)

## [0.7.0] - 2026-03-25

(See GitHub release for v0.7.0 changelog)

## [Previous Versions]

See [GitHub releases](https://github.com/2389-research/tracker/releases) for earlier versions.
