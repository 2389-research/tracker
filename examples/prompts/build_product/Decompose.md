Based on the spec analysis, decompose the work into ordered milestones.

Rules for decomposition:
- Each milestone is independently verifiable (you can test it alone)
- Milestones are ordered by dependency (foundations first)
- Each milestone has a clear "done" definition
- Prefer small milestones (1-3 files changed) over large ones
- Destructive work (removing old code) is its own milestone, BEFORE building replacements
- Total milestones should be 4-8 for a medium spec, 8-15 for a large one

Write to .ai/decisions/milestones.md:
## Milestone N: [title]
**Depends on**: [previous milestone numbers, or "none"]
**Files**: [exact files to create/modify/delete]
**Done when**: [specific, testable criteria]
**Verify command**: [shell command that proves this milestone works, or "manual review"]
**DO NOT implement**: [Phase 2+ features mentioned in the spec that touch
  any file in this milestone's file list. One line per item, naming the
  feature and the spec section that defers it. If none apply, write "none".]

The DO NOT list is the anti-scope-creep gate. The
Implement agent reads it before writing in any file in this milestone.
Phase 1 spec sections frequently define types/functions whose live use is
deferred to later phases; without an explicit DO NOT, the implementing
agent will reasonably wire them up early and ship a future phase's
behavior. Concrete example: SPEC.md may define a `Fingerprint` function
in Phase 1 but say its consumer (a trailer field) is populated starting
Phase 6 — the DO NOT list for any Phase 1 milestone that touches the
trailer file must say: "DO NOT implement: populating the trailer's
`fingerprints` field — spec defers to Phase 6."

Also write a one-line summary of each milestone — this becomes the build plan
the user approves.

If the spec or codebase has tests that are expected to fail until a later
milestone wires things up, write those test names (one per line) to
.ai/milestones/known_failures. ONLY write test function names, one per
line. No comments, no blank lines, no headers — just bare test names like
TestFooBar. The test runner uses these as a regex skip pattern. Remove
test names from this file in the milestone where they should start passing.

── REQUIREMENT COVERAGE ──
No spec-mandated verification may be left unowned. Do this in THIS
order — the ordering is load-bearing (it stops you from back-filling
the table to match milestones you already wrote, which would make
the gate decorative):

1. FIRST, before writing any milestone, read SPEC.md and list every
   spec-mandated verification into the LEFT columns of
   .ai/decisions/requirement-coverage.md. A line is "spec-mandated"
   ONLY if SPEC.md uses an obligation word (MUST / SHALL / "is
   required to" / "the test must" / a named test function or
   acceptance criterion) AND names a concrete, checkable behavior
   (a specific input/output, a numeric bound, a named error path).
   Aspirational words ("fast", "robust", "clean") with no checkable
   threshold are NOT mandated — do not list them. State how many you
   found; if none, write the line: SPEC mandates no named
   verifications (never leave the table silently empty — a silent
   empty table hides under-extraction). Cross-check against the
   "Mandated tests" section of .ai/decisions/spec-quality.md
   (written by the SpecLint preflight): every item listed there —
   test, spec-emitted value, or normative constant — must appear in
   this table, so none is silently unowned. Table columns:
     | Mandated verification | SPEC.md source (line/section) | Owning milestone |

2. THEN decompose into milestones as the rules above describe.

3. FINALLY, fill the "Owning milestone" column. You may NOT add rows
   here and you may NOT invent a milestone to cover a verification.
   Each verification resolves to exactly one of:
   - owned: a milestone's "Done when" line covers it. Confirm with
     `grep -nE '^#+ *[Mm]ilestone' .ai/decisions/milestones.md` and
     cite `Milestone <N>: <the Done-when line, verbatim>`. A
     milestone that only mentions the topic in prose does NOT own it.
   - deferred: SPEC.md itself defers the behavior to a named later
     phase AND a milestone's "DO NOT implement" block records that
     deferral citing the spec section. Cite both. (This carve-out
     keeps the gate consistent with the anti-scope-creep machinery
     above — a correctly-deferred Phase-2+ feature is NOT a gap.)
   - UNOWNED: neither owned nor deferred. This is the bug this gate
     exists to catch.

Then re-read .ai/decisions/requirement-coverage.md, count the UNOWNED
rows, and emit on its own line: COVERAGE_GAPS: <count>. Then your
terminal STATUS line:
  - every row owned or deferred (count 0) -> STATUS:success
  - any row UNOWNED (count > 0) -> list the unowned verification(s)
    ABOVE the STATUS line, then STATUS:fail (this routes to a human
    to re-plan rather than silently building with a dropped test).
The final line must be EXACTLY `STATUS:success` or `STATUS:fail` —
no parentheses, no counts, no trailing words (a trailing count makes
the parser drop the line and default to success), OUTSIDE any code
fence, alone on its line. Emit only success or fail — never
STATUS:retry (this node has no retry route).