You are working in `run.working_dir` on branch `${params.branch}`.

## Philosophy
Iteration > Perfection. Failures are Data. Persistence Wins.

## Setup
Run `git checkout ${params.branch}` before making any changes.

## Task
Read `.ai/streams/${params.stream_id}/task.md` for your assignment. This file contains:
- The task description and checklist
- File ownership (ONLY modify files listed in your ownership section)
- Interface contracts you must implement against

## Process
1. Read `.ai/streams/${params.stream_id}/task.md` to understand what you need to do
2. Read `.ai/streams/${params.stream_id}/iteration-log.md` to see what previous iterations did
3. Check `git log --oneline -10` and `git diff` for recent changes
4. Pick the NEXT uncompleted checklist item from task.md
5. Implement it — write the actual code changes
6. Run `go build ./...` to verify compilation
7. Run `go test ./...` to verify tests pass
8. If tests fail, fix them before moving on
9. Update `.ai/streams/${params.stream_id}/iteration-log.md` with what you did
10. Mark the item as done in task.md: `- [x]`
11. Commit your changes with a descriptive message

## File Ownership
You may ONLY modify files listed in the ownership section of your task.md.
Shared contracts in `.ai/contracts/` are READ-ONLY — implement against them, do not change them.

## Completion
When ALL checklist items in task.md are marked `[x]`:
- Write "RALPH_COMPLETE" as the final line of `.ai/streams/${params.stream_id}/iteration-log.md`
- Set STATUS: success

When you made progress but items remain:
- Set STATUS: fail (this triggers the next iteration)

## Important
- ONE checklist item per iteration — small, testable increments
- If stuck on the same item for 2 iterations, skip it and note why
- Always run go build before committing