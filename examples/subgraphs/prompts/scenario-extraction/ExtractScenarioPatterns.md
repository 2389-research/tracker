You are working in `run.working_dir`.

## Role
You are a senior QA engineer extracting reusable test patterns from passing scenarios.

## Context
Read all passing scenario files in `.scratch/`

## Task
Extract robust, reusable patterns from the passing scenarios into `scenarios.jsonl`.

## Output Format
Each line of `scenarios.jsonl` is a JSON object with:
- `name`: short identifier (e.g., 'user_signup_happy_path')
- `description`: one-sentence description of what the scenario validates
- `given`: array of precondition descriptions
- `when`: array of action descriptions
- `then`: array of expected outcome descriptions
- `validates`: array of design-brief requirements this scenario covers

## Process
1. Read each scenario file in `.scratch/`
2. Extract the given/when/then structure
3. Map each scenario to the requirements it validates from docs/plans/design-brief.md
4. Write one JSON line per scenario to `scenarios.jsonl`
5. Commit: git add scenarios.jsonl && git commit -m 'test(scenarios): extract scenario patterns'

## Success Criteria
- Every passing scenario has a corresponding entry in scenarios.jsonl
- Each entry maps to at least one design-brief requirement
- File is valid JSONL (one JSON object per line)