# Gap 8 — Test Quality (Issue #233)

**Status:** Design v2 (post-squad-review)

**Author:** Claude (Opus 4.7) + Clint Ecker

**Date:** 2026-05-26

**Closes:** Gap 8 of [#233](https://github.com/2389-research/tracker/issues/233) — `build_product` workflow audit recap.

---

## 1. Problem

The original `build_product` audit identified six test-quality smells that ship green when the same agent writes implementation and tests. The five named regression cases from the code-goblin run (issue #233 Appendix A):

| # | Audit case | Smell category |
|---|------------|----------------|
| W4 | `goblin.start` test logs an empty SHA value without asserting on it | zero-assertion shape |
| W5 | `TestRun_SignalHandling` tests stdlib `signal.NotifyContext` instead of the daemon's actual handler | wrong-target |
| W13 | tests use `time.Now()` directly when a `Clock` seam exists | DI bypass |
| W17 | `TestLoop_BusyDrops` uses three `time.Sleep` calls as phase fences | sleep-as-fence |
| W21 | `bytes_trimSpace` declared alongside `bytes.TrimSpace` — Go subtest case-fold collision | subname collision |

PR #249's reviewer rubric covers test-quality in three lines of prose (point 3, `TEST VERIFIES CONTRACT`). The audit shows reviewers handwave past it the same way they handwaved past interface reachability before PR #254 added shown-work discipline.

## 2. Goals

- Add concrete grep-based detection at `FinalSpecCheck` for the three smells (zero-assertion, sleep-as-fence, DI bypass) where grep can produce shown-work evidence — covering W4, W13, W17.
- Cover W5 (wrong-target) by **promoting** Gemini reviewer's existing lane-specific sentence to all three reviewers — no new rubric content.
- Acknowledge W21 (subname collision) as out of scope — Go-specific lexical check, golangci-lint territory.
- Keep the discipline as small as possible. v1 of this spec was rejected 5/5 by squad review for over-building (270-line rubric for 6 smells, 4 of which catch only surface patterns or are unworkable). v2 trims to 3 smells, ~80 inline prompt lines, no shared rubric file.
- Compose cleanly with PR #246's `Setup`-writes-helper pattern (left intact for ci-probe.sh and iface-reachability-rubric.md from PR #254), PR #249's balanced 5-point reviewer rubric, and PR #254's inverted STATUS contract.
- Address the rubber-stamping failure mode the squad identified: agents pasting findings inside code fences, agents handwaving with "this is the timing contract" rationales, agents tampering with shared discipline files.

## 3. Non-goals

- Replacing a real test-quality static analyzer (`golangci-lint`, `eslint`, `clippy`). Per the audit framing in v1 §3, this is a non-goal explicitly.
- **W21 (subname collision)** — Go's `t.Run` case-fold collision is a lexical bug detectable by a 4-line grep pipeline, but the smell is fundamentally Go-specific (Python parametrize ids and JS describe/it strings are NOT normalized the same way). golangci-lint territory; a separate follow-up issue can land a workflow-agnostic fix.
- **Wrong-target detection via grep heuristics.** The v1 import-ratio approach (stdlib-count vs project-count) is high-likelihood false-positive and misses W5's specific shape. v2 relies on prompt-level discipline in the reviewer rubric (Gemini's existing sentence promoted to all three).
- **Per-test contract verification at FinalSpecCheck.** The existing `VerifyMilestone` point 4 (Gap 4, PR #249) already asks reviewers per-milestone "if production code were deleted and rewritten differently but spec-conformantly, would this assertion still pass for the right reason?" That covers the W4 "assertion on the wrong thing" shape semantically; v2 adds a grep-based zero-assertion check at FinalSpecCheck as a backstop, not a replacement.
- **Shared rubric file pattern** for Gap 8. PR #254's `.ai/build/iface-reachability-rubric.md` is left intact; Gap 8's 3-smell rubric is small enough that inlining into the FinalSpecCheck prompt is cleaner than another setup-written artifact. The shared-file pattern is the right design when the rubric is large (Gap 7's iface enumeration ran ~165 lines); for Gap 8's compact 3-smell discipline, inlining eliminates the rubric-tampering attack surface squad-review identified.
- **Engine-level parser changes** to defend against code-fenced findings. `parseAutoStatus`'s last-line-wins + skip-fenced semantics are locked by `TestParseAutoStatus_V3FailFirstContract`. v2 mitigates code-fence bypass via prompt-level instruction (findings live outside fences; only grep commands and grep output go inside).
- The 38 concrete audit findings in #233 Appendix A. They are regression cases informing the rubric, not separate work items. v1 implicitly claimed comprehensive coverage; v2 explicitly claims coverage of 3 named cases (W4, W13, W17) at FinalSpecCheck plus W5 at the reviewer layer.

## 4. Design

### 4.1 Single owner: `FinalSpecCheck`

The test-quality check lives in `FinalSpecCheck`, alongside Gap 7's iface-reachability section. The single-owner argument from PR #254 applies for the three smells we keep:

- **Smell 1 (zero-assertion)** — a missing assertion is a terminal-state property of the test suite. Catching mid-milestone with diff scoping leaks the cross-milestone case where milestone 3 adds an assertion-less test that survives to FinalBuild.
- **Smell 3 (sleep-as-fence)** — same shape; a sleep introduced in milestone 2 is still present in milestone 5.
- **Smell 6 (DI bypass)** — fundamentally terminal-state, since the seam might be introduced in milestone N and the bypass test in milestone N+1.

`VerifyMilestone` is **unchanged** by this PR. Point 4 (Gap 4, PR #249) already covers the per-milestone "test verifies contract" shape; Gap 8 adds the grep-based surface-pattern detection at FinalSpecCheck.

### 4.2 Inline rubric, no shared file

Gap 7's `.ai/build/iface-reachability-rubric.md` is left intact. Gap 8 does **not** add a parallel `test-quality-rubric.md`. The 3-smell rubric for Gap 8 is inlined into the FinalSpecCheck prompt directly (~80 lines).

Reasons:

- **Tampering attack surface.** Three agent nodes (`Implement`, `FixMilestone`, `ApplyReviewFixes`) run between Setup and FinalSpecCheck with full file-write access. A shared rubric file in `.ai/build/` is mutable by any of them. Gap 7 inherits this risk for its 165-line iface rubric (out of scope for this PR; documented as known limitation in §7). Gap 8's smaller rubric is inlinable, so v2 sidesteps the risk entirely by living in the workflow definition (immutable at runtime).
- **Drift risk inversion.** The shared-file pattern's primary win is preventing drift across ~5 prompts (FinalSpecCheck + 3 reviewers + iface enumeration). For Gap 8 v2, FinalSpecCheck is the only node running the smell-detection greps; reviewers reference the smell list by name without re-enumerating discipline. Single-source-by-construction.
- **Prompt budget.** FinalSpecCheck grows from ~85 lines (current) → ~165 lines (after Gap 8 v2 inline). Reviewers' point 3 grows by 2 lines × 3 reviewers = 6 lines. Total v2 addition: ~86 prompt lines. v1's design added ~280 rubric-file lines + ~33 FinalSpecCheck lines + 18 reviewer lines = ~330 lines. v2 is roughly **1/4 the size of v1** — meeting the spec §2 goal of "as small as possible consistent with closing the gap."

### 4.3 Inverted STATUS contract — shared with existing iface section, plus targeted defenses

The FinalSpecCheck inverted STATUS contract from PR #254 stays. The new TEST QUALITY section folds under the same contract — no new STATUS rules.

Squad review (Prompt-Pragmatist CRIT-3, Red-Team §6) identified two failure modes the existing contract does NOT defend:

1. **Rubber-stamping** — agent completes cleanly, emits `STATUS:success`, but dispositioned a real FAIL as a false-positive. Mitigation: numbered hits + required shown-work for skips (§4.5 below).
2. **Code-fenced findings bypass** — agent wraps findings in ```text fences; parser skips them; final `STATUS:success` wins despite findings. Mitigation: prompt instruction in §5.1 — "STATUS lines and finding bullets MUST live outside code fences; only grep commands and grep output go inside fences." This is prompt-only; no parser change.

The spec is honest about residual risk: a determined adversary can still emit `STATUS:success` correctly, paste fake "no findings" output, and pass. The defenses raise the cost without eliminating the attack.

### 4.4 Legacy STATUS contract debt at FinalSpecCheck lines 1115-1119

Squad review (Architect I2) identified that FinalSpecCheck's existing tail block:

```
If fully compliant: STATUS:success
If not: emit STATUS:fail on its own line, followed by the
specific list of gaps on subsequent lines
```

contradicts PR #254's inverted contract opening. Under last-line-wins semantics, an agent that reaches the tail section, finds SPEC.md compliance passing, and emits `STATUS:success` per the legacy framing overrides the early `STATUS:fail` — even if test-quality (or iface) sections found real bugs the agent hasn't yet enumerated.

v2 rewrites the tail block to align with the inverted contract: the agent's terminal `STATUS:success` is the only success signal; the tail block reinforces this rather than contradicting it.

This is technically a Gap 7 debt that Gap 8 surfaces. Fixing it here is in-scope because the test-quality section makes the contradiction worse (now three prose surveys under one contract).

### 4.5 Three smells, with named regression mapping

#### Smell 1 — Zero-assertion tests

**Audit reference:** W4 (`goblin.start` test logs empty SHA without asserting).

**Detection:** for each test function, the body MUST contain at least one assertion-class call. Per-language detection:

- **Go:** `grep -rnE 'func Test[A-Z][A-Za-z0-9_]*\(t \*testing\.T\)' --include='*_test.go' .` — for each hit, agent reads the function body and confirms presence of: `t.Error`, `t.Errorf`, `t.Fatal`, `t.Fatalf`, `t.Fail`, `require.`, `assert.`, `cmp.Diff`, `reflect.DeepEqual`, `is.`, `testify.`. A test that ONLY calls `t.Skip` is still zero-assertion. A test with assertions in `t.Run` subtests counts (cite the subtest line).
- **Python:** `grep -rnE '^[[:space:]]*def test_' --include='*.py' .` — for each hit, body must contain `assert`, `self.assert*`, `pytest.raises`, `pytest.warns`, or a hypothesis property.
- **JS/TS:** `grep -rnE '(test|it)\(' --include='*.test.*' --include='*.spec.*' --include='**/__tests__/**' .` — for each hit, body must contain `expect(`, `assert.`, `should.`, `chai.`, `node:assert` calls.
- **Ruby:** `grep -rnE '(def test_|it ['\''"]|describe ['\''"])' --include='*_test.rb' --include='*_spec.rb' .` — body must contain `assert`, `refute`, `expect(`, `should`, `must_`.
- **Rust:** `grep -rnE '#\[test\]' --include='*.rs' .` (also scans inline `#[cfg(test)] mod tests`). Body must contain `assert!`, `assert_eq!`, `assert_ne!`, `panic!`, or unwrap on a value whose correctness is the spec contract.
- **Java/Kotlin:** `grep -rnE '@Test' --include='*.java' --include='*.kt' .` — body must contain `assert*`, `Assertions.`, `Assert.`, `assertThrows`, `verify(`.

**Principle for unenumerated languages:** if the project uses a test framework not in this list, name the framework and the equivalent assertion-class call shapes; if undetectable, skip with named reason citing the framework.

**False-positive guards:**
- Tests that intentionally panic on failure (`var _ = mustParse(...)`) — cite the panic site.
- Tests with assertions in shared helpers — cite the helper file:line.
- Property-based tests (QuickCheck/Hypothesis/proptest) — cite the property function as the assertion source.

**Skip when:** Smell 1 always applies wherever tests exist. No vacuous skip.

#### Smell 3 — Sleep-as-fence

**Audit reference:** W17 (`TestLoop_BusyDrops` uses three `time.Sleep` calls).

**Detection:** grep test files for sleep-class calls. Each hit needs disposition.

- **Go:** `grep -rnE 'time\.Sleep\(' --include='*_test.go' .`
- **Python:** `grep -rnE '(time|asyncio|trio|anyio|gevent)\.sleep\(' --include='test_*.py' --include='*_test.py' .`
- **JS/TS:** `grep -rnE '(await sleep\(|setTimeout\(|new Promise.*setTimeout|waitForTimeout\(|cy\.wait\()' --include='*.test.*' --include='*.spec.*' .`
- **Ruby:** `grep -rnE '(sleep[[:space:]]+[0-9]|Kernel\.sleep|EM\.add_timer)' --include='*_test.rb' --include='*_spec.rb' .`
- **Rust:** find test files via `git ls-files | grep -E '(_test\.rs|/tests/)'` plus inline `#[cfg(test)]` modules in source files; grep for `thread::sleep`, `tokio::time::sleep`, `async_std::task::sleep`, `smol::Timer::after`.
- **Java/Kotlin:** find test dirs via `find . -path '*/test/*' -name '*.java' -o -path '*/test/*' -name '*.kt'`; grep for `Thread\.sleep`, `delay\(`, `Mono\.delay`, `Observable\.timer`, `withTimeoutOrNull`.

**Disposition required for each hit** — one of:
- (a) **The sleep IS the SUT** — test asserts on timing behavior (rate limiter, backoff, timeout). Cite the spec section that calls out the timing contract. Without a spec-section citation, the rationale is blanket and the hit is FAIL.
- (b) **Replaced by deterministic primitive** — `sync.WaitGroup`, channel, condition variable, fake clock advance. Show the primitive's introduction (file:line).
- (c) **Documented waiver** — `.ai/decisions/*.md` names the specific test AND explains the smell-specific exception (not "by design"; not "intentional"). See §4.6.

**Skip when:** no sleep-class hits in any test file → "Smell 3: no sleep calls in tests; passes vacuously." Paste the empty grep output.

#### Smell 6 — DI bypass

**Audit reference:** W13 (tests use `time.Now()` when Clock seam exists).

**Detection — two steps:**

1. **Find injectable seams in production code.** Grep for type declarations and parameter shapes that define a time/random/IO seam:
   - **Go:** `grep -rnE 'type [A-Z][A-Za-z0-9_]* +interface' --include='*.go' . | grep -vE '_test\.go|testdata'`; also `grep -rnE 'now +func\(\) +time\.Time' --include='*.go' .` (function-typed seams); also `grep -rnE 'type [A-Z][A-Za-z0-9_]+Fn +func' --include='*.go' .` (function aliases). For each interface, the agent inspects methods; flag interfaces with `Now()`, `Read`, `Random`, `Sleep`, `Time` methods as seams. Look for STRUCT FIELDS too: `grep -rnE '[[:space:]]+(clock|now|rand|random|sleep)[[:space:]]+[A-Z]' --include='*.go' .`
   - **Python:** `grep -rnE 'class .+\((ABC|Protocol)\)' --include='*.py' .` (PEP 544 protocols and abc.ABC); also `grep -rnE 'def __init__\([^)]*clock=' --include='*.py' .` (constructor injection — duck-typed seams).
   - **TS:** `grep -rnE '(interface|type) [A-Z][A-Za-z0-9_]* *=' --include='*.ts' .` — agent identifies time/random/IO members.
   - **Ruby:** `grep -rnE 'def initialize\([^)]*clock' --include='*.rb' .` and similar — Ruby is duck-typed, so constructor parameter names are the seam.
   - For other languages: agent surveys for module-level functions/factories that accept time/random parameters.

2. **For each detected seam, grep tests for direct use of the bypassed stdlib symbol:**
   - **Go:** `grep -rnE 'time\.Now\(' --include='*_test.go' .`; `grep -rnE 'rand\.(Read|Int|Float)' --include='*_test.go' .`; `grep -rnE 'os\.(Stdin|Stdout|Stderr)' --include='*_test.go' .`
   - **Python:** `grep -rnE '(datetime\.now|time\.time)\(' --include='test_*.py' --include='*_test.py' .` (NOTE: must use `-E` for alternation).
   - **TS:** `grep -rnE '(new Date\(\)|Date\.now\()' --include='*.test.*' --include='*.spec.*' .`
   - **Ruby:** `grep -rnE '(Time\.now|DateTime\.now)' --include='*_test.rb' --include='*_spec.rb' .`

**Disposition for each step-2 hit:** the test using the bypass must either (a) be a test OF the seam itself (cite the seam-implementation file:line that the test exercises), or (b) be using the seam (cite the constructor call passing a mock/fake clock), or (c) be a documented waiver per §4.6.

**Skip when:** step 1 finds no injectable seams → "Smell 6: no Clock/Random/IO seams detected; passes vacuously." Paste the empty seam-search output.

**Known limitations (skip with named reason, not FAIL):**
- Plain JS / duck-typed Python / Elixir / Clojure with implicit seams via parameter passing — the seam shape is not greppable. Cite the language and skip.
- Property-based tests that vary the seam intentionally — cite the property.

### 4.6 Waiver discipline (tightened from v1)

A hit is FAIL unless `.ai/decisions/*.md` documents this specific test by name with a **smell-specific rationale** — not just "names a specific test" (v1 framing) but "explains why this specific test legitimately exhibits the smell-pattern detected." Examples:

- ✓ ACCEPTABLE: "`TestRateLimit_BackoffTiming` — the sleep IS the SUT; SPEC.md §4.2 specifies '750ms minimum backoff' and the test asserts the elapsed time meets this contract."
- ✗ REJECTED: "`TestRateLimit_BackoffTiming` — intentional integration test of timing behavior." (Doesn't cite the spec section; "intentional" applies to every test by definition.)

Before declaring FAIL, the agent runs:

```
grep -rn '<TestName>' .ai/decisions/
```

For each hit, the agent inspects the rationale text. A rationale that names the test AND cites a specific spec section AND explains the smell-specific exception → PASS. Anything less → FAIL.

**The reviewer rubric (§5.3) adds a complementary waiver-audit duty:** reviewers read all `.ai/decisions/*.md` files referenced in the FinalSpecCheck output and flag rationales whose logic, if applied broadly, would void the smell check. This is the asymmetric-attack defense squad review (Red-Team §2) identified as missing from v1.

### 4.7 Reviewer rubric — fold into point 3, no 6th point

Existing reviewer rubric point 3 (`examples/build_product.dip:826-832 / 873-879 / 931-939`) stays as written. v2 adds:

- **All three reviewers** get a 2-line append:
  ```
  Also: show grep evidence the tests do NOT exhibit zero-assertion,
  sleep-as-fence, or DI-bypass smells (see FinalSpecCheck's TEST
  QUALITY section for detection patterns). Audit any `.ai/decisions/*.md`
  waivers referenced — flag rationales whose logic, if applied broadly,
  would void the smell check.
  ```
- **The Gemini-only sentence** at `examples/build_product.dip:936-938` ("Tests that only validate standard-library or third-party-library behavior instead of the project's own logic are FAIL") is **promoted to ReviewClaude and ReviewCodex's point 3 too**. This is the v2 mitigation for W5 (wrong-target) — no rubric content, just propagation of the existing lane-specific sentence.

Total reviewer-prompt growth: ~5 lines per reviewer × 3 reviewers = 15 lines added. Point 3 grows from ~6 lines to ~11 lines per reviewer. Comparable to point 2 (INTERFACE REACHABILITY, ~9 lines after PR #254). PR #249's balance preserved within rounding.

### 4.8 Named-test spot-check

Squad review (Red-Team §1, §8) identified that grep-based detection catches surface patterns but not underlying bugs in 4 of 5 named regression cases. The mitigation:

For projects where the audit-named regression tests exist (i.e., the original code-goblin project, or test suites importing similar shapes), FinalSpecCheck's prompt includes an explicit named-test review at the end of its TEST QUALITY section:

```
If any of these test names exist in the repo, inspect each by name
and verify the test body matches the spec contract — not just that
it has assertions and doesn't sleep:
  - TestRun_SignalHandling  (W5 archetype — verify the test exercises
    the unit's actual signal handler, not stdlib signal.NotifyContext)
  - tests on `start` functions that log SHA/hash values  (W4 archetype
    — verify the assertion targets the value, not just its presence)
  - `TestLoop_*` patterns  (W17 archetype — verify any sleep cite
    a SPEC.md timing-contract section)
```

For new projects without these exact names, the spot-check is empty and the prompt instructs the agent to "pick three recently-modified test files at random; for each, verify the test body matches a spec contract per the rubric above."

This addresses the squad's "catches surface, misses underlying bug" finding while staying small (~10 prompt lines).

### 4.9 Mandatory shown-work for skips and numbered hits

Two prompt-level disciplines added (Prompt-Pragmatist IMPORTANT-6, fix #1):

1. **Skips must paste detection output.** Every "Smell N skipped" line must be immediately followed by the actual detection-grep output (even when empty), not just a one-line note. Lazy agent self-attestation ("Smell 6: no seams detected; skipped") is not acceptable.
2. **Numbered hits per smell.** The agent numbers every hit consecutively within each smell section ("Smell 1 Hit 1: ...", "Smell 1 Hit 2: ...") and emits a final total ("Smell 1 total: 7 hits, all dispositioned"). This converts "don't write ditto/similar" from honor-system to checkable-by-the-reader-or-by-a-second-reviewer-pass.

## 5. Concrete edits

Three sections of `examples/build_product.dip` change. `make sync-workflows` mirrors to `workflows/build_product.dip`.

### 5.1 FinalSpecCheck — new TEST QUALITY section between INTERFACE REACHABILITY and SPEC.md compliance

Insert after line 1093 (end of INTERFACE REACHABILITY section), before line 1095 ("This is the final gate. Read SPEC.md line by line"):

```dip
      TEST QUALITY (issue #233 Gap 8):

      Three smells, each with grep-based detection. STATUS lines and
      finding bullets MUST live OUTSIDE code fences; only grep commands
      and grep output go inside ``` fences. The auto_status parser
      skips fenced lines — emitting STATUS:success inside a fence is
      a no-op; emitting findings inside a fence hides them from the
      parser's notice but not from human review.

      For EACH smell below: number every hit consecutively (Hit 1,
      Hit 2, ...) and emit a total at the section's end. If a smell
      skips (no hits possible — e.g., no seams detected for Smell 6),
      paste the detection-grep output (even when empty) before
      declaring the skip.

      SMELL 1 — Zero-assertion tests (W4 archetype). For each language
      with test files, run the test-function grep then confirm each
      test body contains an assertion-class call. Languages and
      assertion lists:
        Go:      grep -rnE 'func Test[A-Z][A-Za-z0-9_]*\(t \*testing\.T\)' \
                   --include='*_test.go' .
                 Body must contain: t.Error, t.Errorf, t.Fatal, t.Fatalf,
                 t.Fail, require., assert., cmp.Diff, reflect.DeepEqual,
                 is., testify. (NOT t.Skip alone — that's still
                 zero-assertion.)
        Python:  grep -rnE '^[[:space:]]*def test_' --include='*.py' .
                 Body must contain: assert, self.assert*, pytest.raises,
                 pytest.warns.
        JS/TS:   grep -rnE '(test|it)\(' --include='*.test.*' \
                   --include='*.spec.*' --include='**/__tests__/**' .
                 Body must contain: expect(, assert., should., chai.,
                 strict.equal.
        Ruby:    grep -rnE '(def test_|it ['"'"'"']|describe ['"'"'"'])' \
                   --include='*_test.rb' --include='*_spec.rb' .
                 Body must contain: assert, refute, expect(, should.
        Rust:    grep -rnE '#\[test\]' --include='*.rs' .
                 Body must contain: assert!, assert_eq!, assert_ne!,
                 panic!.
        Java/Kotlin: grep -rnE '@Test' --include='*.java' --include='*.kt' .
                 Body must contain: assert*, Assertions., Assert.,
                 assertThrows, verify(.
      For test frameworks not enumerated here, name the framework and
      cite an equivalent assertion-call shape. False-positive guards:
      tests with assertions in t.Run subtests (cite the subtest);
      tests intentionally panicking via mustX patterns (cite the
      panic-site); property tests (cite the property function).

      SMELL 3 — Sleep-as-fence (W17 archetype). Grep test files for
      sleep-class calls. Each hit needs disposition.
        Go:      grep -rnE 'time\.Sleep\(' --include='*_test.go' .
        Python:  grep -rnE '(time|asyncio|trio|anyio|gevent)\.sleep\(' \
                   --include='test_*.py' --include='*_test.py' .
        JS/TS:   grep -rnE '(await sleep\(|setTimeout\(|new Promise.*setTimeout|waitForTimeout\(|cy\.wait\()' \
                   --include='*.test.*' --include='*.spec.*' .
        Ruby:    grep -rnE '(^|[^[:alnum:]_])(sleep[[:space:]]+[0-9]|Kernel\.sleep)' \
                   --include='*_test.rb' --include='*_spec.rb' .
        Rust:    grep -rnE '(thread::sleep|tokio::time::sleep|async_std::task::sleep)' \
                   $(git ls-files | grep -E '(_test\.rs|/tests/)' || echo /dev/null)
        Java/Kotlin: grep -rnE '(Thread\.sleep|delay\(|Mono\.delay)' \
                   $(find . -path '*/test/*' \( -name '*.java' -o -name '*.kt' \) 2>/dev/null || true)
      Each hit needs disposition: (a) the sleep IS the SUT — cite the
      SPEC.md section calling out the timing contract, file:line; or
      (b) replaced by deterministic primitive — cite the primitive's
      introduction; or (c) waiver per `.ai/decisions/*.md` naming the
      specific test with a smell-specific rationale that cites a spec
      section. "Intentional timing test" without a spec citation is
      a blanket waiver — STILL FAIL.

      SMELL 6 — DI bypass (W13 archetype). Two-step detection.
        Step 1 — find seams:
          Go:    grep -rnE 'type [A-Z][A-Za-z0-9_]* +interface' \
                   --include='*.go' . | grep -vE '_test\.go|testdata'
                 Also struct-field seams:
                   grep -rnE '[[:space:]](clock|now|rand|random)[[:space:]]+[A-Z]' \
                   --include='*.go' . | grep -vE '_test\.go|testdata'
          Python: grep -rnE 'class .+\((ABC|Protocol)\)' --include='*.py' .
          TS:    grep -rnE '(interface|type) [A-Z][A-Za-z0-9_]* *=' \
                   --include='*.ts' .
          Ruby:  grep -rnE 'def initialize\([^)]*(clock|now|rand)' \
                   --include='*.rb' .
        Step 2 — for each seam, find test bypasses:
          Go:    grep -rnE 'time\.Now\(|rand\.(Read|Int|Float)|os\.(Stdin|Stdout)' \
                   --include='*_test.go' .
          Python: grep -rnE '(datetime\.now|time\.time)\(' \
                   --include='test_*.py' --include='*_test.py' .
          TS:    grep -rnE '(new Date\(\)|Date\.now\()' \
                   --include='*.test.*' --include='*.spec.*' .
          Ruby:  grep -rnE '(Time\.now|DateTime\.now)' \
                   --include='*_test.rb' --include='*_spec.rb' .
      Each step-2 hit needs disposition: (a) the test exercises the
      seam itself — cite the seam-implementation file:line; or (b)
      the test uses the seam properly — cite the constructor call
      passing a mock/fake; or (c) waiver per `.ai/decisions/*.md`.
      Known limitations (skip with named reason, NOT FAIL): plain JS
      duck-typed seams, implicit-parameter Elixir/Clojure dispatch,
      property tests that vary the seam intentionally.

      NAMED-TEST SPOT CHECK (issue #233 audit cases). If any of these
      test names exist in the repo, inspect each by name and verify
      the test body matches the spec contract — not just surface
      patterns:
        - TestRun_SignalHandling (W5 archetype — verify the test
          exercises the unit's signal handler, not stdlib
          signal.NotifyContext)
        - tests on `start` functions logging SHA/hash values
          (W4 archetype — verify assertions target the value, not
          just its presence)
        - TestLoop_* patterns (W17 archetype — any sleep must cite
          a SPEC.md timing-contract section)
      For new projects without these exact names: pick three
      recently-modified test files; for each, verify the body
      anchors to a spec contract per the rubric above.

      Regression cases this section exists to catch: W4 (zero-
      assertion goblin.start), W13 (time.Now bypass when Clock
      seam exists), W17 (TestLoop_BusyDrops triple sleep). W5
      (wrong-target) is covered by the cross-reviewer point 3
      upgrade in this PR. W21 (subname collision) is explicitly
      out of scope for this gate — Go-specific lexical bug,
      golangci-lint territory.
```

### 5.2 FinalSpecCheck — rewrite legacy STATUS tail (lines 1115-1119)

Replace:

```
      Write the final compliance report to .ai/decisions/compliance.md

      If fully compliant: STATUS:success
      If not: emit STATUS:fail on its own line, followed by the
      specific list of gaps on subsequent lines (the parser requires
      the STATUS value to be exactly `success`, `fail`, or `retry` —
      no trailing prose on the same line).
```

with (aligning to the inverted contract opened at lines 1048-1059):

```
      Write the final compliance report to .ai/decisions/compliance.md

      Emit your terminal `STATUS:success` line ONLY if all three
      sections above passed: INTERFACE REACHABILITY (every method has
      a production caller or carve-out), TEST QUALITY (every smell-hit
      dispositioned, named-test spot-check verified), and SPEC.md
      compliance (every requirement implemented, no extras, no
      TODO/FIXME/HACK, tests pass, no unexpected files in .ai/build/
      outside the allowlist of ci-probe.sh + iface-reachability-rubric.md).
      Any failure in any section means the early `STATUS:fail` from
      the first line of your response stays in place — do NOT emit
      `STATUS:success`. The parser requires the STATUS value to be
      exactly `success`, `fail`, or `retry` — no trailing prose on
      the same line; no STATUS lines inside code fences.
```

This resolves the dual-contract debt and reaffirms the inverted contract under the new TEST QUALITY section.

### 5.3 Reviewer rubric — point 3 upgrade × 3 prompts

For all three reviewers (ReviewClaude at line 826-832, ReviewCodex at 873-878, ReviewGemini at 931-939):

- **Promote the Gemini-only sentence** to ReviewClaude and ReviewCodex too:
  ```
  Tests that only validate standard-library or third-party-library
  behavior instead of the project's own logic are FAIL.
  ```
  (Currently exists only at lines 936-938 in ReviewGemini.)

- **Append a 2-line discipline addition** to all three reviewers' point 3:
  ```
  Also: show grep evidence the tests do NOT exhibit zero-assertion,
  sleep-as-fence, or DI-bypass smells (see FinalSpecCheck's TEST
  QUALITY section for detection patterns). Audit any
  `.ai/decisions/*.md` waivers referenced — flag rationales whose
  logic, if applied broadly, would void the smell check.
  ```

Total reviewer-prompt growth: ~5 lines × 3 = 15 lines.

### 5.4 `VerifyMilestone` — no change

v2 does not modify `VerifyMilestone`. Its TEST-VERIFIES-CONTRACT (point 4, Gap 4, PR #249) covers the semantic "assertion targets the wrong thing" case for the milestone diff. Gap 8's grep-based zero-assertion at FinalSpecCheck is the backstop for the cross-milestone leak case.

### 5.5 `Setup` — no change for Gap 8

v2 does **not** add a `test-quality-rubric.md` to Setup. The Gap 7 `ci-probe.sh` and `iface-reachability-rubric.md` blocks are untouched. The FinalSpecCheck allowlist at lines 1103-1108 stays exactly as PR #254 wrote it (no new helper file → no allowlist update needed).

## 6. Build and verification

For the implementation PR:

1. Edit `examples/build_product.dip` in three places: FinalSpecCheck TEST QUALITY insert (§5.1), FinalSpecCheck legacy STATUS tail rewrite (§5.2), three reviewers' point 3 upgrade (§5.3).
2. `make sync-workflows` — stage both files.
3. `dippin doctor examples/build_product.dip` — must remain A grade with no new warnings.
4. `dippin simulate -all-paths examples/build_product.dip` — confirm every path still terminates.
5. `tracker validate examples/build_product.dip` — clean.
6. `go build ./... && go test ./... -short` — all 17 packages.
7. `make check-workflows` — sync confirmed.
8. **Update `CHANGELOG.md`** under the next `[Unreleased]` section, matching the pattern of PR #246, PR #249, PR #254.

No new tests in `pipeline/handlers/codergen_test.go` for this PR. Gap 5.1 (parser audit, separate PR) is where new parser test cases would land.

## 7. Risk register

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| FinalSpecCheck prompt grows to ~165 lines after the Gap 8 inline | Medium | Low — dippin-lang has no prompt-length warning (verified: only DIP110 empty, DIP111 timeout-missing) | If a future dippin doctor rule lands, peel the iface section or SPEC.md compliance into a shared file. The growth is one-time. |
| Agent rubber-stamps the 3-smell survey with "all clear" without doing the work | High | High — the dominant Gap 8 failure mode per squad review | Two prompt-level defenses: (a) §4.9 numbered hits + required shown-work-for-skips converts honor-system claims into checkable artifacts; (b) §4.7 reviewer rubric audits the FinalSpecCheck output (and waivers). Residual: a determined agent can still emit STATUS:success with fabricated grep output. Detection raises the cost, doesn't eliminate the attack. |
| Code-fenced findings bypass the parser (Red-Team §6) | Medium | High — silent pass | §5.1 prompt instruction: "STATUS lines and finding bullets MUST live OUTSIDE code fences." Honor-system, but explicit. Parser-level fix is out of scope (would require changing the contract-locked parseAutoStatus). |
| Implement agent pre-authors `.ai/decisions/*.md` waivers (asymmetric attack) | Medium | Medium | §4.6's tightened smell-specific-rationale rule + §4.7's reviewer audit. v1's mitigation was acknowledged as theatre; v2 adds the audit step to the reviewer side. Residual: a sophisticated waiver that names a specific test, cites a real spec section, and explains the smell-specific exception is acceptable — by design. The attacker has to do work; the workflow doesn't catch every paranoid case. |
| Gap 7's `iface-reachability-rubric.md` file remains mutable by Implement/FixMilestone/ApplyReviewFixes (squad Red-Team §7) | Medium | Medium | Out of scope for this PR. File a follow-up issue ("shared rubric files in .ai/build/ are mutable mid-run; add chmod 0444 + SHA verification or move to immutable location"). Documented honestly here so it isn't forgotten. |
| Smell 6 (DI bypass) misfires on duck-typed languages (plain JS, untyped Python, Elixir) | Medium | Low | §4.5 Smell 6 known-limitations section: skip with named reason, not FAIL. The smell is acknowledged as un-greppable for these languages. |
| Sleep-as-fence grep flags legitimate timing tests (rate limiters, backoff) | High | Low | §4.5 Smell 3 disposition rule: "the sleep IS the SUT" is acceptable IF the agent cites a SPEC.md timing-contract section. Without the citation, blanket "intentional" rationales FAIL. |
| Shell portability bugs in the per-language greps | Low | Medium | Spec uses `-E` everywhere (not BRE); `[[:space:]]` not `\s`; the line-385 BRE-vs-ERE bug v1 had is fixed in v2's §5.1. Tests on macOS BSD grep + Linux GNU grep should both work. |
| Cumulative reliability degrades across the 3-smell survey at high reasoning_effort | Medium | Medium | 3 smells × ~3 grep invocations × per-hit disposition vs v1's 6 smells × 5 grep invocations. Roughly 1/4 the cumulative-decision count. The inverted STATUS contract defends truncation; the rubber-stamping risk (Prompt-Pragmatist CRIT-3) is mitigated by §4.9. |
| Named-test spot-check (§4.8) names tests that don't exist in non-code-goblin projects | Low | Low — spot-check is empty by design when names don't match | Prompt instruction handles this: "for new projects without these exact names, pick three recently-modified test files." Empty spot-check is fine. |

## 8. What was considered and rejected (v1 → v2 history)

v1 was reviewed by 5 squad-experts on 2026-05-26: Architect, Prompt-Pragmatist, Generalizability, Red-Team, YAGNI/Minimalist. **All five returned "major redesign."** Key v1 findings and how v2 addresses them:

**Architect:**
- C1 (stop-ship): FinalSpecCheck allowlist didn't include the new `test-quality-rubric.md` → guaranteed FAIL every run. **v2 fix:** no new helper file, no allowlist update needed (§5.5).
- C2: heredoc delimiter `RUBRIC_EOF` collided with iface rubric's heredoc. **v2 fix:** no new heredoc.
- C3: spec claimed ~180 rubric lines, actual was 270. **v2 fix:** ~80 inline lines, honestly accounted in §4.2.
- I2: legacy "If fully compliant: STATUS:success" at lines 1115-1119 contradicts the inverted contract. **v2 fix:** §5.2 rewrites the tail.

**Prompt-Pragmatist:**
- CRIT-1: cumulative reliability of 6-smell survey degrades to 3-13% at 200+ items. **v2 fix:** 3 smells, ~1/4 the cumulative-decision count; §4.9 numbered hits make the work checkable.
- CRIT-3: inverted STATUS contract defends truncation but NOT rubber-stamping. **v2 fix:** §4.3 acknowledges this honestly; §4.9 + §4.7 reviewer-rubric audit are the explicit anti-rubber-stamp measures.
- IMPORTANT-4: Smell 2 (wrong-target) heuristic is unworkable. **v2 fix:** drop the grep heuristic; promote Gemini's existing prose to all three reviewers (§5.3).
- IMPORTANT-7: Smell 4 (unanchored snapshots) anchoring is unworkable for typical repos. **v2 fix:** drop Smell 4 entirely — existing Implement node lines 448-453 (Gap 3, PR #246) + existing reviewer rubric point 3 prose already cover the named case.

**Generalizability:**
- C1: arbitrary 5-language slice repeats Gap 7 v2's mistake. **v2 fix:** §4.5 adds Ruby; uses principle-not-enumeration for unenumerated languages.
- C2: Ruby uncovered. **v2 fix:** Ruby is now first-class in all 3 smells.
- C3: duck-typed languages make Smell 6 silently skip. **v2 fix:** §4.5 known-limitations section.
- I9: shell bug at line 385 (`grep -rn` without `-E` makes `|` literal). **v2 fix:** all alternations use `-E`.

**Red-Team:**
- §1, §8: rubric catches surface patterns for 4 of 5 named cases but not the underlying bugs. **v2 fix:** §4.8 named-test spot-check requires per-name inspection for the audit-named cases.
- §2: waiver laundering is trivial under v1's "non-blanket rationale" filter. **v2 fix:** §4.6 tightened waiver discipline + §4.7 reviewer waiver audit.
- §6: code-fenced findings bypass parser. **v2 fix:** §4.3 + §5.1 prompt instruction.
- §7: shared rubric file is writeable mid-run. **v2 fix:** no shared file for Gap 8 (eliminates the new risk); Gap 7's file remains the same (filed as follow-up).
- §10: cumulative prompt load past Gap 7's strained budget. **v2 fix:** smaller addition (~86 lines vs v1's ~330).

**YAGNI/Minimalist:**
- CRIT-1: Smell 4 already covered by existing reviewer rubric + Implement node. **v2 fix:** dropped.
- CRIT-2: Smell 5 is golangci-lint territory. **v2 fix:** dropped from scope; documented as non-goal (§3, W21).
- CRIT-3: Smell 2 grep heuristic is high-false-positive engine. **v2 fix:** dropped; reviewer-prose promotion instead.
- Minimum viable Gap 8 = 3 smells, ~80 rubric lines, ~12 FinalSpecCheck lines, 2 reviewer lines × 3. **v2 lands at:** 3 smells, ~80 inline lines in FinalSpecCheck, ~5 reviewer lines × 3.

## 9. References

- Issue #233 — `build_product` audit recap. Appendix A items W4, W5, W13, W17, W21 are the named regression cases.
- PR #246 — closed Gaps 1, 3, 6. Established the `Setup`-writes-shared-helper pattern (`ci-probe.sh`) and the `.ai/decisions/` waiver convention.
- PR #249 — closed Gaps 2, 4. Established the balanced 5-point reviewer rubric; introduced `VerifyMilestone` point 4 TEST-VERIFIES-CONTRACT (the semantic counterpart to this PR's grep-based check).
- PR #254 — closed Gap 7. Established the inverted STATUS contract at `FinalSpecCheck` (lines 1048-1059) and the `iface-reachability-rubric.md` shared-discipline file (which Gap 8 v2 deliberately does NOT replicate — see §4.2).
- v1 squad review (2026-05-26, 5 expert reviewers, this conversation): the source of every v1 → v2 change documented in §8.
- [CLAUDE.md](../../../CLAUDE.md) — repo-level instructions, including the `make sync-workflows` discipline.

## 10. Open questions

To be answered by the next squad-review round:

1. The named-test spot-check (§4.8) is empty for projects without code-goblin-derived test names. Is the fallback ("pick three recently-modified test files") strong enough, or should it specify a minimum count proportional to repo size?
2. The waiver discipline (§4.6) requires "smell-specific rationale citing a spec section." Is "cite a spec section" enforceable as written, or does the agent rubber-stamp the spec citation? (Reviewer audit in §4.7 is the next-layer defense, but reviewers see the same waiver text.)
3. The Gap 7 iface-reachability-rubric.md file mutability (§7 risk row 5) is filed as follow-up. Should v2 cherry-pick a minimal `chmod 0444 + sha check` fix into this PR's Setup edit, or is the follow-up issue the right scope?
4. The code-fence-bypass mitigation (§4.3) is prompt-only. Should a follow-up PR add a parser-level fix to `parseAutoStatus` to detect unclosed fences and fail-closed? (Touches the contract locked by `TestParseAutoStatus_V3FailFirstContract`.)
