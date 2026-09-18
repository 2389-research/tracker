STATUS contract — emit `STATUS:fail` as the FIRST line of your
response, before any other text. Then do the work below. Only at
the very end, if the requirement-coverage gate reports zero UNOWNED
rows (COVERAGE_GAPS: 0), emit a final
`STATUS:success` line — alone on its line, outside any code
fence — to override the early fail. The workflow's `auto_status`
parser is last-line-wins; if your response is truncated for any
reason the early `STATUS:fail` remains and the gate fails closed.
The STATUS value must be exactly `fail` or `success`, with no
trailing prose on that line. Never emit STATUS:retry (this node
has no retry route).

Based on the spec analysis, decompose the work into ordered milestones.

Rules for decomposition:
- Each milestone is independently verifiable (you can test it alone)
- Milestones are ordered by dependency (foundations first)
- Each milestone has a clear "done" definition
- Prefer small milestones (1-3 files changed) over large ones
- Destructive work (removing old code) is its own milestone, BEFORE building replacements
- Total milestones should be 4-8 for a medium spec, 8-15 for a large one

Write to .ai/decisions/milestones.md. The file is machine-parsed
(PickNextMilestone / CheckMilestoneOutputs), so the format is exact:

## Plan summary
[one line per milestone — this becomes the build plan the user approves]

## Milestone N: [title]
**Depends on**: [previous milestone numbers, or "none"]
**Files**:
- `path/to/file.go` (new | modify | delete)
- `path/to/other_test.go` (new)
**Contract tests**: [the EXACT names of the test(s) that prove the
  done-when — one backticked name per item, comma-separated or one per
  bullet: Go `TestX` / `TestX/sub` (a `t.Run("empty input")` subtest
  is reported as `TestX/empty_input` — spaces become `_`), Rust
  `module::test_name`, pytest `path/test_file.py::test_name`, a JS
  describe/it title in backticks (a title without backticks is dropped).
  Write `none — <reason>` ONLY for a genuinely test-free milestone
  (docs, scaffold) and give the one-line reason.]
**Done when**: [specific, testable criteria]
**Verify command**: [shell command that proves this milestone works, or "manual review"]
**DO NOT implement**: [Phase 2+ features mentioned in the spec that touch
  any file in this milestone's file list. One line per item, naming the
  feature and the spec section that defers it. If none apply, write "none".]

Format rules (the parser depends on them):
- Every milestone header is EXACTLY `## Milestone N: title` — two `#`,
  the word Milestone, a plain integer N (1, 2, 3 … — no `#1`, no `01`,
  no `1.1` sub-milestones), a colon, a title. Numbers are unique and
  ascending. Do NOT write a `## Milestone overview` heading; the summary
  section is `## Plan summary` (any heading that is not `## Milestone N`
  is fine there).
- Under `**Files**:` write ONE backticked repo-relative path per bullet,
  as shown — never an inline comma-separated list, never a glob, never
  prose. A milestone that touches no files writes `**Files**: none`.
- The six bold fields keep the names above; a bold field always ends the
  Files list and the Contract tests list.
- `**Contract tests**` is machine-enforced: PickNextMilestone writes the
  names to `.ai/milestones/contract-tests`, verify.sh records every test
  that executed in `.ai/build/executed-tests.txt`, and TestMilestone is RED
  (`CONTRACT-TEST-MISSING`) when a declared name never executed. So name
  tests that will EXIST and RUN under the stack's runner — the test that
  proves the done-when, not a wish list. A milestone that adds source a
  test could exercise must name at least one; "none" is not a way to skip
  writing tests (VerifyMilestone fails an unjustified "none").

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

If the spec or codebase has tests that are expected to fail until a later
milestone wires things up, write those test names (one per line) to
.ai/milestones/known_failures. ONLY write test function names, one per
line. No comments, no blank lines, no headers — just bare test names like
TestFooBar. The test runner skips these (Go: an anchored `-skip` regex).
The file is per-plan: it was cleared before this node ran, so write only
the names expected to fail NOW. The Implement / FixMilestone agents
remove entries when they start passing — name, in the owning milestone's
"Done when", which known_failures entries it makes pass.

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
  - every row owned or deferred (count 0) -> emit the final
    STATUS:success line
  - any row UNOWNED (count > 0) -> list the unowned verification(s),
    then leave the early STATUS:fail standing (this routes to a human
    to re-plan rather than silently building with a dropped test).
The final line must be EXACTLY `STATUS:success` or `STATUS:fail` —
no parentheses, no counts, no trailing words, OUTSIDE any code
fence, alone on its line. Emit only success or fail — never
STATUS:retry (this node has no retry route).