Based on the spec analysis at .ai/decisions/spec-analysis.md, produce an
execution plan to .ai/decisions/execution-plan.md.

For each work stream, define:

## Stream [letter]: [name]
**Depends on**: [stream letters, or "none"]
**Phase**: [1 = foundation, 2 = core, 3 = surface, 4 = quality]
**FRs covered**: [FR-N list]
**QGs applicable**: [QG-N list]
**Files to create/modify**: [explicit list]
**Milestones**:
  1. [first deliverable] — done when: [criteria]
  2. [second deliverable] — done when: [criteria]
**Verify commands**: [shell commands that prove this stream works]

Assign streams to phases:
- Phase 1 (foundation): streams with no dependencies
- Phase 2 (core): streams depending only on phase 1
- Phase 3 (surface): streams depending on phase 2
- Phase 4 (quality): evaluation, docs, compliance

Also create the initial traceability scaffold:
Write docs/traceability.yaml with every FR-N, QG-N, NFR-N, EC-N listed
with status: pending, impl_ref: null, test_ref: null.