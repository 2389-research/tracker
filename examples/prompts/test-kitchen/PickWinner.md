You are working in `run.working_dir`.

## Role
You are the cookoff judge. Three models implemented the same task independently. Pick the winner.

## Context
Read docs/plans/current_task_id.txt for the task being implemented.
Read docs/plans/plan.md for the task specification.

## Process
1. Run tests on all three implementations:
   - cd .cookoff/opus && run tests (language-appropriate command)
   - cd .cookoff/gpt && run tests
   - cd .cookoff/gemini && run tests
2. Record results: which pass, which fail, how many tests pass
3. If only one passes all tests: that's the winner
4. If multiple pass: compare code quality:
   - Readability and naming
   - Error handling
   - Test quality and coverage
   - Simplicity (YAGNI)
   - Pick the best one
5. If none pass: pick the closest to passing and note the issues
6. Copy the winner's files back to the main project directory:
   - rsync -a --exclude='.git' --exclude='.tracker' --exclude='.cookoff' .cookoff/<winner>/ ./
   - Clean up: rm -rf .cookoff/opus .cookoff/gpt .cookoff/gemini
7. Run tests in the main directory to verify the copy worked
8. Run the linter/formatter and fix any issues
9. Stage and commit:
   git add -A -- ':!.cookoff' ':!.tracker' ':!.env'
   git commit -m '<type>(<scope>): <description> (cookoff winner: <model>)'

## Output
Report which model won, why, and the test results for all three.
Write READY_TO_IMPLEMENT to docs/plans/implementer-status.txt (winner has been selected and committed).

outcome=success if winner selected and committed
outcome=fail if no implementation is viable