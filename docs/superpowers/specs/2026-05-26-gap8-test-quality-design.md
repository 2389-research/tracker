# Gap 8 — Test Quality (Issue #233)

**Status:** Design v3 (post-second-squad-review)

**Author:** Claude (Opus 4.7) + Clint Ecker

**Date:** 2026-05-26

**Closes:** Gap 8 of [#233](https://github.com/2389-research/tracker/issues/233) — `build_product` workflow audit recap.

---

## 1. Problem

The original `build_product` audit identified test-quality smells that ship green when the same agent writes implementation and tests. The five named regression cases from the code-goblin run (issue #233 Appendix A):

| # | Audit case | Smell archetype |
|---|------------|-----------------|
| W4 | `goblin.start` test logs an empty SHA value without asserting on it | zero-assertion |
| W5 | `TestRun_SignalHandling` tests stdlib `signal.NotifyContext` instead of the daemon's actual handler | wrong-target |
| W13 | tests use `time.Now()` directly when a `Clock` seam exists | DI bypass |
| W17 | `TestLoop_BusyDrops` uses three `time.Sleep` calls as phase fences | sleep-as-fence |
| W21 | `bytes_trimSpace` declared alongside `bytes.TrimSpace` — Go subtest case-fold collision | subname collision |

PR #249's reviewer rubric covers test-quality in three lines of prose. The audit shows reviewers handwave past it the same way they handwaved past interface reachability before PR #254 added shown-work discipline.

## 2. Goals

- Add detection at `FinalSpecCheck` for two smells (zero-assertion, sleep-as-fence) where grep produces shown-work evidence — covering W4 and W17.
- Fold DI-bypass (W13) into Gap 7's existing interface enumeration as a cross-check, eliminating duplicate enumeration work and the Smell-6 "duck-typed Python" self-contradiction v2 had.
- Cover W5 (wrong-target) by **promoting** Gemini reviewer's existing lane-specific sentence to all three reviewers — no new rubric content.
- Acknowledge W21 (subname collision) as out of scope — Go-specific lexical check, golangci-lint territory.
- Add cross-cutting safety hardening that the Gap 8 squad review surfaced but applies to all FinalSpecCheck gates: `chmod 0444` + SHA check on the Gap 7 rubric (defends against mid-run rewriting), and SHA on SPEC.md at run start (defends against mid-run prompt injection for both Gap 7 and Gap 8 disposition citations).
- Keep the discipline small — v3 trims further from v2 (~80 inline lines → ~55) per the second-round squad review's YAGNI findings. v1 was ~330 lines, v2 was ~165 lines, v3 lands at ~95 prompt-and-helper lines.
- Compose cleanly with PR #246's `Setup`-writes-helper pattern (extended with chmod+sha hardening), PR #249's balanced 5-point reviewer rubric, and PR #254's inverted STATUS contract.

## 3. Non-goals

- Replacing a real test-quality static analyzer (`golangci-lint`, `eslint`, `clippy`).
- **W21 (subname collision)** — Go-specific subtest normalizer bug. golangci-lint territory; a separate follow-up issue handles it.
- **Wrong-target detection via grep heuristics.** v1 import-ratio approach was unworkable; v2 dropped it; v3 keeps the v2 mitigation (promote Gemini's existing sentence to all three reviewers).
- **Per-test contract verification at FinalSpecCheck.** `VerifyMilestone` point 4 (Gap 4, PR #249) already does this semantically per-milestone.
- **A shared rubric file for Gap 8.** v2 inlined into FinalSpecCheck for tamper-resistance; v3 takes the YAGNI position further by folding DI-bypass into Gap 7's existing iface section, leaving only ~30 lines for the two remaining smells.
- **Engine-level parser changes** to defend against fenced/tabled/commented findings. Parser is contract-locked. v3 mitigates via tightened prompt instruction; §10 records the parser-level fix as a follow-up.
- **A named-test spot-check.** v2 had one (§4.8); the second squad review found it weak (Architect: project-specific; YAGNI: window dressing; Red-Team: brittle to rename). v3 drops it; the audit's named regressions (W4/W5/W17/W13) are caught by their respective grep/prose mitigations instead.
- The 38 concrete audit findings in #233 Appendix A. They inform the rubric, not separate work items.

## 4. Design

### 4.1 Single owner: `FinalSpecCheck` (unchanged from v2)

Test-quality (Smells 1 and 3) lives in `FinalSpecCheck` alongside Gap 7's iface-reachability section. DI-bypass (Smell 6 of v2) is folded into Gap 7's existing iface enumeration — see §4.5. `VerifyMilestone` is unchanged.

### 4.2 Inline rubric — survey-first per Gap 7 v3 pattern

Gap 7's `.ai/build/iface-reachability-rubric.md` is left in place but **hardened** (see §4.7). Gap 8's two-smell test-quality rubric is **inlined into FinalSpecCheck** (no new shared file) and opens with a Gap-7-v3-style language survey: detect test-file presence holistically, skip vacuously if none, then apply per-language patterns or the named-framework fallback principle for unenumerated languages.

Total Gap 8 prompt-and-helper budget (vs. v1's ~330 and v2's ~165):

- FinalSpecCheck TEST QUALITY section (inlined): ~30 lines for Smells 1 and 3 with survey-first detection.
- Cross-check append to Gap 7's iface section: ~5 lines for DI-bypass.
- FinalSpecCheck legacy STATUS tail rewrite: ~10 lines (same as v2).
- Reviewer rubric point 3 upgrade: 5 lines × 3 reviewers = 15 lines.
- Setup hardening (chmod + SHA for Gap 7 rubric AND SPEC.md): ~10 lines.
- Honest measured count: ~70 prompt lines + ~10 setup-script lines.

This pulls the spec back below Gap 7's prompt footprint while picking up cross-cutting Gap 7 safety hardening the second-round squad surfaced.

### 4.3 Inverted STATUS contract — shared, with tightened bypass mitigation

The PR #254 inverted contract stays. v3 inherits v2's prompt-level defenses against rubber-stamping (shown-work for skips — see §4.6) and extends the code-fence mitigation:

> "STATUS lines and finding bullets MUST live in plain prose with a leading `-` or `*` bullet marker, flush left, NOT inside: triple-backtick code fences, single-backtick inline code, markdown tables, markdown blockquotes (`>` prefix), task-list markers (`- [ ]`), HTML-like comments (`<!-- ... -->`), or any 'Note:' / 'Aside:' / 'Caveat:' prose prefix. The parser scans only line-leading STATUS markers and only outside fenced code; the wrapping list above defeats parser detection AND violates this rubric's discipline."

This expands v2's prompt instruction to cover the seven bypass shapes the second-round Red-Team review identified. The defense remains prompt-level; engine-level enforcement is filed as follow-up (§10 q4).

### 4.4 Legacy STATUS contract debt at FinalSpecCheck lines 1115-1119 (unchanged from v2)

v2's §5.2 rewrite — replacing the legacy "If fully compliant: STATUS:success" tail block — is preserved in v3 with one fix: the rewrite refers to the canonical allowlist at lines 1103-1108 rather than re-enumerating filenames inline, eliminating the drift risk Architect identified.

### 4.5 Two smells inline, DI-bypass folded into Gap 7

#### Smell 1 — Zero-assertion tests (audit case W4)

A test function with no assertion-class call. Survives any execution that doesn't panic; bug is masked. v3's detection follows Gap 7 v3's principle-not-enumeration pattern:

1. **Survey:** detect test-file presence: `git ls-files | grep -E '(_test\.|test_|/test/|/tests/|/spec/|\.spec\.|\.test\.|\.cy\.|_spec\.)' || true`. If empty, also check for Rust inline tests: `git grep -l '#\[cfg(test)\]' -- '*.rs' || true`. If both empty: skip with one-line note "no test files detected; Smell 1 vacuous."
2. **Enumerate test functions per language.** Per-language detection patterns are listed once in §5.1 (single canonical location — no design/concrete-edits drift). For any framework not enumerated: name the framework, name the equivalent assertion-class call shapes, and proceed (or skip if undetectable, citing the framework by name).
3. **Verify each test body contains at least one assertion-class call.** A test calling only `t.Skip` is still zero-assertion. False-positive guards: tests intentionally panicking via `mustX` patterns (cite the panic site); tests asserting via shared helpers (cite the helper file:line); property-based tests (cite the property function).
4. **Disposition:** hits → FAIL unless `.ai/decisions/*.md` documents this specific test with smell-specific rationale citing a SPEC.md section (see §4.6).

#### Smell 3 — Sleep-as-fence (audit case W17)

A test using sleep-class calls as inter-phase synchronization. Survey-first per §5.1; per-language sleep-class grep patterns (Go: `time.Sleep`; Python: `(time|asyncio|trio|anyio|gevent).sleep`; JS/TS: `await sleep(`, `setTimeout(`, `new Promise.*setTimeout`, `waitForTimeout(`, `cy.wait(`; Ruby: `(^|[^[:alnum:]_])(sleep[[:space:]]+[0-9]|Kernel\.sleep|EM\.add_timer)`; Rust: `(thread::sleep|tokio::time::sleep|async_std::task::sleep|futures_timer::Delay)`; Java/Kotlin: `(Thread\.sleep|delay\(|Mono\.delay|Observable\.timer)`).

**Also add `time.After(` to the Go pattern** — same fence shape as `time.Sleep`, second-round Red-Team finding.

**Principle for unenumerated frameworks** (carried into Smells 1 and 3 alike): "if your test framework's sleep mechanism isn't enumerated here, name it and the equivalent call shapes; if undetectable in this language, skip with one-line note naming the framework."

Disposition for each hit: (a) the sleep IS the SUT — cite the SPEC.md section calling out the timing contract, file:line; OR (b) replaced by deterministic primitive (cite the primitive's introduction); OR (c) documented waiver per `.ai/decisions/*.md`. "Intentional timing test" without a spec-section citation is blanket and FAIL.

**SPEC.md citation defense:** since disposition (a) requires citing SPEC.md, and an attacker could mid-run mutate SPEC.md to manufacture timing-contract sections, v3 hardens this by writing `.ai/build/spec-original.sha256` at Setup (§4.7). Any disposition (a) citation must point to a section whose content is in the SHA-verified original SPEC.md.

#### DI bypass (audit case W13) — folded into Gap 7's iface section

v2's Smell 6 ran its own two-step interface enumeration. The second-round YAGNI review observed this duplicates Gap 7's existing iface enumeration verbatim. v3 instead **appends a ~5-line DI-bypass cross-check to the Gap 7 enumeration prompt**:

> "For each interface you enumerate, if its method set includes any of `Now() time.Time` / `Random()` / `Read([]byte)` / `Sleep(time.Duration)` / equivalents in the project's language (`time_now` / `random_bytes` / `read` in Python; `now()` / `next_u32()` in Rust; etc.), also grep test files for direct calls to the bypassed stdlib symbols (`time.Now(` / `rand.Read(` / `os.Stdin` / `os.Stdout` / language equivalents). Each test-file hit needs disposition: (a) test exercises the seam itself — cite the seam-implementation file:line; OR (b) test passes a mock through the seam — cite the constructor call; OR (c) waiver per `.ai/decisions/*.md`. Bypass with neither citation nor waiver is FAIL."

This is ~5 prompt lines vs. v2's ~30. It works because:

1. **No duplicate enumeration.** Gap 7 already enumerates every interface in the repo. Gap 8's W13 detection just looks at the method sets Gap 7 already found.
2. **No fail-open for duck-typed languages.** v2's standalone Smell 6 had a self-contradictory carve-out (line 147 caught duck-typed Python; line 163 said skip it). v3 eliminates the conflict — for languages without static interfaces (Ruby, plain JS, duck-typed Python), Gap 7's enumeration step already skips vacuously, so the DI-bypass cross-check inherits that skip cleanly with no additional contradiction.
3. **Tamper-resistance.** The cross-check lives in Gap 7's rubric file, which v3 hardens via chmod+sha (§4.7) — so the cross-check is protected by the same defense as Gap 7's enumeration.

The cost: Gap 7's prompt grows by ~5 lines. v3's §5 details the concrete edit.

### 4.6 Waiver discipline (preserved from v2, lightly tightened)

Unchanged from v2 §4.6:
- A hit is FAIL unless `.ai/decisions/*.md` documents the specific test with a **smell-specific rationale citing a SPEC.md section** (not just "names the test").
- Reviewers audit `.ai/decisions/*.md` and flag rationales whose logic, if applied broadly, voids the smell check.
- Second-round Red-Team showed that a moderately sophisticated waiver still passes this filter. v3 acknowledges the residual risk in §7 and §10 q2 honestly; the prompt-level defense raises the cost but doesn't break the asymmetry.

**Shown-work-for-skips (preserved from v2 §4.9):** every "Smell N skipped" line must be immediately followed by the actual detection-grep output (even when empty). Lazy agent self-attestation is not acceptable.

**v3 cut from v2's §4.9:** the numbered-hits-and-totals ceremony. Second-round YAGNI review observed no downstream node validates the count — pure paperwork. Dropped.

### 4.7 Cross-cutting Setup hardening — chmod+sha for Gap 7 rubric AND SPEC.md

This is new in v3. Both round-2 Architect and Red-Team converged on the position that Gap 7's `iface-reachability-rubric.md` is writeable mid-run by `Implement`, `FixMilestone`, and `ApplyReviewFixes`. Since v3 folds DI-bypass INTO Gap 7's rubric, the tampering attack surface affects two gates simultaneously.

Setup additionally hardens against the second-round Red-Team finding on SPEC.md prompt injection (an agent mid-run appending a section to SPEC.md to satisfy Smell 3 disposition (a) citation requirements).

After Setup writes `.ai/build/iface-reachability-rubric.md`, it adds three lines:

```
chmod 0444 .ai/build/iface-reachability-rubric.md
sha256sum .ai/build/iface-reachability-rubric.md > .ai/build/.rubric.sha256
sha256sum SPEC.md > .ai/build/.spec-original.sha256
chmod 0444 .ai/build/.rubric.sha256 .ai/build/.spec-original.sha256
```

(SPEC.md itself is left writeable — it's the project's own spec — but the SHA captures its content at run start. FinalSpecCheck reads `.spec-original.sha256` and re-hashes SPEC.md; mismatch → `STATUS:fail` with "SPEC.md modified mid-run" rationale; the agent's run history is auditable.)

FinalSpecCheck's prompt opens with two verification steps prepended to the existing inverted STATUS contract block:

```
INTEGRITY CHECK (precondition for everything below):
  sha256sum -c .ai/build/.rubric.sha256
  echo "$(sha256sum SPEC.md | cut -d' ' -f1) SPEC.md" \
    | diff - .ai/build/.spec-original.sha256

If either check fails → STATUS:fail with rationale "INTEGRITY:
{rubric|spec} modified mid-run." This is the EARLIEST exit; the
inverted STATUS:fail-first contract still applies, but a verified
INTEGRITY check is a precondition for considering any subsequent
section's STATUS:success.
```

This is ~7 lines added to FinalSpecCheck plus ~5 lines added to Setup. The threat is real and the fix is small; deferring it after surfacing it (v2's §7 row 5 choice) is what Architect pushed back on.

Honesty note: `chmod 0444` is a tripwire, not authentication. A determined attacker can `chmod +w` the file, rewrite it, and re-write the SHA file too. The SHA defends against the casual attack (Implement agent rewrites the rubric without rewriting the SHA); a sophisticated attacker who reads the workflow source knows to update both. This matches CLAUDE.md's documented stance on the activity-log integrity work (#213): "sentinel is detection, not authentication." Documented in §7.

### 4.8 Reviewer rubric — fold into point 3, no 6th point

Same as v2 with two corrections from the round-2 reviews:

1. **Promote Gemini's existing "stdlib-only tests are FAIL" sentence** from `examples/build_product.dip:936-938` to ReviewClaude (line 826-832) and ReviewCodex (line 873-878). v2 already did this; v3 keeps it.
2. **Replace v2's "see FinalSpecCheck's TEST QUALITY section" reference** (which round-2 Red-Team flagged as structurally broken — reviewers run BEFORE FinalSpecCheck and cannot see its content) with a 3-line inline summary of the smell list. v3 adds to all three reviewers' point 3:

   ```
   Also: tests under this section must not exhibit (a) zero-assertion
   bodies; (b) sleep-as-fence (`time.Sleep`, `time.After`, `Thread.sleep`,
   etc. between phases without citing a SPEC.md timing contract); or
   (c) DI bypass (tests calling `time.Now`/`rand.Read`/stdlib-IO when
   the production code defines a Clock/Random/IO seam). Show grep
   evidence for each test file in this section. Audit any
   `.ai/decisions/*.md` waivers referenced — flag rationales whose
   logic, if applied broadly, would void the smell check.
   ```

   This is 7 lines per reviewer × 3 = 21 lines, slightly more than v2's 15 but addresses Red-Team's structural break. Point 3 grows from ~6 lines to ~13 — comparable to point 2 (INTERFACE REACHABILITY, ~9 lines after PR #254). PR #249's balance preserved within rounding.

### 4.9 Honest residual risks (preserved from v2 §4.3 + round-2 additions)

v3 carries v2's honesty about rubber-stamping, waiver laundering, and code-fence bypass. Additions from round-2 review:

- **Rename evasion:** the named-test spot-check in v2 was brittle to test rename; v3 drops it. The audit's named tests are caught by Smells 1 and 3's behavior-pattern detection (which is name-agnostic) plus reviewer rubric point 3's promoted wrong-target sentence. Rename-evasion is now a non-attack against v3's specific design.
- **Sophisticated waiver laundering:** a paragraph-length waiver naming a test, citing a real SPEC.md section, with smell-specific rationale, can still pass — by design. The reviewer audit (§4.8) is the second line; both audits can be defeated by a sufficiently elaborate forgery. v3 accepts this as residual risk per §10 q2.

## 5. Concrete edits

Three sections of `examples/build_product.dip` change. `make sync-workflows` mirrors to `workflows/build_product.dip`.

### 5.1 `Setup` — chmod + SHA hardening (new in v3)

After the existing `cat > .ai/build/iface-reachability-rubric.md <<'RUBRIC_EOF'` block ends (line 265), add:

```dip
      # Cross-cutting safety hardening (issue #233 Gap 8 squad review).
      # Make the rubric immutable to mid-run agents; SHA-pin SPEC.md so
      # later "cite SPEC.md §X" dispositions can be verified against the
      # original spec content, not against a mid-run-appended forgery.
      # This is a tripwire (chmod 0444 + SHA), not authentication: a
      # determined attacker can chmod +w and update both files. The
      # SHA defends against the casual attack and makes the
      # sophisticated attack auditable.
      chmod 0444 .ai/build/iface-reachability-rubric.md
      sha256sum .ai/build/iface-reachability-rubric.md \
        > .ai/build/.rubric.sha256
      sha256sum SPEC.md > .ai/build/.spec-original.sha256
      chmod 0444 .ai/build/.rubric.sha256 .ai/build/.spec-original.sha256
```

### 5.2 Gap 7 iface-reachability-rubric.md — add DI-bypass cross-check (new in v3)

Inside the existing `RUBRIC_EOF` heredoc at line 101-265 (Setup's iface rubric write), insert before `RUBRIC_EOF` (around line 264, after "Known limitations" section):

```dip
      ## DI bypass cross-check (issue #233 Gap 8 W13)

      For each interface enumerated above, inspect its method set.
      If it declares any of (language-equivalents in parens):

        Go:         Now() time.Time, Random(), Read([]byte), Sleep(d)
        Python:     def now(self), def random(self), def read(self, n)
        Rust:       fn now() -> Instant, fn next_u32(&mut self)
        TS:         now(): Date, random(): number, read(buf): Promise
        Java/Kotlin: Instant now(), int nextInt(), int read(byte[])
        Ruby:       def now, def random_bytes(n), def read(n)

      then this interface is a "DI seam" and tests must use it, not
      bypass it to the stdlib. Grep test files for direct calls to the
      bypassed stdlib symbols:

        Go:     grep -rnE '(time\.Now|rand\.(Read|Int|Float)|os\.(Stdin|Stdout|Stderr))' \
                  --include='*_test.go' .
        Python: grep -rnE '(datetime\.now\(|time\.time\(|os\.urandom\()' \
                  --include='test_*.py' --include='*_test.py' .
        Rust:   grep -rnE '(SystemTime::now|Instant::now|rand::random)' \
                  -- '*.rs'
        TS:     grep -rnE '(new Date\(\)|Date\.now\()' \
                  --include='*.test.*' --include='*.spec.*' .
        Ruby:   grep -rnE '(Time\.now|DateTime\.now|SecureRandom\.)' \
                  --include='*_test.rb' --include='*_spec.rb' .

      For each hit: disposition (a) test exercises the seam itself
      (cite seam-implementation file:line); (b) test passes a mock
      through the seam (cite constructor call); (c) waiver per
      `.ai/decisions/*.md`. Bypass with neither citation nor waiver
      is FAIL. For unenumerated languages: name the language, name
      the equivalent stdlib symbols; if undetectable in this language
      (duck-typed languages without static interfaces inherit Gap 7's
      vacuous-skip), proceed with no hits.
```

### 5.3 `FinalSpecCheck` — integrity preamble + TEST QUALITY section + STATUS tail rewrite

**Integrity preamble** — insert at the very top of FinalSpecCheck's prompt (before the existing inverted STATUS block at lines 1046-1059):

```dip
      INTEGRITY CHECK (precondition; runs BEFORE the STATUS contract below):
        sha256sum -c .ai/build/.rubric.sha256
        cur=$(sha256sum SPEC.md | cut -d' ' -f1)
        orig=$(cut -d' ' -f1 .ai/build/.spec-original.sha256)
        [ "$cur" = "$orig" ] || echo "SPEC.md modified mid-run: $cur vs $orig"
      If either check reports failure, your FIRST line of response
      MUST be `STATUS:fail` and your final report rationale MUST
      include "INTEGRITY: {rubric|spec} modified mid-run." This is
      the earliest exit; the integrity verdict overrides any
      subsequent section's STATUS:success.
```

**TEST QUALITY section** — insert between the existing INTERFACE REACHABILITY section (ends ~line 1093) and the SPEC.md compliance section (starts ~line 1095):

```dip
      TEST QUALITY (issue #233 Gap 8):

      STATUS lines and finding bullets MUST live in plain prose with
      a leading `-` or `*` bullet marker, flush left, NOT inside:
      triple-backtick code fences, single-backtick inline code,
      markdown tables, markdown blockquotes (`>` prefix), task-list
      markers (`- [ ]`), HTML-like comments (`<!-- ... -->`), or any
      "Note:" / "Aside:" / "Caveat:" prose prefix. The parser scans
      only line-leading STATUS markers outside fenced code; the
      wrapping list above defeats parser detection AND violates this
      rubric's discipline.

      SURVEY: detect test-file presence:
        git ls-files \
          | grep -E '(_test\.|test_|/test/|/tests/|/spec/|\.spec\.|\.test\.|\.cy\.|_spec\.)' \
          || true
        git grep -l '#\[cfg(test)\]' -- '*.rs' || true
      If both return empty: skip TEST QUALITY entirely with one-line
      note "no test files detected; TEST QUALITY vacuous." Paste the
      empty survey output as evidence.

      Otherwise apply Smells 1 and 3 below to each detected language.
      For frameworks not enumerated, name the framework and the
      equivalent assertion/sleep call shapes; if undetectable in
      this language, skip with one-line note naming the framework.

      SMELL 1 — Zero-assertion tests (W4 archetype). For each test
      function, body must contain at least one assertion-class call.
        Go:      grep -rnE 'func Test[A-Z][A-Za-z0-9_]*\(t \*testing\.T\)' \
                   --include='*_test.go' .
                 Body must contain: t.Error, t.Errorf, t.Fatal,
                 t.Fatalf, t.Fail, require., assert., cmp.Diff,
                 reflect.DeepEqual, is., testify. (NOT t.Skip alone.)
        Python:  grep -rnE '^[[:space:]]*def test_' --include='*.py' .
                 Body must contain: assert, self.assert*,
                 pytest.raises, pytest.warns.
        JS/TS:   grep -rnE '(test|it)\(' \
                   --include='*.test.*' --include='*.spec.*' \
                   --include='*.cy.*' .
                 Body must contain: expect(, assert., should., chai.,
                 strict.equal.
                 NOTE: `**/__tests__/**` is BSD/GNU-grep-incompatible;
                 do NOT use it. Files under `__tests__/` dirs are
                 caught by the `_test`/`.test.`/`.spec.` substring
                 match in the SURVEY step.
        Ruby:    grep -rnE '(def test_|it +["'"'"'"]|describe +["'"'"'"])' \
                   --include='*_test.rb' --include='*_spec.rb' .
                 Body must contain: assert, refute, expect(, should,
                 must_.
        Rust:    grep -rnE '#\[test\]' --include='*.rs' .
                 (Catches inline `#[cfg(test)] mod tests` too.)
                 Body must contain: assert!, assert_eq!, assert_ne!,
                 panic!, expect(.
        Java/Kotlin: grep -rnE '@Test' \
                   --include='*.java' --include='*.kt' .
                 Body must contain: assert*, Assertions., Assert.,
                 assertThrows, verify(.

      SMELL 3 — Sleep-as-fence (W17 archetype). Grep test files for
      sleep-class calls; each hit needs disposition.
        Go:      grep -rnE '(time\.Sleep|<-time\.After)\(' \
                   --include='*_test.go' .
        Python:  grep -rnE '(time|asyncio|trio|anyio|gevent)\.sleep\(' \
                   --include='test_*.py' --include='*_test.py' .
        JS/TS:   grep -rnE '(await sleep\(|setTimeout\(|new Promise.*setTimeout|waitForTimeout\(|cy\.wait\()' \
                   --include='*.test.*' --include='*.spec.*' \
                   --include='*.cy.*' .
        Ruby:    grep -rnE '(^|[^[:alnum:]_])(sleep[[:space:]]+[0-9]|Kernel\.sleep|EM\.add_timer)' \
                   --include='*_test.rb' --include='*_spec.rb' .
        Rust:    files=$(git ls-files | grep -E '(_test\.rs|/tests/)')
                 if [ -n "$files" ]; then
                   grep -nE '(thread::sleep|tokio::time::sleep|async_std::task::sleep|futures_timer::Delay)' \
                     $files
                 fi
                 # Also inline tests:
                 git grep -nE '(thread::sleep|tokio::time::sleep)' \
                   -- '*.rs' | grep -v '#\[cfg(test)\]' \
                   | grep -B1 '#\[test\]' || true
        Java/Kotlin:
                 testdirs=$(find . -path '*/src/test/*' -type d 2>/dev/null)
                 if [ -n "$testdirs" ]; then
                   grep -rnE '(Thread\.sleep|delay\(|Mono\.delay|Observable\.timer)' \
                     $testdirs
                 fi

      Each hit needs disposition: (a) the sleep IS the SUT — cite the
      SPEC.md section calling out the timing contract, file:line. The
      cited section must exist in the SHA-verified SPEC.md (integrity
      preamble above); citations to mid-run-appended SPEC.md content
      are detected via the integrity preamble's mismatch check; OR
      (b) replaced by deterministic primitive — cite the primitive's
      introduction; OR (c) waiver per `.ai/decisions/*.md` naming the
      specific test with smell-specific rationale citing a SPEC.md
      section. "Intentional timing test" without a spec-section
      citation is blanket — STILL FAIL.

      Skip-vacuous output discipline: every "Smell N skipped" line
      must be immediately followed by the actual detection-grep output
      (even when empty). Lazy self-attestation ("Smell N: no hits,
      skipped") without evidence is not acceptable.

      Regression coverage: W4 (zero-assertion) caught by Smell 1;
      W17 (sleep-fence) caught by Smell 3; W13 (DI bypass) caught
      by the cross-check appended to Gap 7's iface enumeration above;
      W5 (wrong-target) caught by the cross-reviewer point 3 upgrade
      in this PR. W21 (subname collision) explicitly out of scope —
      Go-specific lexical bug, golangci-lint territory.
```

**Legacy STATUS tail rewrite** — replace lines 1115-1119 with:

```dip
      Write the final compliance report to .ai/decisions/compliance.md

      Emit your terminal `STATUS:success` line ONLY if the integrity
      preamble passed AND all three sections above passed: INTERFACE
      REACHABILITY (every method has a production caller or carve-out;
      DI-bypass cross-check has no unwaived hits), TEST QUALITY
      (every Smell 1/3 hit dispositioned), and SPEC.md compliance
      (every requirement implemented, no extras, no TODO/FIXME/HACK,
      tests pass, no unexpected files in .ai/build/ outside the
      explicit allowlist enumerated above in this section). Any
      failure in any section means the early `STATUS:fail` from the
      first line of your response stays in place — do NOT emit
      `STATUS:success`. The parser requires the STATUS value to be
      exactly `success`, `fail`, or `retry` — no trailing prose on
      the same line; no STATUS lines inside fences/tables/blockquotes/
      task-lists/HTML-comments/single-backtick-wrappers/"Note:"
      prefixes (per the bypass-wrapping list in the TEST QUALITY
      section above).
```

The allowlist reference now points to "this section above" (the unchanged lines 1103-1108 enumeration) — single source of truth, no inline duplication, no drift risk.

### 5.4 Reviewer rubric — point 3 upgrade × 3 prompts

For all three reviewers (ReviewClaude at line 826-832, ReviewCodex at 873-878, ReviewGemini at 931-939):

- **Promote** the Gemini-only sentence at `examples/build_product.dip:936-938` to ReviewClaude and ReviewCodex point 3.
- **Append** to all three reviewers' point 3:

```text
   Also: tests under this section must not exhibit (a) zero-assertion
   bodies; (b) sleep-as-fence (`time.Sleep`, `time.After`,
   `Thread.sleep`, etc. between phases without citing a SPEC.md
   timing contract); or (c) DI bypass (tests calling `time.Now`/
   `rand.Read`/stdlib-IO when the production code defines a
   Clock/Random/IO seam). Show grep evidence for each test file
   in this section. Audit any `.ai/decisions/*.md` waivers
   referenced — flag rationales whose logic, if applied broadly,
   would void the smell check.
```

Total reviewer-prompt growth: ~7 lines × 3 reviewers + ~3 lines × 2 reviewers for the Gemini-sentence promotion = ~27 lines.

### 5.5 `VerifyMilestone` — no change

Unchanged from v2.

## 6. Build and verification

For the implementation PR:

1. Edit `examples/build_product.dip` in these places:
   - Setup: append chmod+sha hardening block (§5.1) after the iface rubric heredoc.
   - Iface rubric heredoc: append DI-bypass cross-check (§5.2) before `RUBRIC_EOF`.
   - FinalSpecCheck: prepend integrity preamble + insert TEST QUALITY section (§5.3) + rewrite legacy STATUS tail.
   - Three reviewers: promote Gemini-sentence + append point 3 lines (§5.4).
2. `make sync-workflows` — stage both files.
3. `dippin doctor examples/build_product.dip` — must remain A grade with no new warnings.
4. `dippin simulate -all-paths examples/build_product.dip` — confirm every path still terminates.
5. `tracker validate examples/build_product.dip` — clean.
6. `go build ./... && go test ./... -short` — all 17 packages.
7. `make check-workflows` — sync confirmed.
8. **Update `CHANGELOG.md`** under the next `[Unreleased]` section.

No new tests in `pipeline/handlers/codergen_test.go` for this PR. Parser-level fence-bypass detection is filed as follow-up (§10 q4).

## 7. Risk register

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| FinalSpecCheck prompt grows to ~145 lines after v3 (integrity preamble + TEST QUALITY + iface section + SPEC.md compliance) | Medium | Low — dippin-lang has no prompt-length warning (verified) | Smaller than v2's projected ~165. Further peel possible if `dippin doctor` ever warns. |
| Agent rubber-stamps the surveys with "all clear" without doing the work | High | High | Two prompt-level defenses: shown-work-for-skips converts skip-honor-system into pasted grep output; the integrity preamble (§4.7) introduces a CHECKABLE precondition that the agent cannot rubber-stamp without producing a verifiable SHA. Residual: a determined agent can fabricate grep output AND fabricate SHA output (the agent's response is the only signal). Detection raises cost; doesn't eliminate. |
| Code-fenced / tabled / commented / single-backtick-wrapped findings bypass the parser (round-2 Red-Team) | Medium | High | §4.3 + §5.3 expanded prompt instruction covers all seven bypass shapes. Honor-system; parser-level fix is §10 q4 follow-up. |
| Implement pre-authors `.ai/decisions/*.md` waivers (asymmetric attack) | Medium | Medium | §4.6 smell-specific-rationale rule + §4.8 reviewer audit. Round-2 Red-Team showed a sophisticated paragraph-length waiver still passes — by design. Two-layer audit raises cost; doesn't eliminate. |
| `.ai/build/iface-reachability-rubric.md` rewritten mid-run by Implement/FixMilestone/ApplyReviewFixes | Medium | High — would compromise BOTH Gap 7 enumeration AND Gap 8 DI-bypass cross-check | §4.7 chmod+sha at Setup; §5.3 integrity preamble verifies before FinalSpecCheck reads the rubric. Tripwire not authentication: sophisticated attacker can chmod+w and update both files; SHA defends against the casual attack. |
| SPEC.md mutated mid-run to manufacture timing-contract citations for Smell 3 disposition (a) | Medium | Medium | §4.7 captures original SPEC.md SHA at Setup; §5.3 integrity preamble verifies. Same tripwire-not-authentication caveat as above. |
| Smell 1 / Smell 3 grep patterns produce false positives on framework-fragmented codebases (RxJava, Reactor virtual-time, Jest fake-timers) | Medium | Low | Disposition (b) covers deterministic primitives: cite the primitive's introduction. False positives become explicit dispositions rather than silent FAILs. |
| Test conventions not enumerated (Cypress, Crystal, Lua busted, Perl, C#, Swift, Scala, Elixir, Erlang) | Medium | Medium | Principle-not-enumeration fallback in §4.5 + §5.3: name the framework and equivalent call shapes; skip undetectable cases with one-line note naming the framework. Better than v2's enumerate-only-five-langs shape. |
| Round-2 Red-Team rename-evasion concern (W5 dodge via `TestSignalContext`) | Low | Low | v3 dropped v2's named-test spot-check entirely. W5 is now caught by reviewer rubric point 3 prose (promoted Gemini sentence) and behavior-pattern Smell-1/3 detection, both of which are name-agnostic. |
| Cumulative reliability under three FinalSpecCheck surveys (iface, test-quality, SPEC.md) | Medium | Medium | v3's two-smell scope reduces cumulative-decision count vs v2's three smells (DI-bypass folded into iface saves one full enumeration pass). Inverted STATUS contract defends truncation; integrity preamble adds a CHECKABLE precondition gate the agent cannot bluff. |

## 8. What changed v1 → v2 → v3

**v1 (initial draft):** 6 smells, shared rubric file, 5-language enumeration, no named-test discipline. 5/5 squad reviewers returned "major redesign."

**v2 (first redesign):** 3 smells inlined (dropped Smells 2, 4, 5), named-test spot-check added, code-fence bypass mitigation, tightened waiver discipline. Round-2 squad returned 3 minor / 2 major.

**v3 (second redesign, this document):**

From round-2 unanimous fixes (Architect + Prompt-Pragmatist + Red-Team + Generalizability):
- §5.3 allowlist single-source-of-truth (Architect, Prompt-Pragmatist).
- §4.7 cherry-picked Gap 7 chmod+sha into Setup (Architect strong, Red-Team agreed).
- §4.3 + §5.3 expanded code-fence prompt to cover seven bypass shapes (Red-Team).
- §4.5 + §5.3 fixed `**/__tests__/**` portability bug, replaced `find ... || true` with `[ -n "$files" ]` guard (Generalizability).
- §4.5 + §5.3 reconciled §4.5-design vs §5.1-concrete-edits drift in the three places v2 had it (Generalizability).
- §4.5 added Smell 3 Go `time.After(` pattern (Red-Team).

From round-2 YAGNI + Generalizability convergence:
- §4.5 folded Smell 6 (DI bypass) into Gap 7's existing iface enumeration as ~5-line cross-check (YAGNI strongest finding).
- §4.5 survey-first language detection per Gap 7 v3 pattern, principle-not-enumeration in all three smell sections (Generalizability).
- Dropped §4.8 named-test spot-check entirely (Architect + YAGNI + Red-Team).
- Dropped §4.9 numbered-hits ceremony, kept shown-work-for-skips (YAGNI: no downstream validator).
- §4.5 eliminated Smell 6 duck-typed self-contradiction by folding into Gap 7 (which handles duck-typed languages via vacuous skip already) (Generalizability).

From round-2 Red-Team:
- §4.8 reviewers get 3-line inline smell summary instead of "see FinalSpecCheck section" (Red-Team's structural-break finding).
- §4.7 SPEC.md SHA added to defend against mid-run prompt-injection for Smell 3 disposition (a) (Red-Team).
- §7 acknowledged rubber-stamping and waiver laundering as residual risks honestly.

Total prompt-and-helper budget: ~70 lines (v3) vs ~165 (v2) vs ~330 (v1).

## 9. References

- Issue #233 — `build_product` audit recap.
- PR #246 — closed Gaps 1, 3, 6. Established `Setup`-writes-shared-helper pattern.
- PR #249 — closed Gaps 2, 4. Established balanced 5-point reviewer rubric; introduced `VerifyMilestone` point 4 TEST-VERIFIES-CONTRACT.
- PR #254 — closed Gap 7. Established inverted STATUS contract; established iface-reachability-rubric.md shared-discipline file (which v3 hardens via chmod+sha).
- v1 squad review (2026-05-26, 5 reviewers, conversation transcript): 5/5 major redesign verdict that drove v1 → v2.
- v2 squad review (2026-05-26, 5 reviewers, conversation transcript): 3 minor / 2 major verdict that drove v2 → v3.
- [CLAUDE.md](../../../CLAUDE.md) — repo-level instructions; #213 activity-log integrity work establishes "tripwire not authentication" pattern v3's chmod+sha follows.

## 10. Open questions

To be answered by a third squad review (if convened) OR by the implementing PR:

1. v3's chmod+sha hardening (§4.7) defends Gap 7 too. Should Gap 7's spec be amended retroactively with a §11 note documenting the cross-cutting fix? Or is the cross-cutting nature acceptable as documented here?
2. Round-2 Red-Team's sophisticated-waiver-laundering finding is documented in §4.6 and §7 as residual risk. Is the asymmetric-attack defense (smell-specific rationale + spec citation + reviewer audit) acceptable, or should waivers be limited to N entries per `.ai/decisions/*.md` file?
3. The parser-level fix for fenced/tabled/commented STATUS bypass (round-2 Red-Team §5) is filed as follow-up since `parseAutoStatus` is contract-locked by `TestParseAutoStatus_V3FailFirstContract`. Should this follow-up PR also detect unclosed fences and fail-closed? (Touches the same parser contract.)
4. Round-2 Generalizability raised the absence of Cypress, Crystal, Lua busted, Perl, C#, Swift, Scala, Elixir, Erlang conventions. v3's principle-not-enumeration fallback handles these via prompt instruction. Is this acceptable, or should a future PR enumerate more?
