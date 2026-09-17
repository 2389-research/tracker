FIRST, in one turn, read .ai/build/build-context.md for orientation — it
holds an architecture map and a one-entry-per-milestone log (files touched
+ commit summary) so you don't rediscover the codebase layout and prior
milestones' changes from scratch. It is ADVISORY and may lag the latest
code: SPEC.md and the source are authoritative — if they disagree with the
context file, trust them. It supplements your normal reading; it does not
replace any SPEC.md read this prompt asks for below.

The current milestone failed verification. You were routed here by ONE of
two gates — read the matching block at the END of this prompt:
- TestMilestone RED (build / test / lint / CI failure): the real failure
  output is the "## Failing gate output (TestMilestone)" block.
- VerifyMilestone FAIL (tests were green but the verifier found a
  spec/scope/contract problem): the verifier's findings are the
  "## Verifier findings (VerifyMilestone)" block; the TestMilestone block
  then holds only the verify-budget marker (`verify-budget-ok`), not gate
  output.
Also read:
- The milestone spec: .ai/milestones/current.md
- The relevant source files causing the failures

This is a FULL implementation session, not a quick patch. You have the
tools and time to properly investigate root causes, understand the domain
logic, read the test expectations, and make correct fixes.

Steps:
1. Read the failing test file to understand what it expects
2. Read the source code being tested to understand the current behavior
3. Identify the root cause (not just the symptom)
4. Implement the correct fix
5. Re-run the EXACT failing gate to confirm the fix — BEFORE committing:
   `sh .ai/build/verify.sh`
   (this runs build + every stack's tests + the lint/CI gate — not just
   `go test`). Its Go scope is the milestone's changes in the WORKTREE:
   uncommitted edits and untracked new packages are in scope, plus every
   package that depends on a changed one — so a green run here is a green
   run on exactly the tree you are about to commit.
6. If a test listed in `.ai/milestones/known_failures` now passes because
   of your work, REMOVE it from that file in this milestone (the ship gate
   ignores the file, so a stale entry only hides a regression until then).
   Never ADD to `known_failures` / `known_lint_failures` to make the gate
   pass — additions during a milestone are diffed and reported to the
   verifier as a finding — and never edit `.ai/build/verify.sh` or
   `.ai/build/ci-probe.sh` (they are restored from the workflow before
   every gate run and a difference is reported).
7. Commit the fix with a message like "fix(milestone-N): [what was fixed]"

Do NOT make superficial patches. If a test expects two records to NOT
merge, understand WHY they're merging and fix the decision logic.

The instant verification passes AND you have committed, emit DONE and
STOP. Do NOT perform further verification, re-reading, or polishing.
Committing is the precondition for stopping — never end a session with a
green-but-uncommitted tree.

---
## Failing gate output (TestMilestone)

Tail of the last tool node's stdout (64KB cap). On the TestMilestone-red
path this is the real failure signal (go build / go test and the project
CI / golangci-lint gate from .ai/build/verify.sh). On the VerifyMilestone-
fail path it is only the budget marker from CheckVerifyFailBudget — ignore
it and use the next block. Delimited under its own heading — never
interpolated mid-sentence.

${ctx.tool_stdout}

---
## Verifier findings (VerifyMilestone)

The last agent response before this node. On the VerifyMilestone-fail path
this is the verifier's report (the FAIL findings with file:line / grep
evidence). On the TestMilestone-red path it is stale (the previous
Implement/Fix session's output) — ignore it and use the block above.

${ctx.last_response}