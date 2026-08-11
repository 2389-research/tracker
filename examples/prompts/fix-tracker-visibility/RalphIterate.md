You are working in `run.working_dir`.

## Philosophy
Iteration > Perfection. Failures are Data. Persistence Wins.

## Task
Read `.ai/ralph/task.md` for your assignment — fixing tracker TUI silent periods.
This is an iterative loop. Each pass, you should make real, testable progress.

## Process
1. Read `.ai/ralph/task.md` for the full plan and checklist
2. Read `.ai/ralph/iteration-log.md` to see what previous iterations did
3. Check `git log --oneline -20` and `git diff` for recent changes
4. Pick the NEXT uncompleted checklist item from task.md
5. Implement it — write the actual Go code changes
6. Run `go build ./...` to verify compilation
7. Run `go test ./...` to verify tests pass (or at minimum `go vet ./...`)
8. If tests fail, fix them before moving on
9. Update `.ai/ralph/iteration-log.md` with what you did
10. Mark the item as done in `.ai/ralph/task.md`: `- [x]`
11. Commit your changes with a descriptive message

## Completion
When ALL checklist items in task.md are marked `[x]`:
- Write "RALPH_COMPLETE" as the final line of `.ai/ralph/iteration-log.md`
- Set STATUS: success

When you made progress but items remain:
- Set STATUS: fail (this triggers the next iteration)

## Important
- ONE checklist item per iteration — small, testable increments
- If stuck on the same item for 2 iterations, skip it and note why
- Always run go build before committing
- Read your previous iteration log to avoid repeating work