# #303 — Turn-limit breach = guard, not guillotine

**Issue:** #303 (P1, area/engine + area/agent + refactor). Epic #308, Phase 1.
**Builds on (all merged into `main`):** #302 (`CommitWIP` recoverable refs, PR #315),
#295 (graph-level `fallback_target` catch-all, PR #311), #297 (build_product
commit-first / stop-when-green, PR #314).
**Branch:** `fix/303-turn-limit-guard` (off `origin/main` @ `003d91a`).
**Status:** revised after a 6-expert panel review (2026-06-05). The review
corrected the central persistence mechanism and surfaced several real bugs; this
revision absorbs every CRITICAL and IMPORTANT finding. See "Review corrections".

## Problem

Turn-limit exhaustion maps unconditionally to `OutcomeFail`. `codergen.go`
`buildSuccessOutcome` flips status to `OutcomeFail` whenever `buildTurnLimitMsg`
is non-empty (codergen.go:553-556). That conflates two situations:

- **Pathological** — a looping / no-progress agent. Stopping is correct.
- **Steady progress that ran out of a coarse budget** — e.g. `code-goblin` run
  `7b6e08c9e2b2`, GREEN at turn 48 (`go test ./...` + `go vet`) but uncommitted.
  The breach discarded the milestone; the cross-review/FinalSpecCheck safety net
  never ran. (In build_product today, `Implement`'s success edge routes to
  `CommitIfDirty` (#297); on a *fail*/breach it routes to `EscalateMilestone`
  and the commit node is never reached — so the green tree is lost.)

`buildTurnLimitMsg` (codergen.go:616-624) already distinguishes `LoopDetected`
from plain exhaustion, but only in the message text — the distinction never picks
an outcome.

## How persistence ACTUALLY works (corrected by review — read this first)

The original spec assumed the engine's success path commits the dirty tree. **It
does not, in any real run:**

- `WithGitArtifacts` is **never called outside tests** (verified: no non-test
  caller in the repo). So in a normal `tracker run`, the engine's git repo is
  `nil` and `CommitNode` / `CommitWIP` / `commitWIPBeforeRouting` are **all
  no-ops** (graceful skip). #302's WIP preservation only fires when a *library
  embedder* opts into `WithGitArtifacts` — not the CLI.
- Even when enabled, the artifact repo lives at `<workdir>/.tracker/runs/<runID>`
  (engine_run.go:316-318; `git -C <dir>`) — a sibling of the agent's working
  tree, not its root. `git add .` there cannot stage product code written to
  `<workdir>/`.

**Product code is persisted entirely by `.dip` tool nodes and the agent's own
commits.** build_product's `CommitIfDirty` (#297) does `git status --porcelain`
→ `git add -A` → `git -c user.name=... commit` in the **working-tree product
repo** (build_product.dip:462-482), on `Implement`'s success edge.

**Consequence for #303:** "commit-if-green" is NOT an engine commit. It is
purely a **reclassification**: a green breach returns `OutcomeSuccess`, which
takes the node's *success* edge — and in build_product that edge already routes
through `CommitIfDirty`, which commits the product tree. PR1 therefore needs
**zero engine commit code** (true — but because returning success routes through
the pipeline's own commit-on-success node, not because the engine commits
anything). For a pipeline with no commit-on-success node, persisting green work
is the pipeline author's responsibility — exactly as all product persistence
already works in tracker. This boundary (engine never reaches into the user's
real working repo) matches #302's explicit design and is preserved.

## Reusable mechanisms (confirmed against code)

1. **The verify affordance.** `newVerifier(cfg)` + `verifier.run(ctx)`
   (agent/verify.go) run *outside* the `MaxTurns` budget. Verify-on-breach = run
   it once after the turn loop exhausts. Caveat: `newVerifier` returns nil when
   `!cfg.VerifyAfterEdit` and the whole file is `//go:build !windows`.
2. **`wait.human`** (PR2) already supports 4 labeled edges + autonomous
   interviewers (`AutoApprove`/`Autopilot`/`Webhook`).
3. **dippin v0.35.0:** `max_turns`/`fallback_target` first-class; `defaults:` has
   the budget ceilings. No turn-breach attr exists. A custom attr is accepted
   **only inside a `params:` block** — a bare `turn_breach_policy:` on an agent
   node is a **fatal parse error** (empirically verified). Params spill into
   `node.Attrs` via dippin_adapter.go:297-301 (only if no typed field set the key).

## Design decisions (squad consensus)

| # | Subtlety | Decision |
|---|----------|----------|
| 1 | Layering seam | Three-layer contract (below). agent produces facts; codergen classifies into outcome + `turn_breach_class` marker; engine routes with existing machinery. **The decision to run verify-on-breach is policy → codergen sets a `VerifyOnBreach` flag in `SessionConfig`; the agent stays mechanism-only.** |
| 2 | Operator-node shape | **Reuse `wait.human`** (PR2). Reached via author-wired `when ctx.turn_breach_class = operator_decision`. |
| 3 | commit-if-green | **Classification only.** Green breach → `OutcomeSuccess` → the pipeline's success-path commit node persists the tree (build_product: #297 `CommitIfDirty`). No engine commit code. |
| 4 | New `.dip` attrs | `turn_breach_policy` inside a **`params:` block** (the only form v0.35.0 accepts), read through a typed `AgentNodeConfig` accessor. Default (`guard`) needs **no** `.dip` change. |
| 5 | Never silent-advance | The ONLY path to success on a breach is `BreachVerify==Passed` from an **explicit** `cfg.VerifyCommand`. `auto_status` cannot manufacture success on a breach; auto-detected-only verify → `operator_decision`. |
| — | Default policy | **`guard` is the default.** `turn_breach_policy: fail` opts back into today's guillotine. |
| — | Delivery | **Two PRs.** PR1 = classification core (this doc's focus). PR2 = operator node + warm continue+N. |

## The seam — three layers

```
agent/ (mechanism, native only)        codergen (policy + classify)        engine (route)
───────────────────────────────        ────────────────────────────        ──────────────
runTurnLoop exhausts → MaxTurnsUsed     buildConfig sets cfg.VerifyOnBreach  success → take success edge
if cfg.VerifyOnBreach && !LoopDetected    = (policy != "fail")                 (pipeline's commit node
  → run verify once (explicit cmd only) classifyBreach(node,sessResult,policy)  persists the tree)
  → SessionResult.BreachVerify            → status + class                    fail → fallback_target /
                                          buildSessionStats carries BreachVerify   strict-failure (#295),
                                          ctx.turn_breach_class = class             WIP if git-artifacts on
```

## PR1 — verify → classify (core). Go only. No `.dip` edits.

Scope: the **native** backend only (`agent.Session`). claude-code/acp never set
`MaxTurnsUsed`, so they never enter the breach branch today — but PR1 adds an
**explicit native guard** so a future change to their result-building can't
silently misclassify (see step 2).

### 1. agent layer — verify-on-breach (mechanism)

- `agent/result.go`: add `BreachVerify BreachVerifyState` to `SessionResult`.
  Tri-state enum: `BreachVerifyNotRun` (0, zero value) / `BreachVerifyPassed` /
  `BreachVerifyFailed`. (Enum, not a bool pair, so "couldn't verify" ≠ "verified
  red"; the zero value is the safe NotRun.)
- `agent/config.go`: add `VerifyOnBreach bool` to `SessionConfig`. The *agent*
  reads only this flag — it never sees `turn_breach_policy`.
- `agent/session.go`: at the `result.MaxTurnsUsed = true` site (session.go:210-212),
  if `cfg.VerifyOnBreach && !result.LoopDetected`, run one verify pass and record
  `BreachVerify`. Gated behind `runTurnLoop` returning `err == nil` (provider
  errors return early — verify-on-breach must never mask them).
- `agent/verify.go` (+ `verify_windows.go` no-op stub): add
  `resolveBreachVerifier(cfg)` — returns a verifier **only when `cfg.VerifyCommand`
  is explicitly set** (NOT auto-detected; decision #5). Independent of the
  `VerifyAfterEdit` gate. Reuse `verifier.run`; emit `EventVerify`. A real
  execution error (binary missing, bad workdir — distinct from a test failure)
  → `BreachVerifyFailed` **and** surface the error text via `EventVerify` /
  `ctx` (CLAUDE.md: never swallow). No explicit command → `NotRun`.

### 2. codergen layer — policy + classify

- `pipeline/node_config.go`: add `TurnBreachPolicy string` to `AgentNodeConfig`,
  read from `turn_breach_policy` (graph-default then node-override; struct-literal
  default `"guard"`). An unrecognized value warns and defaults to `guard`.
- `pipeline/handlers/codergen.go` `buildConfig`: set
  `config.VerifyOnBreach = (policy != "fail")` so the opt-out path pays no verify
  cost.
- **Extract** the classification into a helper to stay under the gocyclo/gocognit
  ≤8 gate (`buildSuccessOutcome` is already at 8/8):

  ```
  // classifyBreach returns (status, class). Called only when turnLimitMsg != "".
  func classifyBreach(policy string, r agent.SessionResult, native bool) (TerminalStatus, string) {
    if policy == "fail" || !native { return OutcomeFail, "" }   // opt-out / non-native: today's behavior
    switch {
    case r.LoopDetected:                  return OutcomeFail,    "pathological"
    case r.BreachVerify == Passed:        return OutcomeSuccess, "verified_green"
    default:                              return OutcomeFail,    "operator_decision" // Failed OR NotRun
    }
  }
  ```

- In `buildSuccessOutcome`: when `turnLimitMsg != ""`, call `classifyBreach`.
  Write `ctx.turn_breach_class` (new `ContextKeyTurnBreachClass`) **only when
  class != ""** (absent, not empty, on the opt-out path).
- **auto_status (decision #5):** on a breach (`turnLimitMsg != ""`), `auto_status`
  must NOT be able to upgrade a non-green classification to success. Apply
  `auto_status` only on the non-breach path, or only let it *downgrade* on a
  breach — a missing/early `STATUS:` line (which `parseAutoStatus` defaults to
  success) cannot manufacture a breach success.
- **applyDeclaredWrites precedence:** it can flip status → Fail *after*
  classification. Write `turn_breach_class` only after the final status is known
  (or clear `verified_green` if a declared-writes failure demotes to Fail) so the
  trace is never `Fail` + `verified_green`.
- `pipeline/handlers/transcript.go` `buildSessionStats` + `pipeline.SessionStats`:
  carry `BreachVerify` so the trace entry actually shows the verify result
  (AC: "trace shows verify+commit, not fail").
- Determine `native` from the resolved backend (`selectBackend`), threaded into
  the classify call.

### 3. engine layer — nothing new

Green breach → `OutcomeSuccess` → success edge (pipeline's commit node persists).
Non-green/pathological → `OutcomeFail` → existing `fallback_target` (#295) /
strict-failure routing (+ `commitWIPBeforeRouting` when git-artifacts is on). The
new `turn_breach_class` marker is advisory; routing is still governed by fail-edge
selection — a node without an operator edge falls back to #295/halt. Context-key
marker (not a typed `Outcome` field) is required because edge conditions can read
only `PipelineContext` strings, never `Outcome` fields (condition.go).

### PR1 tests (TDD — each watched failing first; negative control)

agent (`agent/session_test.go`, `agent/verify_test.go` patterns; mock completer +
`MaxTurns` drives exhaustion, temp-dir shell scripts as `VerifyCommand`):
- `TestSession_VerifyOnBreach_Green_RecordsPassed` (explicit cmd, exit 0).
- `TestSession_VerifyOnBreach_Red_RecordsFailed` (exit 1).
- `TestSession_VerifyOnBreach_NoExplicitCommand_NotRun` (auto-detect not used).
- `TestSession_VerifyOnBreach_ExecError_RecordsFailedAndSurfaces`.
- `TestSession_LoopDetected_SkipsBreachVerify` — assert via a sentinel side-effect
  (verify writes a file; assert absent) so it fails-first once verify is added
  without the loop guard.
- `TestSession_VerifyOnBreach_DisabledFlag_NoRun` (`VerifyOnBreach=false`).

codergen (`pipeline/handlers/codergen_test.go`):
- `TestCodergen_BreachGreen_AdvancesAsSuccess` — asserts `OutcomeSuccess` **and**
  `ctx.turn_breach_class=verified_green` (both, so dropping the marker fails).
- `TestCodergen_LoopDetected_ClassifiesPathologicalFail` — `BreachVerify=Passed`
  ignored; assert `class=pathological` (the marker is the fail-first signal,
  since status is already fail today).
- `TestCodergen_BreachNonGreen_RoutesOperatorMarker` (`Failed` → operator).
- `TestCodergen_BreachNotRun_RoutesOperatorMarker` (tri-state `NotRun` → operator
  — proves the `default` arm catches the zero value).
- `TestCodergen_TurnBreachPolicyFail_PinsGuillotine` — opt-out reproduces today's
  outcome + the **exact** `buildTurnLimitMsg` string (hard-coded golden), asserts
  `turn_breach_class` **absent**; covers BOTH plain-exhaustion and LoopDetected.
- `TestCodergen_BreachGreen_AutoStatusCannotForceSuccessWhenRed` — auto_status:true
  + verify RED/NotRun + no STATUS line → stays Fail.
- `TestCodergen_BreachGreen_DeclaredWritesDemotion_ClearsMarker`.
- `TestCodergen_NonNativeBackend_BreachUnchanged` — claude-code/acp result → no
  marker, today's behavior.

build_product .dip-level (`pipeline/build_product_failure_routing_test.go`): the
case-study rescue is proven at the routing level — a green breach takes the
`Implement -> CommitIfDirty` success edge (not `-> EscalateMilestone`). (This,
not an artifact-repo assertion, is where "green work is persisted" is verified.)

Verification adds `make complexity` (CI-only gate) to catch the ≤8 break.

## PR2 — operator-decision node + warm continue (follow-up, separate PR)

Sketch; finalized in its own plan. Review-flagged hazards to design around:

- **Operator node = `wait.human`, freeform mode**, labeled edges
  `continue` / `commit_advance` / `stop` / `abandon`, reached via
  `when ctx.turn_breach_class = operator_decision` (ordered before the
  unconditional `-> EscalateMilestone` fallback; narrow `= operator_decision` so
  `pathological` still falls through to the catch-all).
- **Autonomous default — footgun:** freeform mode reads the `default` attr (NOT
  `default_choice`). Must pin `default: stop`, and order `stop`/`abandon` before
  `continue` so a missing-default regression fails safe. `AutoApprove`/`Webhook`
  resolve to the default; **`--autopilot lax`/`mentor` bias to "forward progress"
  and are not deterministically safe** — document, and frame the prompt so
  "continue" is the risky option.
- **Warm `continue +N` needs NEW plumbing:** `MaxTurns` is read statically;
  nothing reads context. PR2 must add a context/disk-driven `MaxTurns` override in
  `buildConfig`. `PriorEpisodeSummaries` already carry across (warm) for free.
- **Cap circuit-breaker** in a tool node that gates the operator's `continue`
  edge (mirror build_product `fix_attempts`), since the global `RestartCount`
  (engine) is shared across all loops and unsuitable. `BudgetGuard` covers only a
  global cost backstop, not a per-loop ceiling.
- **`commitWIPBeforeRouting` bypass:** it runs only inside `checkStrictFailure`,
  which early-returns when a node has conditional edges — so adding the operator
  conditional edge means the fail→operator route skips WIP. (Moot while
  git-artifacts is off, but real if enabled.) Address in PR2.
- **dippin:** the continue attr/cap rides in a `params:` block. New cycle +
  4-way gate expands `dippin simulate -all-paths`; every operator edge must be
  labeled and reach a terminal (DIP005). Re-run doctor/simulate to A / 100%.
- **UX:** rely on the standard `last_response` append (`\n\n---\n`) so the gate
  routes through `ReviewHybridContent` (fullscreen glamour) — don't inline the
  long message into the node `label`.
- **Windows:** breach-verify entry point guarded by a `_windows.go` stub.

## Review corrections (what changed from the first draft)

1. commit-if-green is classification-only; the engine does not (and by default
   cannot) persist the product tree — the pipeline's commit-on-success node does.
2. Verify-on-breach decision moved out of `agent/` via `SessionConfig.VerifyOnBreach`.
3. Strict green bar: explicit `VerifyCommand` required; `auto_status` can't force
   breach success; auto-detected-only → operator.
4. Explicit native guard in `classifyBreach`.
5. `classifyBreach` helper extraction to satisfy the ≤8 complexity gate (CI-only).
6. `turn_breach_policy` must live in a `params:` block (bare = fatal parse error).
7. `BreachVerify` added to `buildSessionStats` so the trace shows it.
8. Opt-out pin hard-codes the golden message + asserts marker absent + covers loop.
9. Verify execution-error handling defined (→ Failed + surfaced).
10. PR2 footguns documented (auto-approve default attr, MaxTurns plumbing,
    RestartCount, WIP-bypass-on-conditional-edge, simulate cycle, Windows).

## Out of scope

#304 (cost + no-progress detector — the `classifyBreach` switch leaves a clean
extension point; `LoopDetected` suffices for pathological now), #298, #299,
#300/#301, #305, #306, #313.

## Verification (both PRs)

`go build ./...` · `GOOS=darwin GOARCH=arm64 go build ./...` ·
`go test ./... -short` · `go test -race -short ./pipeline/ ./agent/` ·
**`make complexity`** (the ≤8 gate that the pre-commit hook skips) ·
`dippin doctor` on the three core pipelines (A grade; PR1 touches no `.dip`).
CHANGELOG under [Unreleased]: graduated turn-limit guard
(verify → classify → operator decision) + `turn_breach_policy` opt-out; builds on
#302/#295/#297; completes the Phase-1 turn-limit track.
