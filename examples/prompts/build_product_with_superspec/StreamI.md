You are building Stream I of the product spec. Read:
- SPEC.md (full context, especially QG-1 and QG-10)
- docs/execution-plan.md (your stream's milestones)
- docs/traceability.yaml (current state, read-only for you — see below)

Your scope: Architecture doc, data model doc, API doc, operator runbook,
evaluation methodology doc, prompt/model registry, ADRs for major decisions,
known limitations doc, traceability matrix completion.

Cover: QG-1 (Spec Traceability), QG-10 (Documentation Gates).

CRITICAL: The traceability file must link every FR-N, QG-N, NFR-N, EC-N to:
- at least one implementation reference (file:function or file:class)
- at least one test reference (test file:test name)
- an acceptance note

Verify completeness against SPEC.md. Flag any requirement without an impl_ref.
Commit with conventional messages referencing stream and QG IDs.

Write your traceability as an OVERLAY — docs/traceability.stream-i.yaml — holding
ONLY the requirement IDs you cover, one per line in the master's flat
format, e.g.
    FR-2: {status: done, impl_ref: "pkg/ingest/feed.go:Fetch", test_ref: "pkg/ingest/feed_test.go:TestFetch", note: "acceptance: ..."}
NEVER edit docs/traceability.yaml itself: another stream runs in parallel
and the phase merge folds every overlay into the master mechanically
(two streams editing one file = an add/add conflict that stops the run).
Commit the overlay with your code.
Your overlay may carry ANY requirement whose entry you complete (the gaps
you found), not only QG-1/QG-10 — the merge applies it last in this phase.
A requirement that genuinely cannot have a test (document it) goes in
docs/traceability-waivers.txt as `<ID>  <reason>` — the FinalGates FAIL
every implemented requirement with `test_ref: null` that is not waived.