Read the critique at .ai/decisions/critique.md.

Select the winning implementation using this strict priority:
  1. SPEC FIDELITY — the implementation that most faithfully matches
     the spec wins. Anything extra is a penalty, not a bonus.
  2. TEST EVIDENCE — among spec-faithful implementations, prefer the
     one with the strongest passing test evidence.
  3. MINIMALITY — among tied implementations, prefer the smallest,
     cleanest diff.

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

IMPORTANT: Output exactly one of these as the last line:
STATUS:success