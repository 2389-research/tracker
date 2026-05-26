# Gap 8 Test Quality Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close Gap 8 of issue #233 by adding sleep-as-fence detection at `FinalSpecCheck`, upgrading the three reviewer rubrics for zero-assertion + DI-bypass shown-work, promoting Gemini's wrong-target sentence to all reviewers, and fixing the legacy STATUS tail's contradiction with PR #254's inverted contract.

**Architecture:** Three edits to `examples/build_product.dip`, mirrored to `workflows/build_product.dip` via `make sync-workflows`. No Setup changes, no Gap 7 rubric file changes, no parser changes. Total prompt growth ≈ 40 lines. The audit found honest LLM oversight, not adversarial tampering — defense is sized to the observed threat model (per spec §4.5 / §4.6).

**Tech Stack:** Dippin pipeline language (`.dip` files), Go test suite (`go test`), `dippin doctor` / `dippin simulate` validators, `tracker validate`, GNU Make.

**Spec:** `docs/superpowers/specs/2026-05-26-gap8-test-quality-design.md`

**Branch:** `feat/gap8-test-quality` (already created; v5 spec already committed at `c4afc05`)

---

## File Structure

| File | Action | Responsibility |
|------|--------|----------------|
| `examples/build_product.dip` | Modify | Source of truth for the workflow. All four edits land here. |
| `workflows/build_product.dip` | Modify (via `make sync-workflows`) | Mirror; auto-generated from `examples/` — never edit by hand. |
| `CHANGELOG.md` | Modify | Add an `[Unreleased]` entry under `### Changed` documenting the Gap 8 closure. |

No new files. No test files. No Go source changes. No `Setup` heredoc changes. No Gap 7 `iface-reachability-rubric.md` changes.

---

## Task 1: Insert Smell 3 (sleep-as-fence) section into FinalSpecCheck

**Files:**
- Modify: `examples/build_product.dip` (insert between line 1093 end of INTERFACE REACHABILITY and line 1094 start of SPEC.md compliance prose — see exact anchor below)

- [ ] **Step 1: Re-read the current FinalSpecCheck prompt**

Run: `sed -n '1037,1120p' examples/build_product.dip`

Expected: confirm lines 1046-1093 are the INTERFACE REACHABILITY section (ends with the regression-cases recap mentioning I9 / I10), line 1095 begins with `This is the final gate. Read SPEC.md line by line.`, and lines 1115-1119 hold the legacy STATUS tail that Task 4 will rewrite.

- [ ] **Step 2: Identify the exact insertion anchor**

The Smell 3 block goes BETWEEN the INTERFACE REACHABILITY regression-cases recap (ending around line 1093 with `Both shipped green because this check didn't exist.`) and the SPEC.md compliance prose (starting around line 1095 with `This is the final gate. Read SPEC.md line by line.`).

Find the exact anchor text using:

```bash
grep -n "Both shipped green because this check didn't exist." examples/build_product.dip
grep -n 'This is the final gate' examples/build_product.dip
```

Expected: two single-hit lines confirming the boundary. The Edit tool will use the `Both shipped green ... check didn't exist.` line as the `old_string` end-anchor.

- [ ] **Step 3: Insert the Smell 3 section**

Use the Edit tool with the exact existing `old_string` covering the boundary, replacing with the same text plus the new Smell 3 block inserted before `This is the final gate`.

The Smell 3 block to insert (this is the literal prompt text the agent will read at run time; preserve all indentation as 6-space leading prose per the existing `prompt:` block style at line 1045):

```
      TEST QUALITY — sleep-as-fence (issue #233 Gap 8 W17):

      Grep test files for sleep-class calls:
        Go:        grep -rnE '(time\.Sleep|<-time\.After)\(' \
                     --include='*_test.go' .
        Python:    grep -rnE '(time|asyncio|trio|anyio|gevent)\.sleep\(' \
                     --include='test_*.py' --include='*_test.py' .
        JS/TS:     grep -rnE '(await sleep\(|setTimeout\(|waitForTimeout\(|cy\.wait\()' \
                     --include='*.test.*' --include='*.spec.*' --include='*.cy.*' .
        Rust:      git grep -nE '(thread::sleep|tokio::time::sleep|async_std::task::sleep)' -- '*.rs'
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
      caught by reviewer rubric point 3 upgrade (also in this PR) —
      not detected here. W21 (subname collision) is out of scope.

```

(Trailing blank line is intentional so the existing `This is the final gate.` line keeps a blank line before it — matches the existing block separation between INTERFACE REACHABILITY and SPEC.md compliance.)

- [ ] **Step 4: Verify the insertion landed correctly**

Run: `sed -n '1090,1140p' examples/build_product.dip`

Expected: see the existing iface-reachability recap, then the new TEST QUALITY block beginning `TEST QUALITY — sleep-as-fence`, then the original `This is the final gate. Read SPEC.md line by line.` line.

Run: `grep -c 'TEST QUALITY — sleep-as-fence' examples/build_product.dip`

Expected: `1`

- [ ] **Step 5: Commit the FinalSpecCheck Smell 3 insertion**

Run:

```bash
git add examples/build_product.dip
git commit -m "feat(build_product): add sleep-as-fence detection at FinalSpecCheck (#233 Gap 8)

Insert TEST QUALITY section into FinalSpecCheck between INTERFACE
REACHABILITY and SPEC.md compliance. Greps test files for
sleep-class calls (Go time.Sleep / <-time.After, Python
time/asyncio/trio/anyio/gevent .sleep, JS/TS sleep / setTimeout /
waitForTimeout / cy.wait, Rust thread::sleep / tokio::time::sleep,
Ruby sleep / Kernel.sleep, Java/Kotlin Thread.sleep / delay /
Mono.delay). Each hit needs disposition: (a) cite SPEC.md timing
contract, (b) cite deterministic primitive that replaced sleep,
or (c) waiver per .ai/decisions/*.md naming the test with
smell-specific rationale citing a SPEC.md section. Blanket
'intentional timing test' rationales FAIL.

Covers audit case W17 (TestLoop_BusyDrops uses three time.Sleep
calls). W4/W5/W13 are caught at reviewer layer in subsequent
commits; W21 is explicit non-goal (golangci-lint territory).

Per spec docs/superpowers/specs/2026-05-26-gap8-test-quality-design.md §5.1.
"
```

Verify: `git log -1 --stat` shows `examples/build_product.dip` modified, ~32 insertions, 0 deletions.

---

## Task 2: Promote Gemini's stdlib-only sentence to ReviewClaude and ReviewCodex

**Files:**
- Modify: `examples/build_product.dip:826-832` (ReviewClaude point 3)
- Modify: `examples/build_product.dip:873-878` (ReviewCodex point 3)

The exact sentence currently lives only in ReviewGemini at lines 936-938:

```
are FAIL. Tests that only validate standard-library or
         third-party-library behavior instead of the project's own
         logic are FAIL.
```

This task copies that sentence into the other two reviewers' point 3.

- [ ] **Step 1: Locate ReviewClaude's point 3 end anchor**

Run: `sed -n '826,832p' examples/build_product.dip`

Expected output (the literal closing lines of ReviewClaude point 3):

```
      3. TEST VERIFIES CONTRACT: For each assertion in a test under this
         section, does it verify the spec's contract, or does it mirror
         what the implementation happens to produce? Snapshot tests
         regenerated from current output are FAIL. Tests asserting
         `attempts == 2` when the spec says "max 2 retries" (= 3 attempts)
         are FAIL.
      4. SCOPE: Was anything implemented beyond what this section asks
```

The change appends a new sentence to the existing point 3, before line `      4. SCOPE:`.

- [ ] **Step 2: Edit ReviewClaude point 3**

Replace this exact text:

```
         `attempts == 2` when the spec says "max 2 retries" (= 3 attempts)
         are FAIL.
      4. SCOPE: Was anything implemented beyond what this section asks
```

…in the ReviewClaude prompt (the FIRST occurrence — confirm by reading lines 800-845 first if needed), with:

```
         `attempts == 2` when the spec says "max 2 retries" (= 3 attempts)
         are FAIL. Tests that only validate standard-library or
         third-party-library behavior instead of the project's own
         logic are FAIL.
      4. SCOPE: Was anything implemented beyond what this section asks
```

Note: this text appears in all three reviewer prompts; use Edit with `replace_all: false` and a larger `old_string` if needed to disambiguate ReviewClaude's occurrence (e.g., include the `Write the report to .ai/build/review-claude.md.` anchor below).

- [ ] **Step 3: Edit ReviewCodex point 3**

Locate ReviewCodex's identical block at lines 873-878 and apply the same insertion. Use a disambiguating anchor (e.g., `Write the report to .ai/build/review-codex.md.`) in the Edit call.

- [ ] **Step 4: Verify both promotions landed**

Run: `grep -c "Tests that only validate standard-library" examples/build_product.dip`

Expected: `3` (was `1` — now appears in all three reviewer prompts).

- [ ] **Step 5: Verify line counts**

Run: `wc -l examples/build_product.dip`

Expected: increased by 6 lines (3 lines per reviewer × 2 reviewers) vs the pre-Task-2 count.

- [ ] **Step 6: Commit**

```bash
git add examples/build_product.dip
git commit -m "feat(build_product): promote 'stdlib-only tests FAIL' to all 3 reviewers (#233 Gap 8 W5)

The sentence 'Tests that only validate standard-library or
third-party-library behavior instead of the project's own logic
are FAIL' existed only in ReviewGemini point 3. Promote to
ReviewClaude and ReviewCodex point 3 so all three reviewers
catch the wrong-target audit case (W5: TestRun_SignalHandling
tests stdlib signal.NotifyContext instead of daemon handler).

Per spec docs/superpowers/specs/2026-05-26-gap8-test-quality-design.md §5.2.
"
```

---

## Task 3: Append shown-work upgrade to all three reviewers' point 3

**Files:**
- Modify: `examples/build_product.dip` — ReviewClaude (line ~832 post-Task-2), ReviewCodex (line ~880 post-Task-2), ReviewGemini (line ~938)

The text to append after each reviewer's point 3 closing sentence (which after Task 2 is `... third-party-library behavior instead of the project's own logic are FAIL.`):

```
         Also: for each test file under this section, show grep
         evidence that the tests do NOT exhibit (a) zero-assertion
         bodies — test functions with no t.Error/t.Fatal/require/
         assert/expect/should/panic/etc. calls; or (b) DI bypass —
         tests calling time.Now, rand.Read, stdlib-IO when the
         production code defines a Clock/Random/IO seam. Cite the
         grep command and its output for each hit AND for the
         empty-result case. Audit any `.ai/decisions/*.md` waivers
         referenced — flag rationales whose logic, if applied
         broadly, would void the smell check, and verify cited
         SPEC.md sections actually contain relevant content.
```

- [ ] **Step 1: Apply the append to ReviewClaude**

Edit `examples/build_product.dip`. Find the unique ReviewClaude anchor: the closing `are FAIL.` line of point 3 immediately preceding `      4. SCOPE:`, in the block that ends with `Write the report to .ai/build/review-claude.md.` a few lines below.

Replace:

```
         third-party-library behavior instead of the project's own
         logic are FAIL.
      4. SCOPE: Was anything implemented beyond what this section asks
```

(in the ReviewClaude prompt — use the `review-claude.md` anchor below to disambiguate) with:

```
         third-party-library behavior instead of the project's own
         logic are FAIL.
         Also: for each test file under this section, show grep
         evidence that the tests do NOT exhibit (a) zero-assertion
         bodies — test functions with no t.Error/t.Fatal/require/
         assert/expect/should/panic/etc. calls; or (b) DI bypass —
         tests calling time.Now, rand.Read, stdlib-IO when the
         production code defines a Clock/Random/IO seam. Cite the
         grep command and its output for each hit AND for the
         empty-result case. Audit any `.ai/decisions/*.md` waivers
         referenced — flag rationales whose logic, if applied
         broadly, would void the smell check, and verify cited
         SPEC.md sections actually contain relevant content.
      4. SCOPE: Was anything implemented beyond what this section asks
```

- [ ] **Step 2: Apply the same append to ReviewCodex**

Use the `review-codex.md` anchor below to disambiguate.

- [ ] **Step 3: Apply the same append to ReviewGemini**

Use the `review-gemini.md` anchor below to disambiguate.

- [ ] **Step 4: Verify three appends landed**

Run: `grep -c "Also: for each test file under this section, show grep" examples/build_product.dip`

Expected: `3`

Run: `grep -c "verify cited" examples/build_product.dip`

Expected: `3`

- [ ] **Step 5: Sanity-check the structure of each reviewer's point 3 → point 4 boundary**

Run: `grep -B1 -A1 '4\. SCOPE' examples/build_product.dip | head -30`

Expected: three occurrences of `4. SCOPE`, each preceded by a line ending with `SPEC.md sections actually contain relevant content.`.

- [ ] **Step 6: Commit**

```bash
git add examples/build_product.dip
git commit -m "feat(build_product): append shown-work for zero-assertion + DI-bypass to all 3 reviewers (#233 Gap 8 W4/W13)

Each reviewer's point 3 (TEST VERIFIES CONTRACT) now demands grep
evidence for two semantic smells: (a) zero-assertion test
functions, (b) DI bypass (time.Now / rand.Read / stdlib-IO when
production code defines a Clock/Random/IO seam). Plus a waiver
audit step: flag .ai/decisions/*.md rationales whose logic would
broadly void the smell check, and verify cited SPEC.md sections
contain relevant content (sophisticated waivers can name a real
section that doesn't actually relate).

Covers audit cases W4 (goblin.start zero-assertion) and W13
(time.Now bypass when Clock seam exists). PR #249's shown-work
standard for point 1 (SPEC LITERALS) is now applied to point 3
for the two semantic smells where grep was unworkable at
FinalSpecCheck (semantic judgment is reviewer's job).

Per spec docs/superpowers/specs/2026-05-26-gap8-test-quality-design.md §5.2.
"
```

---

## Task 4: Rewrite the legacy STATUS tail at FinalSpecCheck lines 1115-1119

**Files:**
- Modify: `examples/build_product.dip:1115-1119` (post-prior-tasks line numbers will shift; locate by content)

The legacy tail currently reads (per the spec's §5.3 baseline reference):

```
      If fully compliant: STATUS:success
      If not: emit STATUS:fail on its own line, followed by the
      specific list of gaps on subsequent lines (the parser requires
      the STATUS value to be exactly `success`, `fail`, or `retry` —
      no trailing prose on the same line).
```

This contradicts PR #254's inverted contract at lines 1048-1059. v5 replaces it with:

```
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

- [ ] **Step 1: Locate the legacy tail by content**

Run: `grep -n 'If fully compliant: STATUS:success' examples/build_product.dip`

Expected: one hit, around line 1147 (line number shifted by Task 1's insertion of ~32 lines).

- [ ] **Step 2: Replace the tail**

Edit `examples/build_product.dip`. Replace this exact block:

```
      If fully compliant: STATUS:success
      If not: emit STATUS:fail on its own line, followed by the
      specific list of gaps on subsequent lines (the parser requires
      the STATUS value to be exactly `success`, `fail`, or `retry` —
      no trailing prose on the same line).
```

with:

```
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

- [ ] **Step 3: Verify the rewrite**

Run: `grep -c 'If fully compliant: STATUS:success' examples/build_product.dip`

Expected: `0`

Run: `grep -c 'Emit \`STATUS:success\` as your terminal line ONLY if INTERFACE' examples/build_product.dip`

Expected: `1`

- [ ] **Step 4: Confirm the inverted STATUS contract is now coherent**

Run: `sed -n '1046,1060p' examples/build_product.dip`

Expected: the existing PR #254 inverted contract block (lines 1048-1059 baseline) is unchanged — opens with `STATUS contract — emit STATUS:fail as the FIRST line of your response`.

Run: `grep -n 'STATUS:success' examples/build_product.dip | head -20`

Expected: STATUS:success appears in (a) the inverted contract block at line ~1052 ("emit a final `STATUS:success` line"), (b) the new tail rewrite. No "If fully compliant: STATUS:success" lazy framing anywhere.

- [ ] **Step 5: Commit**

```bash
git add examples/build_product.dip
git commit -m "fix(build_product): align FinalSpecCheck STATUS tail with PR #254 inverted contract (#233 Gap 8)

Lines 1115-1119 carried the pre-PR-#254 legacy STATUS framing:
'If fully compliant: STATUS:success / If not: emit STATUS:fail'.
Under parseAutoStatus's last-line-wins semantics, this could
override the inverted contract's early STATUS:fail when a passing
SPEC.md section was reached mid-survey but other sections still
needed checking.

Rewrite: terminal STATUS:success requires ALL three sections
(INTERFACE REACHABILITY, TEST QUALITY, SPEC.md compliance) to
pass; otherwise the early STATUS:fail from the first line stays
in place. Refers to the existing allowlist enumeration at
lines 1103-1108 (single source of truth) rather than
re-enumerating filenames inline.

Per spec docs/superpowers/specs/2026-05-26-gap8-test-quality-design.md §5.3.
"
```

---

## Task 5: Sync workflows and run verification gates

**Files:**
- Modify: `workflows/build_product.dip` (auto-generated by `make sync-workflows`)

- [ ] **Step 1: Run sync-workflows**

Run: `make sync-workflows`

Expected: command succeeds; `workflows/build_product.dip` is regenerated from `examples/build_product.dip`. Examine `git diff workflows/build_product.dip` to confirm the four edits mirrored cleanly.

- [ ] **Step 2: Run dippin doctor**

Run: `dippin doctor examples/build_product.dip`

Expected: output ends with `Grade: A` (or `A/100`); zero new warnings. If `dippin doctor` reports a warning that wasn't present pre-change, do NOT proceed — investigate which edit triggered it and fix.

- [ ] **Step 3: Run dippin simulate -all-paths**

Run: `dippin simulate -all-paths examples/build_product.dip`

Expected: every path terminates; no infinite-loop detection; same path count as pre-change (the spec doesn't change graph topology).

- [ ] **Step 4: Run tracker validate**

Run: `tracker validate examples/build_product.dip`

Expected: clean output, exit 0.

- [ ] **Step 5: Run Go build + tests**

Run: `go build ./...`

Expected: clean, exit 0.

Run: `go test ./... -short`

Expected: all 17 packages pass.

- [ ] **Step 6: Run check-workflows**

Run: `make check-workflows`

Expected: clean, exit 0. (Confirms `examples/` and `workflows/` are in sync — the previous `make sync-workflows` should have left both consistent.)

- [ ] **Step 7: Commit the synced workflow**

```bash
git add workflows/build_product.dip
git commit -m "chore: sync workflows/build_product.dip to examples/ (#233 Gap 8)
"
```

Verify: `git status` reports `nothing to commit, working tree clean`.

- [ ] **Step 8: Sanity-check the verification gates again from a clean tree**

This is paranoia, not duplicate work — guards against the case where some gate quietly cached pre-change state:

```bash
go build ./... && go test ./... -short && \
  dippin doctor examples/build_product.dip && \
  dippin simulate -all-paths examples/build_product.dip && \
  tracker validate examples/build_product.dip && \
  make check-workflows && \
  echo "ALL GATES GREEN"
```

Expected: ends with `ALL GATES GREEN`. If any gate fails, fix the root cause; do NOT proceed to Task 6.

---

## Task 6: Update CHANGELOG.md and final commit

**Files:**
- Modify: `CHANGELOG.md` — add a new entry to the existing `## [Unreleased]` section under `### Changed`, after the Gap 7 entry that's already there.

- [ ] **Step 1: Verify CHANGELOG.md current structure**

Run: `sed -n '1,20p' CHANGELOG.md`

Expected: `## [Unreleased]` heading at line 8, `### Changed` at line 10, and a Gap 7 bullet immediately below.

- [ ] **Step 2: Add the Gap 8 entry**

Insert this entry into `CHANGELOG.md`, as a new bullet IMMEDIATELY AFTER the Gap 7 entry inside `## [Unreleased] / ### Changed` (so Gap 8 appears below Gap 7 in the Unreleased section):

```markdown
- **`build_product` workflow: closed Gap 8 from the #233 audit (test-quality smells)** ([#233](https://github.com/2389-research/tracker/issues/233)). The audit caught five test-quality regressions that shipped green when the same agent wrote impl and tests: zero-assertion (`goblin.start` test logs an empty SHA without asserting on it — W4), wrong-target (`TestRun_SignalHandling` tests stdlib `signal.NotifyContext` not the daemon's handler — W5), DI bypass (tests call `time.Now()` directly when a `Clock` seam exists — W13), sleep-as-fence (`TestLoop_BusyDrops` uses three `time.Sleep` calls between phases — W17), subname collision (`bytes_trimSpace` shadows `bytes.TrimSpace` under Go's subtest case-fold — W21). The audit's failure mode was reviewers handwaving past prose-only checks; v5 mitigates by adding shown-work demands at the layer that catches each smell most reliably:
  - **`FinalSpecCheck` grows a TEST QUALITY section for sleep-as-fence only** — one new ~30-line block between INTERFACE REACHABILITY (Gap 7) and SPEC.md compliance. Per-language sleep-class greps (Go `time.Sleep` / `<-time.After`, Python `(time|asyncio|trio|anyio|gevent).sleep`, JS/TS `await sleep` / `setTimeout` / `waitForTimeout` / `cy.wait`, Rust `thread::sleep` / `tokio::time::sleep` / `async_std::task::sleep`, Ruby `sleep` / `Kernel.sleep`, Java/Kotlin `Thread.sleep` / `delay` / `Mono.delay`). Each hit needs disposition: (a) the sleep IS the SUT — cite the SPEC.md timing-contract section file:line, OR (b) replaced by a deterministic primitive — cite the primitive's introduction, OR (c) waiver per `.ai/decisions/*.md` naming the test with smell-specific rationale citing a SPEC.md section. "Intentional timing test" without a spec citation is blanket — FAIL. Covers W17.
  - **Reviewer rubric point 3 strengthened across all three reviewers** (`ReviewClaude`, `ReviewCodex`, `ReviewGemini`). Two changes: (1) The Gemini-only sentence "Tests that only validate standard-library or third-party-library behavior instead of the project's own logic are FAIL" is **promoted to ReviewClaude and ReviewCodex** — covers W5 across all three lanes. (2) Each reviewer's point 3 now demands shown-work grep evidence for two semantic smells the audit specifically found: (a) zero-assertion test bodies, (b) DI bypass to `time.Now` / `rand.Read` / stdlib-IO when production code defines a Clock/Random/IO seam. Cite the grep command and its output for each hit AND for the empty-result case (same shown-work standard as point 1 SPEC LITERALS, per PR #249). Reviewer also audits `.ai/decisions/*.md` waivers and verifies cited SPEC.md sections actually contain content supporting the waiver's rationale (defeats the "cite a real section that doesn't actually relate" forgery shape). Covers W4 and W13.
  - **Legacy `FinalSpecCheck` STATUS tail fixed.** The pre-PR-#254 framing at the bottom of FinalSpecCheck (`If fully compliant: STATUS:success / If not: emit STATUS:fail`) contradicted PR #254's inverted contract opening — under last-line-wins parsing, a passing SPEC.md section reached mid-survey could override the early `STATUS:fail` for INTERFACE REACHABILITY or TEST QUALITY. The tail is rewritten to require all three sections to pass before emitting terminal `STATUS:success`, with single-source-of-truth reference to the existing allowlist at lines 1103-1108.
  - **What's intentionally NOT in this PR.** W21 (Go subtest case-fold collision) is explicit non-goal — Go-specific lexical bug, golangci-lint territory. No chmod+sha tripwire on Gap 7's rubric file. No integrity preamble. No SPEC.md SHA verification. No DI-bypass cross-check folded into Gap 7's rubric heredoc. No inline TEST QUALITY rubric enumerating Smell 1 (zero-assertion) at FinalSpecCheck. The audit found honest LLM oversight, not adversarial mutation — defense is sized to the observed threat model. Three squad-review rounds taught us what to trim; v5 is ~40 prompt lines (vs ~330 in v1, ~165-200 in earlier drafts).
  - Spec: `docs/superpowers/specs/2026-05-26-gap8-test-quality-design.md` (v5). Plan: `docs/superpowers/plans/2026-05-26-gap8-test-quality-plan.md`.
  - With Gap 8 landed, seven of eight #233 audit gaps are closed (1, 2, 3, 4, 6, 7, 8). Gap 5 (engine-level `auto_status` audit + `OutcomeHumanOverride` + re-run `FinalSpecCheck` after `ApplyReviewFixes`) remains as the final Chunk C work before `release: v0.31.0` closes out #233.
```

- [ ] **Step 3: Verify CHANGELOG structure is intact**

Run: `sed -n '1,30p' CHANGELOG.md`

Expected: `## [Unreleased]` heading still present, Gap 7 entry still present above the new Gap 8 entry, no other sections disturbed.

Run: `grep -n '^## \[' CHANGELOG.md | head -5`

Expected: `## [Unreleased]` is the first heading; `## [0.30.0]` is the next heading below.

- [ ] **Step 4: Re-run verification gates**

Run:

```bash
go build ./... && go test ./... -short && \
  dippin doctor examples/build_product.dip && \
  make check-workflows && \
  echo "ALL GATES GREEN"
```

Expected: ends with `ALL GATES GREEN`.

(Skipping `dippin simulate -all-paths` and `tracker validate` since CHANGELOG.md changes can't affect them — but if Task 5's commit boundary was crossed, run them too.)

- [ ] **Step 5: Final commit**

```bash
git add CHANGELOG.md
git commit -m "docs(changelog): add Gap 8 entry to [Unreleased] (#233)

Documents the FinalSpecCheck sleep-as-fence section, the
three-reviewer point 3 upgrade for zero-assertion + DI-bypass +
wrong-target, and the legacy STATUS tail fix. Covers audit cases
W4/W5/W13/W17; W21 is explicit non-goal. Notes the deliberate
omissions vs earlier drafts (no chmod+sha, no integrity preamble,
no SPEC.md SHA, no DI-bypass fold-in) sized to the audit's
observed threat model (honest LLM oversight, not adversarial
mutation).

Spec: docs/superpowers/specs/2026-05-26-gap8-test-quality-design.md
Plan: docs/superpowers/plans/2026-05-26-gap8-test-quality-plan.md
"
```

- [ ] **Step 6: Verify branch state**

Run: `git log --oneline main..HEAD | head -10`

Expected (in commit order from oldest to newest, top of list is newest):

```
<sha> docs(changelog): add Gap 8 entry to [Unreleased] (#233)
<sha> chore: sync workflows/build_product.dip to examples/ (#233 Gap 8)
<sha> fix(build_product): align FinalSpecCheck STATUS tail with PR #254 inverted contract (#233 Gap 8)
<sha> feat(build_product): append shown-work for zero-assertion + DI-bypass to all 3 reviewers (#233 Gap 8 W4/W13)
<sha> feat(build_product): promote 'stdlib-only tests FAIL' to all 3 reviewers (#233 Gap 8 W5)
<sha> feat(build_product): add sleep-as-fence detection at FinalSpecCheck (#233 Gap 8)
<sha> docs: simplify Gap 8 spec to v5 per user feedback
<sha> docs: redesign Gap 8 spec to v3 post-second-squad-review
<sha> docs: redesign Gap 8 test-quality spec to v2 post-squad-review
<sha> docs: design v1 for Gap 8 test-quality check (#233)
```

(Implementation commits: 6 to land Tasks 1-6 plus the prior 4 spec commits.)

Run: `git status`

Expected: `nothing to commit, working tree clean`.

---

## Self-Review Pass

The plan covers every concrete edit named in v5's §5:

| Spec § | Plan Task |
|--------|-----------|
| §5.1 Smell 3 insert | Task 1 |
| §5.2 Reviewer point 3 promotion + append | Task 2 (Gemini sentence promotion), Task 3 (shown-work append) |
| §5.3 Legacy STATUS tail rewrite | Task 4 |
| §5.4 Setup unchanged | N/A — no task |
| §5.5 Gap 7 rubric unchanged | N/A — no task |
| §5.6 VerifyMilestone unchanged | N/A — no task |
| §6 Build & verification | Task 5 (gates), Task 6 (CHANGELOG) |

No "TBD" / "TODO" / "implement later" placeholders. Every code/text block is the literal content to insert. Every command is the literal command to run with its expected output. Steps are 2-5 minutes apiece. Task boundaries match commit boundaries.
