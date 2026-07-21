# trackerchat

Drive [Tracker](../../README.md) pipelines from your terminal. Type a request,
answer gates inline, watch the run happen — a REPL front-end.

`trackerchat` is a **second consumer of the [transport
boundary](../../docs/architecture/transport-boundary.md)**, built from the same
[`transport/chatops`](../../transport/chatops) core as the Slack bot
([`trackerbot`](../trackerbot)). It proves the boundary is real: the only
Slack-specific code there becomes terminal I/O here — everything else (the
`Runner`, commands, gating, estimate/confirm, budget-bump, steer, durable resume)
is shared and inherited for free.

```
$ trackerchat
trackerchat — type a request (e.g. `make me a CLI that greets people`), or /quit.
> run ask_and_execute
🔎 Estimate: ~$0.30–$2.10 · 6 steps · 3 agent nodes …
🚀 starting `Ask & Execute` — I'll keep you posted here.
❓ Which approach should I take?
   1) Minimal  [default]
   2) Full-featured
   (reply with a number, a label, or your own text)
> 1
…
✅ done — success · $0.42 · 1m03s
```

## How it works

The REPL is one conversation, so it uses one fixed thread identity. Each input
line is routed by the same rules the Slack bot applies to a thread:

- **A pending gate?** The line answers it (a number or label selects a choice;
  anything else is the free-text / "other" answer).
- **Otherwise** it's a fresh request — dispatched on a goroutine so the loop
  stays free to read the gate answers a run will ask for.

The transport-neutral logic lives in [`transport/cli`](../../transport/cli)
(`Session`, the `ThreadUI`, gate rendering, answer mapping) and is unit-tested
end-to-end via a fake dispatcher — no LLM or Slack needed. `cmd/trackerchat` is
just the wiring: build a `Config`, a `RunManager`, and a `Runner`, then run the
session over stdin/stdout.

## Commands

The full [`trackerbot` command set](../trackerbot/README.md#commands) works here
too — `run <workflow>`, free-text requests (with a provider key), `retry`,
`bump <dollars>`, `steer <guidance>`, `status`, `cancel`, `runs`, `workflows`,
`help` — plus `/quit` (or `/exit`) to leave.

## Configuration

| Env var | Purpose | Default |
|---|---|---|
| `ANTHROPIC_API_KEY` / etc. | provider keys the workflows use (and natural-language intent) | — |
| `TRACKERCHAT_WORKDIR` | where `run <name>` finds local `.dip` files | `.` |
| `TRACKERCHAT_RUNS` | base dir for isolated per-run workdirs | `$TMPDIR/trackerchat-runs` |
| `TRACKERCHAT_MODEL` | model for natural-language intent | `claude-haiku-4-5-20251001` |
| `TRACKERCHAT_BACKEND` | agent backend (`native`/`claude-code`/`acp`) | `native` |
| `TRACKERCHAT_MAX_COST_CENTS` | fail-closed per-run cost ceiling in cents; `0` disables | `500` ($5) |
| `TRACKERCHAT_KEEP_WORKDIRS` | `1` retains finished-run workdirs (else reaped) | — |

Without a provider key the REPL still works with the explicit
`run <workflow> [k=v …]` grammar.

## Run

```bash
export ANTHROPIC_API_KEY=sk-…
go run ./cmd/trackerchat          # or: go build -o trackerchat ./cmd/trackerchat && ./trackerchat
```

## Limitations

- One conversation per process: a new request while a run is active is rejected
  (that thread is busy). Control commands (`status`, `cancel`, `steer`) still
  work mid-run.
- Output interleaves with your typing, as any REPL bot's does.
- Human gates are answered from a single input slot, so gates must be
  **sequential** (the norm). A workflow whose *parallel* branches each open a
  human gate at the same time can only answer one from the terminal — fine for
  the built-in workflows, which gate sequentially.
- Type answers only after a gate is shown: a line typed before its gate prompt
  appears is treated as a fresh request (and rejected while a run is active), not
  as the answer.
