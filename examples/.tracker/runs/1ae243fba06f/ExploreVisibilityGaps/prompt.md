# Context Summary (fidelity: summary:high)

## graph.default_max_retries
2

## graph.default_fidelity
summary:high

---

You are working in `run.working_dir`.

## Task
Investigate why the tracker TUI appears "locked up" during normal operation.
The TUI sometimes shows no feedback while work is actively happening.

## Investigation Steps
1. Read the TUI implementation:
   - tui/app.go, tui/nodelist.go, tui/agentlog.go, tui/statusbar.go
   - tui/thinking.go, tui/messages.go, tui/adapter.go, tui/state.go
2. Read the execution pipeline:
   - pipeline/engine.go (Engine.Run loop, selectEdge)
   - agent/session.go (Session.Run, turn loop)
   - llm/client.go (Complete, streaming)
   - llm/trace_logger.go (event batching, 150ms flush)
3. Read the handler chain:
   - pipeline/handlers/codergen.go
   - cmd/tracker/main.go (event routing)
4. Identify EVERY silent gap — where operations happen with NO TUI feedback:
   - Between node transitions (edge selection)
   - Between LLM turns inside a session
   - During context compaction
   - Before first TraceRequestStart
   - Between tool completion and next LLM request
   - During provider network latency
   - Output buffering in trace logger

## Output
Write a comprehensive analysis to `.ai/visibility/gaps-analysis.md` with:
- **Silent Gaps**: Every identified gap, its duration range, and why it's silent
- **Event Flow**: The complete chain from engine to TUI, noting where events are missing
- **Root Causes**: Categorized list of why each gap exists
- **Severity Ranking**: Which gaps cause the most user confusion, ranked