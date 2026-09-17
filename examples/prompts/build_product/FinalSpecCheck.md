INTERFACE REACHABILITY:

STATUS contract — emit `STATUS:fail` as the FIRST line of your
response, before any other text. Begin your enumeration. Only
at the very end, after every check in this prompt passes
(interface reachability AND test quality AND the SPEC.md
compliance check that follows), emit a final `STATUS:success`
line — alone on its
line, outside any code fence — to override the early fail. The
workflow's `auto_status` parser is last-line-wins, so the final
success replaces the early fail when everything passes; if your
response is truncated for any reason, the early `STATUS:fail`
remains and the section fails closed. Do NOT emit
`STATUS:success` inside a table cell or narrative paragraph;
the parser scans for line-leading STATUS markers only.

Read `.ai/build/iface-reachability-rubric.md` FIRST (it was
written by Setup at the start of this run; Read the file). It
contains the language-detection step, enumeration grep patterns
per language, caller discipline (call-syntax targeting,
receiver-context requirements, test-file exclusion globs),
stdlib/framework carve-out principle, waiver rules, library-API
carve-out, and known-limitation skips. Apply it across the
entire repo. Enumerate every method declared on any interface /
protocol / trait / typeclass / abstract class in the repo (NOT
just last-milestone diff). For each declared method, name a
non-test production caller with grep evidence per the rubric,
OR cite the applicable carve-out:
  - stdlib / framework wiring site
  - `.ai/decisions/library_api.md` entry
  - `.ai/decisions/*.md` waiver with non-blanket rationale
  - known-limitation skip with named reason

Output is prose enumeration — name each method, give file:line
of the production caller (or the carve-out reason). Do NOT
group, do NOT skip, do NOT write "ditto" or "similar." If you
run out of context mid-enumeration, stop and leave the early
`STATUS:fail` from your response's first line in place; you
may add a separate line stating how many methods you reached.
Do NOT emit `STATUS:fail <N>` or `STATUS:fail with count` —
the parser requires the STATUS value to be exactly `fail`,
`success`, or `retry`, with no trailing prose on that line.

Regression cases this check exists to catch: `AuthStatus(ctx) error` defined and
unit-tested but never called from `cmd/goblin/main.go`;
`IsRebaseInProgress() bool` defined and tested but unused in
`internal/review/loop.go`. Both shipped green because this
check didn't exist.

TEST QUALITY — sleep-as-fence:

Grep tracked test files for sleep-class calls. `git grep` is
used throughout so dependency directories (`node_modules/`,
`vendor/`, `.venv/`, `target/`, build outputs) — which are
gitignored by definition — are excluded by construction; only
project test files are checked.
  Go:        git grep -nE '(time\.Sleep|<-time\.After)\(' \
               -- '*_test.go' || true
  Python:    git grep -nE '(time|asyncio|trio|anyio|gevent)\.sleep\(' \
               -- 'test_*.py' '*_test.py' || true
  JS/TS:     git grep -nE '(await sleep\(|setTimeout\(|waitForTimeout\(|cy\.wait\()' \
               -- '*.test.*' '*.spec.*' '*.cy.*' \
                  ':(glob)**/__tests__/**' \
                  ':(glob)**/test/**' \
                  ':(glob)**/tests/**' \
               || true
  Rust:      git grep -nE '(thread::sleep|tokio::time::sleep|async_std::task::sleep)' \
               -- '*_test.rs' ':(glob)**/tests/**/*.rs' || true
             # Catches `tests/` integration tests and `_test.rs`
             # files. Inline `#[cfg(test)] mod tests` blocks in
             # source files aren't filtered here — agent should
             # note them as a limitation if the project relies
             # on the inline-test convention.
  Ruby:      git grep -nE '(^|[^[:alnum:]_])(sleep[[:space:]]+[0-9]|sleep\([0-9]|Kernel\.sleep)' \
               -- '*_test.rb' '*_spec.rb' || true
  Java/Kotlin: git grep -nE '(Thread\.sleep|delay\(|Mono\.delay)' \
               -- ':(glob)**/src/test/**/*.java' \
                  ':(glob)**/src/test/**/*.kt' || true
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
caught by reviewer rubric point 3 (see ReviewClaude /
ReviewCodex / ReviewGemini) — not detected here. W21
(subname collision) is out of scope.

BEHAVIORAL CONTRACTS (issue #306):

Read .ai/decisions/behavioral-contracts.md and
.ai/decisions/spec-ambiguities.md (both written by ReadSpec). For
EVERY behavioral contract, disposition it with concrete evidence —
the same show-your-work bar as the interface-reachability and
spec-literal checks: run its stated verification method (the named
test or the grep) and paste the command + result. An undispositioned
contract (no evidence shown) keeps the early STATUS:fail in place.
For every ambiguity ruling, confirm the shipped code follows the
ruling (cite file:line); a ruling the code contradicts, or a
contradiction with no ruling, keeps the early STATUS:fail in place.
A "rejects at startup, not at runtime"-class guarantee must be
dispositioned with the startup-path test/grep its verification method
names — not skipped as untestable.

SPEC-EMITTED-VALUE ASSERTIONS (issue #417): For every spec-prescribed
EMITTED value (log event name, error code, status string), confirm a
test asserts the SPEC LITERAL as a hard-coded expectation — NOT a
variable referencing the production constant. A test of the form
`assert(emitted == prod.Constant)` does not count: it ratifies the
constant rather than pinning it to the spec, so a wrong constant
ships green. Cite the asserting file:line and the quoted expected
value. A spec-emitted value with no literal-asserting test keeps the
early STATUS:fail in place.

CONTRACT-FIDELITY AT EXTERNAL SEAMS (issue #416): For every external
seam (LLM provider adapter / VCS-host CLI like `gh` / subprocess
whose arg or response shape is contractual), FAIL when the only test
exercising it uses a fake the production code also defines. Require a
recorded/golden real-provider response OR a CI-reachable real-CLI
invocation; cite the seam file:line and the test path. An
undispositioned external seam keeps the early STATUS:fail in place.

This is the final gate. Read SPEC.md line by line.

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
  in place — do NOT emit the terminal STATUS:success.

Also verify:
- No UNEXPECTED leftover files in .ai/build/ — the workflow
  intentionally writes, and you must NOT flag, any of:
  the green-gate helpers (verify.sh, ci-probe.sh,
  iface-reachability-rubric.md); the budget counters
  (spec_forge_attempts, review_fix_attempts); the milestone
  bookkeeping (milestone-start-sha, declared-files.raw,
  declared-files.list, scoped-milestones.md); the per-node
  build-context file (build-context.md); the run base marker
  (run-base-sha) and cumulative review diff (review-diff.md);
  and the reviewer reports (review-claude.md, review-codex.md,
  review-gemini.md). Only flag files outside this explicit
  allowlist. (.ai/decisions/*.md workflow artifacts —
  spec-analysis, milestones, requirement-coverage, compliance,
  review-synthesis, spec-forge-log, SPEC.original — and the
  .ai/milestones/ loop state (current.md, done/, fix_attempts,
  verify_fail_attempts, known_failures, known_lint_failures)
  are expected outputs, never "extras".)
- No TODO/FIXME/HACK comments added
- All "throw away" items from the spec were actually removed
- Tests pass (check the "FinalBuild stdout" fenced block at the
  END of this prompt)

Write the final compliance report to .ai/decisions/compliance.md

Emit `STATUS:success` as your terminal line ONLY if INTERFACE
REACHABILITY (every method has a production caller or
carve-out), TEST QUALITY (every sleep-fence hit dispositioned),
BEHAVIORAL CONTRACTS (every contract dispositioned with evidence,
every ambiguity ruling followed), SPEC-EMITTED-VALUE ASSERTIONS
(every emitted value pinned to its spec literal by a test),
CONTRACT-FIDELITY AT EXTERNAL SEAMS (every seam backed by a golden
real response or a real-CLI invocation, not a self-defined fake),
and SPEC.md compliance (every requirement implemented, no
extras, no TODO/FIXME/HACK, tests pass, no unexpected files
in .ai/build/ outside the allowlist enumerated above) all
passed. Otherwise the early `STATUS:fail` from your first
line stays in place. The parser requires `STATUS:<value>` on
its own line, no leading characters, no fences/tables.

---
## FinalBuild stdout

Tail of FinalBuild's stdout, 64KB cap. Delimited here under its
own heading — never interpolated mid-sentence.

${ctx.tool_stdout}