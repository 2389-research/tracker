You are building Stream B of the product spec. Read:
- SPEC.md (full context)
- .ai/decisions/execution-plan.md (your stream's milestones)

Your scope: Entity extraction, event candidate extraction, temporal parsing,
location extraction, synopsis generation per article.

Cover: FR-3 (Entity and Event Extraction).

RULES:
- Implement exactly what your milestones specify.
- Follow all engineering constraints (EC-1 through EC-5).
- Write tests for every public function. Target 95% coverage on core logic.
- Respect complexity limits (cyclomatic <= 10, cognitive <= 15).
- Update docs/traceability.yaml: set impl_ref and test_ref for your FRs.
- Commit with conventional messages referencing stream and FR IDs.