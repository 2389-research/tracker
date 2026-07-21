# trackerbot

Drive [Tracker](../../README.md) pipelines from Slack. Mention the bot with a
request and it starts a run in a thread, keeps that thread updated with
notifications and clarifying questions, and delivers the result — with an
arbitrary number of runs going at once, each isolated in its own thread.

> **New here?** The [**Slack Bot getting-started guide**](https://2389-research.github.io/tracker/trackerbot.html)
> is the friendly walkthrough — with a visual of a full conversation, the Slack
> app setup, commands, security, and troubleshooting. This README is the in-repo
> reference (source layout, demux internals, the setup checklist).

```
@trackerbot make me a CLI that greets people
@trackerbot run build_product
@trackerbot retry         @trackerbot status        @trackerbot cancel        @trackerbot runs
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
directory. The bot is a pure consumer of the
[transport boundary](../../docs/architecture/transport-boundary.md) — the
`tracker` library (`Config.Interviewer`, the event stream, and `RunManager`) —
so it inherits panic containment, the one-terminal-event guarantee, per-run
isolation, and durable resume from the core (see the boundary doc's invariants
section). No engine changes.

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
| `TRACKERBOT_ALLOWED_USERS` | comma-separated Slack user ids allowed to drive the bot; empty = open (logged as a warning) | — |
| `TRACKERBOT_MAX_COST_CENTS` | fail-closed per-run cost ceiling in cents; `0` disables | `500` ($5) |
| `TRACKERBOT_CONFIRM_OVER_CENTS` | require a Run/Cancel click when the estimated cost is at/above this; `0` disables | `200` ($2) |
| `TRACKERBOT_KEEP_WORKDIRS` | `1` retains finished-run workdirs (else reaped to bound disk) | — |

Natural-language intent needs a provider key; without one the bot still works
with the explicit `run <workflow> [k=v …]` grammar.

**Security & cost.** Set `TRACKERBOT_ALLOWED_USERS` to restrict who can trigger
paid runs — without it, anyone in the bot's channels can. Every run carries a
fail-closed budget (`TRACKERBOT_MAX_COST_CENTS`), and workflow names are
validated so a mention can never load an arbitrary `.dip` off the host.

## Run

```bash
export SLACK_BOT_TOKEN=xoxb-…
export SLACK_APP_TOKEN=xapp-…
export ANTHROPIC_API_KEY=sk-…
go run ./cmd/trackerbot          # or: go build -o trackerbot ./cmd/trackerbot && ./trackerbot
```

## Slash command & App Home (optional Tier-3 surfaces)

Two extra entry points, both reusing the same Runner. They need a little more
Slack app config and a live workspace to verify (the pure view builder is
unit-tested; the event plumbing is workspace-verified):

- **`/tracker <what you want>`** — a slash command works anywhere, no `@mention`
  needed. The bot opens a thread (posting the request) and runs there, so every
  in-thread command (`retry`, `steer`, gates…) applies unchanged.
  Setup: **Slash Commands** → create `/tracker` (Socket Mode needs no request
  URL); add the `commands` Bot Token Scope; reinstall the app.
- **App Home tab** — a standing dashboard: how-to + a live list of active runs,
  refreshed each time it's opened.
  Setup: **App Home** → enable the **Home Tab**; **Event Subscriptions** →
  subscribe to the `app_home_opened` bot event.

## Commands

- `@trackerbot <free text>` — pick a workflow via the LLM and start it.
- `/tracker <free text>` — the same, as a slash command (opens a thread).
- `@trackerbot run <workflow> [k=v …]` — start a named built-in/local workflow.
- `@trackerbot retry` — re-run this thread's last workflow (also `again` / `rerun`).
- `@trackerbot bump <dollars>` — re-run the last workflow with a raised cost ceiling (offered after a `budget_exceeded` run).
- `@trackerbot steer <guidance>` — inject a note into the running workflow (surfaces at the next node; the workflow must reference `steer.guidance` to act on it — e.g. `${ctx.steer.guidance}` in an agent prompt).
- `@trackerbot workflows` — list workflows you can run.
- `@trackerbot status` — this thread's run state, with a live `5/9 steps · $1.12 · <node>` progress digest.
- `@trackerbot cancel` — stop this thread's run.
- `@trackerbot runs` — list active runs.
- `@trackerbot help` — usage.

## Extending (decision points)

The transport-neutral logic lives in [`transport/chatops`](../../transport/chatops)
(Runner, the interviewer, notifier, delivery, store, intent) and is fully tested
without Slack; `cmd/trackerbot` is just the Slack transport (Socket Mode + Block
Kit). Four spots in `transport/chatops` are marked `DECISION POINT` for tuning:

- **`intent.go` (D1)** — natural-language → workflow classification.
- **`notify.go` (D2)** — which events reach the thread; cost throttle.
- **`delivery.go` (D3)** — adapt success delivery to what was built.
- **`runner.go` (D4)** — at-capacity policy (reject / queue / preempt).

A new chat transport (Discord/Teams/Email) reuses all of `transport/chatops` and
supplies only a `ThreadUI` + its inbound event loop.

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
