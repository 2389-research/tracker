Based on the spec analysis at .ai/decisions/spec-analysis.md, produce an
execution plan to docs/execution-plan.md — a COMMITTED path: the streams
read it from git worktrees, where .ai/ (gitignored) is invisible.

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

Also create the initial traceability scaffold, docs/traceability.yaml, in
this EXACT flat format — one requirement per line, a flow-style mapping
keyed by the ID (comments and blank lines allowed, nothing else; a nested
or block-style YAML matrix is REJECTED by the CommitScaffold step):

    # Traceability matrix — one line per requirement (do not nest)
    FR-1: {status: pending, impl_ref: null, test_ref: null, note: null}
    FR-2: {status: pending, impl_ref: null, test_ref: null, note: null}
    QG-1: {status: pending, impl_ref: null, test_ref: null, note: null}

List EVERY FR-N, QG-N, NFR-N and EC-N from the spec, all `status: pending`,
`impl_ref: null`, `test_ref: null`, `note: null`. The flat shape is what
lets each parallel stream ship its own overlay file that the phase merge
folds in mechanically — never edit this file from a stream.

Do not commit: the CommitScaffold step commits SPEC.md,
docs/execution-plan.md and docs/traceability.yaml by name once the plan is
approved.