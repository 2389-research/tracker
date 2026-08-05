# Tracker Architecture Docs

This directory collects per-subsystem design documentation for tracker.
Start with [`ARCHITECTURE.md`](../../ARCHITECTURE.md) at the repo root for a
high-level overview and reading order; this index is a flat map to the
subsystem write-ups.

All diagrams are mermaid and render on GitHub. Diagram styles vary by
subject: sequence diagrams for time-ordered flows (run loops, steering,
retry), flowcharts for layer or data-flow maps, state diagrams for outcome
transitions.

## Subsystems

| Doc | Scope |
|---|---|
| [`engine.md`](./engine.md) | Pipeline execution engine: run loop, outcomes, edge selection, retry, restart, checkpoint resume, budget guard, steering channel, git artifact integration, emitted events. |
| [`handlers.md`](./handlers.md) | `Handler` interface, `HandlerRegistry`, shape → handler mapping, and an index of every per-handler doc below. |
| [`handlers/codergen.md`](./handlers/codergen.md) | LLM agent node (`codergen` / `box` shape): backend selection, prompt resolution, session wiring, transcript collection, response capture. |
| [`handlers/tool.md`](./handlers/tool.md) | Shell command node (`tool` / `parallelogram`): safe-key allowlist, command expansion, timeouts, output capping, denylist/allowlist. |
| [`handlers/human.md`](./handlers/human.md) | Human gate node (`wait.human` / `hexagon`): choice, freeform, yes/no, interview, hybrid review modes; autopilot and webhook interviewers. |
| [`handlers/parallel-fan-in.md`](./handlers/parallel-fan-in.md) | Fan-out (`parallel` / `component`) and join (`parallel.fan_in` / `tripleoctagon`): branch dispatch, `suggested_next_nodes`, branch overrides, `parallel.results` JSON. |
| [`handlers/subgraph.md`](./handlers/subgraph.md) | Sub-pipeline composition (`subgraph` / `tab`): named graph reference resolution, `subgraph_params` passing, scoped events. |
| [`handlers/conditional.md`](./handlers/conditional.md) | Routing-only diamond nodes (`conditional` / `diamond`): no-op handler that delegates to edge condition evaluation. |
| [`handlers/manager-loop.md`](./handlers/manager-loop.md) | Async child-pipeline supervisor (`stack.manager_loop` / `house`). |
| [`agent.md`](./agent.md) | Session turn loop, tool registry, context compaction, episodic memory, plan-before-execute, repository localization, steering. |
| [`llm.md`](./llm.md) | `llm.Client`, middleware stack, `TokenTracker`, cost estimator via model catalog, and the four provider adapters (`anthropic`, `openai`, `google` / Gemini, `openaicompat`). |
| [`bedrock-gateway.md`](./bedrock-gateway.md) | Operator setup for gateway routing: `TRACKER_GATEWAY_URL` + `TRACKER_GATEWAY_KIND` (`cf-aig` default vs `bedrock`), per-provider suffix conventions, precedence, and the bedrock-kind caveats. |
| [`adapter.md`](./adapter.md) | `pipeline/dippin_adapter.go` — the bridge from dippin-lang IR to tracker's `Graph` model. Documents every field mapping and naming convention. |
| [`context-flow.md`](./context-flow.md) | User-facing model of context, fidelity, scoping, declared `reads:`/`writes:`, and safe-key restrictions. |
| [`tui.md`](./tui.md) | Bubbletea state machine, sidebar, activity log, modal content types (hybrid, review, interview), verbosity cycling, zen mode, search. |
| [`transport-boundary.md`](./transport-boundary.md) | The UI-agnostic boundary a front-end plugs into — `tracker.Config` → `Engine`, the `Interviewer` gate seam, the event streams, `RunManager` for concurrency — so TUI, Slack (`cmd/trackerbot`), and web/mobile are peers. How to build a new transport. |
| [`backends.md`](./backends.md) | `AgentBackend` interface and the three implementations: native (`agent.Session`), claude-code (subprocess + NDJSON), ACP (Agent Client Protocol). Environment scoping and per-node override semantics. |
| [`artifacts.md`](./artifacts.md) | Workdir layout, `checkpoint.json`, `activity.jsonl`, `status.json` per node, stage `prompt.md` / `response.md`, git-backed history, bundle export, `.dipx` bundle identity stamping (v0.26.0+). |
| [`linux-security-primitives.md`](./linux-security-primitives.md) | Kernel primitives the `writable_paths` jail relies on: Landlock ABI v3, `openat2` RESOLVE_* flags, EACCES vs EXDEV vs ELOOP, `mkdirat`/`unlinkat` against an `openat2` dirfd, `PR_SET_PDEATHSIG`, the `/proc/self/exe __jail-exec` re-exec pattern, refuse-to-start gates, and residual escape classes. |
| [`agent-tool-jail-checklist.md`](./agent-tool-jail-checklist.md) | The `writable_paths` seam invariant — every agent tool must route filesystem mutations and subprocesses through `exec.ExecutionEnvironment` — plus the `make tools-jail-check` lint that enforces it. |
| [`writable-paths-audit-checklist.md`](./writable-paths-audit-checklist.md) | The 9-class reviewer audit checklist for auditing a change to the `writable_paths` jail itself, grounded in the #275 review rounds. |
| [`security-pr-process.md`](./security-pr-process.md) | The "freeze and prove" process pattern for security-boundary PRs: spec-first threat model → freeze the public API → prove the contract with invariant/property tests → audit-class sweep → small patch against the frozen contract. |

## Where to start

Three reading orders, depending on what you want to learn:

- **"How does a run execute?"** — [`engine.md`](./engine.md), then
  [`handlers.md`](./handlers.md), then whichever per-handler doc is
  relevant to the node type you're looking at, then
  [`artifacts.md`](./artifacts.md) to see what lands on disk.
- **"How does data move between nodes?"** — start at
  [`context-flow.md`](./context-flow.md) (authoritative
  user-facing doc), then [`engine.md#outcomes-and-routing`](./engine.md#outcomes-and-routing)
  for routing decisions, then [`adapter.md`](./adapter.md) for how prompt /
  condition variables are declared upstream in dippin-lang.
- **"How does an agent actually call an LLM?"** — start at
  [`handlers/codergen.md`](./handlers/codergen.md), then
  [`agent.md`](./agent.md) for the session turn loop, then
  [`llm.md`](./llm.md) for the provider adapters and middleware stack. If
  the node uses an external backend, read [`backends.md`](./backends.md).

## Security / `writable_paths` jail

Two companion docs for the agent filesystem jail (`agent/exec/jail*.go`,
`pipeline/handlers/codergen_jail.go`):

- [`agent-tool-jail-checklist.md`](./agent-tool-jail-checklist.md) — the
  standing invariant (every `agent/tools/` tool routes mutations and
  subprocesses through `exec.ExecutionEnvironment`), the per-tool threat-model
  table, and the `make tools-jail-check` lint. Use it when **adding a tool**.
- [`writable-paths-audit-checklist.md`](./writable-paths-audit-checklist.md) —
  the 9-class reviewer checklist for auditing a change to the jail itself. Use
  it when **reviewing a diff** that touches jail code.

## Pre-v0.10 design docs

Archival design documents from tracker's pre-v0.10 era live under
`docs/plans/`. Those are historical and not linked individually here —
they capture design deliberation, not the current system.

## Conventions

- **Doc-relative links everywhere.** Cross-doc links inside this tree:
  `./engine.md`, `./handlers/tool.md`, `../manager-loop.md`. Source-file
  links use the same convention — doc-relative from the current file:
  `../../pipeline/engine.go` from a file in `docs/architecture/`,
  `../../../pipeline/handlers/tool.go` from a file in
  `docs/architecture/handlers/`. Pick the form that actually resolves when
  the doc is rendered on GitHub; do not use bare repo-relative paths like
  `pipeline/engine.go`.
- **Mermaid** is used everywhere. Every subsystem doc should render on
  GitHub. If a diagram needs more than ~15 nodes, split it into multiple
  diagrams rather than one unreadable megadiagram.
- **Don't duplicate** — the adapter doc owns naming mismatches, the
  context-flow doc owns declared reads/writes, `CLAUDE.md` owns gotchas.
  Subsystem docs cross-link rather than re-describe.
- **Describe what IS**, not what's coming. Facts belong in these docs;
  design deliberation belongs in `docs/plans/`.
