# Transport Implementation Plans — Six Front-Ends

**Date:** 2026-07-21
**Status:** proposed
**Companion to:** [`2026-07-21-transport-expansion.md`](2026-07-21-transport-expansion.md)
(directional overview). This doc is the buildable, milestone-level plan for each.

Every transport plugs into the same seams — nothing here touches the engine:

| Seam | Type | Role |
|---|---|---|
| `Config.Interviewer` | `handlers.Interviewer` | answer human gates |
| `Config.EventHandler` / `AgentEvents` | event handlers | progress streams |
| `tracker.RunManager` | keyed by session id | N concurrent runs |
| `ResolveSource` / `Workflows` | catalog | what to run |
| `Result` / `Diagnose` / `ExportBundle` | outputs | deliver + inspect |
| `transport/conformance` `RunInterviewerSuite` | test kit | prove correctness |

Each transport also inherits the core invariants for free (one terminal event, panic
containment, per-run isolation, durable resume) — see the [transport
boundary](../architecture/transport-boundary.md).

Effort key: **S** ≈ ~1 wk · **M** ≈ 2–4 wk · **L** ≈ 5–8 wk (one engineer, rough).

---

## Phase 0 — shared `transport/chatops` core *(do first; effort S)*

`cmd/trackerbot` already isolates the transport-agnostic logic behind a `ThreadUI` seam.
Lift it into a library package so every chat-shaped transport (Slack, Discord, Teams, Email)
reuses it and only implements a `ThreadUI` + its event loop + auth.

**What moves into `transport/chatops`:**
- `Runner` (session→run routing, admission, lifecycle, reap) — `runner.go`
- `IntentResolver` (grammar + LLM) — `intent.go`
- `notifier` (event → notable-post filter, cost throttle) — `notify.go`
- delivery (`deliver`, `formatDiagnosis`) — `delivery.go`
- `store` (durable resume) — `store.go`
- The public interfaces: `ThreadUI` (`Post`, `PostGate`), `Gate`, `GateAnswer`,
  `Interviewer` bridge, `Intent`.

**What each transport still provides:** a `NewThreadUI(sessionID) ThreadUI`, an inbound event
loop that calls `Runner.OnMention` / `Runner.OnInteraction`, and auth.

**Milestones**
1. Extract packages, keep `cmd/trackerbot` compiling against the new `transport/chatops` API
   (pure move + rename; tests green).
2. Generalize `RunnerDeps` so `sessionID` is transport-neutral (already `thread_ts`-shaped —
   just rename semantics); document the `ThreadUI` contract.
3. Ship a `chatops.Interviewer` that any `ThreadUI` drives, validated by
   `RunInterviewerSuite` (trackerbot already passes it).

**Payoff:** Discord ≈ 1 wk, Teams/Email mostly a `ThreadUI` + auth swap.

---

## 1. GitHub / GitLab — highest dev leverage *(effort M–L)*

**Interaction.** `/tracker run <workflow> [k=v…]` in an issue or PR comment (also a label, or
`workflow_dispatch`). The issue/PR is the session; its body + thread are context for
"fix this issue."

**Package:** `cmd/tracker-gh/` (or `transport/github/`). Not chatops-shaped (comments +
Check Runs, not a live thread), so it's its own transport, but reuses `RunManager`, intent,
`ResolveSource`, delivery helpers.

**Components**
- **App + webhooks:** a GitHub App (installation tokens, least-priv perms); a webhook
  receiver for `issue_comment`, `pull_request`, `pull_request_review`.
- **`GitHubInterviewer`:** posts a gate as a PR comment (choice → task-list checkboxes or a
  comment `/approve`/`/revise`; freeform → reply comment). **A PR *review approval* resolves
  an approve-gate** — map `pull_request_review` submitted→gate answer.
- **Status surface:** one **sticky status comment** edited in place as events stream (the
  GitHub-side live card) + a **Check Run** whose `in_progress`/`completed` + summary mirror
  the run — native red/green on the PR.
- **Delivery:** push a branch and open a PR (Tracker's `WithGitArtifacts` + `ExportBundle`
  already make every run a git history → git-native delivery is near-free); post a diff
  summary; set the Check Run conclusion. On failure, a `Diagnose` comment.

**Milestones**
1. **Skeleton:** App auth + webhook receiver; `/tracker run X` in a comment starts a run via
   `RunManager` keyed by `owner/repo#number`; ack comment.
2. **Status + gates:** sticky status comment (edit-in-place) + Check Run progress;
   `GitHubInterviewer` for comment/review gates; `RunInterviewerSuite` green.
3. **Delivery:** branch push + PR open; diff summary; Check Run conclusion; failure diagnosis.
4. **Polish:** label triggers, `workflow_dispatch`, repo `.tracker/config`, GitLab port
   (MRs + pipelines + notes — same shape).

**Auth/deploy.** GitHub App + a public webhook endpoint — **or** run inside GitHub Actions
(no server, but no live streaming; status posted at end). **Risks:** public endpoint, API
rate limits, comment-vs-review gate mapping.

## 2. Web dashboard — the platform surface *(effort L)*

**Interaction.** A web app: workflow picker → launch → watch live → answer gates → browse
history + cost.

**Package:** `cmd/tracker-web/` — a Go HTTP server embedding `RunManager`, + a frontend
(`web/`, any framework or server-rendered + htmx).

**Components**
- **Run API:** `POST /runs` (start), `GET /runs` (`ListRuns`), `GET /runs/:id` (state/`Result`),
  `POST /runs/:id/cancel`, `GET /workflows` (`Workflows`).
- **Live stream:** `GET /runs/:id/events` over **SSE (simplest) or WebSocket** — forward
  `PipelineEvent`/`agent.Event` verbatim (already emitted). A shared multiplexer per run.
- **`WebInterviewer`:** a gate becomes a pending prompt delivered over the stream; the browser
  POSTs the answer to `/runs/:id/gates/:gateID`. Same channel-bridge shape as `SlackInterviewer`.
- **Views:** live run (pipeline lamps, cost meter, activity log — the TUI in the browser),
  parallel-branch side-by-side, history (`Audit`/`Diagnose`), cost dashboard.

**Milestones**
1. **Read-only:** server + `RunManager`; list/launch workflows; SSE live view of a run (no
   gates yet) — proves the event bridge.
2. **Interactive:** `WebInterviewer` + gate UI (all four modes); cancel/resume; conformance.
3. **History & cost:** `ListRuns`/`Audit`/`Diagnose` views; cost dashboards; artifact download
   (`ExportBundle`).
4. **Auth & multi-user:** OAuth/sessions, RBAC, per-user run isolation (`WithWorkDirBase`).

**Auth/deploy.** Your app auth + hosting; it's a product, not a bot. **Payoff:** richest
surface, best demos, and the API the **mobile app** later reuses.

## 3. MCP server — cheapest strategic win *(effort S–M)*

**Interaction.** Tracker as an MCP server that other agents (Claude Desktop/Code, etc.) call:
"use tracker to build X." Tracker becomes infrastructure other agents orchestrate.

**Package:** `cmd/tracker-mcp/` — an MCP server over stdio (local, like the claude-code
backend) and/or streamable HTTP.

**Tools (thin wrappers over the library):**
- `list_workflows` → `Workflows`; `resolve_source` → `ResolveSource`.
- `run_workflow{workflow, params, budget}` → `RunManager.Start`; returns a run id.
- `run_status{id}` / `run_result{id}` → `ManagedRun` / `Diagnose`.
- `cancel_run{id}` → `RunManager.Cancel`.
- Progress → MCP progress notifications from the event stream.

**Gates.** Agent-driven runs default to **autopilot** (`Config.Autopilot`) — no human. Where
the host supports **MCP elicitation**, surface gates as elicitation requests; otherwise expose
a `pending_gates`/`answer_gate` tool pair for a supervising agent to resolve.

**Milestones**
1. **Core tools:** list/resolve/run/status/result over stdio; autopilot gates; a run completes
   end-to-end from Claude Desktop.
2. **Streaming + control:** progress notifications; cancel; budget passthrough.
3. **Gates:** elicitation (where supported) or the `answer_gate` tool; HTTP transport for
   remote hosting.

**Auth/deploy.** stdio = local, zero infra. HTTP = your auth. **Best effort-to-value ratio.**
**Risks:** the gate model in agent-driven context (autopilot default is the safe answer);
MCP elicitation maturity.

## 4. Discord — cheapest boundary re-proof *(effort S)*

**Interaction.** Near-identical to Slack: bot, mention or slash command, thread/channel = run.
Discord has buttons, select menus, **modals**, rich embeds.

**Package:** `cmd/trackerbot-discord/` — a `DiscordThreadUI` over `transport/chatops` (Phase 0).

**Components**
- **Gateway client** (WebSocket — no public endpoint, like Socket Mode): dispatch
  `MESSAGE_CREATE`, `INTERACTION_CREATE` to `Runner.OnMention`/`OnInteraction`.
- **`DiscordThreadUI`:** `Post` → channel message; `PostGate` → buttons (choice/yes-no),
  a **modal** (interview/freeform), embeds for the status card.
- Reuse runner, intent, notifier, delivery, store from chatops unchanged.

**Milestones**
1. Gateway bot + `DiscordThreadUI.Post`; `!tracker run X` starts a run; ack.
2. Gates: button + modal interactions → `OnInteraction`; `RunInterviewerSuite` green.
3. Slash commands, embeds/status card, delivery (file attachments, links).

**Auth/deploy.** Bot token + Gateway. **Risks:** interaction tokens expire (~15 min) → long
gates need followup webhooks; threading model differs slightly. **This is the test that the
Phase 0 shared core actually pays off** — should be a ~1 wk transport swap.

## 5. Microsoft Teams — enterprise unlock *(effort M)*

**Interaction.** Enterprise Slack. A Teams bot (Bot Framework), @mention or message
extension, **Adaptive Cards** (rich, form-like — great for the status card and interview forms).

**Package:** `cmd/trackerbot-teams/` — a `TeamsThreadUI` over `transport/chatops`.

**Components**
- **Bot Framework endpoint:** an HTTP messaging endpoint receiving Activities; Azure Bot
  Service registration; dispatch to `Runner`.
- **`TeamsThreadUI`:** `Post` → channel message; `PostGate` → **Adaptive Card** with
  Action.Submit inputs (choice/yes-no/freeform/interview in one card); card updates for status.
- Reuse chatops core.

**Milestones**
1. Bot Framework echo + Azure registration; `@tracker run X` starts a run; ack card.
2. Adaptive Card gates (all modes) + status card updates; conformance.
3. SSO/RBAC, message extension launcher, delivery (files, links).

**Auth/deploy.** Azure Bot Service + Bot Framework + OAuth/SSO; needs a public messaging
endpoint. **Risks:** Azure ceremony, enterprise compliance. Transport pattern is identical to
Slack — the cost is the Microsoft setup.

## 6. Email + mobile *(email M · mobile L)*

**Email — universal, async approvals.**
- **Package:** `cmd/tracker-email/` — inbound webhook receiver (SES/Mailgun/Postmark) + SMTP out.
- **`EmailThreadUI`:** `Post`/status → digest emails (not per-event); `PostGate` → a gate email
  ("reply APPROVE / REVISE …"); parse replies (Message-ID/References threading, strip quoted
  text) → `OnInteraction`. Reuse chatops runner/intent/delivery/store.
- **Milestones:** (1) inbound parse + `run <workflow>` → run + ack email; (2) gate emails +
  reply parsing + conformance; (3) digest notifications + result delivery with attachments.
- **Auth/deploy:** inbound webhook + sender allowlist. **Risks:** deliverability, reply-quote
  parsing, async latency (gates are slow).

**Mobile — pocket approvals.**
- A thin native (or PWA) client over the **web dashboard API (#2)**: push notifications for
  pending gates → approve/steer from your phone; view live runs.
- **Defer until #2 exists** — then it's mostly a client (auth + push + a few screens), not a
  new transport. **Effort L** as a native app; **M** as a PWA reusing the web frontend.

---

## Cross-cutting build notes

- **Conformance first:** for any transport with human gates, wire `RunInterviewerSuite` in the
  first PR that adds the interviewer — it's the acceptance test.
- **Session id per transport:** PR `owner/repo#n`, web session, MCP run id, Discord thread,
  Teams conversation, email thread — all just `RunManager` keys.
- **Delivery adapters (`DeliveryStrategy`):** the deploy-URL / git-PR / file-upload / link
  decision is shared logic worth centralizing so every transport delivers consistently.
- **Budget + authz** are per-transport policy but reuse the same `Config.Budget` and an
  `Authorizer` seam (already in trackerbot) — lift that into chatops too.

## Documentation & website (per transport)

**DRY principle — one home per transport.** Each transport's setup, config, commands, and
env belong on **its own dedicated page**; overview surfaces only *summarize and link*. The
[Transports page](../../site/content/transport.html) and
[`transport-boundary.md`](../architecture/transport-boundary.md) are the map — a transport
gets a short paragraph + a link there, never a duplicated setup section. (The Slack section on
the Transports page was trimmed to exactly this once `trackerbot.html` existed.)

**Docs deliverable checklist — every transport ships with:**
1. **A dedicated site page** — `site/content/<transport>.html` on the `trackerbot.html`
   template: what it is, a visual/example, setup, commands/usage, config/env table, security,
   troubleshooting, further-reading. Add it to `site/data/nav.yaml`.
2. **An in-repo README** — `cmd/<transport>/README.md` (or `transport/<name>/README.md`): the
   reference — source layout, internals, the setup checklist. The site page is the friendly
   guide; the README is the technical reference (and links to the site page, as trackerbot's does).
3. **Overview updates (summarize + link, don't duplicate):**
   - Transports page: one row in the "instances" table + a one-line pointer to the dedicated page.
   - `transport-boundary.md`: one row in "The transports as instances" + one line in "Building a
     new transport" if the transport pattern teaches something new (e.g. GitHub's PR-review-as-gate).
   - Top-level `README.md`: a line in the transports list, linking the dedicated page.
4. **`CHANGELOG.md`** — an "Added" entry in the same PR as the code.
5. **`ROADMAP.md`** — promote the transport up a tier when it ships (Maintenance contract).
6. **Diagrams** — each transport page owns its own flow diagram; the boundary page keeps only
   the one shared shape. Verify any diagram against reality (see the `build_product` diagram
   audit — models and stages must match the code).

**Website mechanics:** pages are hand-written HTML with front matter (title/description/og/
`mermaid`/`jsonld`); wide tables auto-stack on mobile and the on-page TOC auto-builds — no extra
work. Push to `main` touching `site/**` → the `docs.yml` workflow deploys to `gh-pages`. Preview
locally with `cd site && hugo server`.

## Rollout sequence & dependencies

```
Phase 0 (chatops core, S)
   ├─► MCP server (S–M)        ← independent, ship first for strategic value
   ├─► Discord (S)             ← validates Phase 0
   ├─► GitHub (M–L)            ← independent, highest dev leverage
   ├─► Web dashboard (L)       ← independent; unlocks Mobile
   │      └─► Mobile (L/M)     ← depends on Web API
   ├─► Teams (M)               ← depends on chatops core
   └─► Email (M)               ← depends on chatops core
```

**Recommended order:** Phase 0 → **MCP** (cheap + strategic) → **Discord** (proves the core) →
**GitHub** (leverage) → **Web** (platform) → Teams / Email / Mobile (market-driven).
