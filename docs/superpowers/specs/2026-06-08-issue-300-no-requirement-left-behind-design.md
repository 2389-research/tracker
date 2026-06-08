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

### Edit 1 — `Decompose` (`:436`): coverage table + owner-or-fail gate

Add the attribute:

```
auto_status: true
```

Append to the Decompose prompt (after the existing decomposition rules and the `known_failures` block, before the node ends):

- **Derive the mandated-verification list from SPEC.md itself.** Read `SPEC.md` and enumerate every verification the spec *mandates*: named tests (e.g. "a test that …", explicitly named test functions), behavioral checks ("must reject …within 100ms"), and acceptance gates. #300 is **self-sufficient** — it derives this list from SPEC.md directly; it does NOT depend on #301's spec-coherence preflight (whose rule (f) will *later* feed this same table — soft coupling, not a hard dependency).
- **Coverage requirement.** Every mandated verification MUST appear in some milestone's "Done when" criteria.
- **Coverage table artifact.** Write `.ai/decisions/requirement-coverage.md` — a table mapping each spec-mandated verification → its owning milestone, citing the SPEC.md line/section each came from. Suggested columns: `Mandated verification | SPEC.md source (line/section) | Owning milestone`.
- **Gate.** If every mandated verification has an owning milestone → emit `STATUS:success` as the terminal line. If ANY mandated verification has no owner, that is a decomposition bug: describe the unowned verification(s) and emit `STATUS:fail` (routes to `EscalateReview` so a human re-plans, rather than the build silently proceeding with a dropped requirement).

The new prompt text contains no literal `escalate`/`tests-pass` tokens (defensive — Decompose's outcome flows through `auto_status`/STATUS, not `ctx.tool_stdout` substring-matching, but kept clean to avoid any cross-node stdout coincidence).

### Edit 2 — `VerifyMilestone` check 5 "SPEC GAPS BEYOND MILESTONE NOTES" (`:861`)

Append an owner-or-fail rule to check 5 (the node already has `auto_status: true`):

> A requirement may NOT be deferred to "future work" / "a later milestone" unless a **named later milestone in `.ai/decisions/milestones.md` actually owns it**. Before accepting any such deferral, `grep` `.ai/decisions/milestones.md` for the owning milestone and cite it (milestone number + the "Done when" line that covers it). If no named milestone owns the deferred requirement → `STATUS:fail`. (This is the exact `code-goblin` miss: the verifier saw the 429/cancel gap and waved it through as "future work" with no owning milestone.) Cross-check against `.ai/decisions/requirement-coverage.md` (written by Decompose) when present.

### Edit 3 — `FinalSpecCheck` (`:1378`)

Append the same owner-or-fail rule into FinalSpecCheck's SPEC.md compliance section (the node already has `auto_status: true` and uses the fail-closed first-line-`STATUS:fail` pattern):

> For EVERY requirement: if it is not implemented and the report defers it to "future work," that deferral is only acceptable when a **named later milestone in `.ai/decisions/milestones.md` owns it** — grep for the owner and cite it. A future-work deferral with no named owner keeps the early `STATUS:fail` in place (do NOT emit the terminal `STATUS:success`).

Because FinalSpecCheck is fail-closed, the rule is phrased as "an unowned deferral blocks the terminal `STATUS:success`" rather than "emit STATUS:fail" — consistent with its existing contract.

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

- `Decompose` node has `Attrs["auto_status"] == "true"`.
- `Decompose` prompt contains `requirement-coverage.md`.
- `Decompose` prompt contains the coverage-mapping language (e.g. substrings proving "every … mandated …" + "owning milestone" + the `.ai/decisions/requirement-coverage.md` path).
- `Decompose` prompt instructs reading `SPEC.md` to derive mandated verifications.
- `VerifyMilestone` prompt contains the owner-or-fail "future work" rule (substrings: `future work` + `milestones.md` + named-owner language).
- `FinalSpecCheck` prompt contains the owner-or-fail "future work" rule.

### Regression-pin assertions (GREEN before and after; guard against removal/regression)

- The `Decompose -> EscalateReview when ctx.outcome = fail` edge still exists (assert via `g.Edges`).
- The `Decompose -> ApprovePlan when ctx.outcome = success` edge still exists.
- `VerifyMilestone` and `FinalSpecCheck` still carry `auto_status: true`.
- **No unconditional `Decompose -> <X>` edge** was added beyond the two conditional ones (assert Decompose's out-edges are exactly the success→ApprovePlan and fail→EscalateReview pair — no new fallback, no loop-target fallback).

### Test-hygiene notes

- Coverage/complexity hooks exclude `_test.go`; the new file is additive string-asserts on embedded `.dip` data — no new production Go, no coverage regression risk.
- Assertions use stable substrings, not full-paragraph matches, so benign prompt rewording does not flake them (matching the #299 test's `strings.Contains` discipline).

## Verification gates (all before the PR)

- `dippin validate examples/build_product.dip` — passes.
- `dippin doctor examples/build_product.dip examples/ask_and_execute.dip examples/build_product_with_superspec.dip` — **A** grade across the board (build_product baseline A 90/100 — must not regress).
- `dippin simulate -all-paths examples/build_product.dip` — 100% terminate, no new cycle / dead-stop. (Confirms the now-live `Decompose -> EscalateReview` fail path is reachable and terminates.)
- `go build ./... && go test ./... -short` — green; each new assertion proven RED before the `.dip` edit.
- `gofmt -l pipeline/build_product_requirement_coverage_test.go` — prints nothing.

## Out of scope (deliberate)

- **#301 (spec-coherence preflight)** — the sibling Phase-2 item. It adds a new `SpecLint` subgraph whose rule (f) enumerates mandated tests to *feed* this issue's coverage table. #300 stays self-sufficient: Decompose derives the mandated-verification list from SPEC.md itself. Do NOT build the preflight here. The coupling is soft and one-directional (future #301 → this table).
- **#305 / #320 / #304 / #306 / #307** — untouched.
- The fail-closed-vs-simple STATUS choice for Decompose (settled: simple). No engine change to `parseAutoStatus` or the default-success behavior.

## Soft coupling with #301 (documented, not built)

#301's preflight rule (f) ("Mandated tests enumerated and assignable → emit the list for Decompose to own") will, once built, produce the mandated-verification list that Decompose currently derives itself. When #301 lands, Decompose can consume that list instead of (or in addition to) re-deriving it — a refinement, not a dependency. This issue ships the consumer (the coverage gate) independently.

## Commit / PR

- Branch: `fix/300-no-requirement-left-behind` (off `main`).
- Commit messages end with `Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`. NEVER `--no-verify`.
- PR title: `feat(build_product): no-requirement-left-behind — Decompose coverage + owner-or-fail Verify (#300)`. Body closes #300, refs epic #308 Phase 2 (second item), notes the soft coupling with #301, ends with the Claude Code generation line.
