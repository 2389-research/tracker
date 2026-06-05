# #303 — Turn-limit breach = guard, not guillotine

**Issue:** #303 (P1, area/engine + area/agent + refactor). Epic #308, Phase 1.
**Builds on (all merged into `main`):** #302 (`CommitWIP` recoverable refs, PR #315),
#295 (graph-level `fallback_target` catch-all, PR #311), #297 (build_product
commit-first / stop-when-green, PR #314).
**Branch:** `fix/303-turn-limit-guard` (off `origin/main` @ `003d91a`).

## Problem

Turn-limit exhaustion maps unconditionally to `OutcomeFail`. `codergen.go`
`buildSuccessOutcome` flips status to `OutcomeFail` whenever `buildTurnLimitMsg`
is non-empty (codergen.go:553-556). That conflates two very different
situations:

- **Pathological** — a looping / no-progress agent. Stopping is correct.
- **Steady progress that ran out of a coarse budget** — e.g. `code-goblin` run
  `7b6e08c9e2b2`, GREEN at turn 48 (`go test ./...` + `go vet` passed) but
  uncommitted. The breach discarded the milestone; the cross-review/FinalSpecCheck
  safety net never ran.

`buildTurnLimitMsg` (codergen.go:616-624) *already* distinguishes `LoopDetected`
from plain exhaustion — but only in the message text. The distinction never
picks an outcome.

## Key findings from the code (these shape the design)

1. **The success path already commits the dirty tree.** `advanceToNextNode →
   emitGitCommit → CommitNode` runs `git add .` + `git commit` in the artifact
   repo on *every* successful node (engine.go:450, git_artifacts.go:131). So
   **"commit-if-green" needs no new engine commit code**: if codergen classifies
   a green breach as `OutcomeSuccess`, the existing machinery commits and
   advances. `CommitWIP` (#302) stays on the fail paths only. → no double-commit,
   no orphan ref, no new primitive.

2. **The verify affordance is reusable as-is.** `newVerifier(cfg)` +
   `verifier.run(ctx)` (agent/verify.go) already run *outside* the `MaxTurns`
   budget. Verify-on-breach = run it once after the turn loop exhausts.

3. **`wait.human` already does everything the operator node needs.** Freeform +
   labeled-edges mode + `LabeledFreeformInterviewer`; `AutoApproveInterviewer` /
   `AutopilotInterviewer` / `WebhookInterviewer` all implement the interfaces and
   carry `Actor()`. The autonomous default is the existing
   `default_choice` / `TimeoutAction` machinery — so reusing `wait.human` makes
   `--auto-approve` / `--autopilot` / `--webhook-url` work for free.

4. **dippin-lang v0.35.0:** `max_turns` and `fallback_target` are first-class
   node attrs; `defaults:` carries `max_total_tokens` / `max_cost_cents` /
   `max_wall_time`. There is **no** first-class `turn_breach_policy` attr — it
   rides via `node.Attrs` (Params pass-through). To be verified with
   `dippin validate` before PR2 relies on it; PR1 needs it only parseable.

## Design decisions (agreed)

| # | Subtlety | Decision |
|---|----------|----------|
| 1 | Layering seam | Three-layer contract (below). agent produces facts; codergen turns facts into outcome + a `turn_breach_class` marker; engine routes/commits with existing machinery. |
| 2 | Operator-node shape | **Reuse `wait.human`** (no new handler). Reached via author-wired `when ctx.turn_breach_class = operator_decision` edge. Autonomous default via existing interviewer machinery. |
| 3 | commit-if-green vs `CommitWIP` | Green breach → `OutcomeSuccess` → existing success-path `CommitNode` commits the dirty tree as a real advance. `CommitWIP` unchanged, fail-paths only. |
| 4 | New `.dip` attrs | `turn_breach_policy` (+ PR2's continue-cap) via `node.Attrs` Params pass-through, read through a typed accessor on `AgentNodeConfig`. Default behavior needs **no** `.dip` change. |
| 5 | Never silent-advance | A non-green breach is `OutcomeFail` → routes to `fallback_target`/halt with WIP preserved (#302). Verify failure ≠ success. Provider errors still hard-fail (untouched). |
| — | Default policy | **`guard` is the default.** `turn_breach_policy: fail` opts back into today's guillotine. |
| — | Delivery | **Two PRs.** PR1 = core (this doc's focus). PR2 = operator node + warm continue+N. |

## The seam — three layers

```
agent/ (facts)                codergen (classify)              engine (route/commit)
─────────────────             ───────────────────             ─────────────────────
runTurnLoop exhausts          buildSuccessOutcome reads        success → emitGitCommit
  → MaxTurnsUsed=true           MaxTurnsUsed / LoopDetected       commits dirty tree (exists)
  → (new) verify-on-breach      / BreachVerify + policy         fail → commitWIPBeforeRouting
  → SessionResult.BreachVerify  → Outcome.Status                  (#302) → fallback/operator
                                + ctx.turn_breach_class
```

No logic is smeared across layers: each layer has one job and a typed contract.

## PR1 — verify → commit-if-green → classify (core)

**Go only. No `.dip` edits. dippin doctor unchanged (A grade, nothing touched).**
Scope: the **native** backend (`agent.Session`). claude-code/acp breach handling
is unchanged (they don't drive `agent.Session`'s turn loop); noted, not solved.

### 1. agent layer — verify-on-breach (facts)

- `agent/result.go`: add `BreachVerify BreachVerifyState` to `SessionResult`.
  Tri-state: `BreachVerifyNotRun` (0, default) / `BreachVerifyPassed` /
  `BreachVerifyFailed`. Tri-state (not `bool`) so codergen can tell "verified
  failing" from "could not verify" — both are non-green, but the distinction is
  useful in the trace and for #304 later.
- `agent/session.go`: after `runTurnLoop` returns `stoppedNaturally == false`
  (the existing `result.MaxTurnsUsed = true` site, session.go:210-212), and only
  when **not** `LoopDetected` (pathological skips verify — we're stopping
  regardless), run a single verify pass and record the state.
- `agent/verify.go`: add `resolveBreachVerifyCommand(cfg)` — resolves
  `cfg.VerifyCommand` else `detectVerifyCommand(cfg.WorkingDir)`, **independent of
  the `VerifyAfterEdit` gate** (the point of verify-on-breach is to rescue green
  work even when the in-loop verifier was off). No command resolvable →
  `BreachVerifyNotRun` → treated as non-green downstream (safe; never advances).
  Reuse `verifier{cmd,workDir}.run(ctx)`; emit the existing `EventVerify`.

### 2. codergen layer — classify (outcome + marker)

`pipeline/handlers/codergen.go` `buildSuccessOutcome`. Add a typed
`TurnBreachPolicy` field to `AgentNodeConfig` (node_config.go) read from
`turn_breach_policy` (default `guard`; `fail` = opt-out). Replace the flat
"`turnLimitMsg != "" → Fail`" with:

```
if turnLimitMsg != "" {                      // a breach happened
  switch policy {
  case "fail":                               // opt-out: today's guillotine, pinned
      status = OutcomeFail
      class  = "" (unset — preserve exact prior behavior)
  default: // "guard"
      switch {
      case sessResult.LoopDetected:          // pathological
          status = OutcomeFail;  class = "pathological"
      case sessResult.BreachVerify == Passed: // green → rescue
          status = OutcomeSuccess; class = "verified_green"
      default:                                // steady progress, non-green
          status = OutcomeFail;  class = "operator_decision"
      }
  }
}
```

- `auto_status: true` still overrides (an explicit STATUS line is authoritative)
  — applied after, exactly as today.
- `class` is written to `ctx.turn_breach_class` (new `ContextKeyTurnBreachClass`)
  so PR2's operator edge can route on it. Also keep setting
  `ctx.turn_limit_msg` (unchanged).
- Green path keeps `last_response` / `episode_summaries` updates, so the advance
  is a normal warm success — the trace entry is `success` and carries the
  verify result, **not** fail (AC: "trace shows verify+commit, not fail").

### 3. engine layer — nothing new for green

Green breach → `OutcomeSuccess` → existing `emitGitCommit`/`CommitNode` commits
the dirty tree. Non-green/pathological → `OutcomeFail` → existing
`commitWIPBeforeRouting` (#302) + `fallback_target`/strict-failure routing
(#295). No engine edits in PR1 beyond what the marker needs (a context key).

### PR1 tests (TDD — each watched failing first; negative control)

agent:
- `TestSession_VerifyOnBreach_Green_RecordsPassed` — exhaust turns with a green
  tree → `SessionResult.BreachVerify == Passed`.
- `TestSession_VerifyOnBreach_Red_RecordsFailed`.
- `TestSession_VerifyOnBreach_NoCommand_NotRun`.
- `TestSession_LoopDetected_SkipsBreachVerify` — pathological never verifies.

codergen:
- `TestCodergen_BreachGreen_AdvancesAsSuccess` — `BreachVerify=Passed` →
  `OutcomeSuccess`, `ctx.turn_breach_class=verified_green`. *(fails today: returns fail)*
- `TestCodergen_LoopDetected_ClassifiesPathologicalFail` — always fail, never
  success, regardless of verify.
- `TestCodergen_BreachNonGreen_RoutesOperatorMarker` — `OutcomeFail` +
  `class=operator_decision`.
- `TestCodergen_TurnBreachPolicyFail_PinsGuillotine` — opt-out reproduces today's
  outcome **and** error/message byte-for-byte; no `turn_breach_class` set.

engine (integration):
- `TestEngine_BreachGreen_CommitsDirtyTreeAndAdvances` — drive a node to a
  green breach; assert the artifact repo committed the file (CommitNode) and the
  run advanced past the node, no `tracker/wip/...` ref created.
- `TestEngine_BreachNonGreen_PreservesWIPAndRoutesFallback` — non-green breach
  still makes a WIP ref (#302 path) and routes to `fallback_target`.

Negative control: with the codergen classification reverted, the green/operator
tests fail (node returns fail / no marker) — confirmed, then restored.

## PR2 — operator-decision node + warm continue (follow-up, separate PR)

Sketch only; finalized in its own plan.

- **Operator node = `wait.human`** with labeled edges:
  `continue` / `commit_advance` / `stop` / `abandon`. Reached via
  `<Implement> -> <Operator> when ctx.turn_breach_class = operator_decision`.
- **Autonomous default** = node `default_choice` + `TimeoutAction` → the issue's
  safe default (`stop + commit-WIP + route-to-fallback`, never silent advance).
  `--auto-approve` (deterministic first/default), `--autopilot <persona>` (LLM
  judge), `--webhook-url` (external callback) all flow through the existing
  interviewer machinery — no parallel path.
- **`continue +N` = warm resume.** `continue` edge routes back to the codergen
  node; on re-entry it bumps `MaxTurns` by N and carries `PriorEpisodeSummaries`
  (already populated via `applyEpisodeContextUpdates` →
  `ContextKeyEpisodeSummaries` → `injectPriorEpisodes`). Bounded by a **cap**
  (turn cap and/or cost ceiling) enforced with a per-node circuit breaker — a
  disk counter à la build_product's `fix_attempts` (CLAUDE.md: the checkpoint
  restart counter is global and unsafe for this).
- **`.dip` attrs:** the continue-cap (+ operator-node shape) ride via Params
  pass-through; `dippin validate` confirmed first. If rejected → request a
  dippin-lang grammar change (never `go install`).
- **build_product wiring + dippin doctor/simulate** re-run to A grade and
  100% terminate-success once the operator node is added.

## Out of scope (do not touch)

#304 (cost + no-progress detector — PR1 may *consume* a no-progress signal later
but works without it; `LoopDetected` suffices for pathological), #298, #299,
#300/#301, #305, #306, #313.

## Verification (both PRs)

`go build ./...` · `GOOS=darwin GOARCH=arm64 go build ./...` ·
`go test ./... -short` · `go test -race -short ./pipeline/ ./agent/` ·
`dippin doctor examples/build_product.dip examples/ask_and_execute.dip
examples/build_product_with_superspec.dip` (A grade; PR1 touches no `.dip`).
CHANGELOG under [Unreleased]: graduated turn-limit guard
(verify → commit-if-green → classify → operator decision) + `turn_breach_policy`
opt-out; builds on #302/#295; completes the Phase-1 turn-limit track.
