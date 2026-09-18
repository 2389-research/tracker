Read the critique at .ai/decisions/critique.md.

Select the winning implementation using this strict priority:
  1. SPEC FIDELITY — the implementation that most faithfully matches
     the spec wins. Anything extra is a penalty, not a bonus.
  2. TEST EVIDENCE — among spec-faithful implementations, prefer the
     one with the strongest passing test evidence.
  3. MINIMALITY — among tied implementations, prefer the smallest,
     cleanest diff.

Read the candidate evidence too — .ai/candidates/<name>.diff and
.ai/candidates/<name>.test for each of claude, codex, gemini. A candidate
whose result line said EMPTY DIFF, MISSING WORKTREE or TESTS FAIL is
disqualified unless every candidate is in that state (the run only reached
you past that gate if a human chose "critique-anyway").

Write your decision to .ai/decisions/selection.md with:
## Winner
Name of winning implementation (claude, codex, or gemini).

## Rationale
Why this one won. Reference specific spec requirements.

## Runner-up
Second place and what it did differently.

## Rejected
For EACH non-winner: why it was not selected. Be specific — this is
the record of why that approach was not taken.

## Spec compliance checklist
For the winner only: check each numbered requirement from the spec.

WINNER contract — the LAST line of .ai/decisions/selection.md must be
exactly `WINNER: <name>` where <name> is one of claude, codex, gemini —
alone on its line, nothing else on it, outside any code fence. ApplyWinner
merges ONLY what that line names: it reads the file mechanically, ignores
prose (a "## Winner" heading or "beat codex" sentence is never parsed), and
refuses to merge anything when the line is missing, names anything other
than exactly one candidate, or when several WINNER: lines disagree. So
write ONE WINNER: line, as the final line, and never draft an earlier one
you might forget to change.

IMPORTANT: Output exactly one of these as the last line of your RESPONSE:
STATUS:success