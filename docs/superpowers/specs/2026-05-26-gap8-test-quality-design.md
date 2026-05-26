# Gap 8 — Test Quality (Issue #233)

**Status:** Design v5 (radical simplification post-user-feedback)

**Author:** Claude (Opus 4.7) + Clint Ecker

**Date:** 2026-05-26

**Closes:** Gap 8 of [#233](https://github.com/2389-research/tracker/issues/233).

---

## 1. Problem

The `build_product` audit named five test-quality regressions that shipped green when the same agent wrote both implementation and tests:

| # | Audit case | Archetype |
|---|------------|-----------|
| W4 | `goblin.start` test logs an empty SHA value without asserting on it | zero-assertion |
| W5 | `TestRun_SignalHandling` tests stdlib `signal.NotifyContext` instead of the daemon's handler | wrong-target |
| W13 | tests use `time.Now()` directly when a `Clock` seam exists | DI bypass |
| W17 | `TestLoop_BusyDrops` uses three `time.Sleep` calls as phase fences | sleep-as-fence |
| W21 | `bytes_trimSpace` declared alongside `bytes.TrimSpace` — Go subtest case-fold collision | subname collision |

These are honest LLM oversights, not adversarial attacks. The audit found NO evidence of mid-run rubric tampering or SPEC.md mutation. The defense should be sized to that threat model.

## 2. Goals

- Catch W17 (sleep-as-fence) at `FinalSpecCheck` with one grep and disposition discipline.
- Catch W4, W5, W13 at the **reviewer layer** by upgrading reviewer rubric point 3 to demand shown-work grep evidence for the three smell shapes.
- Cover W21 by explicit out-of-scope acknowledgement (golangci-lint territory).
- **Minimal cleverness.** No chmod+sha tripwires. No integrity preambles. No SPEC.md SHA. No DI-bypass cross-check inside Gap 7's rubric. No 7-wrapper bypass enumeration in the prompt. (The one smell that ships at FinalSpecCheck — sleep-as-fence — enumerates per-language sleep-call shapes; that's a single smell with language patterns, not a matrix.)
- Compose with PR #246, PR #249, PR #254 without modifying any of them beyond the minimum required.

## 3. Non-goals

- Replacing a real test-quality static analyzer.
- **W21 (subname collision)** — Go-specific lexical bug; golangci-lint territory.
- **Wrong-target / zero-assertion / DI-bypass detection at FinalSpecCheck** — these are semantic judgments that reviewers do better than grep. The reviewer rubric upgrade demands shown-work; FinalSpecCheck stays out of these smells.
- **Cross-cutting safety hardening of Gap 7** (chmod+sha tripwire). The audit found no evidence of mid-run tampering. If a future audit shows actual tampering, file as a separate hardening PR with its own threat model. Don't bundle defense-for-an-unobserved-attack into a test-quality PR.
- **SPEC.md SHA verification.** Same argument as above.
- **A shared rubric file for Gap 8.** Three prompt lines per reviewer don't need a file; the FinalSpecCheck Smell 3 grep doesn't need a file either.
- **Engine-level parser changes.** The parser is contract-locked. The inverted STATUS contract (PR #254) gives fail-closed on truncation; that's the load-bearing defense.
- The 38 audit findings in #233 Appendix A. They inform the rubric, not separate work items.

## 4. Design

### 4.1 Single owner: `FinalSpecCheck`, but only for sleep-as-fence

Sleep-as-fence (W17) is the one smell where grep is mechanically reliable and the disposition is concrete (cite SPEC.md timing-contract section OR cite deterministic primitive replacement). It lives in `FinalSpecCheck` as a ~12-line section between INTERFACE REACHABILITY and SPEC.md compliance.

All other smells (W4 zero-assertion, W5 wrong-target, W13 DI bypass) are semantic and live at the reviewer rubric layer. Reviewers reading test bodies catch them more reliably than grep.

`VerifyMilestone` is unchanged. `Setup` is unchanged. Gap 7's `iface-reachability-rubric.md` is unchanged.

### 4.2 The smell that earns its place: sleep-as-fence

W17's pattern (test using `time.Sleep` between phases) is mechanically detectable, language-portable (sleep exists in every test framework), and resists handwave dispositions. Grep produces shown-work evidence (file:line); each hit demands disposition (a) cite a SPEC.md timing-contract section, (b) cite a deterministic primitive that replaced the sleep, or (c) cite a `.ai/decisions/*.md` waiver naming this specific test.

"Intentional integration test" without a spec-section citation is blanket — FAIL. This is the only enforcement the prompt adds. Everything else lives in reviewer prose.

### 4.3 Reviewer rubric upgrade: shown-work for three semantic smells

Point 3 of all three reviewer rubrics (ReviewClaude, ReviewCodex, ReviewGemini) currently says:

> "TEST VERIFIES CONTRACT: For each assertion in a test under this section, does it verify the spec's contract, or does it mirror what the implementation happens to produce? Snapshot tests regenerated from current output are FAIL. Tests asserting `attempts == 2` when the spec says 'max 2 retries' (= 3 attempts) are FAIL."

Plus ReviewGemini at lines 936-938:

> "Tests that only validate standard-library or third-party-library behavior instead of the project's own logic are FAIL."

v5 makes two changes:

1. **Promote** the Gemini-only sentence to ReviewClaude and ReviewCodex.
2. **Append** to all three reviewers' point 3:

```text
   Also: for each test file under this section, show grep evidence
   that the tests do NOT exhibit (a) zero-assertion bodies — test
   functions with no t.Error/t.Fatal/require/assert/expect/should/
   panic/etc. calls; or (b) DI bypass — tests calling time.Now,
   rand.Read, stdlib-IO when the production code defines a
   Clock/Random/IO seam. Cite the grep command and its output for
   each hit AND for the empty-result case. Audit any
   `.ai/decisions/*.md` waivers referenced — flag rationales whose
   logic, if applied broadly, would void the smell check, and
   verify cited SPEC.md sections actually contain relevant content.
```

This is ~9 lines per reviewer × 3 = 27 lines. Point 3 grows from ~6 lines to ~15 — comparable to point 2 (INTERFACE REACHABILITY, ~9 lines after PR #254). Balance preserved within rounding.

### 4.4 Legacy STATUS tail fix (small)

FinalSpecCheck's existing tail (lines 1115-1119) says "If fully compliant: STATUS:success / If not: emit STATUS:fail." This contradicts PR #254's inverted contract under last-line-wins semantics. v5 fixes the tail in a 5-line edit (§5.3 below) to align with the inverted contract — single source of truth for the allowlist via reference to lines 1103-1108, not inline re-enumeration.

### 4.5 What v5 explicitly does NOT do

- **No chmod+sha on the iface rubric.** The audit found no rubric tampering. A future audit that finds it would file a separate PR with a real defense (relocate the file outside the workdir, per CLAUDE.md #213's pattern). The chmod+sha "tripwire" is detection-by-accident at best — the workflow source documents how to bypass it. Better to do nothing than do something that overstates protection.
- **No integrity preamble at FinalSpecCheck.** Same argument.
- **No SPEC.md SHA verification.** Same argument.
- **No DI-bypass cross-check inside Gap 7's rubric.** W13 detection at reviewer layer is sufficient. The fold-in saved ~25 prompt lines in v3 but introduced an attack-surface concentration concern and language-coverage inconsistency. v5 simply lets reviewers catch W13.
- **No new TEST QUALITY rubric section enumerating Smell 1 + Smell 6.** Reviewer prose handles W4 and W13.
- **No named-test spot-check.** v2 had one; v3 dropped it; v5 keeps it dropped.
- **No 7-wrapper STATUS-bypass enumeration.** The inverted STATUS contract from PR #254 already gives fail-closed on truncation. Parser-level fence-bypass is filed as follow-up (§10 q1).

The cumulative prompt growth across all of FinalSpecCheck + three reviewers is ~40 prompt lines in v5 vs v4's ~165-200 and v1's ~330.

### 4.6 Honest residual risks

- An agent rubber-stamps the sleep-fence grep with "all clear" without running it. Same residual risk as PR #254's iface enumeration. Mitigation: reviewers see the FinalSpecCheck output; reviewer audit catches blatant cases. Determined fabrication isn't defended.
- A waiver naming a real test, citing a real SPEC.md section that's irrelevant to the smell, passes the FinalSpecCheck check and the reviewer audit at point 3 (which requires verifying cited content is relevant — but the reviewer doing the verification is the same model class). Two-layer audit raises the cost; doesn't eliminate. Acknowledged.
- W5 rename evasion (round-3 Red-Team finding): an agent who renames `TestRun_SignalHandling` to `TestSignalContext` evades any name-based spot-check. v5 has no name-based spot-check; W5 detection is "reviewer prose alone." Acknowledged.
- DI-bypass detection at the reviewer layer (not FinalSpecCheck) means each reviewer runs the grep independently. Three runs instead of one. Reviewers run in parallel; wall-clock is the same.

These residuals match the v4 set minus the ones v4's elaborate machinery introduced (e.g., chmod+sha self-bypass, parser/spec contradiction, regex anchor bugs).

## 5. Concrete edits

Two sections of `examples/build_product.dip` change; `make sync-workflows` mirrors.

### 5.1 `FinalSpecCheck` — Smell 3 (sleep-as-fence) section

Insert between INTERFACE REACHABILITY (ends ~line 1093) and the SPEC.md compliance section (starts ~line 1095):

```dip
      TEST QUALITY — sleep-as-fence (issue #233 Gap 8 W17):

      Grep test files for sleep-class calls:
        Go:        grep -rnE '(time\.Sleep|<-time\.After)\(' \
                     --include='*_test.go' .
        Python:    grep -rnE '(time|asyncio|trio|anyio|gevent)\.sleep\(' \
                     --include='test_*.py' --include='*_test.py' .
        JS/TS:     grep -rnE '(await sleep\(|setTimeout\(|waitForTimeout\(|cy\.wait\()' \
                     --include='*.test.*' --include='*.spec.*' --include='*.cy.*' .
        Rust:      find . -type f -name '*.rs' \
                     \( -path '*/tests/*' -o -name '*_test.rs' \) 2>/dev/null \
                     -exec grep -nE '(thread::sleep|tokio::time::sleep|async_std::task::sleep)' {} + \
                     || true
                   # Restricts to `tests/` integration tests and
                   # `_test.rs` files. Inline `#[cfg(test)] mod tests`
                   # blocks in source files aren't filtered — agent
                   # should note as a limitation when relevant.
        Ruby:      grep -rnE '(^|[^[:alnum:]_])(sleep[[:space:]]*\(?[0-9]|Kernel\.sleep)' \
                     --include='*_test.rb' --include='*_spec.rb' .
        Java/Kotlin: find . -path '*/src/test/*' -type f \
                       \( -name '*.java' -o -name '*.kt' \) 2>/dev/null \
                       -exec grep -nE '(Thread\.sleep|delay\(|Mono\.delay)' {} + \
                       || true
      For other languages, name the framework and its sleep-class
      call shape and run the equivalent grep.

      Paste each grep's full output (even when empty). For each hit,
      give disposition: (a) the sleep IS the SUT — cite the SPEC.md
      section calling out the timing contract, file:line; OR (b)
      replaced by deterministic primitive — cite the primitive's
      introduction; OR (c) waiver per `.ai/decisions/*.md` naming
      the specific test with smell-specific rationale citing a
      SPEC.md section. "Intentional timing test" without a
      spec-section citation is blanket — FAIL.

      W4 (zero-assertion), W5 (wrong-target), W13 (DI bypass) are
      caught by reviewer rubric point 3 upgrade (§5.2 of this PR's
      spec) — not detected here. W21 (subname collision) is out of
      scope.
```

### 5.2 Reviewer rubric — point 3 upgrade × 3 prompts

For ReviewClaude (line 826-832), ReviewCodex (line 873-878), ReviewGemini (line 931-939):

- **Promote** the Gemini-only sentence at `examples/build_product.dip:936-938` (`"Tests that only validate standard-library or third-party-library behavior instead of the project's own logic are FAIL."`) to ReviewClaude and ReviewCodex point 3.
- **Append** to all three reviewers' point 3:

```text
   Also: for each test file under this section, show grep evidence
   that the tests do NOT exhibit (a) zero-assertion bodies — test
   functions with no t.Error/t.Fatal/require/assert/expect/should/
   panic/etc. calls; or (b) DI bypass — tests calling time.Now,
   rand.Read, stdlib-IO when the production code defines a
   Clock/Random/IO seam. Cite the grep command and its output for
   each hit AND for the empty-result case. Audit any
   `.ai/decisions/*.md` waivers referenced — flag rationales whose
   logic, if applied broadly, would void the smell check, and
   verify cited SPEC.md sections actually contain relevant content.
```

Total reviewer-prompt growth: ~9 lines × 3 reviewers + ~3 lines × 2 reviewers (Gemini-sentence promotion) = ~33 lines.

### 5.3 Legacy STATUS tail rewrite (5 lines)

Replace FinalSpecCheck lines 1115-1119 with:

```dip
      Write the final compliance report to .ai/decisions/compliance.md

      Emit `STATUS:success` as your terminal line ONLY if INTERFACE
      REACHABILITY (every method has a production caller or
      carve-out), TEST QUALITY (every sleep-fence hit dispositioned),
      and SPEC.md compliance (every requirement implemented, no
      extras, no TODO/FIXME/HACK, tests pass, no unexpected files
      in .ai/build/ outside the allowlist enumerated above) all
      passed. Otherwise the early `STATUS:fail` from your first
      line stays in place. The parser requires `STATUS:<value>` on
      its own line, no leading characters, no fences/tables.
```

This aligns the tail with PR #254's inverted contract under last-line-wins semantics and refers to the existing allowlist enumeration earlier in the SPEC.md compliance section rather than re-listing filenames.

### 5.4 `Setup` — no change

Unchanged. No chmod+sha, no SPEC.md SHA capture, no new files.

### 5.5 Gap 7 `iface-reachability-rubric.md` — no change

Unchanged. No DI-bypass cross-check folded in.

### 5.6 `VerifyMilestone` — no change

Unchanged.

## 6. Build and verification

1. Edit `examples/build_product.dip`:
   - FinalSpecCheck: insert Smell 3 (§5.1) between INTERFACE REACHABILITY and SPEC.md compliance.
   - FinalSpecCheck: rewrite legacy STATUS tail (§5.3).
   - Three reviewers: promote Gemini-sentence and append point 3 lines (§5.2).
2. `make sync-workflows` — stage both files.
3. `dippin doctor examples/build_product.dip` — must remain A grade.
4. `dippin simulate -all-paths examples/build_product.dip` — every path terminates.
5. `tracker validate examples/build_product.dip` — clean.
6. `go build ./... && go test ./... -short` — all 17 packages pass.
7. `make check-workflows` — sync confirmed.
8. Empirically test the Smell 3 grep patterns against a small sandbox repo containing intentional sleeps in tests — verify each language's grep produces hits. This is implementation-level verification, not part of the spec.
9. **Update `CHANGELOG.md`** under `[Unreleased]`.

## 7. Risk register

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| Agent rubber-stamps Smell 3 grep with "all clear" | Medium | Medium | Shown-work discipline — agent pastes grep output (empty or non-empty). Reviewer audit at point 3 catches blatant cases. Determined fabrication isn't defended. |
| Sleep grep flags legitimate timing tests (rate limiters, backoff) | High | Low | Disposition (a) accepts "the sleep IS the SUT" IF agent cites a SPEC.md timing-contract section. Without citation, blanket "intentional" rationales FAIL. Cited section must be real; reviewer audit verifies relevance. |
| W4 / W5 / W13 escape detection (no FinalSpecCheck grep) | Medium | Medium | Reviewer rubric point 3 demands shown-work for these at reviewer layer. Three independent reviewers (including adversarial Gemini lane from PR #249) raise the catch probability. Reviewer prose is what the audit identified as needing strengthening — v5 strengthens it without adding FinalSpecCheck machinery. |
| W5 rename evasion (`TestSignalContext` instead of `TestRun_SignalHandling`) | Medium | Medium | No name-based spot-check. Reviewer rubric "stdlib-only tests are FAIL" is name-agnostic — promotes to all three reviewers. |
| Sleep grep false positives on Java/Kotlin reactive (`Mono.delay`) | Medium | Low | Disposition (b) accepts "replaced by deterministic primitive" — cite the primitive's introduction (e.g., `StepVerifier.withVirtualTime`). |
| Waivers in `.ai/decisions/*.md` cite real spec sections that are irrelevant | Medium | Medium | Reviewer audit at point 3 must verify cited content is relevant. Two-layer audit raises cost; sophisticated forgery still possible (acknowledged residual). |
| Parser sees `STATUS:success` lines inside agent's fenced grep-output blocks | Low | Medium | Inverted STATUS contract from PR #254: the early `STATUS:fail` (first line) survives truncation. Parser-level fix for bypass shapes (table cells, blockquotes, etc.) is filed as follow-up (§10 q1). |
| FinalSpecCheck prompt grows by ~12 lines (sleep section) + ~5 lines (tail rewrite) | Low | Low | Smaller than any prior version. dippin-lang has no prompt-length warning. |

## 8. v1 → v2 → v3 → v4 → v5 condensed history

Five iterations across three squad review rounds (round 1 on v1, round 2 on v2, round 3 on v3 — v4 was a between-rounds draft that the user steered away from before squad review). Compressed lessons:

- **v1** (6 smells, shared rubric file, 5-language enumeration): squad 5/5 major redesign. Over-engineered, arbitrary slice, fail-open by default.
- **v2** (3 smells inlined, named-test spot-check, code-fence prompt): squad 3 minor / 2 major. Named-test spot-check brittle; chmod+sha cherry-pick demanded.
- **v3** (DI-bypass folded into Gap 7, chmod+sha + integrity preamble, dropped named-test spot-check, dropped numbered hits): squad 1 ship / 1 minor / 3 major. New bugs: parser/spec contradiction (`-` bullets mandated, parser ignores), SURVEY regex misses top-level test directories, Rust inline-test pipeline mathematically broken, sha256sum macOS portability, SPEC.md mismatch exit code, DI-bypass language slice inconsistent with Gap 7.
- **v4** (between-rounds draft, never squad-reviewed): bug-fix iteration on v3. User feedback: "produce a simpler specification/design that achieves most if not all the goals with pragmatism and elegance and simplicity with minimal to no cleverness or hacks." Drove v5.
- **v5** (this version): radical simplification. One grep at FinalSpecCheck (sleep-as-fence — the only smell where grep is mechanically reliable). W4 / W5 / W13 caught at reviewer layer with shown-work prose. No chmod+sha, no integrity preamble, no SPEC.md SHA, no DI-bypass fold-in, no inline TEST QUALITY rubric, no 7-wrapper bypass enumeration. ~40 prompt lines total vs v1's ~330 and v4's ~165-200.

The simplification is justified by the audit's actual evidence: honest LLM oversight, not adversarial tampering. The defense should be sized to the observed threat model. If a future audit shows mid-run rubric mutation or SPEC.md prompt injection, file as separate hardening PR with a proper defense (file relocation, signing, sandboxing — not chmod+sha tripwires).

## 9. References

- Issue #233 — `build_product` audit recap.
- PR #246 — Gaps 1/3/6.
- PR #249 — Gaps 2/4. Reviewer rubric balanced 5-point structure; shown-work standard at point 1; adversarial Gemini lane that v5's reviewer-layer detection leans on.
- PR #254 — Gap 7. Inverted STATUS contract (load-bearing in v5).
- Squad reviews 2026-05-26 (rounds 1, 2, 3): drove v1 → v5.
- CLAUDE.md — repo-level instructions; #213 activity-log integrity pattern (the right defense shape if mid-run tampering becomes observed — relocate the file outside the workdir).

## 10. Open questions

1. Parser-level fix for STATUS-bypass shapes (round-3 Red-Team) — file as follow-up. Out of v5 scope.
2. Pattern decision rule for workflow discipline placement (Architect rounds 2 + 3) — file as follow-up doc task. Out of v5 scope. v5 simply doesn't add new patterns.
3. End-to-end validation against the original code-goblin SPEC.md (per the brief's "Validation strategy after Chunk C lands") — file as v0.31.0-release follow-up issue. Out of v5 scope.
