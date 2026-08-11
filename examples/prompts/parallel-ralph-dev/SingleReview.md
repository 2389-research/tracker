You are working in `run.working_dir` on branch `feature/integration`.

## Task
Review the integrated feature for correctness, quality, and completeness.

## Context
- `.ai/feature-spec.md` or brainstorm context — the requirements
- `.ai/contracts/` — the agreed interfaces
- `.ai/gate-results.txt` — objective gate results
- `.ai/streams/stream-*/task.md` — what each stream was supposed to do
- `git diff main...HEAD` — all changes being reviewed

## Review Focus
Objective gates already verified: build, tests, vet, TODOs.
Focus your review on what automated checks CANNOT catch:
1. **Contract adherence** — do implementations match the agreed interfaces?
2. **Requirement coverage** — are all requirements addressed?
3. **Security** — auth, injection, secrets, unsafe deserialization
4. **Edge cases** — error paths, empty inputs, concurrency
5. **Consistency** — do the two streams' code feel like one coherent feature?

## Output
Write your review to `.ai/review.md` with:
- **Verdict**: approve / request_changes
- **Issues**: numbered list of concerns (if any)
- **Suggestions**: optional improvements (non-blocking)

If approving, set STATUS: success.
If requesting changes, set STATUS: fail with details.