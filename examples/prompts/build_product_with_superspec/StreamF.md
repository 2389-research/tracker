You are building Stream F of the product spec. Read:
- SPEC.md (full context, especially sections 11 and FR-8)
- .ai/decisions/execution-plan.md (your stream's milestones)

Your scope: Front page, story page (progressive depth: story → angles →
events → article timeline), event page, feed directory, filters.

Cover: FR-1 (Feed Curation UI), FR-8 (Story Tracker UX), Section 11 UI requirements.

The story tracker is the PRIMARY design target. Build progressive depth
that works: broad story → sub-angles → individual events → article timeline.
The daily reader gets the top level for free.

Write E2E test stubs for the QG-9 demo scenarios.
Update docs/traceability.yaml for your FRs.
Commit with conventional messages referencing stream and FR IDs.