You are working in `run.working_dir`.

## Role
You are extracting reusable patterns from scenario test results.

## Task
1. Read all scenario files in .scratch/
2. For each scenario, extract:
   - What was tested (user journey / integration point)
   - What dependencies were exercised
   - Key assertions and expectations
3. Write patterns to scenarios.jsonl (one JSON object per line)
4. Commit scenarios.jsonl

## Output Format (scenarios.jsonl)
Each line is a JSON object with:
- scenario: name of the scenario
- journey: description of the user journey tested
- dependencies: list of real dependencies exercised
- assertions: list of key assertions made
- result: pass/fail

## Output
Commit scenarios.jsonl and report summary.