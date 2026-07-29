# Transport Boundary

A **transport** is a front-end that drives Tracker runs and relays them to a
human: the terminal TUI, the Slack bot ([`cmd/trackerbot`](../../cmd/trackerbot)),
or a future web/mobile app. This doc describes the boundary those front-ends
plug into — a small, deliberate library surface (`tracker.Config` → `Engine`,
plus `RunManager`) that the core exposes so **every transport is a first-class
peer**, none privileged.

The design goal: what the library can do and what the product can do are the
same set. The `tracker` CLI proves it — since the CLI→library unification it
runs on nothing but this boundary (see the run/TUI paths in
[`cmd/tracker/run.go`](../../cmd/tracker/run.go)).

Source: top-level [`tracker.go`](../../tracker.go), [`tracker_runmanager.go`](../../tracker_runmanager.go),
[`pipeline/events.go`](../../pipeline/events.go), [`pipeline/handlers/human.go`](../../pipeline/handlers/human.go).

## The shape

```mermaid
flowchart TB
    subgraph transports["Transports (peers)"]
        tui["TUI<br/>BubbleteaInterviewer"]
        slack["Slack<br/>SlackInterviewer"]
        web["web / mobile<br/>(future)"]
    end
    subgraph boundary["Boundary (this doc)"]
        cfg["tracker.Config<br/>NewEngineFromGraph / Run"]
        rm["RunManager<br/>(N concurrent runs)"]
        iv["handlers.Interviewer<br/>(gate answering)"]
        ev["PipelineEvent + agent.Event<br/>(progress stream)"]
    end
    subgraph core["Core (UI-agnostic)"]
        eng["pipeline.Engine"]
        reg["HandlerRegistry"]
    end
    transports --> cfg
    transports --> rm
    transports -->|implement| iv
    transports -->|subscribe| ev
    cfg --> eng
    rm --> cfg
    iv --> reg
    eng --> ev
```

The core (`pipeline.Engine`) knows nothing about LLMs, subprocesses, human
gates, terminals, or Slack — see [`engine.md`](./engine.md). A transport is
fully described by **six interaction categories**, all composed from existing
public types.

## 1. Start a run

| Entry | Use |
|---|---|
| `tracker.Run(ctx, source, cfg)` | one-call convenience (parses, runs, closes) |
| `tracker.NewEngineWithContext(ctx, source, cfg)` → `Engine.Run(ctx)` | parse + run, holding the engine |
| `tracker.NewEngineFromGraph(ctx, graph, cfg)` → `Engine.Run(ctx)` | run a **pre-parsed** graph; the caller resolved the source (and any `subgraph_ref` files → `Config.Subgraphs`) itself |

Everything a transport supplies is a field on `tracker.Config` (all optional):

- `Interviewer handlers.Interviewer` — the human-gate seam (category 2).
- `EventHandler pipeline.PipelineEventHandler`, `AgentEvents agent.EventHandler`,
  `LLMTrace llm.TraceObserver` — the progress streams (category 3).
- `Subgraphs`, `ToolSafety`, `Budget`, `Backend`, `Model`, `Provider`,
  `GatewayURL`/`GatewayKind`, `Params`, `Context`, `Git`, `CheckpointDir`,
  `ResumeRunID`, `TokenTracker`, `LLMClient`.

`Config.TokenTracker` lets an in-process transport (the TUI) share one token/cost
tracker with the engine for its live spend readout.

## 2. Answer human gates — the key seam

`Config.Interviewer` accepts anything implementing the interviewer family in
[`pipeline/handlers/human.go`](../../pipeline/handlers/human.go); the human
handler upgrades to the richest supported mode via type assertion:

```go
type Interviewer interface { Ask(prompt string, choices []string, def string) (string, error) }
type FreeformInterviewer interface { Interviewer; AskFreeform(prompt string) (string, error) }
type LabeledFreeformInterviewer interface { FreeformInterviewer; AskFreeformWithLabels(...) (string, error) }
type InterviewInterviewer interface { FreeformInterviewer; AskInterview([]Question, *InterviewResult) (*InterviewResult, error) }
```

Optional side-interfaces (checked by assertion): `Actor() pipeline.Actor` (override
auditing), `Cancel()` (torn down on `Engine.Close`), `SetPipelineContext(ctx)`
(a run cancellation unblocks a waiting gate), and `GateAware`
(`BeginGate(GateInfo)`). The handler calls `BeginGate` immediately before any
`Ask*` method, handing over `{RunID, NodeID, GateID, Mode, Label}` — the same
`GateID` that rides the `gate_opened` event (see §3) — so an out-of-process
transport can correlate the blind `Ask*` callback with the event stream and key
a pending-gate record. It fires with or without an event emitter attached;
interviewers that do not implement it are unaffected.

Reference implementations prove the seam is transport-neutral: `ConsoleInterviewer`,
`tui.BubbleteaInterviewer`, `WebhookInterviewer`, the autopilot interviewers, and
`trackerbot.SlackInterviewer`.

## 3. Observe progress

Two Config-wired streams (a transport merges them):

- **`pipeline.PipelineEventHandler`** — lifecycle: `pipeline_started` (carries a
  `RunSnapshot` — node inventory + resume state, so a mid-run subscriber can seed
  its model), `stage_*`, `cost_updated` (carries a `NodeID` for per-node
  attribution + a `CostSnapshot`), `budget_exceeded`, `validation_overridden`,
  and the terminal event (carries an authoritative `TerminalStatus` — `success` /
  `validation_overridden` / `fail` / `budget_exceeded` / `paused_billing`; the set
  is an **open enum**, so classify with
  `pipeline.TerminalStatus(s).IsSuccess()` rather than switching exhaustively, and
  note `paused_billing` is a *resumable* stop, not a failure). The **top-level** engine
  emits exactly one terminal event carrying `TerminalStatus` for its own run —
  including panic and invariant-error exits, which the backstop covers — so "any
  event with a non-empty `TerminalStatus` **and an unscoped `NodeID`**" is the
  run-finished signal. (A subgraph / `manager_loop` child that trips the shared
  budget guard emits its own scoped `budget_exceeded`, which also carries a
  `TerminalStatus`; its `NodeID` is scoped with a `/`.)
  Gate lifecycle (#509): `gate_opened` / `gate_resolved` bracket every
  interviewer call, carrying a `GateDetail` on `PipelineEvent.Gate` with `NodeID`
  set to the gate node. `gate_opened` describes the question (`GateID`, `Mode`,
  `Label`, `Prompt`, `Choices`); `gate_resolved` repeats the same `GateID` and
  adds `Response`, `Outcome`, `Actor`, `TimedOut`, `Error`. Exactly one
  resolution follows each open — on failure, timeout, and interviewer error too —
  so an event-sourced consumer can reconstruct which question got which answer
  without owning the gate lifecycle itself, and never sees a gate stuck open.
- **`agent.EventHandler`** — per-tool-call activity: `session_*`, `tool_call_*`,
  `text_delta`, usage, provider/model. `turn_metrics` carries per-turn
  `Provider`/`Model`/`Usage` attribution alongside its `Metrics` payload, the
  same shape as `llm_finish` (#508) — either event is a valid basis for per-turn
  cost rollups.
- **`llm.TraceObserver`** (via `Config.LLMTrace`) — raw request/reasoning/text
  trace, for a transport that wants live tokens without owning the client.

`StreamEvent` (NDJSON, `tracker.NewNDJSONWriter`) is the flat wire form of all
three streams, and it is at **payload parity with `activity.jsonl`**: alongside
`terminal_status` / `node_id` / `gate_id` it carries the cost snapshot
(`total_cost_usd`, `provider_totals`, `wall_elapsed_ms`, `estimated`), the
decision detail (`edge_from`, `edge_to`, `edge_priority`, `condition_match`,
`context_snapshot`, `conditions_tried`), the tool diagnostics (`trunc_*`,
`marker_*`, `route_tail`, `auto_status_*`), the override detail (`override_*`),
the full `GateDetail` (`gate_mode`, `gate_label`, `gate_prompt`, `gate_choices`,
`gate_questions`, `gate_response`, `gate_outcome`, `gate_actor`,
`gate_timed_out`), per-turn agent usage (`token_input`, `token_output`,
`token_cache_read`, `token_cache_write`, `turn_cost_usd`), and the
`pipeline_started` node inventory (`snapshot_nodes`, `snapshot_start_node`,
`snapshot_exit_node`, `snapshot_current_node`, `snapshot_completed_nodes`).
Field names match the `activity.jsonl` schema wherever the datum is shared, so a
control plane needs one decoder and never has to parse the audit log out of
band; only the `snapshot_*` group and the per-turn cache/cost extras are
wire-only. Every field is `omitempty`, so a given event carries only the fields
documented for its `type`.

### Don't block the engine: buffered handlers

Handlers are called **synchronously on the engine goroutine**. A subscriber that
does network I/O (a control plane POSTing events) therefore slows or stalls the
run, and several handler sources sharing one sink serialize against each other.
Wrap such a subscriber instead of hand-rolling a queue:

```go
h, err := pipeline.NewBufferedPipelineHandler(mySink, 256, pipeline.OverflowDropOldest)
if err != nil { return err }
defer h.Close()            // flushes pending events; idempotent
cfg.EventHandler = h
// ... after the run:
log.Printf("dropped %d events", h.Dropped())
```

- Hands events to a background goroutine over a bounded queue;
  `pipeline.NewBufferedAgentHandler` is the `agent.EventHandler` equivalent.
- The overflow policy is **explicit** — `OverflowBlock` (backpressure, drops
  nothing), `OverflowDropOldest` (freshest view; for progress UIs), or
  `OverflowDropNewest` (keeps the earliest prefix). There is no usable zero
  value: an unset policy is a constructor error, so no caller loses events by
  omission. `Dropped()` accounts for every discarded event.
- **Invariant: an event with a non-empty `TerminalStatus` is never dropped**, on
  any policy — it is the run-finished signal above. At a full queue it evicts the
  oldest *non-terminal* event instead, searching past (and rotating to the tail)
  any queued terminal event; only a queue holding nothing but undelivered
  terminal events applies backpressure. That rotation is the one case where the
  wrapper reorders a stream — a protected terminal event can land after
  non-terminal events that arrived later, never dropped, and terminal events keep
  their order relative to each other. A terminal event submitted after `Close` is
  delivered synchronously rather than dropped, and `Close` waits for it.
- Delivery is serialized — the wrapper never invokes the wrapped handler from
  two goroutines at once, so the sink need not be thread-safe on its own account.
- A panicking downstream handler is recovered (logged once to stderr) and
  neither the forwarding goroutine nor the engine dies. `Close` waits for the
  flush, so a subscriber that never returns keeps `Close` waiting — bound your
  I/O.

## 4. Control a run

- Cancel: cancel the `ctx` passed to `Run` (or `RunManager.Cancel(key)`).
- Resume: point `Config.CheckpointDir` at an existing checkpoint (or set
  `Config.ResumeRunID`); the engine replays from it automatically.
- Steer: set `Config.SteeringChan` to a `<-chan map[string]string`. Each map
  sent is merged into the pipeline context at the next inter-node boundary
  (drained non-blockingly, so sends never stall the engine), visible to edge
  selection and the next node's prompt. Senders namespace their keys (e.g.
  `steer.guidance`); a workflow acts on a steered value only if it references
  that key. The `trackerbot` `steer <text>` command is a consumer of this seam.

## 5. Deliver results & inspect state

- `tracker.Result` — `RunID`, `Status`, `Context`, `Cost`, `ArtifactRunDir`,
  `TokensByProvider`, `Trace`. `Engine.Run` returns the fail `Result` **alongside**
  the error, so a failed run is still correlatable.
- Read-only APIs any transport can serialize: `ListRuns`, `Audit`, `Diagnose`,
  `Simulate`, `ResolveRunDir`, `MostRecentRunID`, `LoadActivityLog`.
- `ExportBundle` for a portable run history; `NewLLMClient(cfg)` for a standalone
  model call outside a run (e.g. request classification).

## 6. Resolve what to run

`ResolveSource(name, workDir)` (path → local `.dip` → built-in catalog), the
`Workflows()` catalog, `LookupWorkflow`, `OpenWorkflow` — for a transport that
offers a workflow picker or maps free text onto a built-in.

## Concurrency: RunManager

[`tracker_runmanager.go`](../../tracker_runmanager.go) owns **N concurrent runs**
keyed by a caller-chosen external id, so a service (Slack, web) can drive many at
once from one process:

- `Start(ctx, key, source, cfg)` launches a run in its own goroutine, keyed by
  `key` (e.g. a Slack `thread_ts`); an atomic claim guards the active-key and an
  optional concurrency cap (`WithMaxConcurrent` → `ErrAtCapacity` /
  `ErrRunKeyActive` — mechanism, not policy).
- `ManagedRun` exposes `State`/`Done`/`Result`/`RunID`/`ResumeRunID`; the manager
  exposes `Get`/`List`/`Cancel`/`Forget`.
- `RunState` is an **open enum** (`starting`, `running`, `succeeded`, `failed`,
  `canceled`, `paused`) — use `State().Terminal()` plus the states you care about
  rather than an exhaustive switch. `RunPaused` maps from the engine's
  `paused_billing` terminal: the run is *finished-but-resumable*, so `Terminal()`
  is **true** (its goroutine exited, `Done()` is closed, the key is free for the
  resume attempt) while `ResumeRunID()` returns the id to feed back as
  `Config.ResumeRunID`. A control plane must distinguish `paused` from `failed` —
  "add credit and resume" vs "this run is dead".
- Per-run isolation makes concurrency safe: distinct `WorkingDir` per run
  (`WithWorkDirBase`), no shared mutable state, and the webhook interviewer's
  callback port defaults to `:0` so two webhook-gated runs never collide.

## The transports as instances

| Transport | Interviewer | Progress | Concurrency |
|---|---|---|---|
| **TUI** ([`tui.md`](./tui.md)) | `BubbleteaInterviewer` via `Config.Interviewer` | `EventHandler`/`AgentEvents` → `prog.Send`; shares `Config.TokenTracker` | one run per process |
| **Slack** ([`cmd/trackerbot`](../../cmd/trackerbot/README.md)) | `ThreadInterviewer` (thread gates) | `notifier` filters events → thread | `RunManager`, one run per thread |
| **CLI REPL** ([`cmd/trackerchat`](../../cmd/trackerchat/README.md)) | `ThreadInterviewer` (inline text gates) | messages printed to the terminal | one conversation per process |
| **web / mobile** | implement `handlers.Interviewer` | subscribe to the streams | `RunManager` |

Both the Slack bot and the CLI REPL share `transport/chatops` (the `Runner`,
interviewer, notifier, delivery, commands); each supplies only a `ThreadUI` +
its inbound loop. `cmd/trackerchat` (a ~200-line terminal front-end + the
`transport/cli` package) is the concrete demonstration that a second transport
is I/O-only.

## Building a new transport

1. Implement `handlers.Interviewer` (+ the richer extensions you support) to
   present gates in your medium; pass it as `Config.Interviewer`.
2. Provide a `PipelineEventHandler` (and optionally `AgentEvents` / `LLMTrace`)
   that renders progress; pass via `Config`.
3. For many concurrent runs, hold a `RunManager` keyed by your session id, with
   `WithWorkDirBase` for isolation.
4. Resolve requests to a workflow with `ResolveSource` / `Workflows`; deliver
   with `Result` + `Diagnose` / `ExportBundle`.
5. **Prove it with the conformance suite.** Run
   [`transport/conformance`](../../transport/conformance) `RunInterviewerSuite`
   from a test to verify your interviewer honours every gate mode (choice /
   yes-no / freeform / interview) and unblocks on cancellation — the executable
   definition of a correct interviewer, the same suite `SlackInterviewer` passes.

Nothing above touches the engine — the boundary is the whole contract.

## The invariants a transport inherits (enforced, not by convention)

These hold in the **core** regardless of transport — a front-end cannot violate
them, and gets them for free:

- **Exactly one top-level terminal event** carries `TerminalStatus` — including
  panic and invariant-error exits (the engine's backstop). Your run-finished
  signal never goes missing.
- **A handler panic is contained** — it becomes a `fail` terminal event, never a
  crashed host, so a `RunManager` driving many runs survives one bad run.
- **Per-run isolation** — distinct `WorkingDir`, no shared mutable state, per-run
  webhook port; N concurrent runs never collide.
- **Durable resume** — checkpoints are written atomically and keyed
  deterministically, so a process crash resumes from the last completed node.

Your transport's job is presentation and identity (who may run, how gates look);
these safety and durability properties are not yours to re-implement.
