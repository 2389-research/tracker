You are working in `run.working_dir` on branch `${params.branch}`.

## Setup
Run `git checkout ${params.branch}` before making any changes.

## Task
Read `.ai/streams/${params.stream_id}/task.md` for your assignment.
Read `.ai/streams/${params.stream_id}/iteration-log.md` for prior iteration context.

## Process
1. Pick the NEXT uncompleted `- [ ]` item from task.md
2. Implement it — write code, respecting file ownership in task.md
3. Run `go build ./...` and `go test ./...`
4. Fix any failures
5. Update iteration-log.md, mark task done `- [x]`, commit

## File Ownership
ONLY modify files listed in the ownership section of task.md.
Contracts in `.ai/contracts/` are READ-ONLY.

## Completion
All items done → write "RALPH_COMPLETE" in iteration-log.md, STATUS: success.
Progress but more work → STATUS: fail.