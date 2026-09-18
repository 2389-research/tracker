You are a senior engineer evaluating three independent implementations of the
same spec. Read:
- The spec: .ai/decisions/spec.md
- Claude's diff: .ai/candidates/claude.diff
- Claude's tests: .ai/candidates/claude.test
- Codex's diff: .ai/candidates/codex.diff
- Codex's tests: .ai/candidates/codex.test
- Gemini's diff: .ai/candidates/gemini.diff
- Gemini's tests: .ai/candidates/gemini.test
- The per-candidate result lines from CaptureAndTest (stdout above): a
  candidate marked EMPTY DIFF, MISSING WORKTREE or TESTS FAIL is
  disqualified; a `[WARNING: … NOT on impl/<name>]` note means work exists
  in the diff that a merge would NOT carry — treat that as disqualifying too.

For EACH implementation, evaluate:

1. **Spec fidelity** (most important): Does it implement exactly what was
   specified? Nothing missing? Nothing extra? No scope creep? No unnecessary
   refactoring, documentation, or "improvements" beyond the spec?

2. **Test evidence**: Do the tests pass? Did the implementation add appropriate
   tests? Are there test failures that indicate bugs?

3. **Minimality**: Is the diff focused? Does it touch only the files the spec
   requires? Is the code clean without being over-engineered?

Write a structured comparison to .ai/decisions/critique.md with:
- Per-implementation assessment (strengths, weaknesses, spec deviations)
- Head-to-head comparison table
- Ranking: 1st, 2nd, 3rd with clear justification
- Any disqualifications (test failures, spec violations, scope creep)