# Issue #300 — no-requirement-left-behind (Decompose coverage + owner-or-fail Verify)

**Epic:** #308 Phase 2 ("Enforce quality regardless of spec/language"), **second** item (after #299, merged in PR #321).
**Scope:** `.dip`-only change to `examples/build_product.dip` + one new test file + CHANGELOG. No engine Go changes.
**Status:** design (pre squad review, 2026-06-08).

## Problem

Two failure modes let spec-mandated requirements vanish from a `build_product` run:

1. **Decompose drops a requirement.** `Decompose` (`:436`) can produce a set of milestones whose collective "Done when" criteria omit a verification the SPEC explicitly mandates — the test is never assigned to anyone. Its rules cover ordering, size, anti-scope-creep (`DO NOT implement`), and `known_failures`, but **nothing requires every spec-mandated verification to map to a milestone**.

2. **Verify/FinalSpecCheck wave a gap through as "future work."** `VerifyMilestone` check 5 ("SPEC GAPS BEYOND MILESTONE NOTES", `:861`) cross-checks SPEC bullets but has **no rule against deferring a requirement to unowned future work**. `FinalSpecCheck` (`:1378`) asks "is it implemented?" per requirement but does not police future-work deferrals.

On the audited `code-goblin` run, two spec-mandated tests — a synthetic-429 retry test and a cancel-within-100ms test (SPEC lines 290, 323) — were owned by no milestone. The milestone verifier **saw** the gap and explicitly waved it through as "future work"; because the run halted before `FinalSpecCheck` (the silent-halt bug, Phase 0), even the final gate never re-examined it.

## Critical pre-design finding — Decompose's fail-edge is currently dead

`Decompose` has **no `auto_status: true`** attribute (unlike `VerifyMilestone:748` and `FinalSpecCheck:1385`). In `pipeline/handlers/codergen.go:627-637` (`resolveTerminalStatus`), an agent node's terminal status **defaults to `OutcomeSuccess`**, and `parseAutoStatus(responseText)` is only consulted when `node.Attrs["auto_status"] == "true"`. Therefore a normal `Decompose` completion **always yields `outcome=success`**, and the existing edge

```
Decompose -> EscalateReview  when ctx.outcome = fail        (:1587)
```

is **dead** — it can never fire from a normal Decompose run today.

**Consequence for #300:** the issue's acceptance criterion — "a mandated test with no owner makes `Decompose` FAIL (routes to EscalateReview)" — cannot be met by prompt prose alone. We must add `auto_status: true` to the `Decompose` node so its STATUS line becomes authoritative, and have the prompt emit `STATUS:success`/`STATUS:fail`. This remains **`.dip`-only** (a node attribute, no engine Go change) but is more than a prose edit, and is the load-bearing change of this issue.

### STATUS pattern decision (settled)

Decompose uses the **simple, VerifyMilestone-style pattern** (`:938-940`), not the fail-closed FinalSpecCheck pattern:

- Every mandated verification has an owner → terminal `STATUS:success`.
- Any mandated verification is unowned → `STATUS:fail` (routes to `EscalateReview`).

Rationale: the simple pattern keeps Decompose's happy-path contract intact (a successful decomposition that forgets the STATUS line still defaults to success — fail-open on the happy path), and the only *new* way to fail is the explicit unowned-verification case this issue introduces. The fail-closed pattern (STATUS:fail first line / STATUS:success last) would change Decompose's happy-path contract more invasively for marginal benefit on a planning node that has no truncation-sensitive enumeration. Confirmed with the issue author.

## The change

Three substantive edits to `examples/build_product.dip`, plus prose-sync, all `.dip`-only.

> **Squad-review note (2026-06-08, five-expert panel).** The architecture below was validated — the dead-edge finding is correct (one reviewer empirically confirmed `auto_status: true` preserves the `dippin doctor` A 90/100 and all 100 `simulate` paths terminate), and the feared `FinalSpecCheck` allowlist collision was **refuted** (its allowlist is `.ai/build/`-scoped; the new artifact is in `.ai/decisions/`). The panel surfaced two criticals and a cluster of prompt-reliability fixes, all folded into the edits below: the **DO-NOT-implement deferral carve-out** (else the gate false-fails correct decompositions), the **read-back gate** (vs. fail-open tail-token judgment), a **closed inclusion test** for "mandated verification", and **STATUS hygiene**. See "Squad-review consensus folded in" at the end for the full list with sources.

### Edit 1 — `Decompose` (`:436`): coverage table + owner-or-fail gate

Add the attribute `auto_status: true` to the node. Append to the Decompose prompt (after the existing decomposition rules and the `known_failures` block, before the node ends) a block enforcing this **ordered** procedure — the ordering is load-bearing (it breaks the self-grading loop where an agent back-fills the table from milestones it already wrote, making the gate decorative):

1. **FIRST — enumerate, before writing any milestone.** Read `SPEC.md` and write the LEFT columns of `.ai/decisions/requirement-coverage.md` — every *spec-mandated verification* and its SPEC.md source line/section. Do not consult or write milestones during this step. #300 is **self-sufficient**: it derives this list from SPEC.md directly and does NOT depend on #301's preflight (whose rule (f) will *later* feed this same table — soft coupling, not a hard dependency).
   - **Closed inclusion test for "spec-mandated verification"** (prevents both false-fails and misses): a line qualifies ONLY if the SPEC.md text uses an obligation word (MUST / SHALL / "is required to" / "the test must" / an explicitly named test function or acceptance criterion) AND names a concrete, checkable behavior (a specific input→output, a numeric bound, a named error path). Aspirational adjectives ("fast", "robust", "clean") with no checkable threshold are NOT mandated verifications — do not list them. When unsure whether a line qualifies, list it but mark its owner `N/A — aspirational, no checkable threshold` rather than failing the build.
   - **Affirmative extraction.** State explicitly how many mandated verifications were found. If zero, write an affirmative `SPEC mandates no named verifications` line — never a silent empty table (a silent-empty table is the under-extraction failure mode, the same bug class relocated).
2. **THEN decompose** into milestones per the rules above.
3. **FINALLY — assign owners.** Fill the "Owning milestone" column by matching each pre-listed verification to a milestone. You may NOT add rows in this step, and you may NOT invent a milestone to cover a verification. Each mandated verification resolves to exactly one of three states:
   - **owned** — a milestone's "Done when" line covers it. "Owns it" means: `grep -nE '^#+ *[Mm]ilestone' .ai/decisions/milestones.md` returns a milestone whose "Done when" block contains a line covering this exact requirement; cite it as `Milestone <N>: <the Done-when line, verbatim>`. A milestone that merely *mentions* the topic in prose without a covering "Done when" line does NOT own it.
   - **deferred** — SPEC.md itself defers the behavior to a named later phase AND a milestone's `DO NOT implement` block records that deferral citing the spec section. Cite both. (This carve-out is mandatory: without it the gate contradicts the existing anti-scope-creep machinery — Decompose `:458-471`, VerifyMilestone check 4 `:858-859` — and would `STATUS:fail` a *correct* decomposition that properly defers Phase-2+ work, e.g. the spec's own Phase-1 `Fingerprint` whose consumer is deferred to Phase 6.)
   - **UNOWNED** — neither owned nor deferred. This is the decomposition bug the issue targets.

**Gate (read-back, not recall).** After filling the table, re-read `.ai/decisions/requirement-coverage.md` and count the UNOWNED rows. Emit `COVERAGE_GAPS: <N>` (the count) on its own line, then the terminal STATUS line:
- every row is `owned` or `deferred` (N = 0) → emit `STATUS:success`.
- any row is `UNOWNED` (N > 0) → list the unowned verification(s) ABOVE the STATUS line, then emit `STATUS:fail` (routes to `EscalateReview` via `:1587` so a human re-plans).

**STATUS hygiene (copied from FinalSpecCheck's existing warnings):** the final line must be EXACTLY `STATUS:success` or `STATUS:fail` — no parentheses, no counts, no trailing words (`STATUS:fail (2 unowned)` is dropped by the parser → silent success on a detected gap). Emit it OUTSIDE any code fence, alone on its own line. Emit ONLY `success` or `fail` — never `STATUS:retry` (Decompose has no retry edge; a stray retry re-runs the node under the global restart budget). The new prompt text contains no literal `escalate`/`tests-pass` tokens (defensive — Decompose routes via `auto_status`/STATUS, not `ctx.tool_stdout` substring-match).

**Keep the coverage table out of `milestones.md`.** It is its own file (`requirement-coverage.md`); writing milestone-coverage content into `milestones.md` would inflate `PickNextMilestone`'s `grep -ciE '^#{1,3}\s*milestone\s'` count (`:511`).

### Edit 2 — `VerifyMilestone` check 5 "SPEC GAPS BEYOND MILESTONE NOTES" (`:861`)

Append an owner-or-fail rule to check 5 (the node already has `auto_status: true`):

> A requirement may NOT be waved through as "future work" / "a later milestone" unless it is EITHER (a) **owned** by a named later milestone — `grep -nE '^#+ *[Mm]ilestone' .ai/decisions/milestones.md` and cite the milestone number + the "Done when" line, verbatim, that covers it — OR (b) **deferred**: SPEC.md defers it to a named later phase AND a milestone's `DO NOT implement` block records that deferral citing the spec section (cite both — consistent with check 4's existing deferred-field semantics). A deferral that is neither owned nor documented-deferred → `STATUS:fail`. (This is the exact `code-goblin` miss: the verifier saw the 429/cancel gap and waved it through as "future work" with no owning milestone.) Cross-check against `.ai/decisions/requirement-coverage.md` (written by Decompose) when present; on any disagreement, SPEC.md re-read live is authoritative over the table (consistent with the existing `:754-756` "SPEC.md and source are authoritative" framing).

### Edit 3 — `FinalSpecCheck` (`:1378`)

Append the same owner-or-deferred-or-fail rule into FinalSpecCheck's SPEC.md compliance section (the node already has `auto_status: true` and uses the fail-closed first-line-`STATUS:fail` pattern):

> For EVERY requirement: if it is not implemented and the report defers it to "future work," that deferral is acceptable ONLY when it is EITHER owned by a named later milestone in `.ai/decisions/milestones.md` (grep + cite the milestone and its "Done when" line) OR documented-deferred (SPEC.md defers to a named phase AND a `DO NOT implement` block records it, citing the spec section). A future-work deferral that is neither keeps the early `STATUS:fail` in place (do NOT emit the terminal `STATUS:success`).

Because FinalSpecCheck is fail-closed, the rule is phrased as "an unowned/undocumented deferral **blocks** the terminal `STATUS:success`" — NOT "emit STATUS:fail" (which mid-prose would just be one more fail line the final success overrides). Keep this phrasing; do not let an implementer normalize it to Edit 2's "emit STATUS:fail."

### Documenting the new `.ai/decisions/` artifact (hygiene — refuted-collision follow-through)

The feared FinalSpecCheck allowlist collision was refuted (its "no UNEXPECTED leftover files" rule is `.ai/build/`-scoped, `:1491-1498`/`:1509-1510`; `requirement-coverage.md` is in `.ai/decisions/`). But to prevent doc-drift, add `requirement-coverage.md` to the documented set of expected `.ai/decisions/` files (the `Cleanup` comment near `:1564` preserves `.ai/decisions/`), and note in FinalSpecCheck's "nothing extra" framing that `.ai/decisions/*.md` workflow artifacts are not "extras."

### Prose-sync (the #299 doc-in-prompt-truthfulness lesson)

After Edit 1, several existing prose sites that describe Decompose behavior must stay consistent:

- `VerifyMilestone:759-763` ("if Decompose dropped a requirement during milestone planning … the verifier had no path to discover the gap") and `:865` ("a SPEC.md bullet that Decompose dropped silently passed through") — still true, but now Decompose *also* owns a coverage gate. Add a brief note that Decompose writes `requirement-coverage.md` and fails on an unowned mandated verification, so the two layers are described consistently (defense in depth, not contradiction). This note is also the positive prose-sync pin the test plan asserts.
- `EscalateReview` retry copy (`:1538`, "Go back to Decompose and re-plan the build from scratch") — remains truthful; Decompose can now route here directly on a coverage failure.

No prose may state or imply that Decompose "always succeeds" or "cannot fail." (None currently does; this is a guard against introducing such a claim.)

### Prose-sync (the #299 doc-in-prompt-truthfulness lesson)

After Edit 1, several existing prose sites that describe Decompose behavior must stay consistent:

- `VerifyMilestone:759-763` ("if Decompose dropped a requirement during milestone planning … the verifier had no path to discover the gap") and `:865` ("a SPEC.md bullet that Decompose dropped silently passed through") — still true, but now Decompose *also* owns a coverage gate. Add a brief note that Decompose writes `requirement-coverage.md` and fails on an unowned mandated verification, so the two layers are described consistently (defense in depth, not contradiction).
- `EscalateReview` retry copy (`:1538`, "Go back to Decompose and re-plan the build from scratch") — remains truthful; Decompose can now route here directly on a coverage failure.

No prose may state or imply that Decompose "always succeeds" or "cannot fail." (None currently does; this is a guard against introducing such a claim.)

## Edges — no new edges, no unconditional fallbacks to loop targets

This change adds **zero** graph edges. It relies entirely on edges that already exist:

```
Decompose      -> EscalateReview  when ctx.outcome = fail      (:1587, now LIVE via auto_status)
Decompose      -> ApprovePlan     when ctx.outcome = success   (:1586)
VerifyMilestone -> FixMilestone   when ctx.outcome = fail      (:1604)
VerifyMilestone -> EscalateMilestone                            (:1605, existing unconditional fallback — NOT a loop target)
FinalSpecCheck -> EscalateReview  when ctx.outcome = fail      (:1652)
```

Adding `auto_status: true` to Decompose changes only the *node's ability to emit `outcome=fail`* — it does not change routing. The `Decompose -> ApprovePlan when success` / `Decompose -> EscalateReview when fail` pair is exhaustive for Decompose's two STATUS outcomes; no fallback edge is added (and none to a loop target). This satisfies the CLAUDE.md edge-routing rule.

## Testing (RED-first, mirroring `build_product_quality_gate_test.go`)

New file `pipeline/build_product_requirement_coverage_test.go`, package `pipeline`, using `loadBuildProduct(t)` and `g.Nodes[id].Attrs[...]`. This issue is **agent-prompt rules with no executable shell to drive**, so the tests are **graph string-asserts only** — there is no Layer-B runtime shell test (unlike #299, whose `ci-probe.sh` heredoc was executable). A `dippin simulate -all-paths` run (a verification gate, not a Go test) covers reachability/termination.

### Negative-control assertions (RED before the `.dip` edit; verified absent pre-change)

- `Decompose` prompt contains `requirement-coverage.md`.
- `Decompose` prompt contains the coverage-mapping language (substrings: `Owning milestone` + `mandated`). **Do NOT assert the bare token `SPEC.md`** on the Decompose prompt — it is already GREEN at `:467` ("SPEC.md may define a `Fingerprint`…") and would prove nothing (test-reviewer I1). Assert a distinctive new phrase instead (e.g. `Owning milestone`, and `COVERAGE_GAPS`).
- `Decompose` prompt contains the `COVERAGE_GAPS` sentinel and the DO-NOT-implement deferral carve-out language (substring `DO NOT implement` within the new gate block, plus `deferred`).
- `VerifyMilestone` prompt contains the owner-or-fail "future work" rule (anchor tokens matched separately: `future work` + `milestones.md`, plus `DO NOT implement` for the carve-out — not a full-sentence match, so re-wording doesn't flake).
- `FinalSpecCheck` prompt contains the owner-or-fail "future work" rule (same anchor-token discipline).

### Regression-pin assertions (GREEN before and after; guard against removal/regression)

- **`TestDecomposeFailEdgeIsLive` (the load-bearing pin).** Assert `g.Nodes["Decompose"].Attrs["auto_status"] == "true"` AND the `Decompose -> EscalateReview when ctx.outcome = fail` edge exists, in ONE test, with a comment tying them together: *the fail edge is dead without `auto_status` — `codergen.go:635`*. This is the only guard against silently re-deadening the edge; the existing `routesFailure` helper (`build_product_failure_routing_test.go:52`) passes regardless of `auto_status` (it only checks for conditional edges), so without this dedicated pin a future `auto_status` removal goes undetected (test-reviewer C1).
- The `Decompose -> ApprovePlan when ctx.outcome = success` edge still exists.
- `VerifyMilestone` and `FinalSpecCheck` still carry `auto_status: true`.
- **No new/unconditional `Decompose` out-edge.** Use `g.OutgoingEdges("Decompose")` (filters on `e.From`), assert `len == 2`, assert each via `hasEdgeWithCondition(g, "Decompose", "ApprovePlan", "ctx.outcome = success")` / `(... "EscalateReview", "ctx.outcome = fail")`, and loop asserting every out-edge has `Condition != ""`. Do **not** scan `g.Edges` loosely — the incoming restart edges `ApprovePlan -> Decompose` (`:1589`) and `ResetReviewBudget -> Decompose` (`:1658`) would pollute a substring match (test-reviewer I2). Condition strings retain the `ctx.` prefix in `Edge.Condition` (verified), so the precedent helpers work verbatim.

### Test-hygiene notes

- Reuse the existing helpers in `build_product_failure_routing_test.go` (same package `pipeline`): `loadBuildProduct`, `hasEdgeWithCondition`, `hasUnconditionalEdgeTo`. Do NOT redefine them.
- Add a prose-sync truthfulness pin (mirroring #299's `TestQualityGateVerifyPromptTruthful`): assert the new Decompose-coverage note appears in `VerifyMilestone`'s prompt, so a future edit can't silently drop the defense-in-depth cross-reference (test-reviewer M2).
- Coverage/complexity hooks exclude `_test.go` and run at CI (`make ci`), not pre-commit; the new file is additive string/edge-asserts on embedded `.dip` data — no new production Go, no coverage regression risk (test-reviewer M1).
- Assertions use stable substrings, not full-paragraph matches, so benign prompt rewording does not flake them (matching the #299 test's `strings.Contains` discipline). **Re-confirm count-0 of each chosen negative-control substring against the current `.dip` before writing the assertion.**

**Why no Layer-B runtime test (vs. #299).** This issue is agent-prompt rules with no executable shell to drive — string-asserts prove the prompt *text* exists; the LLM's runtime behavior (does it actually fail on an unowned test?) cannot be unit-tested. The durable reachability guard for the now-live fail path is the **graph edge-assert pair above** (edge exists + node can emit fail via `auto_status`), NOT `dippin simulate` — `simulate -all-paths` is a one-time pre-PR CLI confirmation (no in-repo precedent shells out to it from a Go test), not an ongoing gate (test-reviewer I3).

## Verification gates (all before the PR)

- `dippin validate examples/build_product.dip` — passes.
- `dippin doctor examples/build_product.dip examples/ask_and_execute.dip examples/build_product_with_superspec.dip` — **A** grade across the board (build_product baseline A 90/100 — must not regress).
- `dippin simulate -all-paths examples/build_product.dip` — 100% terminate, no new cycle / dead-stop. (Confirms the now-live `Decompose -> EscalateReview` fail path is reachable and terminates.)
- `go build ./... && go test ./... -short` — green; each new assertion proven RED before the `.dip` edit.
- `gofmt -l pipeline/build_product_requirement_coverage_test.go` — prints nothing.

## Residual risk (documented, not fixed here)

**Decompose-retry budget drain under autopilot (adversarial C2).** Making Decompose's fail edge live adds a new entry into the *existing* `EscalateReview -> retry -> ResetReviewBudget -> Decompose` (`:1657-1658`) cycle, which loops back to an already-completed node and consumes the **global** `RestartCount` (`engine_run.go`, capped at `max_restarts: 50`, `:9`). The cycle is gated by a human/autopilot decision at the `EscalateReview` gate (default label `accept` → Cleanup, which terminates), so it does not spin autonomously; `dippin simulate -all-paths` confirms termination. The realistic exposure is a `--autopilot lax`/`mid` driver repeatedly choosing "retry" on a genuinely unsatisfiable spec, draining the restart budget a later milestone fix loop would need. **The DO-NOT-implement carve-out (Edit 1, critical #1) removes the main *deterministic* re-fail trigger** — after it, a Decompose `STATUS:fail` signals a genuine, human-fixable spec gap (the intended semantic) rather than a correct-but-rejected plan. A dedicated `decompose_attempts` circuit breaker (analogous to the `review_fix_attempts` counter) would close the residual autopilot case but requires new tool nodes + edges — a structural change beyond this prompt-rule issue. **Filed as a follow-up rather than built here** (note it in the PR body). This is not a regression: the same retry-loop budget property already exists for `ReadSpec`/`FinalSpecCheck -> EscalateReview`.

## Out of scope (deliberate)

- **#301 (spec-coherence preflight)** — the sibling Phase-2 item. It adds a new `SpecLint` subgraph whose rule (f) enumerates mandated tests to *feed* this issue's coverage table. #300 stays self-sufficient: Decompose derives the mandated-verification list from SPEC.md itself. Do NOT build the preflight here. The coupling is soft and one-directional (future #301 → this table).
- **A `decompose_attempts` circuit breaker** for the retry-budget residual above — follow-up, not this issue.
- **#305 / #320 / #304 / #306 / #307** — untouched.
- The fail-closed-vs-simple STATUS choice for Decompose (settled: simple, hardened with the read-back gate). No engine change to `parseAutoStatus` or the default-success behavior.

## Squad-review consensus folded in (five-expert panel, 2026-06-08)

| # | Finding | Severity | Source(s) | Resolution |
|---|---------|----------|-----------|------------|
| 1 | Owner-or-fail collides with the DO-NOT-implement deferral gate → false-fails correct decompositions | CRITICAL | Adversarial C1 + Prompt I3b | 3-way classify `owned\|deferred\|UNOWNED`; fail only UNOWNED; mirrored in all three nodes (Edits 1/2/3) |
| 2 | Fail-open tail-token judgment is unreliable for the very runs that drop requirements | CRITICAL | Prompt C1/I2 | Read-back gate over `requirement-coverage.md` + `COVERAGE_GAPS: N` sentinel + enforced enumerate→decompose→assign ordering |
| 3 | "Spec-mandated verification" too fuzzy | IMPORTANT | Prompt I1 | Closed inclusion test (obligation word + checkable behavior; aspirational → `N/A` owner, not fail) |
| 4 | Empty/pathological spec → vacuous pass (under-extraction) | IMPORTANT | Adversarial I4 | Affirmative extraction count + per-row SPEC.md cite; explicit "mandates none" line |
| 5 | Restart-budget drain on the now-live retry loop | IMPORTANT | Adversarial C2 | Documented as residual; carve-out removes the deterministic trigger; breaker is a follow-up |
| 6 | Test-plan gaps | IMPORTANT | Test C1/I1/I2/M2 | `TestDecomposeFailEdgeIsLive` pin; drop bare `SPEC.md` assert; `OutgoingEdges` len==2 + `Condition!=""`; prose-sync pin |
| 7 | "Named owner" under-specified | MINOR | Prompt I3a | Pinned to `## Milestone N` header + verbatim "Done when" line |
| 8 | STATUS hygiene (trailing count dropped; fence; retry third outcome) | MINOR | Prompt M1/M2 + Engine-sem 1 | Exact-token + outside-fence + only success/fail warnings copied into Decompose |
| 9 | Undocumented permanent `.ai/decisions/` artifact | MINOR | Adversarial I3 + Workflow-safety 5a | Document `requirement-coverage.md` in Cleanup + FinalSpecCheck "not extras"; keep out of `milestones.md` |
| 10 | FinalSpecCheck Edit 3 must keep "blocks STATUS:success" phrasing | MINOR | Prompt M4 | Phrasing pinned in Edit 3 |

**Validated by the panel (no change required):** the dead-edge `auto_status` finding (engine-semantics reviewer empirically confirmed A 90/100 doctor + 100/100 simulate termination); zero CLAUDE.md invariant violations (workflow-safety reviewer); the FinalSpecCheck allowlist collision was **refuted** (`.ai/build/`-scoped, artifact is in `.ai/decisions/`); declared-writes demotion is inert (no `writes:` attr on Decompose); restart re-entry is `auto_status`-safe.

## Soft coupling with #301 (documented, not built)

#301's preflight rule (f) ("Mandated tests enumerated and assignable → emit the list for Decompose to own") will, once built, produce the mandated-verification list that Decompose currently derives itself. When #301 lands, Decompose can consume that list instead of (or in addition to) re-deriving it — a refinement, not a dependency. This issue ships the consumer (the coverage gate) independently.

## Commit / PR

- Branch: `fix/300-no-requirement-left-behind` (off `main`).
- Commit messages end with `Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`. NEVER `--no-verify`.
- PR title: `feat(build_product): no-requirement-left-behind — Decompose coverage + owner-or-fail Verify (#300)`. Body closes #300, refs epic #308 Phase 2 (second item), notes the soft coupling with #301, ends with the Claude Code generation line.
