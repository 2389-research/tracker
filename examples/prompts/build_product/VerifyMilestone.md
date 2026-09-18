STATUS contract — emit `STATUS:fail` as the FIRST line of your
response, before any other text. Then run the checks below. Only
at the very end, after every check is PASS or WARN, emit a final
`STATUS:success` line — alone on its line, outside any code
fence — to override the early fail. The workflow's `auto_status`
parser is last-line-wins; if your response is truncated for any
reason the early `STATUS:fail` remains and the gate fails closed.
The STATUS value must be exactly `fail` or `success`, with no
trailing prose on that line. Never emit STATUS:retry (this node
has no retry route).

FIRST, in one turn, read .ai/build/build-context.md for orientation — it
holds an architecture map and a one-entry-per-milestone log (files touched
+ commit summary) so you don't rediscover the codebase layout and prior
milestones' changes from scratch. It is ADVISORY and may lag the latest
code: SPEC.md and the source are authoritative — if they disagree with the
context file, trust them. It supplements your normal reading; it does not
replace any SPEC.md read this prompt asks for below.

Read the current milestone spec at .ai/milestones/current.md.
Read SPEC.md. An earlier version of this verifier only read
.ai/milestones/current.md — if Decompose dropped a requirement
during milestone planning, or a milestone was approved with
incomplete criteria, the verifier had no path to discover the
gap. Reading SPEC.md directly closes that loop.

TestMilestone already handled the language-stack tests AND the
project CI gate, so the verifier does NOT
need to re-run them. Note that the "TestMilestone stdout" fenced
block at the END of this prompt is the
tail of TestMilestone's stdout (64KB cap by default); on a
repo with verbose test output the language-test lines may be
elided entirely. TestMilestone ends its stdout with exactly ONE of
three terminal markers (printed via `printf` at end-of-command, so
it survives the tail cap by construction):
  - `tests-pass` — GREEN. A REAL oracle ran and passed: a language
    suite executed a POSITIVE number of tests (a green `go test` over
    zero test files is compilation, not verification), or the
    project's own CI target (a Makefile `ci`/`check`/`lint`/`test`
    target) returned 0. "Passed" means ALL of:
      (a) every build stack detected ANYWHERE in the tree (each
          `go.work` / `go.mod` / `package.json` / `pyproject.toml` /
          `Cargo.toml`, excluding node_modules/, vendor/, .ai/,
          testdata/) built and its test runner returned 0, run in that
          stack's own directory — a `=== stack: <kind> in <dir> ===`
          header per stack (a Makefile `ci`/`check`/`lint`/`test`
          target counts as a stack);
      (b) the project CI gate returned 0: the Makefile
          `ci`/`check`/`lint` target, when one exists.
    The language-native lint gates (go vet/golangci-lint, tsc/eslint,
    ruff/mypy, cargo fmt/clippy) still RUN and their output is shown,
    but they are ADVISORY: a native-lint failure does not turn the
    gate red, and a native-lint pass does NOT count as verification
    of anything — only an executed test suite or the project's own CI
    target does. Treat visible native-lint findings as context for
    check 7 (unrequested churn) and for the operator, never as
    evidence that a done-when is met.
  - `tests-not-yet-verifiable` — verify.sh exited 3: NO runnable
    oracle exists. No build stack was detected, or every detected
    suite executed zero tests and there is no CI target — nothing
    could have caught a regression. This is DENY-BY-DEFAULT: it is
    NEVER a green. Decide from the milestone's done-when. If ANY
    done-when criterion implies testable code, or the milestone added
    source a test could exercise, emit STATUS:fail so the fix loop
    ADDS the tests / test packaging (a go.mod, a package.json test
    script, a pyproject, a Makefile `test` target) — the work is
    already committed, so a fail here REFINES the milestone rather
    than discarding it. Emit STATUS:success only for a milestone that
    is genuinely verification-free (docs-only, scaffolding the plan
    explicitly marks test-free), and justify that from the done-when
    verbatim. A `NOTE: no build system detected — nothing was tested
    this milestone` line, when visible, corroborates this marker (the
    ship gate FAILS on it). A `NOTE: ... operator opt-out stamp is
    present` line means an operator silenced the test gate for the
    whole project — a FAIL finding unless the project is genuinely
    test-free.
  - `__ROUTE_ESCALATE__` — TestMilestone gave up: an environment
    problem the fix loop cannot solve (e.g. a Makefile present but
    `make` not installed — `_TRACKER_CI_MAKE_MISSING` / `ESCALATE:
    environment problem`; EnsureEnv normally catches this BEFORE
    TestMilestone), or the fix budget is exhausted (`ESCALATE:
    milestone failed after N attempts`). Either way the engine's
    `TestMilestone -> EscalateMilestone` edge on this marker should
    have routed AWAY from this verifier; if you see
    `__ROUTE_ESCALATE__` here the routing missed — STATUS:fail and
    name the specific cause from the surrounding lines.
  - NONE of the three present → ANOMALY → STATUS:fail (see check 6).
The `tests-pass` marker does NOT prove every check executed —
only that none failed. Treat its presence as authoritative for
"TestMilestone step succeeded"; if a milestone implies tests
must have RUN (not skipped), check the visible probe / test
lines as corroboration when present, but absence is not FAIL
(those lines are routinely elided by the tail cap). These lines,
when visible, ARE findings:
  - `NOTE: no Go test files in scope (...)` — the milestone's Go
    scope ran zero tests; a green build proves only compilation.
    Under the positive-executed-test rule this normally surfaces as
    `tests-not-yet-verifiable`, but if it is visible next to
    `tests-pass` (another stack or the CI target supplied the
    oracle) it is still a finding: FAIL unless the milestone's
    done-when needs no Go tests (docs-only, scaffolding the
    milestone plan explicitly marks test-free).
  - `WARNING: .ai/build/<verify.sh|ci-probe.sh> differed from the
    workflow's lib/... and was RESTORED` — something in the workdir
    rewrote the green-gate script; TestMilestone restored it before
    running. FAIL (self-ratification): name the file and look for
    the edit in the diff / agent transcript.
  - `known_failures: entries ADDED since milestone start` /
    `known_lint_failures: entries ADDED since milestone start`
    (followed by `  + <entry>` lines) — a test or lint rule was
    added to an escape hatch AFTER PickNextMilestone snapshotted
    the hatches at milestone start (i.e. during Implement/Fix).
    FAIL unless justified: an operator added it via
    EscalateMilestone's "mark a known failure, then retry" (say
    so), or the milestone plan (.ai/decisions/milestones.md) names
    that test as expected-to-fail until a LATER, named milestone.
    An agent silencing its own red test is never justified.
  - `operator stamp CREATED since milestone start` followed by
    `  + .ai/build/no-tests-ok` — the project-wide test-gate
    opt-out appeared after milestone start. FAIL unless the
    operator states they created it (it was not present when the
    milestone began, so a build session is the likely author).
  - `WARNING: no milestone-start snapshot for ...` — the baseline
    is missing (a resume of an older run); every entry is listed,
    judge each on the plan.
  Note the known_failures skip is Go-only (`go test -skip`, each
  entry anchored per path segment); npm/pytest/cargo ignore the
  file, so a listed non-Go test still fails the gate.
Visible CI probe lines (`--- running make ---`, `INFO: ...`) and
native-lint output corroborate but are not required — and, per the
marker rules above, native-lint output is advisory and never counts
as verification.

VERIFY — work through every item; cite file:line and grep output
for every finding:

1. DONE-WHEN: Every "done when" criterion in the milestone is
   satisfied. List each criterion verbatim and mark it satisfied
   (with the file/test that proves it) or not.

2. FILES IN SCOPE: Only the files listed in the milestone were
   modified. Determine this milestone's commit range — Implement
   commits with messages referencing the milestone (per its
   "Commit your work with a conventional commit message
   referencing the milestone" rule). Run:
     git log --oneline -30
   to see recent commits; identify those for the current
   milestone (by the milestone tag/number in the message), then
   for the OLDEST such commit run:
     git diff --name-only <oldest-milestone-commit>^..HEAD
   Paste the resulting file list inline. If you cannot reliably
   identify the milestone's commits this way (e.g., no
   conventional-commit references), fall back to:
     git log --oneline -10 --stat
   and review the file lists manually. Files outside the
   milestone's declared list are FAIL.

3. SPEC LITERALS: For every literal value in
   the spec sections this milestone covers — exact command
   strings, exact JSON key names, exact header names, exact
   integer constants, exact log key names — grep the
   implementation for that literal and write the grep command
   and its result inline. A missing literal resolves to one of
   three severities (see the SEVERITY TIERS rule below); decide
   per literal and cite your evidence:
     - FAIL (blocking): the literal is a CONTRACT value — a wire
       format, a JSON key, a header name, a numeric constant, an
       exact command string — where a paraphrase changes observable
       behavior. Missing + no covering ADR = FAIL. Silent paraphrase
       (`--head <branch>` for `--head <owner>:<branch>`) is the
       canonical ship-wrong; stays FAIL.
     - WARN (non-blocking): the literal is an ILLUSTRATIVE API
       identifier named in spec PROSE (e.g. `signal.NotifyContext`
       when the code uses `signal.Notify` + a manual channel) AND
       the milestone's own behavioral tests prove the implemented
       form is equivalent (the relevant tests are green) AND a
       `.ai/decisions/` ADR documents the deviation. All three
       hold → WARN, not FAIL: cite the ADR path, the passing test,
       and why the behavior is equivalent. (Run 634a2527ff56 hard-
       FAILed behaviorally-correct signal handling on exactly this
       prose-identifier mismatch and triggered the fix loop that
       collapsed the run.)
     - PASS: the literal is present byte-for-byte, OR missing but an
       ADR names it AND it is a contract value the ADR justifies a
       genuine substitution for.
   Concretely: when a literal is missing, before deciding run
     grep -rn '<literal>' .ai/decisions/
   to look for a documented deviation, and check whether the
   milestone's behavioral tests for that area are green. Surface the
   decision-log path + rationale in your report so the operator can
   audit it. No ADR (or failing behavioral tests) for a deviated
   identifier ⇒ FAIL.
   Example workflow:
     - Spec section says: `gh pr list --head <owner>:<branch> --json number,headRefName,headRepository`
     - Run: `grep -rn '<owner>:<branch>' --include='*.go' .`
     - Run: `grep -rn 'headRepository' --include='*.go' .`
     - If output is empty, run `grep -rn '<owner>:<branch>' .ai/decisions/`
       and `grep -rn 'headRepository' .ai/decisions/` — a hit
       with a rationale on a contract value means PASS (cite the
       decision file); an ADR on a behaviorally-verified PROSE
       identifier means WARN; no hit means FAIL (cite the missing
       literal).

4. TEST-VERIFIES-CONTRACT: For each test
   touched by this milestone, read its assertions and ask: "If
   the production code were deleted and rewritten differently
   but spec-conformantly, would this assertion still pass for
   the right reason?" If no — the test mirrors the implementation
   rather than verifying the spec — that test is FAIL. The
   canonical pattern: a test asserting `attempts == 2` when the
   spec says "max 2 retries" (= 3 attempts) is FAIL. A test
   asserting a populated field that the spec marks as deferred
   (a "DO NOT implement" entry from this milestone) is FAIL.
   TEST SHAPE: when the done-when names a concrete test shape — a
   "built binary" smoke test, a "subprocess", a "real `gh`", an
   end-to-end run through `main()` — the test must exercise that
   exact harness. A "built binary" smoke contract requires
   spawning the built binary (compile + run the process); an
   in-process call to an unexported function is a weaker reading
   that does NOT prove the wiring and is FAIL. This is the SAME
   rubric the Implement prompt now self-applies before DONE, so a
   test reaching here should already satisfy it — if it does not,
   name the shape the done-when required and the weaker shape the
   test used.

5. SPEC GAPS BEYOND MILESTONE NOTES: For every SPEC.md bullet
   intersecting this milestone's file list (whether or not the
   milestone notes mention it), is it satisfied? An earlier version of the
   verifier only checked .ai/milestones/current.md's done-when
   criteria — so a SPEC.md bullet that Decompose dropped
   silently passed through. Cross-check against SPEC.md directly.
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
   .ai/decisions/requirement-coverage.md (written by Decompose)
   when present; if it disagrees with a live SPEC.md re-read, SPEC.md
   wins.

5b. BEHAVIORAL CONTRACTS (issue #306): Read
   .ai/decisions/behavioral-contracts.md. For every contract whose
   SPEC.md guarantee intersects THIS milestone's file list,
   disposition it with concrete evidence — the same show-your-work
   bar as the SPEC LITERALS grep in check 3: run the contract's
   stated verification method (the named test or the grep) and paste
   the command + its result inline. A contract that is in-scope for
   this milestone but left undispositioned (no test/grep evidence
   shown) is FAIL. If SPEC.md was contradictory in this milestone's
   area, confirm a ruling exists in .ai/decisions/spec-ambiguities.md
   and the implementation follows it; an absent-but-needed ruling is
   FAIL. Contracts whose guarantee does not touch this milestone's
   files are out of scope here (FinalSpecCheck dispositions the full
   set) — say so, do not invent evidence.

6. TESTS + CI PASSED: The terminal marker in the "TestMilestone
   stdout" fenced block at the END of this prompt is authoritative
   for what TestMilestone concluded (see the preamble above for
   the exact rules). `tests-pass` is printed only when EVERY
   detected stack's runner passed with a real oracle (a positive
   executed-test count, or the project's own CI target) AND the
   project CI gate — the Makefile target, if any — passed; the
   language-native lint gates are advisory and do not gate green.
   The marker is emitted at end-of-command via `printf`, and
   `tool_stdout` keeps the tail of stdout, so it survives
   truncation by construction — its presence is reliable, its
   absence is not "routine elision".
   TestMilestone has FOUR terminal paths (see the tool node body
   and .ai/build/verify.sh for the exact branches):
     (i)   verify.sh exit 0 → `printf 'tests-pass'` + exit 0
           (green — routes to VerifyMilestone)
     (ii)  verify.sh exit 3 → `printf 'tests-not-yet-verifiable'`
           + exit 0 (no runnable oracle — routes to VerifyMilestone
           so YOU decide from the done-when; deny-by-default)
     (iii) .ai/build/ci-make-missing present (`make` binary
           missing — ci-probe.sh prints `_TRACKER_CI_MAKE_MISSING`;
           short-circuits BEFORE the attempt counter and resets
           it to 0; EnsureEnv normally catches this earlier)
           OR
           (verify.sh red && this is the 3rd consecutive red —
           `--- attempt 3 of 3 ---`; the counter is bumped only
           AFTER a completed red verify, and reset to 0 when
           escalating so a retry starts fresh)
           → `printf '__ROUTE_ESCALATE__'` + exit 1 (routes to
           EscalateMilestone)
     (iv)  verify.sh red && attempt 1 or 2 → NO marker + exit 1
           (normal in-progress failure — routes to FixMilestone)
   Under normal routing only paths (i) and (ii) reach
   VerifyMilestone, so the contract for THIS node is: "if you see
   me, the upstream tool emitted `tests-pass` or
   `tests-not-yet-verifiable`." The marker cases the verifier
   must distinguish:
     - `tests-pass` present → PASS for THIS check (the NOTE /
       WARNING / ADDED lines in the preamble are still findings).
     - `tests-not-yet-verifiable` present → NOT a green. Apply the
       preamble's deny-by-default rule: FAIL for THIS check when
       any done-when implies testable code or the milestone added
       source a test could exercise (the fix loop must ADD the
       tests / packaging); PASS only for a genuinely
       verification-free milestone, justified from the done-when
       verbatim. Never treat it as `tests-pass` with a caveat.
     - `__ROUTE_ESCALATE__` present → FAIL, with one of two causes:
         * `_TRACKER_CI_MAKE_MISSING` / `ERROR: <Makefile> present
           but 'make' not installed` = env-missing (operator must
           install `make`).
         * `ESCALATE: milestone failed after N attempts` =
           the fix loop exhausted (milestone genuinely broken;
           prior attempts didn't converge).
       Either cause should have been caught upstream by the
       `TestMilestone -> EscalateMilestone when ctx.tool_stdout
       endswith __ROUTE_ESCALATE__` edge; if you see it here,
       the routing missed — name the specific cause from the
       surrounding lines.
     - No marker present → ANOMALY. STATUS:fail and surface
       explicitly; do NOT silently treat as passing.
       The path-(iv) "normal failure" branch above also emits
       no marker, but its `ctx.outcome = fail` should route
       to FixMilestone — never to VerifyMilestone. So a missing
       marker here means ONE of:
         * routing bug: a failing path-(iv) TestMilestone run
           reached VerifyMilestone instead of FixMilestone
           (rare — surface "TestMilestone failed but
           outcome-routing landed me here; investigate
           success-edge condition").
         * output-capture problem: tool crashed mid-stream
           before printf, `output_limit` misconfigured below
           the marker length, or the tail buffer was lost
           (surface "TestMilestone produced no terminal marker
           — output capture or script integrity is broken").
   Visible CI probe lines (`--- running make ---`, `INFO: ...`),
   native-lint output, and language-test output are corroborating
   signals, not required. The 64KB tail cap CAN elide earlier
   verbose language-test output on a large repo, but never the
   end-of-command marker. Do NOT FAIL on absence of visible
   language-test output — the marker covers that signal.

7. NO EXTRA WORK: No unnecessary changes, docs, or refactoring
   outside scope. Walk the diff for files / functions / fields
   the milestone didn't ask for.

SEVERITY TIERS (applies to every finding above):
Tag each finding FAIL, WARN, or PASS.
  - FAIL = a contract or behavioral violation: the milestone does
    not do what the spec requires, or a test does not verify the
    contract, or a CONTRACT literal is wrong. Blocking.
  - WARN = a deviation that is behaviorally correct and documented:
    an illustrative/prose-named identifier differs from the spec
    wording, the milestone's behavioral tests for it are green, AND
    a `.ai/decisions/` ADR records the deviation. Non-blocking —
    show your work (ADR path + passing test) but do NOT fail on it.
  - PASS = satisfied as written.
WARN is reserved for the check-3 SPEC-LITERAL prose-identifier case
ONLY. Scope violations (check 2 / check 7), unmet behavior, and tests
that don't verify the contract (check 4) are FAIL when violated —
never WARN, even with an ADR.
WARN findings are recorded, not gated: a milestone whose only
open items are WARN still passes. Do NOT escalate a WARN to FAIL
just because an identifier didn't match prose — that false-positive
FAIL is what this tier exists to prevent.

If every check is PASS or WARN: emit the final STATUS:success line
(list any WARNs with their ADR + passing-test citations ABOVE it so
the operator can audit them). If any check is FAIL, describe exactly
what failed (with the file:line, grep output, or failing assertion)
and leave the early STATUS:fail standing — do not emit
STATUS:success. If you paste grep or test output, close its code
fence before the terminal STATUS line.

---
## TestMilestone stdout

Tail of TestMilestone's stdout, 64KB cap. Delimited here under its
own heading — never interpolated mid-sentence.

${ctx.tool_stdout}