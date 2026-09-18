The winning implementation has been applied. Verify:

1. Read the spec at .ai/decisions/spec.md
2. Check every numbered requirement is satisfied
3. Verify no files were modified outside the spec's scope
4. Verify no documentation files were created unless required by spec
5. Verify no leftover worktrees in .ai/worktrees/ (the candidate diffs and
   test logs in .ai/candidates/ are the decision record and STAY there)
6. Confirm tests pass (see the FinalBuild stdout block above)

If everything checks out, output STATUS:success
If something is wrong, describe what and output STATUS:fail