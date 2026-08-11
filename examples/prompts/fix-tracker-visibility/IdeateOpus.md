You are working in `run.working_dir`.

## Task
Read `.ai/visibility/gaps-analysis.md` and propose solutions for fixing tracker's
silent periods. Focus on PRACTICAL, minimal-diff fixes.

## Constraints
- Solutions must not break the existing event architecture
- Prefer emitting new events over restructuring the TUI
- Each fix should be independently shippable
- Consider both --no-tui (console) and TUI dashboard modes

## Think About
- What new event types or messages are needed?
- Where should "heartbeat" or "phase change" events be emitted?
- How to show per-turn progress in agentic loops?
- How to surface edge selection / context compaction visually?
- What's the right granularity — too many events is also bad

## Output
Write proposals to `.ai/visibility/ideas-opus.md` with:
- **Proposed Fixes**: Numbered list with file, location, and change description
- **Priority Order**: Which fixes give most visibility improvement per effort
- **Risk Assessment**: What could break with each change