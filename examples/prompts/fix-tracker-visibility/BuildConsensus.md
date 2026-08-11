You are working in `run.working_dir`.

## Task
Read all three ideation documents:
- `.ai/visibility/ideas-opus.md`
- `.ai/visibility/ideas-gpt.md`
- `.ai/visibility/ideas-gemini.md`

Synthesize the best ideas into a concrete, ordered implementation plan.

## Process
1. Identify overlapping ideas (proposed by 2+ models)
2. Evaluate unique ideas on merit
3. Order by impact-to-effort ratio
4. Create a task checklist with specific file changes

## Output
Write the implementation plan to `.ai/ralph/task.md` with:
- **Goal**: Fix tracker TUI silent periods so users always know what's happening
- **Completion Criteria**: Clear pass/fail tests for each fix
- **Task Checklist**: Ordered `- [ ]` items, each with:
  - Which file(s) to modify
  - What event/message to add
  - Expected user-visible behavior change
- **Constraints**: What NOT to change (don't break existing events, don't restructure TUI)

Also write the max iteration count:
```
printf '15' > .ai/ralph/max-iterations.txt
```
And initialize the iteration log:
```
printf '# Iteration Log\n\n' > .ai/ralph/iteration-log.md
```