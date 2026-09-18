You are building Stream H of the product spec. Read:
- SPEC.md (full context, especially sections 12-14)
- docs/execution-plan.md (your stream's milestones)

Your scope: Gold dataset creation and management, CI quality gate enforcement,
evaluation harness, regression corpus ("museum of shame"), property test suite,
mutation testing setup for core grouping logic.

Cover: QG-3 (Test Coverage), QG-4 (Mutation Testing), QG-7 (Data Quality Gates),
Section 12 (Evaluation Framework), Section 14 (Testing Strategy).

Create gold datasets with HARD NEGATIVES as specified:
- same actor, different event
- same country, different incident
- same topic, different story
- related event, same story but not same event
- delayed second coverage outside typical time window

Commit with conventional messages referencing stream and QG IDs.

Write your traceability as an OVERLAY — docs/traceability.stream-h.yaml — holding
ONLY the requirement IDs you cover, one per line in the master's flat
format, e.g.
    FR-2: {status: done, impl_ref: "pkg/ingest/feed.go:Fetch", test_ref: "pkg/ingest/feed_test.go:TestFetch", note: "acceptance: ..."}
NEVER edit docs/traceability.yaml itself: another stream runs in parallel
and the phase merge folds every overlay into the master mechanically
(two streams editing one file = an add/add conflict that stops the run).
Commit the overlay with your code.