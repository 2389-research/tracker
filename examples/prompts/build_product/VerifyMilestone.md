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
elided entirely. The authoritative success signal is the
`tests-pass` sentinel — TestMilestone prints it when its step
succeeded under the workflow's rules. "Succeeded" means:
the language-stack test runner ran and returned 0 (if one of
`go.mod` / `package.json` / `pyproject.toml` / `Cargo.toml`
was detected) OR no known build system was detected and the
runner was skipped; AND the project CI gate ran and returned 0
(a Makefile `ci`/`check`/`lint` target, OR — when no such target
ran — the language-native gates: go vet/golangci-lint, tsc/eslint,
ruff/mypy, cargo fmt/clippy for each detected toolchain)
OR there was nothing to gate (no recognized toolchain). The
sentinel does NOT prove every check executed —
only that none failed. Treat its presence as authoritative for
"TestMilestone step succeeded"; if a milestone implies tests
must have RUN (not skipped), check the visible probe / test
lines as corroboration when present, but absence is not FAIL
(those lines are routinely elided by the tail cap). The `escalate` sentinel
means TestMilestone gave up (one of two causes: `make` binary
missing, indicated by `ERROR: <Makefile> present but 'make'
not installed`; OR fix attempts exhausted, indicated by the
`ESCALATE: milestone failed after N attempts` line) — either
way the engine's edge from `TestMilestone -> EscalateMilestone
when ctx.tool_stdout contains escalate` should have routed
away from this verifier; if you see `escalate` here, the
routing missed and you should surface the cause explicitly.
Visible CI probe lines (`--- running make ---`, `INFO: ...`)
corroborate but are not required.

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

6. TESTS + CI PASSED: The `tests-pass` sentinel in the
   "TestMilestone stdout" fenced block at the END of this
   prompt is authoritative for "TestMilestone step
   succeeded" — TestMilestone prints it when its language-stack
   runner passed-or-was-validly-skipped AND its project CI gate
   passed (a Makefile target OR the language-native gates)
   or had nothing to gate (see the preamble above for the exact
   rules). The sentinel is emitted at
   end-of-command via `printf`, and `tool_stdout` keeps the
   tail of stdout, so the sentinel survives truncation by
   construction — its presence is reliable, its absence is
   not "routine elision".
   TestMilestone has THREE terminal paths (see the tool node
   body for the exact branches):
     (i)  TEST_EXIT==0 → `printf 'tests-pass'` + exit 0
          (success — routes to VerifyMilestone)
     (ii) CI_RC==2 (`make` binary missing — short-circuits
          BEFORE the TEST_EXIT / ATTEMPTS logic and resets
          ATTEMPTS to 0)
          OR
          (TEST_EXIT!=0 && ATTEMPTS>3) (fix loop exhausted)
          → `printf 'escalate'` + exit 1 (routes to
          EscalateMilestone)
     (iii) TEST_EXIT!=0 && ATTEMPTS<=3 → NO marker + exit 1
          (normal in-progress failure — routes to FixMilestone;
          a failing language-native gate folds into
          TEST_EXIT here, so it routes the same way)
   Under normal routing only path (i) reaches VerifyMilestone,
   so the contract for THIS node is: "if you see me, the
   upstream tool emitted `tests-pass`." The three sentinel
   cases the verifier should distinguish:
     - `tests-pass` present → PASS, no further check required.
     - `escalate` present → FAIL, with one of two causes:
         * `ERROR: <Makefile> present but 'make' not installed`
           = env-missing (operator must install `make`).
         * `ESCALATE: milestone failed after N attempts` =
           the fix loop exhausted (milestone genuinely broken;
           prior attempts didn't converge).
       Either cause should have been caught upstream by the
       `TestMilestone -> EscalateMilestone when ctx.tool_stdout
       contains escalate` edge; if you see `escalate` here,
       the routing missed — name the specific cause from the
       surrounding lines.
     - Neither sentinel present → ANOMALY. STATUS:fail and
       surface explicitly; do NOT silently treat as passing.
       The path-(iii) "normal failure" branch above also emits
       no sentinel, but its `ctx.outcome = fail` should route
       to FixMilestone — never to VerifyMilestone. So missing
       sentinels here mean ONE of:
         * routing bug: a failing path-(iii) TestMilestone run
           reached VerifyMilestone instead of FixMilestone
           (rare — surface "TestMilestone failed but
           outcome-routing landed me here; investigate
           success-edge condition").
         * output-capture problem: tool crashed mid-stream
           before printf, `output_limit` misconfigured below
           the ~12-byte sentinel length, or the tail buffer
           was lost (surface "TestMilestone produced no
           terminal sentinel — output capture or script
           integrity is broken").
   Visible CI probe lines (`--- running make ---`, `INFO: ...`)
   and language-test output are corroborating signals, not
   required. The 64KB tail cap CAN elide earlier verbose
   language-test output on a large repo, but never the
   end-of-command sentinel. Do NOT FAIL on absence of visible
   language-test output — the sentinel covers that signal.

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

If every check is PASS or WARN: STATUS:success (list any WARNs with
their ADR + passing-test citations so the operator can audit them).
If any check is FAIL, describe exactly what failed (with the
file:line, grep output, or failing assertion) and: STATUS:fail

---
## TestMilestone stdout

Tail of TestMilestone's stdout, 64KB cap. Delimited here under its
own heading — never interpolated mid-sentence.

${ctx.tool_stdout}