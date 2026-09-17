Review the implementation against SPEC.md. Your PRIMARY read is the
cumulative base..worktree review diff at .ai/build/review-diff.md — it
captures every tracked-file change this build produced. Newly-created
UNTRACKED files are listed there by path only; their contents are NOT in
the diff, so open those files directly. Read the rest of the full working
tree ON DEMAND only when a finding needs surrounding context (it is
available, not mandated). The build loop has
finished — either every planned milestone completed, or the operator chose
to stop early (the "accept" gate) and ship what exists. Review what is
actually present; do not assume unfinished work will arrive later.

DO NOT TRUST SPEC ✅ MARKERS:
- SPEC.md may contain ✅ or "Done" checkmarks. These were the author's
  plan, not evidence of completion. Treat them as nothing. Verify each
  requirement against the code yourself.

RUBRIC — for every spec section, answer these seven questions in
.ai/build/review-claude.md, in order, with PASS or FAIL per question
and concrete evidence (grep output, file:line, snippet) for any FAIL:

1. SPEC LITERALS: For every literal value in this section — exact
   command strings, exact JSON key names, exact header names, exact
   integer constants, exact log key names — does the code contain it
   byte-for-byte? Show grep evidence. (E.g., spec says
   `--head <owner>:<branch>`; grep the code for that literal.)
2. INTERFACE REACHABILITY: For every interface method defined in
   this section's files, name at least one production (non-test)
   caller with grep evidence — show the grep command, paste its
   output, cite the file:line. Apply the same show-your-work
   standard as SPEC LITERALS at point 1. "Looks reachable" /
   "exported, so something must call it" without a file:line hit
   is FAIL. Follow the discipline in
   `.ai/build/iface-reachability-rubric.md` (test-file exclusions,
   stdlib carve-out, waiver rules, known limitations).
3. TEST VERIFIES CONTRACT: For each assertion in a test under this
   section, does it verify the spec's contract, or does it mirror
   what the implementation happens to produce? Snapshot tests
   regenerated from current output are FAIL. Tests asserting
   `attempts == 2` when the spec says "max 2 retries" (= 3 attempts)
   are FAIL. Tests that only validate standard-library or
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
   for? Phase 2+ features built in a Phase 1 milestone are FAIL.
5. ARCHITECTURE & LEFTOVERS: Were the spec's technical-guidance
   sections followed? Are leftover artifacts from removed code gone?
6. SPEC-EMITTED-VALUE ASSERTIONS: For every value the spec prescribes
   that the code EMITS — a log event name, an error code, a status
   string (e.g. a skip reason like "no_branch"), a wire/JSON value —
   find a test whose ASSERTED EXPECTED value is the SPEC LITERAL
   itself, not a variable that references the production constant.
   `assert(emitted == prod.Constant)` is FAIL — it ratifies whatever
   the production constant happens to be, so a wrong constant ships
   green. `assert(emitted == "review.skip")` (the spec literal,
   hard-coded in the test) is PASS. Cite the assertion file:line and
   quote the asserted expected value. A spec-emitted value with no
   test asserting the literal is FAIL. This is distinct from point 1
   (which checks the literal exists in the code); point 6 checks a
   TEST pins it to the spec literal independent of the production
   constant.
7. CONTRACT-FIDELITY AT EXTERNAL SEAMS: For every EXTERNAL SEAM — an
   LLM provider adapter, a VCS-host CLI invocation (e.g. `gh`), or a
   subprocess whose argument shape or response shape is contractual —
   FAIL when the ONLY test exercising that seam uses a fake that the
   PRODUCTION code also defines (a fake the code under test ships and
   the test reuses ratifies the code's own assumptions, proving
   nothing about the real seam). Cite the seam (file:line of the
   production call) and the test path. A passing seam requires EITHER
   a recorded/golden REAL-provider response (cite the fixture) OR a
   CI-reachable REAL-CLI invocation (cite the test that shells the
   actual tool). Detection keys on the seam's ROLE (crosses a process
   or provider boundary whose arg/response shape is a contract) —
   never on a specific provider, tool, or language.

Your lane emphasis (after the rubric): missing requirements,
architectural violations, leftover artifacts. Cite spec section
and file:line for every finding.

Write the report to .ai/build/review-claude.md.