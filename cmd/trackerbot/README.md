# trackerbot

Drive [Tracker](../../README.md) pipelines from Slack. Mention the bot with a
request and it starts a run in a thread, keeps that thread updated with
notifications and clarifying questions, and delivers the result — with an
arbitrary number of runs going at once, each isolated in its own thread.

```
@trackerbot make me a CLI that greets people
@trackerbot run build_product
@trackerbot status        @trackerbot cancel        @trackerbot runs
```

## How it works

One process holds **one** outbound Socket Mode WebSocket and runs **N**
concurrent pipelines, one per thread:

- **`thread_ts` is the run identity.** A mention in a new thread starts a run
  keyed by that thread; every later message routes by it.
- **Outbound (run → Slack) is automatic:** each run has its own thread-bound UI,
  so its notifications and gate questions post to its own thread with no shared
  state.
- **Inbound (Slack → run) is a two-level demux:** `thread_ts` picks the run, and
  a `gate_id` (encoded in a button's `action_id`, or the thread's pending
  freeform gate) picks the exact question to answer. A button click for one
  run's gate can never wake another's.

Concurrency is bounded by a cap; each run executes in its own isolated working
directory. See the parent [Transport boundary](../../ROADMAP.md) work — the bot
is a pure consumer of the `tracker` library (`Config.Interviewer`, the event
stream, and `RunManager`).

## Human gates

All four gate modes work in-thread:

| Mode | Presentation |
|---|---|
| choice / yes-no | buttons |
| freeform | "reply in this thread" |
| interview | one question at a time (buttons or reply), accumulated into a form |

## Slack app setup

1. Create an app at <https://api.slack.com/apps>.
2. **Socket Mode**: enable → generate an **App-Level Token** (`xapp-…`, scope
   `connections:write`).
3. **OAuth & Permissions** → Bot Token Scopes: `app_mentions:read`, `chat:write`,
   `channels:history`, `groups:history`.
4. **Event Subscriptions** → subscribe to bot events: `app_mention`,
   `message.channels`, `message.groups`.
5. **Interactivity & Shortcuts**: on (Socket Mode needs no request URL).
6. Install to the workspace → copy the **Bot Token** (`xoxb-…`).
7. Invite the bot to a channel: `/invite @yourbot`.

## Configuration

| Env var | Purpose | Default |
|---|---|---|
| `SLACK_BOT_TOKEN` | bot token (`xoxb-…`) | required |
| `SLACK_APP_TOKEN` | app-level token (`xapp-…`) | required |
| `ANTHROPIC_API_KEY` / etc. | provider keys the workflows use | — |
| `TRACKERBOT_WORKDIR` | where `run <name>` finds local `.dip` files | `.` |
| `TRACKERBOT_RUNS` | base dir for isolated per-run workdirs | `$TMPDIR/trackerbot-runs` |
| `TRACKERBOT_MAX_CONCURRENT` | concurrent run cap | `8` |
| `TRACKERBOT_MODEL` | model for natural-language intent | `claude-haiku-4-5-20251001` |
| `TRACKERBOT_BACKEND` | agent backend (`native`/`claude-code`/`acp`) | `native` |

Natural-language intent needs a provider key; without one the bot still works
with the explicit `run <workflow> [k=v …]` grammar.

## Run

```bash
export SLACK_BOT_TOKEN=xoxb-…
export SLACK_APP_TOKEN=xapp-…
export ANTHROPIC_API_KEY=sk-…
go run ./cmd/trackerbot          # or: go build -o trackerbot ./cmd/trackerbot && ./trackerbot
```

## Commands

- `@trackerbot <free text>` — pick a workflow via the LLM and start it.
- `@trackerbot run <workflow> [k=v …]` — start a named built-in/local workflow.
- `@trackerbot status` — this thread's run state.
- `@trackerbot cancel` — stop this thread's run.
- `@trackerbot runs` — list active runs.
- `@trackerbot help` — usage.

## Extending (decision points)

The transport-neutral logic is testable without Slack (see the `_test.go` files).
Four spots are marked `DECISION POINT` for tuning:

- **`intent.go` (D1)** — natural-language → workflow classification.
- **`notify.go` (D2)** — which events reach the thread; cost throttle.
- **`delivery.go` (D3)** — adapt success delivery to what was built.
- **`runner.go` (D4)** — at-capacity policy (reject / queue / preempt).

## Durability

Each thread gets a deterministic workdir + checkpoint under `TRACKERBOT_RUNS`,
and active runs are recorded in `trackerbot-state.json`. On startup the bot
**resumes** any run that was interrupted by a previous exit — it replays from the
checkpoint and posts "🔄 resuming…" in the thread. Completed runs clear their
record and checkpoint.

## Limitations

- Human gates inside *parallel* pipeline branches share one interviewer; freeform
  replies are single-slot per thread. Fine for sequential gates (the norm).
- The live Slack rendering is verified manually; the routing, gate, resume, and
  intent logic are unit-tested.
