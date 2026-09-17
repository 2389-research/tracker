Read SPEC.md thoroughly. Understand:
- What is being built (the product)
- What exists already (what to keep, what to throw away)
- What the technical constraints are
- What the success criteria are

Produce a structured analysis to .ai/decisions/spec-analysis.md:
## Product summary (2-3 sentences)
## What exists and should be kept
## What must be replaced or removed
## Key technical decisions prescribed by the spec
## Implicit requirements (things the spec assumes but doesn't state)
## Risks and ambiguities

── SPEC-CONTRACT ARTIFACTS (issue #306) ──
Beyond the analysis above, WRITE TWO machine-checkable contract
artifact FILES to disk at the exact paths below (both are required —
do not skip either; downstream nodes read them off disk). Derive their
content from SPEC.md. Detection keys on the SHAPE of the prose
(modal verbs, timing/ordering phrases, contradiction between two
statements) — NEVER on the product, language, or domain.

(i) .ai/decisions/spec-ambiguities.md — one DEFINITE ruling per
    detected contradiction. A contradiction is two statements in
    SPEC.md that cannot both hold (a constant stated two ways, an
    ordering asserted one place and contradicted another, a value
    "required" here and "optional" there). For EACH, write one entry:
    ## Ambiguity N
    **Conflicting statements**: quote both, each with its SPEC.md
      location (section / line).
    **Ruling**: the single resolution this build will follow,
      stated as a concrete buildable decision.
    **Rationale**: one line.
    The ruling MUST be definite — never "it depends", never "either
    is acceptable", never deferred to the implementer. If SPEC.md is
    internally consistent, write the single line: "no contradictions
    detected". (A retry-count stated as "max 2 retries" in one
    section and "retries up to 3 times" in another is ONE
    contradiction and gets ONE ruling, e.g. "3 total attempts (2
    retries after the initial try)".)

(ii) .ai/decisions/behavioral-contracts.md — every NON-LITERAL prose
    guarantee, each with a concrete verification method. A prose
    guarantee is any sentence whose SHAPE is a behavioral obligation:
    a modal verb (MUST / MUST NOT / SHALL / never / always), a
    timing/ordering phrase ("at startup, not at runtime", "before
    X", "within N ms", "on the first call"), or a cardinality
    guarantee ("exactly once", "at most one"). For EACH, write:
    ## Contract N
    **Guarantee**: the prose, quoted, with its SPEC.md location.
    **Verification method**: a SPECIFIC test (named test function /
      test shape) OR a SPECIFIC grep that would prove the guarantee
      holds. "Manual review" is not a verification method. A
      "rejects at startup, not at runtime" guarantee is verified by
      a startup-path test/grep (e.g. a test that constructs the
      component with a bad config and asserts it fails at
      construction), NOT skipped as untestable.
    If SPEC.md states no such guarantees, write the single line:
    "no behavioral contracts detected".
    When SPEC.md marks a constant "normative" (a value the spec says
    implementations must use exactly — not merely a default or an
    example), record it as a contract with
    **Verification method**: "a test asserts this exact value as a
    hard-coded expectation" (the point-6 / SPEC-EMITTED-VALUE bar — a
    test asserting against the production constant does NOT satisfy
    it).

Both files are inputs to ApprovePlan (the human sees them before the
build) and to VerifyMilestone / FinalSpecCheck (which disposition
each in-scope contract). Do not editorialize SPEC.md — extract and
rule, do not invent guarantees the spec does not make.