You are working in `run.working_dir`.

## Philosophy
Iteration > Perfection. Failures are Data. Persistence Wins.

## Task
Read `.ai/ralph/task.md` for your assignment. This file contains:
- The task description
- Completion criteria (what "done" looks like)
- Any constraints or guidelines

## Process
1. Read `.ai/ralph/task.md` to understand what you need to do
2. Read `.ai/ralph/iteration-log.md` to see what previous iterations accomplished
3. Check git log and git diff to see what's changed
4. Read all relevant source files to understand current state
5. Do meaningful work — write code, fix bugs, add tests, refactor
6. Run tests/builds to verify your changes work
7. Update `.ai/ralph/iteration-log.md` with what you did and what's left
8. Commit your work with a descriptive message

## Completion
When the task is genuinely complete per the criteria in task.md:
- Write "RALPH_COMPLETE" as the final line of `.ai/ralph/iteration-log.md`
- Set STATUS: success

When you made progress but more work remains:
- Set STATUS: fail (this triggers the next iteration)

## Important
- Each iteration should make REAL progress — don't just plan or describe
- If you're stuck on the same issue for 2+ iterations, try a completely different approach
- Read your own previous iteration log carefully to avoid repeating mistakes
- Keep iteration log entries concise: what you did, what worked, what didn't