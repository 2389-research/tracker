You are building Stream H of the product spec. Read:
- SPEC.md (full context, especially sections 12-14)
- .ai/decisions/execution-plan.md (your stream's milestones)

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

Update docs/traceability.yaml for your QGs.
Commit with conventional messages referencing stream and QG IDs.