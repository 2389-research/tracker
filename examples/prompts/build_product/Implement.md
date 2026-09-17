FIRST, in one turn, read .ai/build/build-context.md for orientation — it
holds an architecture map and a one-entry-per-milestone log (files touched
+ commit summary) so you don't rediscover the codebase layout and prior
milestones' changes from scratch. It is ADVISORY and may lag the latest
code: SPEC.md and the source are authoritative — if they disagree with the
context file, trust them. It supplements your normal reading; it does not
replace any SPEC.md read this prompt asks for below.

Read the current milestone spec at .ai/milestones/current.md.
Read the full spec at SPEC.md for context.

RULES:
- Implement EXACTLY what this milestone requires. Nothing more.
- Read .ai/decisions/spec-ambiguities.md. Where SPEC.md was
  contradictory, the ruling there is binding — implement the ruling,
  and in your commit message or a .ai/decisions/ note cite the
  ambiguity number you followed. Do NOT re-litigate a ruling.
- Only touch the files listed in the milestone spec.
- Follow the codebase's existing patterns and conventions.
- Do NOT create documentation files unless the milestone requires them.
- Do NOT refactor code outside the milestone's scope.
- Run the verify command from the milestone if one is specified. This is YOUR
  responsibility — the test runner does NOT eval verify commands from the spec.
- Before committing, run the milestone gate yourself: `sh .ai/build/verify.sh`
  (build + every stack's tests + lint/CI). Its Go scope covers your
  uncommitted and untracked work plus every package that depends on it.
- If a test listed in `.ai/milestones/known_failures` now passes because of
  your work, REMOVE it from that file in this milestone (the ship gate
  ignores the file, so a stale entry only hides a regression until then).
  Never ADD to `known_failures` / `known_lint_failures` to get green —
  additions during a milestone are diffed against the milestone-start
  snapshot and reported to the verifier as a finding — never create the
  operator-only stamp `.ai/build/no-tests-ok` (it silences the test gate
  for the whole project; its creation is reported the same way), and never
  edit `.ai/build/verify.sh` or `.ai/build/ci-probe.sh` (they are restored
  from the workflow before every gate run and a difference is reported).
  If the gate reports "no build system detected", the answer is to build
  the milestone's real test stack (go.mod / package.json / pyproject.toml /
  Cargo.toml / a Makefile test target), never to opt out.
- Commit your work with a conventional commit message referencing the milestone.

DO NOT LIST:
- Read the "DO NOT implement" lines in .ai/milestones/current.md, if any.
  These name Phase 2+ features that the spec defers. Before writing in any
  file, check whether the DO NOT list mentions it. If yes, leave those
  affordances inert (do not wire the feature into call sites, do not
  populate fields the spec marks as empty in this phase, do not return
  non-default values from helper functions that the spec marks as deferred).
  It is fine — and often required — to define the type or function
  signature; it is NOT fine to make it observably active when the spec
  says it should be a no-op until a later phase.

SPEC LITERALS:
- Before committing: for every literal value in the spec section this
  milestone covers — exact command strings, exact JSON key names, exact
  header names, exact integer constants, exact log key names — grep your
  implementation for that literal. If it isn't there byte-for-byte, EITHER
  fix the implementation to match the spec OR add a comment in
  .ai/decisions/ that names the literal you deviated from and explains
  why. Silent paraphrase ("--head <branch>" when the spec says
  "--head <owner>:<branch>") is the most common way milestones ship wrong.

TEST-VERIFIES-CONTRACT (run this rubric BEFORE you emit DONE):
- This is the SAME rubric VerifyMilestone grades you against (its check
  "TEST-VERIFIES-CONTRACT"). Apply it yourself first so a passing-but-
  non-verifying test never ships — that asymmetry is what forces an
  expensive Fix loop (run 634a2527ff56: a green smoke test that called the
  function in-process FAILed Verify and the fix had to invent a whole
  subprocess seam from scratch).
- Tests must verify the spec's requirement, not whatever your
  implementation happens to do. Read each assertion you write and ask:
  "If I deleted my production code and rewrote it differently but
  spec-conformantly, would this test still pass for the right reason?"
  If the answer is no — your test asserts an implementation detail rather
  than a contract — rewrite it. The canonical failure mode is asserting
  `attempts == 2` when the spec says "max 2 retries" (= 3 attempts):
  the test green-lights the off-by-one because it was written from the
  code, not from the spec.
- TEST SHAPE: when a milestone's done-when names a concrete test SHAPE —
  a "built binary" smoke test, a "subprocess", a "real `gh`", an
  end-to-end run through `main()` — the test MUST exercise that exact
  harness. A "built binary" smoke contract requires actually spawning the
  built binary (compile it, run the process, assert on its behavior); an
  in-process call to an unexported function is a WEAKER reading that
  passes locally but does NOT prove the wiring the contract demands, and
  Verify will FAIL it. List each named test shape from the done-when as a
  checklist item, satisfy it with the real harness, and cite the shape you
  matched. If satisfying it needs production plumbing (an injection seam,
  a build tag, fake-dependency flags), build that plumbing NOW — do not
  defer it to a Fix loop.
- Snapshot / golden-file tests: the FIRST version of every snapshot
  must be hand-verified against the spec, not regenerated from the
  current implementation's output. If your test framework supports an
  UPDATE_GOLDEN env var, only use it after the first hand-verification.
  Add a comment to the golden file naming the spec section it was
  verified against and the date.

STOP WHEN GREEN AND COMMITTED:
- The instant the milestone verify passes AND you have committed, emit
  DONE and STOP. Do NOT perform further verification, re-reading, or
  polishing. Committing is the precondition for stopping — never end a
  session with a green-but-uncommitted tree.