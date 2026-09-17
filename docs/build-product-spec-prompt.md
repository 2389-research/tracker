# Prompt: write a `SPEC.md` for `build_product`

Give this prompt to an LLM (or a person) that is drafting the `SPEC.md` a
`build_product` / `build_product_with_superspec` run consumes. It encodes the
shape the pipeline's spec-reading nodes actually check for — `SpecLint`
(hard gate), `ReadSpec`, `Decompose`, `VerifyMilestone`, `FinalSpecCheck` —
so a spec written to it passes the coherence preflight on the first try and
decomposes cleanly into milestones.

Copy everything between the rules.

---

You are writing `SPEC.md` for an autonomous build pipeline. The pipeline
does not talk to you after you hand the spec over: a linter gates it, then
agents decompose it into milestones, implement each one, and finally verify
the result against the spec line by line. Every ambiguity you leave becomes
a guess by an agent; every dangling reference becomes invented content. Write
for a literal, careful reader who has only this file and the repository.

## Hard rules — the spec is rejected if any is violated

1. **Self-contained.** Every document, file path, section, or artifact you
   mention must exist in the repository or inside `SPEC.md` itself. Never
   write "see PLAN.md" or "as described in the design doc" unless that file
   is committed. If you need content from elsewhere, inline it.

2. **Single-source constants.** State every number and named parameter
   exactly once, in one place, one way. Retry counts, timeouts, limits,
   sizes, port numbers, default values — define each in a **Constants**
   table and refer to it by name everywhere else. "max 2 retries" in one
   section and "up to 3 attempts" in another is a rejection.

3. **Contracts match signatures.** If you say an interface derives, computes,
   or returns something, the signature you declare must actually receive the
   inputs and produce the outputs that require. A header "derived from
   `review_id` + `sha`" on a function that receives neither is unbuildable.

4. **Buildable substance.** Name at least one concrete component (a package,
   module, type, command, endpoint, or file) AND state at least one
   acceptance criterion that could be turned directly into a test assertion.
   Pure aspiration ("should be fast and reliable") with nothing nameable is
   rejected.

5. **Mandated verification is ownable.** Every test you require, every
   emitted value you prescribe (log event name, error code, status string,
   exit code), and every constant you mark **normative** must be concrete
   enough that one milestone can own it and one test can assert it as a
   hard-coded literal. Put them in the **Mandated verification** section.

6. **CLI literals are valid.** If you prescribe an exact external command
   (`gh pr create --head owner:branch`, `git push --force-with-lease`), the
   flag string must be correct against that tool's real grammar. A
   mis-parsing literal is treated as unverifiable and rejects the spec.

## Strong preferences — warnings, and where builds go wrong

The linter reports these as advisory findings (rules d, e, i). They do not
block the build, but they are shown to the human at `ApprovePlan` as the
places where the implementing agents will have to guess.

- Phrase every behavioral guarantee so it can become a named test. Prefer
  "`Client.Do` returns `ErrTimeout` within 5 s (see Constants) when the
  server sends no bytes" over "handles slow servers gracefully".
- Example code must parse against the signatures you declare. If a snippet
  is illustrative and not exact, label it `(illustrative)`.
- Use modal verbs deliberately. **MUST / MUST NOT / never / always /
  exactly once / before X / at startup** are each extracted as a contract
  requiring a verification method. Do not use them casually.
- Mark a constant **normative** only when implementations must use that
  exact value; otherwise call it a default.
- If some work is deliberately deferred, put it in a **Later phases /
  out of scope** section and name the phase. Agents implementing Phase 1
  are told not to build Phase 2 types "for later"; a feature mentioned
  without a phase label is ambiguous.

## Required structure

Use exactly these top-level headings, in this order. Leave a section in
place with `none` rather than deleting it.

```markdown
# <Product name>

## Summary
Two to four sentences: what is being built, for whom, and the single most
important outcome.

## Current state
What already exists in this repository that the build must KEEP, and what
it must REPLACE or REMOVE. Name files/packages. If greenfield, say so.

## Constants
| Name | Value | Normative? | Used by |
|------|-------|------------|---------|
Define every number/parameter here once. Refer to rows by Name elsewhere.

## Components
One subsection per component. For each: purpose, public interface
(signatures with types, or endpoint shapes, or CLI usage), inputs,
outputs, errors it can return, and which Constants it uses.

## Behavior
Ordered, testable statements of what the system does. Use MUST/MUST NOT
only for real obligations. Each statement should map to at least one item
in Acceptance criteria.

## Acceptance criteria
Numbered. Each is a single checkable sentence with a concrete observation
("running `X` exits 0 and prints `Y`", "a request without header H returns
400 with body `{"error":"missing_h"}`"). This is what the final compliance
check reads.

## Mandated verification
- Tests: name or precisely describe each test the build must include.
- Emitted values: every exact string/code the system must produce
  (log event names, error codes, status strings, exit codes).
- Normative constants: the rows of Constants marked Normative.
Each must be assertable as a hard-coded expectation. Write `none` per
bullet if there are none.

## Technical constraints
Language, framework, versions, dependencies allowed/forbidden, platform,
performance budgets (as Constants), security requirements.

## Later phases / out of scope
Anything mentioned above that is NOT part of this build, each tagged with
a phase (`Phase 2`, `Phase 3`) or `out of scope`, with one line of why.

## Open questions
`none`, ideally. If a decision is genuinely undecidable now, state the
default the build should assume — never leave a choice to the implementer.
```

## Before you finish, check your own draft

- grep your spec for every number you wrote; each should appear once in
  Constants and elsewhere only by name.
- For every "see", "as in", "described in", "refer to": does the target
  exist?
- For every MUST / never / always / exactly / before / within: can you
  point to the acceptance criterion or test that proves it?
- For every code snippet: does it match the signatures in Components, or is
  it labeled illustrative?
- For every external command literal: is the flag grammar real?
- Is there at least one named component and one assertable acceptance
  criterion? (There should be many.)

Output only the finished `SPEC.md` content. Do not add commentary before or
after it.

---

## Notes for tracker operators

- From the CLI, the file must land at the repo root as `SPEC.md`. An
  embedding host (tracker-runner, or anything using the library API) can
  instead pass it as the declared `spec` file input via `Config.Inputs`
  (`tracker.FileInput`) — `Setup` stages it to `SPEC.md`.
- If `SpecLint` still fails, the spec-forge loop makes up to three
  autonomous reconciliation passes and writes its reasoning to
  `.ai/decisions/spec-forge-log.md`; the findings it acted on are in
  `.ai/decisions/spec-quality.md`. Reading those two files is the fastest
  way to see which rule above the draft missed.
- The rules here mirror the `SpecLint` prompt in `examples/build_product.dip`
  (CRITICAL a/b/c/f/g/h, WARN d/e/i). `TestSpecAuthoringPromptMirrorsSpecLintRules`
  in `pipeline/spec_lint_preflight_test.go` fails if a rule letter or this
  document's coverage of it drifts — update both in the same change.
- `SpecLint`'s advisory findings are rendered by `ShowPlan` ahead of the
  `ApprovePlan` gate in `build_product.dip`; `build_product_with_superspec.dip`
  writes the same `.ai/decisions/spec-quality.md` but its `ApprovePlan` does
  not yet render it — read the file directly.
