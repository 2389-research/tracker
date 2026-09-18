You are building Stream C of the product spec. Read:
- SPEC.md (full context, especially sections 5.1-5.3 and FR-4)
- .ai/decisions/execution-plan.md (your stream's milestones)

Your scope: Event candidate retrieval, multi-signal event assignment
(entity overlap + temporal proximity + semantic similarity + LLM judgment),
fixed founding identity anchors, singleton handling, explainability payloads.

Cover: FR-4 (Event Formation), FR-6 (Singleton Handling), FR-9 (Explainability for events).

CRITICAL CONSTRAINTS:
- EC-1: No threshold-only grouping. Multi-signal decision function required.
- EC-2: No drifting centroids. Fixed founding identity.
- EC-5: Every grouping decision must be explainable.
- Event identity must be order-invariant (NFR-2).

Write property tests for order invariance and idempotence.
Create initial gold dataset stubs for event-level evaluation.
Update docs/traceability.yaml for your FRs.
Commit with conventional messages referencing stream and FR IDs.