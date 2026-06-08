# No-requirement-left-behind (#300) — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `build_product.dip` refuse to drop a spec-mandated verification — `Decompose` must assign every mandated test/check to a milestone (writing a coverage table and FAILING when one is unowned), and `VerifyMilestone`/`FinalSpecCheck` must reject a "future work" deferral that no named milestone owns.

**Architecture:** Pure `.dip` change to `examples/build_product.dip` (no engine Go). The load-bearing change is adding `auto_status: true` to the `Decompose` agent node so its existing-but-dead `Decompose -> EscalateReview when ctx.outcome = fail` edge becomes live (an agent node defaults to `OutcomeSuccess` unless `auto_status` consults its `STATUS:` line — `pipeline/handlers/codergen.go:627-637`). Decompose then enumerates mandated verifications from `SPEC.md` FIRST, decomposes, assigns owners LAST, writes `.ai/decisions/requirement-coverage.md`, and emits `STATUS:fail` only when a verification is neither owned by a milestone nor a documented-deferred (`DO NOT implement`) item. `VerifyMilestone` check 5 and `FinalSpecCheck` get the same owner-or-deferred-or-fail rule. Tests are graph string/edge asserts (no runtime test — this is agent-prompt rules with no executable shell).

**Tech Stack:** dippin `.dip` workflow DSL, Go `testing` (package `pipeline`), `dippin` CLI for validate/doctor/simulate.

**Branch:** `fix/300-no-requirement-left-behind` (already created off `main`). Design spec: `docs/superpowers/specs/2026-06-08-issue-300-no-requirement-left-behind-design.md` — read it first; it carries the five-expert squad consensus this plan implements.

---

## Context the executor needs

**The file under change:** `examples/build_product.dip`. All line numbers below are verified against the branch as of 2026-06-08 but **RE-GREP before every edit** (they drift — #299 already shifted everything once). Anchors:
- `grep -n 'agent Decompose\|agent VerifyMilestone\|agent FinalSpecCheck\|known_failures\|SPEC GAPS BEYOND\|For EVERY requirement\|tool Cleanup' examples/build_product.dip`
- `Decompose` node: header at `:436`, attrs `model/provider/reasoning_effort` at `:438-440`, `prompt:` at `:441`, the `known_failures` block ends at `:481`, blank line `:482`, then `human ApprovePlan` at `:483`.
- `VerifyMilestone` node: header `:743`, `auto_status: true` already at `:748`, check 5 "SPEC GAPS BEYOND MILESTONE NOTES" at `:861-866`, the Decompose-dropped prose at `:759-763` and `:865`.
- `FinalSpecCheck` node: header `:1378`, `auto_status: true` already at `:1385`, "For EVERY requirement…" block at `:1485-1488`, "Also verify / No UNEXPECTED leftover files in .ai/build/" at `:1490-1498`, terminal STATUS rules at `:1505-1513`.
- `Cleanup` tool: `:1557`, the "Keep: .ai/decisions/…" comment at `:1564`.
- Edges block `:1584-1658`. Relevant: `Decompose -> ApprovePlan when ctx.outcome = success` (`:1586`), `Decompose -> EscalateReview when ctx.outcome = fail` (`:1587`), `VerifyMilestone -> FixMilestone when ctx.outcome = fail` (`:1604`), `FinalSpecCheck -> EscalateReview when ctx.outcome = fail` (`:1652`).

**Indentation:** Decompose/VerifyMilestone/FinalSpecCheck prompts are indented 6 spaces under `prompt:`. Node attrs (`model:`, `auto_status:`) are indented 4 spaces. Match exactly or dippin's dedent changes the emitted text.

**STATUS / outcome mechanics (load-bearing — verified in `pipeline/handlers/codergen.go`):**
- `resolveTerminalStatus` (`:627-639`) defaults `status = OutcomeSuccess` and only calls `parseAutoStatus(responseText)` when `node.Attrs["auto_status"] == "true"`. So **Decompose without `auto_status` always yields success** — its fail edge is dead until we add the attr.
- `parseAutoStatus` (`:795-812`) is **last-STATUS-line-wins**, skips lines inside ``` fences, defaults to success when no STATUS line. `parseStatusLine` (`:824-840`) requires the value to be EXACTLY `success`/`fail`/`retry` — `STATUS:fail (2 unowned)` returns "" and is ignored (→ silent success on Decompose's fail-open default). It also recognizes `retry` — Decompose has no retry edge, so the prompt must forbid `STATUS:retry`.

**No active pre-commit hook** in this clone (`.git/hooks/` has only `*.sample`), so the RED test commit in Task 1 is safe. **NEVER use `--no-verify`.** If you find hooks ARE active and they block the RED commit, keep the RED proof in-session (run + observe failure) and commit test+impl together — never bypass.

**Marker/token constraint:** no new prompt text may contain the bare literal tokens `escalate` or `tests-pass` (engine edges substring-match `ctx.tool_stdout`; these agent prompts route via `auto_status`, so it's defensive — but stay clean). The node name `EscalateReview` is fine (it's `Escalate`, not bare `escalate`); avoid a standalone word `escalate`.

---

## File structure

- **Modify:** `examples/build_product.dip`
  - `Decompose` (`:436`): add `auto_status: true` attr; append the coverage-gate block to the prompt.
  - `VerifyMilestone` (`:861` check 5; `:759-763` prose-sync): append owner-or-deferred rule; add the Decompose-coverage cross-reference note.
  - `FinalSpecCheck` (`:1488`): insert owner-or-deferred rule; note `.ai/decisions/*.md` artifacts aren't "extras".
  - `Cleanup` (`:1564`): add `requirement-coverage` to the preserved-decisions comment.
- **Create:** `pipeline/build_product_requirement_coverage_test.go` (package `pipeline`) — string + edge asserts, reusing helpers from `build_product_failure_routing_test.go`.
- **Modify:** `CHANGELOG.md` — `[Unreleased]/Added` entry.

---

## Task 1: Negative-control + regression tests, proven RED

**Files:**
- Create: `pipeline/build_product_requirement_coverage_test.go`
- Reuse (do NOT redefine): `loadBuildProduct`, `hasEdgeWithCondition`, `hasUnconditionalEdgeTo` from `pipeline/build_product_failure_routing_test.go` (same package).

- [ ] **Step 1: Write the test file.** Each assertion is commented `negative-control` (RED pre-#300) or `regression-pin` (GREEN now, guards removal).

```go
// ABOUTME: Negative-control + regression guard for issue #300 — no-requirement-left-behind:
// ABOUTME: Decompose coverage-table gate + owner-or-deferred "future work" rule in Verify/FinalSpecCheck.
package pipeline

import (
	"strings"
	"testing"
)

// promptOf returns the named node's prompt attr (where the agent-rule text lives).
func promptOf(t *testing.T, g *Graph, id string) string {
	t.Helper()
	n, ok := g.Nodes[id]
	if !ok {
		t.Fatalf("%s node missing from build_product graph (#300)", id)
	}
	return n.Attrs["prompt"]
}

// Test 1 — regression-pin AND the load-bearing #300 pin: Decompose must carry
// auto_status:true. Without it, resolveTerminalStatus (codergen.go:635) defaults
// the node to OutcomeSuccess and the `Decompose -> EscalateReview when fail` edge
// is DEAD — a dropped requirement can never fail the node. This single test ties
// the attr to the fail edge so a future removal of either is caught.
func TestDecomposeFailEdgeIsLive(t *testing.T) {
	g := loadBuildProduct(t)
	n, ok := g.Nodes["Decompose"]
	if !ok {
		t.Fatal("Decompose node missing (#300)")
	}
	if n.Attrs["auto_status"] != "true" {
		t.Error("Decompose lacks auto_status:true — its `outcome = fail` edge is DEAD (codergen.go:635); a dropped requirement cannot fail the node (#300)")
	}
	if !hasEdgeWithCondition(g, "Decompose", "EscalateReview", "ctx.outcome = fail") {
		t.Error("Decompose has no `ctx.outcome = fail` edge to EscalateReview (#300)")
	}
}

// Test 2 — negative-control: Decompose writes the coverage table and uses the
// coverage-mapping language. Distinctive new substrings (NOT the bare token
// "SPEC.md", which is already GREEN at Decompose :467 — "SPEC.md may define a
// Fingerprint" — and would prove nothing).
func TestDecomposeCoverageTablePrompt(t *testing.T) {
	p := promptOf(t, loadBuildProduct(t), "Decompose")
	for _, sub := range []string{
		"requirement-coverage.md",
		"Owning milestone",
		"mandated",
		"COVERAGE_GAPS",
	} {
		if !strings.Contains(p, sub) {
			t.Errorf("Decompose prompt missing coverage-gate substring %q (#300)", sub)
		}
	}
}

// Test 3 — negative-control: Decompose's coverage gate carves out documented
// deferrals via a 3-way classification (owned | deferred | UNOWNED). Asserts
// tokens UNIQUE to the #300 block ("UNOWNED", "neither owned") — NOT the bare
// "DO NOT implement"/"deferred", which the pre-existing anti-scope-creep block
// already contains (would be a no-op assertion; test-reviewer I1, verified
// count=0 for UNOWNED/neither-owned, count>0 for DO-NOT-implement/deferred).
func TestDecomposeDeferralCarveout(t *testing.T) {
	p := promptOf(t, loadBuildProduct(t), "Decompose")
	for _, sub := range []string{"UNOWNED", "neither owned"} {
		if !strings.Contains(p, sub) {
			t.Errorf("Decompose coverage gate missing 3-way-classification substring %q (#300)", sub)
		}
	}
}

// Test 4 — negative-control: VerifyMilestone check 5 gains the owner-or-deferred
// "future work" rule. Anchor tokens matched SEPARATELY (not a full sentence) so
// benign rewording doesn't flake.
func TestVerifyMilestoneOwnerOrFailRule(t *testing.T) {
	p := promptOf(t, loadBuildProduct(t), "VerifyMilestone")
	for _, sub := range []string{"future work", "milestones.md", "DO NOT implement"} {
		if !strings.Contains(p, sub) {
			t.Errorf("VerifyMilestone prompt missing owner-or-deferred substring %q (#300)", sub)
		}
	}
}

// Test 5 — negative-control: FinalSpecCheck gains the same owner-or-deferred rule.
func TestFinalSpecCheckOwnerOrFailRule(t *testing.T) {
	p := promptOf(t, loadBuildProduct(t), "FinalSpecCheck")
	for _, sub := range []string{"future work", "milestones.md", "DO NOT implement"} {
		if !strings.Contains(p, sub) {
			t.Errorf("FinalSpecCheck prompt missing owner-or-deferred substring %q (#300)", sub)
		}
	}
}

// Test 6 — negative-control (prose-sync truthfulness pin, mirrors #299's
// TestQualityGateVerifyPromptTruthful): VerifyMilestone's prompt acknowledges
// that Decompose now owns a coverage gate (defense-in-depth cross-reference).
func TestVerifyMilestoneMentionsCoverageGate(t *testing.T) {
	p := promptOf(t, loadBuildProduct(t), "VerifyMilestone")
	if !strings.Contains(p, "requirement-coverage.md") {
		t.Error("VerifyMilestone prompt does not cross-reference requirement-coverage.md — prose-sync drift (#300)")
	}
}

// Test 7 — regression-pin: Decompose's out-edges are EXACTLY the conditional
// success→ApprovePlan and fail→EscalateReview pair. No new edge, no unconditional
// fallback (which CLAUDE.md forbids near loop targets). Use OutgoingEdges (filters
// on e.From) — NOT a loose g.Edges scan, which the incoming restart edges
// ApprovePlan->Decompose and ResetReviewBudget->Decompose would pollute.
func TestDecomposeOutEdgesUnchanged(t *testing.T) {
	g := loadBuildProduct(t)
	out := g.OutgoingEdges("Decompose")
	if len(out) != 2 {
		t.Fatalf("Decompose has %d out-edges, want exactly 2 (#300)", len(out))
	}
	if !hasEdgeWithCondition(g, "Decompose", "ApprovePlan", "ctx.outcome = success") {
		t.Error("Decompose lost its success→ApprovePlan edge (#300)")
	}
	if !hasEdgeWithCondition(g, "Decompose", "EscalateReview", "ctx.outcome = fail") {
		t.Error("Decompose lost its fail→EscalateReview edge (#300)")
	}
	for _, e := range out {
		if e.Condition == "" {
			t.Errorf("Decompose grew an unconditional out-edge → %s (forbidden near loop targets, #300)", e.To)
		}
	}
}

// Test 8 — regression-pin: the downstream gates keep auto_status:true (their
// STATUS:fail is what enforces the owner-or-deferred rule).
func TestVerifyAndFinalKeepAutoStatus(t *testing.T) {
	g := loadBuildProduct(t)
	for _, id := range []string{"VerifyMilestone", "FinalSpecCheck"} {
		if g.Nodes[id].Attrs["auto_status"] != "true" {
			t.Errorf("%s lost auto_status:true — its STATUS:fail gate is dead (#300)", id)
		}
	}
}
```

- [ ] **Step 2: Run the tests, verify the negative-controls FAIL (RED).**

Run: `go test ./pipeline/ -run 'TestDecompose|TestVerifyMilestone|TestFinalSpecCheck|TestVerifyAndFinal' -v`
Expected:
- **RED (negative-control):** `TestDecomposeCoverageTablePrompt`, `TestDecomposeDeferralCarveout`, `TestVerifyMilestoneOwnerOrFailRule`, `TestFinalSpecCheckOwnerOrFailRule`, `TestVerifyMilestoneMentionsCoverageGate`. Also `TestDecomposeFailEdgeIsLive` is RED on the `auto_status` half (the fail edge exists, but `auto_status` is absent).
- **GREEN (regression-pin):** `TestDecomposeOutEdgesUnchanged`, `TestVerifyAndFinalKeepAutoStatus`.
Confirm the five+one negative-controls are RED — that is the proof.

- [ ] **Step 3: Commit the RED tests** (mirrors #298 `ad0fe76` / #299 precedent — no active hook).

```bash
git add pipeline/build_product_requirement_coverage_test.go
git commit -m "test(300): negative-control tests for no-requirement-left-behind (RED)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 2: Decompose — auto_status + coverage-gate block

**Files:**
- Modify: `examples/build_product.dip` (`Decompose` attrs `:438-440`, prompt tail `:476-481`).

- [ ] **Step 1: Add `auto_status: true` to the Decompose node.** Re-grep: `grep -n 'agent Decompose' examples/build_product.dip`, then read the 4 lines after it. Insert after `reasoning_effort: high`.

Replace:
```
    model: claude-opus-4-6
    provider: anthropic
    reasoning_effort: high
    prompt:
      Based on the spec analysis, decompose the work into ordered milestones.
```
with:
```
    model: claude-opus-4-6
    provider: anthropic
    reasoning_effort: high
    auto_status: true
    prompt:
      Based on the spec analysis, decompose the work into ordered milestones.
```

- [ ] **Step 2: Append the coverage-gate block** to the Decompose prompt, immediately after the `known_failures` paragraph (ends `:481` "…where they should start passing.") and before the blank line preceding `human ApprovePlan`. Re-grep: `grep -n 'where they should start passing' examples/build_product.dip`. Insert after that line (6-space prompt indentation):

```
      ── REQUIREMENT COVERAGE (issue #300, epic #308 Phase 2) ──
      No spec-mandated verification may be left unowned. Do this in THIS
      order — the ordering is load-bearing (it stops you from back-filling
      the table to match milestones you already wrote, which would make
      the gate decorative):

      1. FIRST, before writing any milestone, read SPEC.md and list every
         spec-mandated verification into the LEFT columns of
         .ai/decisions/requirement-coverage.md. A line is "spec-mandated"
         ONLY if SPEC.md uses an obligation word (MUST / SHALL / "is
         required to" / "the test must" / a named test function or
         acceptance criterion) AND names a concrete, checkable behavior
         (a specific input/output, a numeric bound, a named error path).
         Aspirational words ("fast", "robust", "clean") with no checkable
         threshold are NOT mandated — do not list them. State how many you
         found; if none, write the line: SPEC mandates no named
         verifications (never leave the table silently empty — a silent
         empty table hides under-extraction). Table columns:
           | Mandated verification | SPEC.md source (line/section) | Owning milestone |

      2. THEN decompose into milestones as the rules above describe.

      3. FINALLY, fill the "Owning milestone" column. You may NOT add rows
         here and you may NOT invent a milestone to cover a verification.
         Each verification resolves to exactly one of:
         - owned: a milestone's "Done when" line covers it. Confirm with
           `grep -nE '^#+ *[Mm]ilestone' .ai/decisions/milestones.md` and
           cite `Milestone <N>: <the Done-when line, verbatim>`. A
           milestone that only mentions the topic in prose does NOT own it.
         - deferred: SPEC.md itself defers the behavior to a named later
           phase AND a milestone's "DO NOT implement" block records that
           deferral citing the spec section. Cite both. (This carve-out
           keeps the gate consistent with the anti-scope-creep machinery
           above — a correctly-deferred Phase-2+ feature is NOT a gap.)
         - UNOWNED: neither owned nor deferred. This is the bug this gate
           exists to catch.

      Then re-read .ai/decisions/requirement-coverage.md, count the UNOWNED
      rows, and emit on its own line: COVERAGE_GAPS: <count>. Then your
      terminal STATUS line:
        - every row owned or deferred (count 0) -> STATUS:success
        - any row UNOWNED (count > 0) -> list the unowned verification(s)
          ABOVE the STATUS line, then STATUS:fail (this routes to a human
          to re-plan rather than silently building with a dropped test).
      The final line must be EXACTLY `STATUS:success` or `STATUS:fail` —
      no parentheses, no counts, no trailing words (a trailing count makes
      the parser drop the line and default to success), OUTSIDE any code
      fence, alone on its line. Emit only success or fail — never
      STATUS:retry (this node has no retry route).
```

- [ ] **Step 3: Run the Decompose tests, verify they pass.**

Run: `go test ./pipeline/ -run 'TestDecompose' -v`
Expected: `TestDecomposeFailEdgeIsLive`, `TestDecomposeCoverageTablePrompt`, `TestDecomposeDeferralCarveout`, `TestDecomposeOutEdgesUnchanged` all PASS. (`TestVerifyMilestone*` / `TestFinalSpecCheck*` still RED — Tasks 3/4.)

- [ ] **Step 4: Validate the .dip still parses.**

Run: `dippin validate examples/build_product.dip`
Expected: passes. If `dippin` is not on PATH, STOP and ask the user — do NOT `go install` it.

- [ ] **Step 5: Commit.**

```bash
git add examples/build_product.dip
git commit -m "feat(build_product): Decompose coverage gate — every mandated verification owns a milestone (#300)

Add auto_status:true (makes the dead Decompose->EscalateReview fail edge
live) + a coverage-gate block: enumerate mandated verifications from SPEC.md
FIRST, decompose, assign owners LAST into .ai/decisions/requirement-coverage.md,
emit COVERAGE_GAPS:<n> + STATUS:fail when a verification is neither owned by a
milestone nor a documented-deferred (DO NOT implement) item.

Refs epic #308 Phase 2 (second item).

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 3: VerifyMilestone — owner-or-deferred rule + prose-sync note

**Files:**
- Modify: `examples/build_product.dip` (`VerifyMilestone` check 5 `:861-866`).

- [ ] **Step 1: Append the owner-or-deferred rule to check 5.** Re-grep: `grep -n 'SPEC GAPS BEYOND MILESTONE NOTES' examples/build_product.dip`, read through the end of item 5 (the line "…Cross-check against SPEC.md directly."). Insert immediately after that line, inside item 5 (9-space indentation to match the item body):

```
         A requirement may NOT be waved through as "future work" or "a
         later milestone" unless it is EITHER (a) owned — a named LATER
         milestone in .ai/decisions/milestones.md has a "Done when" line
         covering it
         (`grep -nE '^#+ *[Mm]ilestone' .ai/decisions/milestones.md` and
         cite the milestone number + the Done-when line verbatim) — OR (b)
         deferred — SPEC.md defers it to a named later phase AND a milestone's
         "DO NOT implement" block records that deferral citing the spec
         section (cite both; consistent with check 4's deferred-field rule).
         A deferral that is neither owned nor documented-deferred is a
         dropped requirement: STATUS:fail. (This is the code-goblin miss —
         the verifier saw the 429/cancel gap and waved it through as "future
         work" with no owning milestone.) Cross-check against
         .ai/decisions/requirement-coverage.md (written by Decompose) when
         present; if it disagrees with a live SPEC.md re-read, SPEC.md wins.
```

This single insertion satisfies BOTH `TestVerifyMilestoneOwnerOrFailRule` (anchors `future work` + `milestones.md` + `DO NOT implement`) and `TestVerifyMilestoneMentionsCoverageGate` (substring `requirement-coverage.md`).

- [ ] **Step 2: Run the VerifyMilestone tests, verify they pass.**

Run: `go test ./pipeline/ -run 'TestVerifyMilestone|TestVerifyAndFinal' -v`
Expected: `TestVerifyMilestoneOwnerOrFailRule`, `TestVerifyMilestoneMentionsCoverageGate`, `TestVerifyAndFinalKeepAutoStatus` PASS. (`TestFinalSpecCheckOwnerOrFailRule` still RED — Task 4.)

- [ ] **Step 3: Validate + commit.**

```bash
dippin validate examples/build_product.dip
git add examples/build_product.dip
git commit -m "feat(build_product): VerifyMilestone rejects unowned future-work deferrals (#300)

Check 5 now fails a requirement waved through as 'future work' unless a named
milestone owns it (Done-when line) or it is a documented-deferred DO-NOT-implement
item. Cross-references Decompose's requirement-coverage.md (prose-sync).

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 4: FinalSpecCheck — owner-or-deferred rule + artifact hygiene

**Files:**
- Modify: `examples/build_product.dip` (`FinalSpecCheck` "For EVERY requirement" `:1485-1488`; the `.ai/build/` allowlist note `:1490-1498`; `Cleanup` comment `:1564`).

- [ ] **Step 1: Insert the owner-or-deferred rule** into the "For EVERY requirement" block. Re-grep: `grep -n 'For EVERY requirement, prescription' examples/build_product.dip`. Insert after the "Was nothing extra added…" line and before the blank line preceding "Also verify:" (6-space indentation):

Replace:
```
      For EVERY requirement, prescription, and instruction in the spec:
      - Is it implemented?
      - Is it implemented correctly?
      - Was nothing extra added beyond what the spec asked for?

      Also verify:
```
with:
```
      For EVERY requirement, prescription, and instruction in the spec:
      - Is it implemented?
      - Is it implemented correctly?
      - Was nothing extra added beyond what the spec asked for?
      - If it is NOT implemented and the report defers it to "future work":
        every planned milestone is already complete at THIS gate, so being
        "owned" by a milestone is NOT an acceptable excuse — that milestone
        should have built it, and an owned-but-unimplemented requirement is
        a failure, not future work. The deferral is acceptable ONLY when
        SPEC.md itself defers the behavior to a named later phase AND a
        "DO NOT implement" block in .ai/decisions/milestones.md records that
        deferral, citing the spec section (cite both). A future-work deferral
        that is not SPEC-documented as deferred keeps the early STATUS:fail
        in place — do NOT emit the terminal STATUS:success (issue #300).

      Also verify:
```

(Note: FinalSpecCheck is fail-closed — the rule BLOCKS the terminal `STATUS:success`; it must NOT say "emit STATUS:fail", which mid-prose would just be overridden by the final success line.)

- [ ] **Step 2: Note that `.ai/decisions/*.md` artifacts are not "extras".** Re-grep: `grep -n 'No UNEXPECTED leftover files in .ai/build' examples/build_product.dip`. The allowlist is correctly `.ai/build/`-scoped (the new `requirement-coverage.md` is in `.ai/decisions/`, so no collision — this is documentation hygiene only). Read the bullet; append a sentence to it (keep its existing indentation). Change the end of that bullet from:
```
        review-codex.md, review-gemini.md). Only flag files outside
        this explicit allowlist.
```
to:
```
        review-codex.md, review-gemini.md). Only flag files outside
        this explicit allowlist. (.ai/decisions/*.md workflow artifacts —
        spec-analysis, milestones, requirement-coverage (#300), compliance —
        are expected outputs, never "extras".)
```

- [ ] **Step 3: Add `requirement-coverage` to the Cleanup preserved-decisions comment.** Re-grep: `grep -n 'Keep: .ai/decisions/' examples/build_product.dip`. Replace:
```
      # Keep: .ai/decisions/ (spec-analysis, milestones, review-synthesis, compliance)
```
with:
```
      # Keep: .ai/decisions/ (spec-analysis, milestones, requirement-coverage, review-synthesis, compliance)
```

- [ ] **Step 4: Run the FinalSpecCheck tests + full new file, verify all green.**

Run: `go test ./pipeline/ -run 'TestDecompose|TestVerifyMilestone|TestFinalSpecCheck|TestVerifyAndFinal' -v`
Expected: ALL eight tests PASS.

- [ ] **Step 5: Validate + commit.**

```bash
dippin validate examples/build_product.dip
git add examples/build_product.dip
git commit -m "feat(build_product): FinalSpecCheck rejects unowned future-work deferrals (#300)

Final gate now blocks STATUS:success on a future-work deferral that no named
milestone owns and no DO-NOT-implement entry documents. Document
requirement-coverage.md as an expected .ai/decisions/ artifact (Cleanup + the
FinalSpecCheck 'not extras' note).

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 5: CHANGELOG

**Files:**
- Modify: `CHANGELOG.md` (`[Unreleased]` → `### Added`, at the TOP, above the #299 entry).

- [ ] **Step 1: Add the entry.** Re-grep: `grep -n '## \[Unreleased\]' CHANGELOG.md`, insert at the top of `### Added`:

```markdown
- **build_product: no-requirement-left-behind** (closes #300, refs epic #308
  Phase 2 — second item). Stops spec-mandated verifications from vanishing from a
  build. `Decompose` now carries `auto_status: true` — which makes its previously
  **dead** `Decompose -> EscalateReview when fail` edge live (an agent node
  defaults to success unless `auto_status` reads its `STATUS:` line) — and runs a
  coverage gate: it enumerates every spec-mandated verification from `SPEC.md`
  first, decomposes, assigns owners last, and writes a
  `.ai/decisions/requirement-coverage.md` table mapping each mandated verification
  to an owning milestone. A verification that is **neither owned by a milestone
  nor a documented-deferred `DO NOT implement` item** emits `COVERAGE_GAPS:<n>` +
  `STATUS:fail`, routing to a human to re-plan instead of silently building with a
  dropped test. `VerifyMilestone` and `FinalSpecCheck` gain the matching
  owner-or-deferred rule: a requirement may not be waved through as "future work"
  unless a named milestone owns it or a `DO NOT implement` entry documents the
  deferral. `.dip`-only change; no engine code. **Behavior change:** a
  decomposition that drops a mandated test now fails at planning time rather than
  shipping the gap.
```

- [ ] **Step 2: Commit.**

```bash
git add CHANGELOG.md
git commit -m "docs(300): CHANGELOG entry for no-requirement-left-behind (#300)

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Task 6: Full verification + PR

- [ ] **Step 1: dippin gates.**

```bash
dippin validate examples/build_product.dip
dippin doctor examples/build_product.dip examples/ask_and_execute.dip examples/build_product_with_superspec.dip
dippin simulate -all-paths examples/build_product.dip
```
Expected: `validate` passes; `doctor` is **A** across all three (build_product baseline A 90/100 — must not regress; a squad reviewer empirically confirmed `auto_status` keeps it byte-identical); `simulate -all-paths` reports 100% terminate, no new cycle / dead-stop (the now-live `Decompose -> EscalateReview` fail path is reachable and terminates). If `dippin` is not on PATH, STOP and ask the user — do NOT `go install` it.

- [ ] **Step 2: Go gates.**

```bash
go build ./...
go test ./... -short
gofmt -l pipeline/build_product_requirement_coverage_test.go   # must print nothing
```
Expected: build clean; tests green; gofmt clean.

- [ ] **Step 3: Adversarial self-review** (before pushing). Walk each:
  - **Dead-edge revived:** `grep -n 'auto_status' examples/build_product.dip` shows Decompose now has it; `TestDecomposeFailEdgeIsLive` passes.
  - **No new edges / no loop-target fallback:** `git diff main -- examples/build_product.dip` shows ZERO changes in the edges block (`:1584-1658`); `TestDecomposeOutEdgesUnchanged` passes.
  - **Deferral carve-out present** in all three nodes (Decompose, VerifyMilestone, FinalSpecCheck) — guards against false-failing correctly-deferred work.
  - **STATUS hygiene:** Decompose's new text forbids trailing-count/retry and requires outside-fence; no bare `escalate`/`tests-pass` token in any new prose (`git diff main -- examples/build_product.dip | grep -nE '\bescalate\b|tests-pass'` → only `EscalateReview` node refs, no bare token).
  - **Coverage table NOT in milestones.md:** the new prompt writes only `requirement-coverage.md`.
  - **#301 untouched:** no new `SpecLint` subgraph; `git diff main --stat` shows only `build_product.dip`, the test file, CHANGELOG, and the docs/superpowers specs/plans.

- [ ] **Step 4: Push + open the PR.**

```bash
git push -u origin fix/300-no-requirement-left-behind
gh pr create --title "feat(build_product): no-requirement-left-behind — Decompose coverage + owner-or-fail Verify (#300)" --body "$(cat <<'EOF'
Closes #300. Refs epic #308 Phase 2 (second item, after #299/PR #321).

## What

Two failure modes let spec-mandated requirements vanish from a `build_product`
run: (1) `Decompose` could omit a mandated test from every milestone's "Done
when"; (2) `VerifyMilestone`/`FinalSpecCheck` could wave a requirement through as
"future work" without confirming a later milestone owns it. On the `code-goblin`
run, a synthetic-429 test and a cancel-within-100ms test were owned by no
milestone and the verifier waved the gap through.

## The change (`.dip`-only, no engine code)

- **`Decompose`** gains `auto_status: true` — which makes its previously **dead**
  `Decompose -> EscalateReview when ctx.outcome = fail` edge live (an agent node
  defaults to `OutcomeSuccess` unless `auto_status` consults its `STATUS:` line —
  `pipeline/handlers/codergen.go:627-637`). It then runs a **coverage gate**:
  enumerate mandated verifications from `SPEC.md` FIRST, decompose, assign owners
  LAST into `.ai/decisions/requirement-coverage.md`, emit `COVERAGE_GAPS:<n>` +
  `STATUS:fail` when a verification is **neither owned by a milestone nor a
  documented-deferred `DO NOT implement` item**.
- **`VerifyMilestone` check 5 + `FinalSpecCheck`** gain the matching
  owner-or-deferred rule: a "future work" deferral is rejected unless a named
  milestone owns it (cite the "Done when" line) or a `DO NOT implement` entry
  documents the deferral.

## Design subtleties (from a five-expert squad review of the design)

- **DO-NOT-implement carve-out (critical).** The owner-or-fail rule is a 3-way
  classification `owned | deferred | UNOWNED`, failing only on UNOWNED — so it does
  NOT contradict the existing anti-scope-creep machinery by false-failing a
  correct decomposition that properly defers Phase-2+ work.
- **Read-back gate, not tail-token judgment.** Decompose re-reads the table and
  counts UNOWNED rows (`COVERAGE_GAPS:<n>`) rather than relying on a holistic
  final-token judgment — the run that drops a requirement is the one most likely
  to garble its STATUS line.
- **Closed inclusion test** for "spec-mandated verification" (obligation word +
  checkable behavior; aspirational lines get `N/A`, never a fail) — prevents
  false-fail human fatigue.
- **STATUS hygiene** (exact token, outside fence, only success/fail — never
  `retry`) copied from `FinalSpecCheck`'s existing warnings.

## Out of scope / follow-up

- **#301 (spec-coherence preflight)** — its rule (f) will later FEED this coverage
  table; #300 stays self-sufficient (Decompose derives the mandated list from
  SPEC.md itself). Soft, one-directional coupling.
- **Decompose-retry circuit breaker** — the now-live `Decompose -> EscalateReview
  -> retry -> Decompose` cycle consumes the global restart budget under
  autopilot/headless drivers (the same property the existing review-retry loop
  has). The DO-NOT-implement carve-out removes the deterministic re-fail trigger;
  a dedicated `decompose_attempts` breaker is a structural change left as a
  follow-up.

## Tests

`pipeline/build_product_requirement_coverage_test.go` — graph string/edge asserts,
RED-first (no runtime test: this is agent-prompt rules with no executable shell).
`TestDecomposeFailEdgeIsLive` pins the `auto_status`↔fail-edge coupling (the
load-bearing change); `TestDecomposeOutEdgesUnchanged` pins no new/unconditional
Decompose edge; owner-or-deferred and coverage-table prompt rules pinned by
substring; a prose-sync truthfulness pin.

## Verification

`dippin validate` ✓ · `dippin doctor` A grade (90/100, unchanged) ✓ · `dippin
simulate -all-paths` 100% terminate ✓ · `go build ./... && go test ./... -short` ✓

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

- [ ] **Step 5: Address bot review** (CodeRabbit/Copilot/Codex). Per `superpowers:receiving-code-review`: verify each comment against the actual code, fix valid ones, decline invalid ones with a technical rationale, reply in-thread, resolve addressed threads, keep the PR body + CHANGELOG in sync, keep CI green. NEVER `--no-verify`. Stop when findings go minor/incorrect.

---

## Self-review against the spec

- **Decompose `auto_status` + dead-edge revival** → Task 2 Step 1 + `TestDecomposeFailEdgeIsLive` ✓
- **Coverage table + enumerate-first ordering + closed inclusion test + affirmative extraction** → Task 2 Step 2 ✓
- **DO-NOT-implement deferral carve-out (3-way owned|deferred|UNOWNED)** → Task 2 Step 2 + Task 3 Step 1 + Task 4 Step 1 + `TestDecomposeDeferralCarveout` ✓
- **Read-back gate + `COVERAGE_GAPS` sentinel + STATUS hygiene (exact token, fence, no retry)** → Task 2 Step 2 ✓
- **VerifyMilestone owner-or-deferred + prose-sync cross-reference** → Task 3 + Tests 4, 6 ✓
- **FinalSpecCheck owner-or-deferred (blocks STATUS:success, not "emit fail")** → Task 4 Step 1 + Test 5 ✓
- **Artifact hygiene (allowlist note + Cleanup comment)** → Task 4 Steps 2–3 ✓
- **No new edges / no loop-target fallback** → Task 2 (no edge edits) + `TestDecomposeOutEdgesUnchanged` + Task 6 Step 3 diff check ✓
- **Keep coverage table out of milestones.md** → Task 2 Step 2 (writes only requirement-coverage.md) + Task 6 Step 3 ✓
- **Restart-budget residual documented** → PR body "Out of scope / follow-up" ✓
- **#301 untouched / soft coupling noted** → Task 6 Step 3 + PR body ✓
- **dippin validate/doctor/simulate + go gates + gofmt** → Task 6 Steps 1–2 ✓
- **CHANGELOG Added, Phase 2 second item, behavior-change framing** → Task 5 ✓
