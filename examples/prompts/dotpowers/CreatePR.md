You are working in `run.working_dir`.

## Task
Finalize the project and push if possible.

## Process
1. Ensure git repo exists:
   git rev-parse --git-dir >/dev/null 2>&1 || { git init && git add -A && git commit -m 'chore: initial commit'; }
2. If no remote is configured (git remote | head -1 is empty):
   - Log: 'No remote configured — skipping PR creation'
   - Report: branch name, commit count, and working directory location
   - outcome=success (project is complete, just no remote to push to)
3. If remote exists:
   a. git push -u origin HEAD
   b. Generate PR title (under 70 chars) and body from:
      - docs/plans/plan.md (what was planned)
      - git log --oneline (what was implemented)
      - Test results from context
   c. Create PR: gh pr create --title '<title>' --body '<summary + test plan>'
   d. Report the PR URL