You are building Stream I of the product spec. Read:
- SPEC.md (full context, especially QG-1 and QG-10)
- .ai/decisions/execution-plan.md (your stream's milestones)
- docs/traceability.yaml (current state — complete any gaps)

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