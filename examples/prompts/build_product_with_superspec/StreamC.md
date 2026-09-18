You are building Stream C of the product spec. Read:
- SPEC.md (full context, especially sections 5.1-5.3 and FR-4)
- docs/execution-plan.md (your stream's milestones)

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
Commit with conventional messages referencing stream and FR IDs.

Write your traceability as an OVERLAY — docs/traceability.stream-c.yaml — holding
ONLY the requirement IDs you cover, one per line in the master's flat
format, e.g.
    FR-2: {status: done, impl_ref: "pkg/ingest/feed.go:Fetch", test_ref: "pkg/ingest/feed_test.go:TestFetch", note: "acceptance: ..."}
NEVER edit docs/traceability.yaml itself: another stream runs in parallel
and the phase merge folds every overlay into the master mechanically
(two streams editing one file = an add/add conflict that stops the run).
Commit the overlay with your code.