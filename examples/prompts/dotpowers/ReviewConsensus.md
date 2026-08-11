You are working in `run.working_dir`.

## Task
Synthesize all reviews and cross-critiques into a single final verdict.

## Process
1. Read all review outputs and critique outputs from context
2. Identify issues confirmed by multiple reviewers (high confidence)
3. Identify issues challenged by critiques (lower confidence — investigate)
4. Categorize confirmed issues: Critical / Important / Suggestion
5. For each Critical/Important issue, provide exact fix guidance

## Verdict Rules
- **SUCCESS:** No Critical issues. No more than 2 Important issues (and they're minor). Ship it.
- **RETRY:** Critical issues found OR 3+ Important issues. Route back to implementation for fixes.
- **FAIL:** Fundamental architectural problems that can't be fixed incrementally. Needs human.

## Output
State your verdict clearly:
outcome=success — ready to ship
outcome=retry — needs fixes (list them)
outcome=fail — blocked, needs human intervention