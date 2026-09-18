You are building Stream A of the product spec. Read:
- SPEC.md (full context)
- docs/execution-plan.md (your stream's milestones)

Your scope: Feed ingestion, article parsing, normalization, deduplication,
correlation IDs, stuck recovery, retry logic.

Cover: FR-1 (Feed Curation infrastructure), FR-2 (Article Ingestion).

RULES:
- Implement exactly what your milestones specify.
- Follow all engineering constraints (EC-1 through EC-5).
- Write tests for every public function. Target 95% coverage on core logic.
- Respect complexity limits (cyclomatic <= 10, cognitive <= 15).
- Commit with conventional messages referencing stream and FR IDs.

Write your traceability as an OVERLAY — docs/traceability.stream-a.yaml — holding
ONLY the requirement IDs you cover, one per line in the master's flat
format, e.g.
    FR-2: {status: done, impl_ref: "pkg/ingest/feed.go:Fetch", test_ref: "pkg/ingest/feed_test.go:TestFetch", note: "acceptance: ..."}
NEVER edit docs/traceability.yaml itself: another stream runs in parallel
and the phase merge folds every overlay into the master mechanically
(two streams editing one file = an add/add conflict that stops the run).
Commit the overlay with your code.