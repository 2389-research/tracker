You are working in `run.working_dir` on branch `feature/integration`.

## Task
The integration tests are failing after merging parallel stream branches.
Diagnose and fix the issues.

## Process
1. Run `go build ./...` and `go test ./...` to see current failures
2. Read `.ai/contracts/` for the agreed interfaces
3. Read `.ai/streams/stream-a/task.md` and `.ai/streams/stream-b/task.md` for what each stream implemented
4. Identify mismatches between stream implementations and contracts
5. Fix the issues — prefer minimal changes that align with the contracts
6. Run tests again to verify
7. Commit fixes with descriptive messages

## Constraints
- Fix integration issues, don't rewrite stream code
- Align implementations with contracts, not the other way around
- If contracts were wrong, update contracts AND both implementations