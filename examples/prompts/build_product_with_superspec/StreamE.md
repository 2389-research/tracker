You are building Stream E of the product spec. Read:
- SPEC.md (full context, especially FR-7)
- docs/execution-plan.md (your stream's milestones)

Your scope: Front page ranking by independent source count, freshness,
growth rate, activity. Diversity interleaving. Independence group handling
to prevent syndication inflation.

Cover: FR-7 (Front Page Ranking).

Write tests for ranking stability and diversity properties.
Commit with conventional messages referencing stream and FR IDs.

Write your traceability as an OVERLAY — docs/traceability.stream-e.yaml — holding
ONLY the requirement IDs you cover, one per line in the master's flat
format, e.g.
    FR-2: {status: done, impl_ref: "pkg/ingest/feed.go:Fetch", test_ref: "pkg/ingest/feed_test.go:TestFetch", note: "acceptance: ..."}
NEVER edit docs/traceability.yaml itself: another stream runs in parallel
and the phase merge folds every overlay into the master mechanically
(two streams editing one file = an add/add conflict that stops the run).
Commit the overlay with your code.