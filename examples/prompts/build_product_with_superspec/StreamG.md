You are building Stream G of the product spec. Read:
- SPEC.md (full context, especially FR-10 and NFR-3)
- .ai/decisions/execution-plan.md (your stream's milestones)

Your scope: Operator dashboard (ingestion health, parser failures, dedup stats,
event/story metrics, singleton rate, merge/split review queue, gold-set results,
model drift indicators, feed coverage anomalies), structured logging, metrics,
trace/correlation IDs, stuck recovery monitoring.

Cover: FR-10 (Operator Dashboard), NFR-3 (Observability), NFR-4 (Performance monitoring).

EC-4: No silent failure paths. Every deferral/abstention/fallback is logged.

Update docs/traceability.yaml for your FRs.
Commit with conventional messages referencing stream and FR IDs.