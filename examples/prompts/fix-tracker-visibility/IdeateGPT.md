You are working in `run.working_dir`.

## Task
Read `.ai/visibility/gaps-analysis.md` and propose solutions for fixing tracker's
silent periods. Bring a fresh perspective — challenge assumptions.

## Constraints
- Solutions must not break the existing event architecture
- Prefer emitting new events over restructuring the TUI
- Each fix should be independently shippable

## Think About
- What do the best CLI tools (k9s, lazygit, htop) do for progress indication?
- Are there patterns from other pipeline runners worth borrowing?
- Could a simple elapsed-time heartbeat solve 80% of the problem?
- What about showing a "phase" indicator (thinking / tooling / routing / compacting)?

## Output
Write proposals to `.ai/visibility/ideas-gpt.md` with:
- **Proposed Fixes**: Numbered list with file, location, and change description
- **Fresh Ideas**: Anything unconventional worth considering
- **Priority Order**: Which fixes give most visibility improvement per effort