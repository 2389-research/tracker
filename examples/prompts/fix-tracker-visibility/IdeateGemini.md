You are working in `run.working_dir`.

## Task
Read `.ai/visibility/gaps-analysis.md` and propose solutions for fixing tracker's
silent periods. Focus on user experience and perception of responsiveness.

## Constraints
- Solutions must not break the existing event architecture
- Prefer emitting new events over restructuring the TUI
- Each fix should be independently shippable

## Think About
- Perceived performance vs actual performance
- What feedback keeps users confident the system is working?
- Minimal information that eliminates "is it stuck?" anxiety
- How to handle truly long operations (60s+ LLM calls) gracefully

## Output
Write proposals to `.ai/visibility/ideas-gemini.md` with:
- **Proposed Fixes**: Numbered list with file, location, and change description
- **UX Principles**: What makes a CLI feel "alive" vs "frozen"
- **Priority Order**: Which fixes give most visibility improvement per effort