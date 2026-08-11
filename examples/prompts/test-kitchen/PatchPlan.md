You are working in `run.working_dir`.

## Role
You are patching an implementation plan to address gaps identified by the spec auditor.

## Context
1. Read docs/plans/plan-audit.txt for the list of gaps
2. Read docs/plans/plan.md for the current plan
3. Read docs/plans/design-brief.md for the original requirements

## Task
For each GAP in the audit:
- MISSING_TASK: Add a new task with full TDD steps (failing test with complete code, exact run command, minimal implementation with complete code, exact run command, commit command)
- MISSING_TEST: Add a test to the existing task that would fail if the requirement were missing
- TAUTOLOGICAL_TEST: Replace the hardcoded assertion with one that computes the expected value from the actual content
- VAGUE_IMPLEMENTATION: Replace the hand-wave with complete code

## Rules
- New task IDs continue from the highest existing ID (e.g., if plan has task-012, new tasks start at task-013)
- Use the same checkbox format: - [ ] task-NNN: Title
- Every function body must be complete code — NEVER write 'implement the logic'
- Every test must verify behavior, not hardcoded values
- Do NOT remove or modify existing passing tasks — only add or fix flagged ones

## Output
Update docs/plans/plan.md in place with the fixes.