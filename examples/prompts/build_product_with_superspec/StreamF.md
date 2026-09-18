You are building Stream F of the product spec. Read:
- SPEC.md (full context, especially sections 11 and FR-8)
- docs/execution-plan.md (your stream's milestones)

Your scope: Front page, story page (progressive depth: story → angles →
events → article timeline), event page, feed directory, filters.

Cover: FR-1 (Feed Curation UI), FR-8 (Story Tracker UX), Section 11 UI requirements.

The story tracker is the PRIMARY design target. Build progressive depth
that works: broad story → sub-angles → individual events → article timeline.
The daily reader gets the top level for free.

Write E2E test stubs for the QG-9 demo scenarios.
Commit with conventional messages referencing stream and FR IDs.

Write your traceability as an OVERLAY — docs/traceability.stream-f.yaml — holding
ONLY the requirement IDs you cover, one per line in the master's flat
format, e.g.
    FR-2: {status: done, impl_ref: "pkg/ingest/feed.go:Fetch", test_ref: "pkg/ingest/feed_test.go:TestFetch", note: "acceptance: ..."}
NEVER edit docs/traceability.yaml itself: another stream runs in parallel
and the phase merge folds every overlay into the master mechanically
(two streams editing one file = an add/add conflict that stops the run).
Commit the overlay with your code.