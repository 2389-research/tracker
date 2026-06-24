# Fresh-session prompt: `build_product` dogfooding-hardening super-PR

> Paste everything below the line into a fresh Claude Code session at the repo root
> (`/home/clint/code/2389/tracker`, branch `main`, clean tree). It is self-contained.

---

## Mission

A dogfooding run of `build_product` (code-goblin, run **`634a2527ff56`**, 2026-06-23) exposed a
chain of defects that, together, turned a **correct, green build into a discarded one**. Your job is
to fix the whole chain in one reviewable super-PR, with TDD discipline, ralph loops, and
checkpointed subagent reviews — pragmatic, surgical, no over- or under-correction.

**Read these before touching anything** (in order):
1. `CLAUDE.md` (project rules — especially *Edge routing*, *Strict failure edges*, *Never silently
   swallow errors*, *Tool node safety*, *Before committing*). These override your defaults.
2. The seven GitHub issues — `gh issue view 405 406 407 408 409 353 306` (run each). Treat their
   **Acceptance criteria** as your test specification.
3. `docs/architecture/engine.md` (run loop, edge selection, escalation) and
   `docs/architecture/handlers.md`.

### The cascade (why these belong in one PR)

In run `634a2527ff56`, on the final milestone:

1. **#409** — `Implement` shipped a smoke test that *passed* but called an unexported function
   **in-process** instead of spawning the **built binary** the spec contract required.
2. **#408** — `VerifyMilestone` then `FAIL`ed behaviorally-correct signal-handling code purely
   because it used `signal.Notify` + a channel instead of the literal `signal.NotifyContext` named
   in spec prose — even though all signal tests passed and the spec permits documented deviations.
3. **#405** — `CommitIfDirty` swept a compiled `goblin` binary (a test build artifact) into a
   checkpoint commit; Verify flagged that as out-of-scope work too. `.gitignore` only had `.ai/`.
4. The combined false-positive `FAIL` forced an expensive `FixMilestone` loop. It **fixed all three
   findings and reached a fully green tree at turn 47**, then burned turns 48–50 on read-only
   `git status` and hit the hard 50-turn limit.
5. **#406** — the #303/#297 *commit-if-green* turn-limit guard **did not fire on `FixMilestone`**,
   so the green tree was left **uncommitted**; HEAD stayed at the broken pre-fix commit.
6. **#407** — the breach routed to `EscalateMilestone`, which offered `abandon` **without surfacing
   that the milestone's verify command currently PASSED**. The operator abandoned a finished,
   passing build.

**#306** is the upstream prevention (canonical spec rulings so #408-class ambiguities never ship).
**#353** is a separate cost defect from an earlier run (`b68b532619c3`): one reviewer burned 32% of
run cost duplicating a 42-second finding.

---

## Two critical "already exists — do NOT rebuild" facts

Confirm these by reading the code before you start; they change the shape of two issues:

- **#406 is a debugging task, not a build task.** The commit-if-green guard already exists:
  `classifyBreach()` and `applyBreachMarker()` in `pipeline/handlers/codergen.go` (~L860–910) map a
  turn-limit breach with `r.BreachVerify == agent.BreachVerifyPassed` →
  `TurnBreachClassVerifiedGreen` → `OutcomeSuccess`, and the success edge (e.g. `CommitIfDirty`)
  persists the tree. Constants live in `pipeline/context.go` (~L66–83). The real question:
  **why was `BreachVerify` not `Passed` for `FixMilestone` despite a green verify at breach?** Likely
  candidates to investigate: `turn_breach_policy` not set (or not inherited as a graph default) on
  `FixMilestone`; the breach-time verify command not wired/run for that node; or the verify ran but
  its result wasn't classified `Passed`. Reproduce from the run checkpoint, then fix the wiring —
  don't reimplement the guard. See `agent/session.go`, `agent/result.go`, `agent/config.go` for how
  `BreachVerify` is computed.

- **#353's per-node cost ceiling already shipped** (issue #304, v0.40.0). `MaxCostUSD` and
  `NoProgressTurns` are parsed in `CodergenHandler.buildConfig` (`pipeline/handlers/codergen.go`)
  and emit `EventNodeCostLimitExceeded` / `EventNodeNoProgressDetected`. So #353 reduces to:
  (a) **apply** `max_cost_usd` to the review nodes in `build_product.dip`; (b) per-backend
  caching-awareness (shrink context / lower turns when the backend reports no caching);
  (c) **change the fan-out shape** — single reviewer first, panel only on FAIL/large-diff/dispute;
  (d) dedupe the reviewer ↔ `FinalSpecCheck` rubric overlap.

---

## Workstreams

Group the issues by code locus and dependency. **Within a workstream the changes are coupled and
must be designed together.**

### WS-A — Green-tree durability & artifact hygiene  (#405, #406, #407)
The "correct build discarded" cluster. #406 is the root cause (engine), #407 the safety net (.dip
prompt + verify-state surfacing), #405 removes the noise that triggered the cascade (.dip + scaffold).

- **#405** — `Setup`/Milestone-1 seeds a toolchain-appropriate `.gitignore` (Go binary names,
  `*.test`, `target/`, `dist/`, `node_modules/`, …); `CommitIfDirty` skips/untracks known build
  artifacts instead of committing them; Verify's "no extra work" check treats a gitignored artifact
  as non-substantive. Anchor: `examples/build_product.dip` `CommitIfDirty` (~L806), `Setup`.
- **#406** — engine debugging (above). TDD against `pipeline/handlers/codergen_breach_test.go` and
  `pipeline/build_product_operator_decision_test.go`.
- **#407** — `EscalateMilestone` runs and **displays the live verify result** in the escalation
  prompt; when verify is green, `abandon` is **not** the default/first option and discarding a
  passing tree requires explicit confirmation; record verify-state + decision durably.

### WS-B — Implement/Verify rubric alignment & spec contracts  (#409, #408, #306)
All three edit the `Implement` ↔ `VerifyMilestone` ↔ spec triad. **Design together; edit the .dip
serially** (same nodes) to avoid self-conflict.

- **#409** — before `DONE`, `Implement` self-applies the same test-verifies-contract rubric
  `VerifyMilestone` will run (e.g. a "built-binary smoke" contract requires spawning the built
  binary, not an in-process call) and fixes shortfalls in-node. One shared rubric referenced by both.
- **#408** — `VerifyMilestone` gains a **severity tier**: a deviation from a spec-named identifier is
  downgraded `FAIL`→`WARN` **only when BOTH** an ADR under `.ai/decisions/` covers it **AND** the
  milestone's behavioral tests pass (cite both). Contract/behavioral violations stay blocking.
- **#306** — `ReadSpec` emits `.ai/decisions/spec-ambiguities.md` (one DEFINITE ruling per
  contradiction) and `.ai/decisions/behavioral-contracts.md` (each non-literal guarantee + its
  verification method); `ApprovePlan` surfaces both to the human; `Implement` cites rulings;
  `VerifyMilestone` + `FinalSpecCheck` disposition each in-scope contract with evidence.

### WS-C — Review fan-out cost  (#353)
Mostly `.dip` shape + applying the shipped #304 feature. Anchors: the three reviewer nodes
(`ReviewGemini`/`ReviewClaude`/`ReviewCodex`) and `FinalSpecCheck` in `examples/build_product.dip`.

---

## Methodology (non-negotiable)

### TDD per change type
- **Engine (Go — #406, any #353 engine bits):** write the failing test **first**, mirroring the
  existing patterns in `pipeline/handlers/codergen_breach_test.go`, `codergen_guards_test.go`,
  `pipeline/build_product_operator_decision_test.go`, `agent/session_guards_test.go`. Red → green →
  refactor. Run `go test ./... -short` and the race detector on touched packages.
- **`.dip` routing (#405, #407, #409, #353):** the test is `examples/build_product.test.json` —
  scenario fixtures with `scenario` overrides + `immediately_after` / path assertions. **Add a new
  failing scenario that encodes the desired routing first** (e.g. "FixMilestone turn-breach with
  green verify → CommitIfDirty/commit path, never abandon-to-Done"), then edit the `.dip` to make it
  pass. Validate every change with `dippin doctor examples/build_product.dip` (must stay **A**) and
  `dippin simulate -all-paths examples/build_product.dip`. If `dippin` isn't on `PATH`, **ask the
  user** — do NOT `go install` it (CLAUDE.md Critical Rule).
- **Prompt/spec behavioral changes (#408, #306, parts of #409):** not unit-testable. Verify by
  (a) scenario fixtures where routing is observable, (b) a focused re-read against each issue's
  Acceptance criteria, and (c) — strongest — a **dogfood re-run** of the code-goblin spec at the end
  to confirm the cascade no longer occurs (ask the user for the spec / run harness).

### Ralph loops (one per issue)
Drive each issue as a ralph loop: *investigate → make the smallest change → run its
tests/doctor/simulate → review against acceptance criteria → repeat until green*. Use the
`ralph-loop` skill with a `--completion-promise` tied to that issue's acceptance criteria
(e.g. `'#406 DONE: FixMilestone green-breach test passes and tree is committed to a durable ref'`).
Cap iterations; if a loop stalls, stop and escalate to the user rather than thrashing.

### Checkpointed subagent reviews
After **each workstream lands** (not at the very end), dispatch a fresh review subagent
(`fresh-eyes-review` skill or the `code-reviewer` agent) to check, with file:line evidence:
1. Every acceptance-criterion for the workstream's issues is met.
2. **No under-correction** — e.g. #408's WARN tier must NOT let a real contract/behavioral
   violation pass; #406's fix must not mark a non-green breach as green.
3. **No over-correction** — e.g. #408 must NOT become a blanket "write any ADR to dodge any check"
   loophole (require ADR **and** passing behavioral tests); #405 must NOT gitignore real source;
   #407 must NOT make it impossible to abandon a genuinely broken tree.
4. CLAUDE.md rules honored: **no unconditional fallback edge to a loop target** (#407 adds routing —
   prove no new infinite loop), strict failure edges intact, no silently-swallowed errors.
5. `dippin doctor` still **A** on all core pipelines; `go build ./...` + `go test ./... -short` pass.

Act on the review consensus before moving to the next workstream (CLAUDE.md / squad-decides).

### Pragmatism guardrails
- Smallest change that satisfies the acceptance criteria. No speculative abstractions, no
  "while I'm here" refactors of adjacent nodes/prompts. Every changed line traces to one issue.
- Match the existing `.dip` prose/markup idiom and the existing Go style.
- **NEVER `--no-verify`.** If pre-commit hooks fail, fix the root cause.
- Keep `examples/build_product.dip` at doctor grade A throughout — don't let it dip mid-change.

---

## Execution plan (phased)

**Phase 0 — Reproduce & write failing tests.** Pull the case-study checkpoint for run
`634a2527ff56` (ask the user for its location if not under `$XDG_STATE_HOME/tracker/runs/` or
`.tracker/runs/`). For each issue, write the failing test/scenario that encodes the bug. Nothing is
"understood" until it's reproduced red.

**Phase 1 — Engine, in parallel (#406).** Isolated Go files; safe to run while .dip work proceeds.
Debug why `BreachVerify` ≠ `Passed` on `FixMilestone`; fix the wiring; green the new test.

**Phase 2 — `.dip` edits, SERIALIZED.** WS-A (#405, #407) and WS-B (#409, #408, #306) and WS-C
(#353) all edit the single ~2,200-line `examples/build_product.dip`. **Do not parallelize edits to
this file** — run them through one editor, one issue per commit, re-running doctor/simulate between
each. Suggested order: #405 → #407 → (Phase-1 #406 merges) → #409 → #408 → #306 → #353. Checkpoint
review after WS-A, after WS-B, after WS-C.

**Phase 3 — Integration.** `dippin doctor` A across **all** example `.dip` files;
`dippin simulate -all-paths` on the three core pipelines; full `go test ./...` + race on touched
packages. If feasible, **dogfood re-run** the code-goblin spec and confirm: Verify no longer
false-FAILs (#408/#409), no binary committed (#405), a green breach commits and is not abandoned
(#406/#407), reviewer cost is bounded (#353).

**Phase 4 — Ship.** Branch `fix/build-product-dogfood-hardening`. One commit per issue (reviewable,
revertable). Update `CHANGELOG.md` under `[Unreleased]` (group Added/Changed/Fixed; cite each issue
#). Open one super-PR that `Closes #405, #406, #407, #408, #409` and (if included) `#306, #353`.
End commit messages with the project's `Co-Authored-By` trailer; end the PR body with the
`🤖 Generated with Claude Code` line.

---

## Scope decisions to confirm with the user up front

- **#306 and #353 are "maybe" in the original ask.** Default: include them, but if either balloons
  (#353's per-backend caching-awareness in particular can grow an engine tail), **split it into a
  follow-up PR** rather than blocking the green-tree fixes (#405–#409). Ask which they prefer if a
  split looks likely.
- **Super-PR vs. stacked PRs.** One super-PR with clean per-issue commits is the ask. Offer to split
  WS-C (#353) out if the diff gets large — the WS-A/WS-B fixes are the urgent dogfood blockers.
- **Spec-authoring counterpart (#408).** #408 notes a companion fix on the *example spec* side
  (mark prose identifiers normative vs. illustrative). Confirm whether that example-spec edit is in
  scope here or tracked separately.

## Definition of done
- Every issue's Acceptance criteria are demonstrably met (cite the test/scenario proving each).
- `go build ./...`, `go test ./... -short`, race on touched packages: all green.
- `dippin doctor` A on all examples; `simulate -all-paths` clean on the three core pipelines.
- Each workstream passed its checkpoint subagent review (no over/under-correction).
- CHANGELOG updated; super-PR open and linked to all closed issues.

---

## Appendix A — Phase-0 evidence pack (run artifacts live on a different machine)

The case-study run artifacts (`634a2527ff56`, and `b68b532619c3` for #353) are **not on this
machine**. They live on the box that executed the dogfood run. Rather than reproduce live, request a
**self-contained evidence pack** from an agent/LLM with filesystem access there, then work from that.

> **Proxy request to send to the LLM on the run-artifacts machine.** Paste this verbatim; it should
> return one markdown document. It is read-only — it runs `git`/`jq`/`grep`, never mutates the run.

```
You have filesystem access to a machine that ran tracker's `build_product` pipeline. Produce ONE
self-contained markdown "evidence pack" for these runs. Locate each run dir by trying, in order:
  $XDG_STATE_HOME/tracker/runs/<runID>/ , $HOME/.local/state/tracker/runs/<runID>/ ,
  ./.tracker/runs/<runID>/ , $TMPDIR/tracker-audit/<runID>/
Primary run: 634a2527ff56  (code-goblin, 2026-06-23). Secondary (only if present): b68b532619c3.
Also locate the code-goblin product repo/worktree this run built (look for SPEC.md + cmd/goblin).

Emit these sections, each with the literal evidence (paths, raw JSON/transcript excerpts, command
output). Keep excerpts tight but verbatim — do not paraphrase identifiers, numbers, or status values.

A. RUN OVERVIEW — for 634a2527ff56: final status.json / checkpoint (current_node, human_response,
   top-level outcome), the ordered list of nodes visited, and `git -C <product repo> log --oneline -8`
   plus `git -C <product repo> status --porcelain` (the end-state dirty tree).

B. #406 FixMilestone breach — the per-node record for FixMilestone: total turns, turn_limit_msg,
   turn_breach_class, and ANY of these fields if present: breach_verify / BreachVerify, the verify
   command run at breach and its exit code, turn_breach_policy (node attr AND graph default). Quote
   the last ~10 turns of its transcript (the turns where it reached green then ran git status). State
   plainly: was the tree green at breach, and was it committed to any ref? (`git log` proof.)

C. #407 EscalateMilestone — the exact prompt text shown to the operator (labels/options offered),
   whether the milestone verify result appears in that prompt, and human_response / preferred_label.

D. #405 artifact hygiene — CommitIfDirty.tool_stdout showing the `goblin` binary commit; the full
   contents of `.gitignore` as it existed during the run; and the VerifyMilestone finding that
   flagged the committed binary.

E. #408 prose-identifier FAIL — the VerifyMilestone finding about `signal.NotifyContext` (verbatim),
   and the signal-handling tests that PASSED (names + results: SIGINT→130, SIGTERM→143, ctx-propagation).
   Also: was there any `.ai/decisions/` ADR present at the time? List `.ai/decisions/` contents.

F. #409 test-vs-contract — the smoke test as Implement first shipped it (the function it called
   in-process, e.g. runWithSignal), the spec's smoke contract (SPEC.md "Required tests" / Milestone 11
   done-when, quote the lines), and the diff FixMilestone wrote to add the built-binary injection seam
   (`//go:build fakeinject`, --gh-fake/--model-fake).

G. #306 spec ambiguities — quote the SPEC.md passages showing the contradictions/guarantees named in
   the issue: the retry-count contradiction ("max 2 retries" vs "max 2 attempts") and the logger
   "rejects keys at startup, not runtime" guarantee, with line numbers.

H. #353 (only if b68b532619c3 exists) — from its activity.jsonl: every `cost_updated` provider_totals
   line (especially provider "unknown"), any `context_window_warning` event, and ReviewCodex's
   status.json (turns, wall time, input tokens, caching). If the run is absent, say so.

Append the full SPEC.md of the code-goblin product as an appendix (it grounds B/E/F/G). Output only
the markdown document.
```

When the pack comes back, drop it at `docs/plans/634a2527ff56-evidence.md` (gitignored or
uncommitted scratch) and treat it as the Phase-0 reproduction substrate: it pins #406's root cause
(section B), gives #408/#409/#306 their verbatim contract text, and lets you write the failing
`build_product.test.json` scenarios against real routing rather than guesses.
