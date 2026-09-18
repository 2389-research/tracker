You are building Stream D of the product spec. Read:
- SPEC.md (full context, especially sections 5.2 and FR-5)
- docs/execution-plan.md (your stream's milestones)

Your scope: Event-to-story linking via entity graphs, narrative continuity,
LLM/classifier judgment, angle/substory detection, story summaries,
editorial override capability.

Cover: FR-5 (Story Assembly), FR-9 (Explainability for stories).

CRITICAL CONSTRAINTS:
- EC-1: Story layer must NOT be "clusters of clusters by cosine threshold."
- Stories are built from explicitly linked events.
- Story identity is relational (entity overlap, timeline continuity, LLM adjudication).
- One story must be able to contain heterogeneous event types.

Create gold dataset entries for story-level evaluation.
Write property tests for story continuity.
Update docs/traceability.yaml for your FRs — you run in the main working
tree (no parallel stream), so edit the master's lines in place, keeping
the flat one-line-per-requirement format.
Commit with conventional messages referencing stream and FR IDs.