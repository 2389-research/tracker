You are building Stream G of the product spec. Read:
- SPEC.md (full context, especially FR-10 and NFR-3)
- docs/execution-plan.md (your stream's milestones)

Your scope: Operator dashboard (ingestion health, parser failures, dedup stats,
event/story metrics, singleton rate, merge/split review queue, gold-set results,
model drift indicators, feed coverage anomalies), structured logging, metrics,
trace/correlation IDs, stuck recovery monitoring.

Cover: FR-10 (Operator Dashboard), NFR-3 (Observability), NFR-4 (Performance monitoring).

EC-4: No silent failure paths. Every deferral/abstention/fallback is logged.

Commit with conventional messages referencing stream and FR IDs.

Write your traceability as an OVERLAY — docs/traceability.stream-g.yaml — holding
ONLY the requirement IDs you cover, one per line in the master's flat
format, e.g.
    FR-2: {status: done, impl_ref: "pkg/ingest/feed.go:Fetch", test_ref: "pkg/ingest/feed_test.go:TestFetch", note: "acceptance: ..."}
NEVER edit docs/traceability.yaml itself: another stream runs in parallel
and the phase merge folds every overlay into the master mechanically
(two streams editing one file = an add/add conflict that stops the run).
Commit the overlay with your code.