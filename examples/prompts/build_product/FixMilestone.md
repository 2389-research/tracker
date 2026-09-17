FIRST, in one turn, read .ai/build/build-context.md for orientation — it
holds an architecture map and a one-entry-per-milestone log (files touched
+ commit summary) so you don't rediscover the codebase layout and prior
milestones' changes from scratch. It is ADVISORY and may lag the latest
code: SPEC.md and the source are authoritative — if they disagree with the
context file, trust them. It supplements your normal reading; it does not
replace any SPEC.md read this prompt asks for below.

The current milestone failed verification. Read:
- The milestone spec: .ai/milestones/current.md
- The failing gate's real stdout — the "## Failing gate output" block at
  the END of this prompt (build / test / lint / CI output from the gate
  that routed you here)
- The relevant source files causing the failures

This is a FULL implementation session, not a quick patch. You have the
tools and time to properly investigate root causes, understand the domain
logic, read the test expectations, and make correct fixes.

Steps:
1. Read the failing test file to understand what it expects
2. Read the source code being tested to understand the current behavior
3. Identify the root cause (not just the symptom)
4. Implement the correct fix
5. Re-run the EXACT failing gate to confirm the fix: `sh .ai/build/verify.sh`
   (this runs build + every stack's tests + the lint/CI gate — not just `go test`)
6. Commit the fix with a message like "fix(milestone-N): [what was fixed]"

Do NOT make superficial patches. If a test expects two records to NOT
merge, understand WHY they're merging and fix the decision logic.

The instant verification passes AND you have committed, emit DONE and
STOP. Do NOT perform further verification, re-reading, or polishing.
Committing is the precondition for stopping — never end a session with a
green-but-uncommitted tree.

---
## Failing gate output

Tail of the failing gate's stdout (64KB cap) that routed here — the real
failure signal (go build / go test and the project CI / golangci-lint gate
from .ai/build/verify.sh). Delimited under its own heading — never
interpolated mid-sentence.

${ctx.tool_stdout}