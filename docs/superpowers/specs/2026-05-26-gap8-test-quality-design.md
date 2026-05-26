# Gap 8 — Test Quality (Issue #233)

**Status:** Design v1 (pre-squad-review)

**Author:** Claude (Opus 4.7) + Clint Ecker

**Date:** 2026-05-26

**Closes:** Gap 8 of [#233](https://github.com/2389-research/tracker/issues/233) — `build_product` workflow audit recap.

---

## 1. Problem

The original `build_product` audit identified six test-quality smells that ship green when the same agent writes implementation and tests:

| # | Smell | Audit example (Appendix A) |
|---|-------|----------------------------|
| 1 | **Zero-assertion tests** | Test functions that run without `t.Error` / `t.Fatal` / `require.*` / `assert.*`. They pass for any execution that doesn't panic. |
| 2 | **Wrong-target tests** | W4: `goblin.start` logs an empty SHA value; W5: `TestRun_SignalHandling` tests stdlib `signal.NotifyContext` instead of the daemon's actual handler. |
| 3 | **Sleep-as-fence** | W17: `TestLoop_BusyDrops` uses three `time.Sleep` calls between phases. Broader pattern: 10+ sleeps across the test corpus. |
| 4 | **Unanchored snapshots** | Golden files regenerated from current output via `UPDATE_GOLDEN=1` without textual anchoring to SPEC.md content. |
| 5 | **Impossible-to-collide subnames** | W21: `bytes_trimSpace` snake-case subtest declared alongside `bytes.TrimSpace` — Go's `t.Run` case-folder treats them as duplicates, masking coverage. |
| 6 | **DI bypass** | W13: tests call `time.Now()` directly when a `Clock` seam exists, so test behavior depends on wall clock. |

PR #249's reviewer rubric covers point 3 (`TEST VERIFIES CONTRACT`) in three lines of prose. The audit shows reviewers handwave past it the same way they handwaved past interface reachability before PR #254 added shown-work discipline.

## 2. Goals

- Catch the six named regression cases at the final-spec-check stage with shown-work grep evidence.
- Keep the design as **small as possible** consistent with closing the gap. Mirror PR #254's design v3 budget — roughly 1/5 the size that an enumerate-everything-per-language v2 would land at.
- Single owner — one node (`FinalSpecCheck`), by name, gates "no test-quality smell may reach Done."
- Language-agnostic by **detecting language first and skipping vacuously** when the project has no tests or when a specific smell doesn't apply. Per-language refinement is opt-in via `.ai/decisions/`, not enumerated in the prompt.
- Compose cleanly with PR #246's "Setup writes shared helper" pattern, PR #249's balanced 5-point reviewer rubric, and PR #254's inverted STATUS contract (already in place at `FinalSpecCheck`).

## 3. Non-goals

- Replacing a real test-quality static analyzer (`golangci-lint`, `eslint`, `clippy`, `bandit`). The check is grep-shaped and best-effort. Cases requiring AST or callgraph analysis are skipped with a named reason.
- Per-milestone tracking. `FinalSpecCheck`'s repo-wide sweep covers cross-milestone leaks; PR #254 explicitly rejected per-milestone scoping and the same argument applies here.
- A new node, a new STATUS contract, or new edges. The discipline rides on the existing `FinalSpecCheck` inverted-STATUS contract.
- Gap 5 sub-tasks (parser audit, `OutcomeHumanOverride`, post-`ApplyReviewFixes` re-check). Tracked separately under Chunk C.
- The 38 concrete audit findings in #233 Appendix A. They are regression cases informing the rubric, not separate work items.

## 4. Design

### 4.1 Single owner: `FinalSpecCheck`

The test-quality check lives in `FinalSpecCheck` and **only** in `FinalSpecCheck`. The same arguments PR #254 used for iface-reachability apply verbatim:

- The bug is "code shipped Done with smelly tests" — a property of the terminal state, not the build process.
- The workflow is fully automated; catching at milestone-3 vs at FinalSpecCheck has no operational value when no human is debugging mid-run.
- Per-milestone `VerifyMilestone`'s diff scoping creates the cross-milestone leak shape (smell introduced in milestone 2, still passing in milestone 5).
- `FinalSpecCheck` is already the goal-gate before `Cleanup → FinalCommit → Done` and routes failures to `EscalateReview` — the right route both for real smells and for tool-limitation false positives.

`VerifyMilestone` is **unchanged** by this PR.

### 4.2 Shared discipline lives in one file

The check's discipline (per-smell detection patterns, per-language grep commands, false-positive guards, waiver rules) is written **once** to `.ai/build/test-quality-rubric.md` by the `Setup` node — mirroring PR #246 (`ci-probe.sh`) and PR #254 (`iface-reachability-rubric.md`). `FinalSpecCheck` and the three reviewer prompts reference the file by path instead of duplicating ~150 lines of prose into each of five nodes.

This is the highest-leverage architectural decision in v1, carried over from PR #254. It eliminates drift risk, restores PR #249's rubric balance, and keeps the prompt under the line budget that `dippin doctor` warns on.

### 4.3 Inverted STATUS contract — shared with existing iface section

`FinalSpecCheck` already opens with the inverted STATUS contract from PR #254:

> emit `STATUS:fail` as the FIRST line of your response, before any other text. ... Only at the very end, after every check in this prompt passes ... emit a final `STATUS:success` line — alone on its line, outside any code fence — to override the early fail.

The new TEST QUALITY section folds under this same contract. No new STATUS-handling. The agent's terminal `STATUS:success` now requires BOTH iface-reachability AND test-quality AND SPEC.md compliance to pass. Truncation mid-test-quality preserves the early `STATUS:fail` — the section fails closed.

### 4.4 Six smells, one rubric

`.ai/build/test-quality-rubric.md` has six numbered sub-sections, one per smell. Each sub-section contains:

1. **What this smell is** — one-paragraph description with the audit's canonical example.
2. **Per-language detection** — grep command(s) for each supported language. Languages where the smell doesn't apply are skipped explicitly.
3. **False-positive guards** — known-legitimate patterns that hit the grep but aren't bugs (e.g., tests that intentionally panic; sleeps that assert rate-limit timing).
4. **Waiver discipline** — one sentence. Same rule as Gap 7: a hit is FAIL unless `.ai/decisions/*.md` documents this specific test by name with a non-blanket rationale.

The full rubric content is reproduced in §5.1 below. Six sub-sections, ~180 lines of prose.

### 4.5 Per-smell language-conditional skip

Gap 7's language detection was holistic (one check: "does the project have a static-interface language?"). Test quality is different — each smell has its own language applicability:

- **Smells 1, 3, 5** (zero-assertion, sleep-as-fence, subname collision) apply to any project with tests in any language. Detection grep is per-language but skip is rare.
- **Smell 4** (unanchored snapshots) skips if the project uses no snapshot framework. Detection: presence of `.golden`, `.snap`, `__snapshots__/`, `.snapshot` files.
- **Smell 6** (DI bypass) skips if the project defines no injectable seams. Detection: agent runs a one-shot grep for `interface` declarations with `Now()`, `Read*`, `Write*` methods; if none found, skip.

Whole-rubric skip (rare): if the project has no test files at all (`find . -type d -name testdata -o -name tests -o -name test -o -name spec` and `git ls-files | grep -E '(_test|test_|\.test\.|\.spec\.)'` return nothing), skip with one-line note: "no tests detected; test-quality check skipped."

### 4.6 Waiver discipline

Same as Gap 7. A hit is FAIL unless `.ai/decisions/*.md` documents this specific test by name with a non-blanket rationale. Before declaring FAIL, the agent runs:

```
grep -rn '<TestName>' .ai/decisions/
```

A hit naming the test with a rationale that wouldn't apply equally to every test → PASS, cite the decision-log path. Blanket waivers ("API surface" / "by design") → still FAIL.

The asymmetric attack (Implement agent pre-authoring waivers) is the same risk Gap 7 v3 acknowledged; the mitigation is the same — the reviewer rubric is the next layer, and blanket-rationale waivers get flagged by either gate.

### 4.7 Reviewer rubric — fold into point 3, no 6th point

Point 3 (`TEST VERIFIES CONTRACT`) gets a one-sentence upgrade matching PR #254's pattern for point 2. The existing three sentences stay; the new sentence demands shown-work grep evidence and references the rubric file:

```text
3. TEST VERIFIES CONTRACT: For each assertion in a test under this
   section, does it verify the spec's contract, or does it mirror
   what the implementation happens to produce? Snapshot tests
   regenerated from current output are FAIL. Tests asserting
   `attempts == 2` when the spec says "max 2 retries" (= 3 attempts)
   are FAIL. Also: for every test file under this section, show grep
   evidence that the tests do NOT exhibit any of the six smells listed
   in `.ai/build/test-quality-rubric.md` (zero-assertion, wrong-target,
   sleep-as-fence, unanchored snapshot, impossible-to-collide subname,
   DI bypass). Apply the same show-your-work standard as SPEC LITERALS
   at point 1.
```

This is ~5 added lines per reviewer prompt (× 3 prompts = 15 lines total) — restoring the balance with points 1, 2, 4, 5 (each ~5-8 lines). The heavy discipline lives once in the rubric file.

### 4.8 FinalSpecCheck prompt addition

A new TEST QUALITY section is added to the existing prompt, between INTERFACE REACHABILITY and the SPEC.md compliance check. ~18 lines.

The section says: read the rubric; survey for each of the six smells repo-wide; emit prose enumeration with grep evidence (command + output + file:line) for each finding; truncation preserves the early `STATUS:fail`.

Full text reproduced in §5.2.

## 5. Concrete edits

Three artefacts change in `examples/build_product.dip`; `make sync-workflows` mirrors to `workflows/build_product.dip`.

### 5.1 `Setup` node — write the shared rubric file

After the existing `cat > .ai/build/iface-reachability-rubric.md` block, add:

```dip
      # Shared test-quality rubric (issue #233 Gap 8).
      # FinalSpecCheck and the three reviewer prompts source this file
      # so the discipline lives in exactly one place — mirrors the
      # ci-probe.sh pattern from PR #246 (Gap 1) and the
      # iface-reachability-rubric.md pattern from PR #254 (Gap 7).
      cat > .ai/build/test-quality-rubric.md <<'RUBRIC_EOF'
      # Test Quality Rubric (issue #233 Gap 8)

      ## When this rubric applies (language / test-presence detection)

      Survey test presence in this project. If
        git ls-files | grep -E '(_test\.|test_|/test/|/tests/|\.spec\.|\.test\.)'
      returns nothing AND
        find . -type d \( -name testdata -o -name tests -o -name test -o -name spec \) 2>/dev/null
      returns nothing, skip the entire check with a one-line note: "no
      tests detected; test-quality check skipped."

      Otherwise, apply each of the six smell sections below. Per-smell
      language applicability is noted inline.

      ## Smell 1 — Zero-assertion tests

      A test function that runs without invoking any assertion. Such
      tests pass for any execution that doesn't panic. Audit reference:
      the `build_product` audit found multiple such tests in code-goblin.

      Per-language detection — for each test function, the body MUST
      contain at least one assertion-class call. Find test functions:
        Go:      grep -rnE 'func Test[A-Z][A-Za-z0-9_]*\(t \*testing\.T\)' \
                   --include='*_test.go' .
                 For each hit, read the function body and confirm it
                 contains at least one of: t.Error, t.Errorf, t.Fatal,
                 t.Fatalf, t.Fail, t.Skip (note: Skip is escape-hatch;
                 a test that ONLY calls Skip is still zero-assertion),
                 require., assert., cmp.Diff, reflect.DeepEqual,
                 testify., is..
        Python:  grep -rnE '^[[:space:]]*def test_' \
                   --include='*.py' .
                 For each hit, read the function body and confirm it
                 contains at least one of: assert, self.assert,
                 pytest.raises, pytest.warns, mock.assert_called.
        JS/TS:   grep -rnE '(test|it)\(' \
                   --include='*.test.*' --include='*.spec.*' \
                   --include='**/__tests__/**' .
                 For each hit, confirm body contains: expect(, assert.,
                 should., chai..
        Rust:    grep -rnE '#\[test\]' --include='*.rs' .
                 For each hit, confirm the following fn body contains:
                 assert!, assert_eq!, assert_ne!, panic!, .unwrap()
                 (unwrap counts only if the value's correctness is the
                 spec contract; otherwise list as suspicious).
        Java/Kotlin: grep -rnE '@Test' \
                   --include='*.java' --include='*.kt' .
                 For each hit, confirm body contains: assert*,
                 Assertions., Assert., assertThrows, verify(.

      False-positive guards:
        - Tests that intentionally panic on failure (e.g., compile-time
          contract tests using `var _ = mustParse(...)`) — cite the
          panic-site as the assertion.
        - Tests that wrap their body in `t.Run` subtests where the
          subtests assert — cite a subtest's assertion as the parent's.
        - Tests using table-driven patterns where the assertion is in
          a shared helper — cite the helper's assertion line.

      Waiver: a missing assertion is FAIL unless `.ai/decisions/*.md`
      names this specific test with a non-blanket rationale.

      ## Smell 2 — Wrong-target tests

      A test whose body exercises a stdlib stand-in, framework, or
      unrelated layer instead of the unit under test. Audit example:
      `TestRun_SignalHandling` tested `signal.NotifyContext` (stdlib)
      instead of the daemon's actual signal handler.

      Detection — semantic, hard to grep precisely. For each test file:
        1. Identify the unit under test from the file name (e.g.,
           `daemon_test.go` → unit is `daemon`).
        2. Read the test bodies. For each test, show one call expression
           from the body that touches the unit under test (e.g.,
           `daemon.New(...)`, `srv.Start(...)`, an imported method from
           the unit's package).
        3. If a test's call graph is dominated by stdlib paths
           (`signal.`, `time.`, `http.`, `os.`) without an invocation
           of the unit's API, flag it.

      Per-language heuristic — grep for tests whose body imports more
      stdlib than project package:
        Go:      grep -lE 'import \(' --include='*_test.go' . | while read f; do
                   stdlib=$(grep -cE '^\s*"[a-z]+"' "$f")
                   project=$(grep -cE '^\s*"[^"]+/[^"]+"' "$f")
                   [ "$stdlib" -gt "$project" ] && echo "$f: suspicious imports"
                 done

      False-positive guards:
        - Integration tests that legitimately drive the unit through
          its stdlib-typed interface (e.g., `http.Handler` tests).
          Cite the unit's type-assertion or constructor call.
        - Boundary tests that exist specifically to assert stdlib
          interop contracts. Cite the spec section that requires the
          interop.

      Waiver: same as Smell 1.

      ## Smell 3 — Sleep-as-fence

      A test that uses time.Sleep (or equivalent) as a wait mechanism
      between phases, creating races. Audit reference:
      `TestLoop_BusyDrops` uses three sleeps; broader pattern is 10+
      across the codebase.

      Per-language detection:
        Go:      grep -rnE 'time\.Sleep\(' \
                   --include='*_test.go' .
        Python:  grep -rnE '(time|asyncio)\.sleep\(' \
                   --include='test_*.py' --include='*_test.py' .
        JS/TS:   grep -rnE '(await sleep\(|setTimeout\(|new Promise.*setTimeout)' \
                   --include='*.test.*' --include='*.spec.*' .
        Rust:    grep -rnE '(thread::sleep|tokio::time::sleep|std::thread::sleep)' \
                   --include='*.rs' -- $(git ls-files | grep -E '(_test\.rs|/tests/)')
        Java/Kotlin: grep -rnE '(Thread\.sleep|delay\()' \
                   $(find . -type d \( -name test -o -name tests \) 2>/dev/null)

      Each hit needs disposition: either (a) replaced with a
      deterministic sync primitive (sync.WaitGroup, channel, event,
      condition variable, mock clock advance), OR (b) justified with a
      one-sentence rationale that the test asserts timing behavior
      itself (e.g., "test verifies rate-limit backoff timing — the
      sleep IS the SUT").

      False-positive guards:
        - Tests of rate limiters, backoff strategies, timeouts, retry
          delays — the sleep is the contract, not a fence. Cite the
          spec section calling out the timing.
        - Tests that explicitly advance a mock clock by `time.Sleep`
          equivalent (rare; cite the mock-clock injection).

      Waiver: same as Smell 1.

      ## Smell 4 — Unanchored snapshots

      A golden/snapshot test where the expected value is the most-recent
      run's output rather than a value anchored to SPEC.md. Detection:
      golden files regenerated via `UPDATE_GOLDEN=1`,
      `--update-snapshots`, or similar without manual spec-anchored
      review.

      Skip this smell vacuously if the project has no snapshot files.
      Detection of snapshot framework:
        find . -type d \( -name __snapshots__ -o -name testdata \) 2>/dev/null
        git ls-files | grep -E '\.(golden|snap|snapshot)$'
      If neither returns hits, skip with one-line note.

      Per-language detection (when snapshots exist):
        Go:      find . -name '*.golden' -o -name '*.snapshot' | head -50
                 grep -rnE 'os\.Getenv\("UPDATE_GOLDEN"|"-update"' \
                   --include='*_test.go' .
        JS/TS:   find . -name '*.snap' -o -path '*/__snapshots__/*' | head -50
                 # `jest --updateSnapshot` / vitest `update-snapshots` are
                 # default-allowed; the rubric checks anchoring, not the
                 # update mechanism.
        Python:  find . -name '*.snapshot' -o -path '*/.pytest_snapshot/*' | head -50

      Anchoring check — for each snapshot file, the contents MUST
      contain at least one literal also present in SPEC.md (or the
      project's spec equivalent). The agent runs:
        for snap in $(git ls-files | grep -E '\.(golden|snap|snapshot)$'); do
          # extract distinctive tokens from the snapshot, grep SPEC.md
          # for at least one of them
        done

      False-positive guards:
        - Snapshots of generated artefacts (e.g., compiler output,
          binary representations) that have no human-readable spec
          anchor — cite the spec section that names the generation
          contract.
        - Snapshots whose entire purpose is byte-stability
          (e.g., serialization-format tests) — cite the format spec.

      Waiver: same as Smell 1.

      ## Smell 5 — Impossible-to-collide subnames

      Duplicate subtest names that case-fold or normalize to the same
      identifier, masking coverage gaps. Audit example: `bytes_trimSpace`
      declared alongside `bytes.TrimSpace` — Go's t.Run name normalizer
      treats them as duplicates.

      Per-language detection:
        Go:      grep -rnE 't\.Run\("' --include='*_test.go' . \
                   | sed -E 's/.*t\.Run\("([^"]+)".*/\1/' \
                   | tr '[:upper:]' '[:lower:]' \
                   | tr ' .' '__' \
                   | sort | uniq -d
                 Any hit is a subtest-name collision under Go's
                 normalizer (which lowercases and replaces space + dot
                 with underscore).
        Python (pytest): grep -rnE 'pytest\.param\(' --include='*.py' .
                 # ids parameter; check for case-fold dupes
                 grep -rnE '@pytest\.mark\.parametrize.*ids=' \
                   --include='*.py' .
        JS/TS:   grep -rnE '(describe|it)\("' --include='*.test.*' . \
                   | sed -E 's/.*(describe|it)\("([^"]+)".*/\2/' \
                   | tr '[:upper:]' '[:lower:]' \
                   | sort | uniq -d

      Any hit → FAIL. The shared parent test is the unit; cite both
      colliding lines.

      False-positive guards:
        - Intentionally repeated subtest names across DIFFERENT parent
          tests (Go's normalizer scopes by parent) — only collisions
          within the same parent are FAIL.

      Waiver: same as Smell 1.

      ## Smell 6 — DI bypass

      A test that bypasses an injectable seam in favor of real
      time / network / I/O / random. Audit example: tests call
      `time.Now()` directly when a `Clock` seam exists, so test
      behavior depends on wall clock.

      Detection — two steps:
        1. Find injectable seams in production code.
           Go:    grep -rnE 'type [A-Z][A-Za-z0-9_]* +interface' \
                    --include='*.go' . \
                    | grep -vE '_test\.go|testdata'
                  Look for interfaces declaring methods like Now(),
                  Read(p []byte), Random(), Get(), Lookup(), etc.
                  Also look for parameter types like `clock Clock`,
                  `rand io.Reader`, `now func() time.Time`.
           Python: grep -rnE 'class .+\(Protocol\)' --include='*.py' .
                  Look for protocols with timestamp / rand / IO methods.
           TS:    grep -rnE '(interface|type) [A-Z][A-Za-z0-9_]* *=' \
                    --include='*.ts' .
                  Look for interface members typed as time / random /
                  IO functions.
        2. For each detected seam, grep tests for direct use of the
           bypassed stdlib symbol:
           Go:    grep -rn 'time\.Now\(' --include='*_test.go' .
                  grep -rn 'rand\.Read\(' --include='*_test.go' .
                  grep -rn 'os\.(Stdin|Stdout|Stderr)' \
                    --include='*_test.go' .
           Python:grep -rn 'datetime\.now\|time\.time\(' \
                    --include='test_*.py' --include='*_test.py' .
           TS:    grep -rn '(new Date\(\)|Date\.now\()' \
                    --include='*.test.*' .

      Skip if step 1 returns no seams.

      For each step-2 hit in a test file whose unit (under test) has a
      seam, the test MUST be using the seam (e.g., constructing the
      unit with a mock clock and asserting on it) — not bypassing to
      the stdlib. Bypass without justification → FAIL.

      False-positive guards:
        - Tests of the seam itself (e.g., the mock clock's tests use
          real time briefly to verify monotonic behavior) — cite the
          seam-implementation file.
        - Tests where wall-clock dependence is intentional (e.g.,
          benchmarks, end-to-end tests marked `//go:build e2e`) — cite
          the build tag or category.

      Waiver: same as Smell 1.

      ## Known limitations — skip with one-line note, not FAIL

      Some patterns are fundamentally outside grep's reach. Skip these
      with a one-line note rather than flagging:
        - Test bodies generated by macros (Rust `proc_macro`, Scala
          implicit generation) — cite the macro and skip.
        - Property-based tests where the assertion is implicit in the
          property function (QuickCheck, Hypothesis, fast-check) —
          cite the property definition.
        - Behavior-driven tests where assertions are encoded as Gherkin
          steps (Cucumber, behave) — cite the step library.
        - Tests in build-system DSLs (Bazel `cc_test`, Bash testing
          frameworks) — cite the framework's assertion mechanism.

      When you skip, name the specific reason. Don't blanket-skip an
      entire test file as "non-standard framework."

      RUBRIC_EOF
```

### 5.2 `FinalSpecCheck` — new section between INTERFACE REACHABILITY and SPEC.md compliance

```dip
      TEST QUALITY (issue #233 Gap 8):

      Read `.ai/build/test-quality-rubric.md` FIRST (it was written by
      Setup at the start of this run; Read the file). It contains the
      test-presence detection, six numbered smell sections (zero-
      assertion, wrong-target, sleep-as-fence, unanchored snapshot,
      impossible-to-collide subname, DI bypass), per-language detection
      grep commands, false-positive guards per smell, and waiver rules.
      Apply each smell section across the entire repo (NOT just last-
      milestone diff).

      For each of the six smells:
        - If the smell skips vacuously (e.g., no snapshot framework
          detected for Smell 4, no injectable seams for Smell 6), say
          so explicitly with the one-line skip note from the rubric.
        - Otherwise, run the detection grep(s) per the rubric. Paste
          the grep command and its output. For each hit, give a
          disposition: smell confirmed (FAIL), false-positive with
          one-sentence reason citing the rubric's guards, or waived
          with `.ai/decisions/*.md` citation.

      Output is prose enumeration. Do NOT group, do NOT write "ditto"
      or "similar," do NOT skip individual hits. If you run out of
      context mid-enumeration, stop and leave the early `STATUS:fail`
      from your response's first line in place; you may add a separate
      line stating which smell you reached. Do NOT emit
      `STATUS:fail <smell>` or `STATUS:fail mid-survey` — the parser
      requires the STATUS value to be exactly `fail`, `success`, or
      `retry`, with no trailing prose on that line.

      Regression cases this check exists to catch (issue #233 Appendix
      A): zero-assertion tests on `goblin.start`; wrong-target test
      `TestRun_SignalHandling` (W5); three sleeps in `TestLoop_BusyDrops`
      (W17); `bytes_trimSpace`/`bytes.TrimSpace` subtest collision
      (W21); `time.Now()` calls bypassing the Clock seam (W13). All
      shipped green because this check didn't exist.
```

### 5.3 Reviewer rubric point 3 — single-sentence upgrade × 3 prompts

Append to existing point 3 in all three reviewer prompts (ReviewClaude lines 826-831, ReviewCodex lines 873-878, ReviewGemini lines 931-938) — keep their existing prose intact, including the Gemini-only "Tests that only validate standard-library or third-party-library behavior ... are FAIL" sentence, and add the following five lines to each:

```text
   Also: for every test file under this section, show grep evidence
   that the tests do NOT exhibit any of the six smells listed in
   `.ai/build/test-quality-rubric.md` (zero-assertion, wrong-target,
   sleep-as-fence, unanchored snapshot, impossible-to-collide subname,
   DI bypass). Apply the same show-your-work standard as SPEC LITERALS
   at point 1.
```

This is 6 added lines × 3 reviewers = 18 lines total. Each reviewer's point 3 grows from ~6 lines to ~12; comparable to point 2 (INTERFACE REACHABILITY) which is ~9 lines. PR #249's balance is preserved within rounding.

### 5.4 `VerifyMilestone` — no change

v1 does not modify `VerifyMilestone`. Its current 7 checks stand. The single-owner architectural decision (§4.1) makes per-milestone test-quality unnecessary; `FinalSpecCheck` handles it.

## 6. Build and verification

For each session working on this PR:

1. Edit `examples/build_product.dip` — three sections change: `Setup` (add rubric-file write block), `FinalSpecCheck` (add TEST QUALITY section between iface and spec-compliance), reviewer rubric point 3 in `ReviewClaude` / `ReviewCodex` / `ReviewGemini`.
2. `make sync-workflows` — stage both files.
3. `dippin doctor examples/build_product.dip` — must remain A grade with no new warnings.
4. `dippin simulate -all-paths examples/build_product.dip` — confirm every path still terminates.
5. `tracker validate examples/build_product.dip` — clean.
6. `go build ./... && go test ./... -short` — all 17 packages.
7. `make check-workflows` — sync confirmed.
8. **Update `CHANGELOG.md`** — add a Gap 8 entry under the next `[Unreleased]` section, matching the pattern of PR #246, PR #249, PR #254.

## 7. Risk register

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| `FinalSpecCheck`'s prompt grows long once TEST QUALITY is added on top of INTERFACE REACHABILITY + SPEC.md compliance | Medium | Medium — `dippin doctor` may warn | Discipline lives in `.ai/build/test-quality-rubric.md`. The prompt section is ~18 lines. If `doctor` still warns, peel more of the existing FinalSpecCheck content (the SPEC.md line-by-line check) into a shared file too. |
| Agent runs out of context mid-enumeration on a repo with hundreds of test files | High | High — without §4.3's fail-closed default, would silently pass | §4.3's inverted STATUS contract (PR #254) handles this. Any truncation preserves the early `STATUS:fail`. The agent may emit a progress note on a separate non-STATUS line; the parser rejects trailing prose on a STATUS line. |
| Implement agent pre-authors `.ai/decisions/*.md` waivers for its own future verifier | Medium | Medium — same risk Gap 7 acknowledged | Reviewer rubric point 3 is the next layer; reviewers see all `.ai/decisions/` files and can flag blanket-rationale waivers. `FinalSpecCheck`'s repo-wide sweep reads decision files line-by-line. |
| Smell 2 (wrong-target) is fundamentally semantic and the grep heuristic produces false positives | High | Low — produces a FAIL that EscalateReview can clear | False-positive guards explicitly list legitimate boundary tests. The agent must cite the unit's API call expression; lack of citation = FAIL. EscalateReview's "accept" path closes the loop for justified false positives. |
| Smell 6 (DI bypass) misfires on tests that legitimately use real stdlib (e.g., the seam's own tests) | Medium | Low | Step 1 of detection requires finding a seam first; tests of the seam itself cite the seam-implementation file as the false-positive guard. |
| Sleep-as-fence grep flags legitimate timing tests (rate limits, backoffs) | High | Low | False-positive guards explicitly call out timing-as-contract tests; agent cites the spec section. |
| `.ai/decisions/` waiver text written by the Implement agent gets reused across multiple smells, becoming a de facto blanket | Low | Medium | The waiver discipline requires the waiver to name the SPECIFIC test (not the smell category). A waiver naming "all sleep-fence tests" would fail the non-blanket check. |
| Snapshot-anchoring check (§5.1 Smell 4) is hard to automate — the agent might rubber-stamp it | Medium | Medium | The rubric requires explicit shown work: agent must list each snapshot file, the distinctive token it grepped from SPEC.md, and the SPEC.md file:line where that token lives. No citation = FAIL. |
| Reviewer rubric point 3 upgrade still gets handwaved | Medium | Medium | Adversarial Gemini lane (PR #249) is the existing mitigation. v1 demands shown grep evidence (same standard as point 1 SPEC LITERALS and point 2 INTERFACE REACHABILITY). |

## 8. What was considered and rejected

This is design v1. No prior squad-review iterations exist yet. Provisional alternatives considered and rejected during brainstorming:

**Compact prose-only rubric** — would write a smaller rubric naming the six smells with one example each, no per-language grep patterns. Rejected because the audit explicitly found agents handwave past prose-only checks (the motivating failure for PR #249's shown-work standard). Less drift risk but lower-confidence detection.

**Bifurcated by detectability** — would split mechanical smells (1, 3, 5) and semantic smells (2, 4, 6) into different disciplines. Rejected because (a) different cognitive load per smell increases agent error rate, (b) squad review of Gap 7 v2 flagged the "arbitrary slice" framing in the 6-language enumeration; same shape of risk here.

**New dedicated `TestQuality` node** — would add a separate agent node between `VerifyMilestone` loop exit and `FinalSpecCheck`. Rejected because (a) Gap 7's single-owner argument applies verbatim (terminal-state bugs go in `FinalSpecCheck`), (b) a new node + new auto_status contract + new edges duplicates the existing inverted-STATUS contract for no operational benefit, (c) squad review of Gap 7 v2 rejected this kind of multi-node design as 3-5x over-built.

**Per-milestone `VerifyMilestone` check** — would add the smell check to `VerifyMilestone` so each milestone is gated. Rejected because (a) `VerifyMilestone`'s diff-scoped review creates a cross-milestone leak shape (smell introduced milestone 2, still passing at milestone 5), (b) the workflow is fully automated; catching early in a milestone has no operational value when no human is debugging mid-run, (c) per-milestone gating compounds the LLM-managed file-mutation reliability degradation issue raised in Gap 7's squad review.

**A `golangci-lint`-style mechanical analyzer wrapper** — would shell out to language-specific linters. Rejected because (a) the workflow targets any product, not Go-only, (b) requires the product to already configure a linter, (c) doesn't catch all six smells (`golangci-lint`'s `testifylint` covers ~Smell 1 partial; nothing covers Smells 2, 4, 6).

## 9. References

- Issue #233 — `build_product` audit recap. Appendix A items W4, W5, W13, W17, W21, etc. are the named regression cases.
- PR #246 — closed Gaps 1, 3, 6. Established the `Setup`-writes-shared-helper pattern (`ci-probe.sh`) and the `.ai/decisions/` waiver convention.
- PR #249 — closed Gaps 2, 4. Established the balanced 5-point reviewer rubric.
- PR #254 — closed Gap 7. Established the inverted STATUS contract at `FinalSpecCheck` and the `iface-reachability-rubric.md` shared-discipline-file pattern that this design extends.
- [CLAUDE.md](../../../CLAUDE.md) — repo-level instructions including `make sync-workflows` discipline.

## 10. Open questions

To be answered by the squad review:

1. Is repo-wide test-quality sweep at `FinalSpecCheck` the right scope, or should some smells (e.g., Smell 5 subname collisions) be per-milestone at `VerifyMilestone`? Single-owner argument from Gap 7 v3 applies, but smell 5 is cheap to detect per-milestone.
2. Smell 2 (wrong-target) is the most-semantic smell. Should the rubric drop the per-language grep heuristic and rely on agent judgment alone, OR should it be removed entirely from v1 as unsuitable for grep-shaped detection?
3. Smell 4 (unanchored snapshots) requires the agent to match snapshot tokens to SPEC.md content. Is this reliable across multi-format snapshots (text, binary-like)? Should the rubric require snapshots to live under `.ai/decisions/` with explicit spec-citation, or accept the looser anchoring check?
4. The waiver discipline allows `.ai/decisions/*.md` to override findings. Should the waiver list cap at N entries to prevent waiver-creep, or is the non-blanket-rationale check sufficient?
5. Is the prompt budget at `FinalSpecCheck` (~105 lines after this PR) past the `dippin doctor` warning threshold? If so, should SPEC.md compliance also peel out to a shared file?
