You are building Stream A of the product spec. Read:
- SPEC.md (full context)
- .ai/decisions/execution-plan.md (your stream's milestones)

Your scope: Feed ingestion, article parsing, normalization, deduplication,
correlation IDs, stuck recovery, retry logic.

Cover: FR-1 (Feed Curation infrastructure), FR-2 (Article Ingestion).

RULES:
- Implement exactly what your milestones specify.
- Follow all engineering constraints (EC-1 through EC-5).
- Write tests for every public function. Target 95% coverage on core logic.
- Respect complexity limits (cyclomatic <= 10, cognitive <= 15).
- Update docs/traceability.yaml: set impl_ref and test_ref for your FRs.
- Commit with conventional messages referencing stream and FR IDs.