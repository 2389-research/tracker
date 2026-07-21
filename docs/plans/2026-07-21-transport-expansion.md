# Transport Expansion — Six Front-Ends

**Date:** 2026-07-21
**Status:** proposed (directional)
**Premise:** the [transport boundary](../architecture/transport-boundary.md) + the
`transport/conformance` suite make each new front-end mostly *presentation*. Every plan
below maps to the same seams: `Config.Interviewer` (gates), the event streams
(`EventHandler` / `AgentEvents`), `RunManager` (N concurrent runs keyed by a session id),
`ResolveSource` / `Workflows` (what to run), `Result` / `Diagnose` / `ExportBundle`
(deliver). Each new interviewer runs `RunInterviewerSuite` before shipping.

## Shared groundwork (do this first — makes 4/5/6 cheap)

`cmd/trackerbot` already factors the transport-agnostic logic behind a `ThreadUI` seam:
`runner.go` (session→run routing, admission, lifecycle), `intent.go` (free-text → workflow),
`notify.go` (event → notable-post filter), `delivery.go`, `store.go` (durable resume). Lift
those into a reusable `transport/chatops` package so a new chat transport is a `ThreadUI`
implementation + auth, not a rewrite. Discord/Teams/Email then reuse the runner, intent,
notifier, delivery, and store wholesale. **Effort: S.** High leverage — it's the payoff of
the boundary work.

---

## 1. GitHub / GitLab — *highest dev leverage*

**Interaction.** `/tracker run <workflow>` in an issue or PR comment (or a label, or
`workflow_dispatch`). The issue/PR *is* the session. "Fix this issue" uses the issue body +
thread as context.

**Boundary mapping.**
- Interviewer: gate questions posted as PR comments; **a PR review approval is a natural
  gate-approve**. Long/structured gates → a comment with task-list checkboxes.
- Events: update a single **sticky status comment** (the live card, GitHub-side) + a **Check
  Run** whose progress/conclusion mirrors the run — native red/green on the PR.
- RunManager keyed by `owner/repo#number`.
- Deliver: **push a branch + open a PR.** This is where Tracker shines — `WithGitArtifacts`
  + `ExportBundle` already make every run a git history, so git-native delivery is almost free.
  Post a diff summary; set the Check Run conclusion.

**Auth/deploy.** A GitHub App (per-install tokens, scoped perms) + webhooks
(`issue_comment`, `pull_request`). Needs a public endpoint — **or** run inside GitHub Actions
(no server, but no live streaming). GitLab is the same shape (webhooks + MRs + pipelines).

**Effort: M–L.** App + webhook handling + Check Runs + comment threading. **Watch-outs:**
public endpoint (vs Slack's Socket Mode), rate limits, mapping gate modes to comments/reviews.

## 2. Web dashboard — *the platform surface*

**Interaction.** A web app: workflow picker, launch, watch live, answer gates, browse history
and cost.

**Boundary mapping.**
- `RunManager` server-side; stream `PipelineEvent`/`agent.Event` to the browser over
  **WebSocket/SSE** (already emitted — just forward them).
- Interviewer renders gates as web forms/modals, resolved by a browser POST.
- `Workflows`/`ResolveSource` for the picker; `ListRuns`/`Audit`/`Diagnose`/`Result` for
  history, cost dashboards, and side-by-side parallel-branch views. The TUI, in a browser.

**Auth/deploy.** Your app's auth (OAuth/sessions), multi-user, RBAC, hosting. It's a product.

**Effort: L.** Backend is mostly RunManager+events→WS wiring; the frontend is the work.
**Payoff:** the richest surface, best demos, and the backend the **mobile app** later reuses.

## 3. MCP server — *cheapest strategic win*

**Interaction.** Expose Tracker as an MCP server so *other* agents (Claude Desktop/Code, etc.)
call it as a tool: "use tracker to build X." Tracker becomes infrastructure other agents
orchestrate.

**Boundary mapping.** MCP tools ↔ library calls: `list_workflows` (`Workflows`),
`run_workflow` (`Run`/`RunManager`), `get_status`/`get_result` (`ManagedRun`/`Diagnose`),
`resolve` (`ResolveSource`). Events → MCP progress notifications. **Gates:** default to
**autopilot** (agent-driven, no human), or surface via MCP elicitation where supported.

**Auth/deploy.** stdio (local, like the claude-code backend) or streamable HTTP.

**Effort: S–M.** MCP servers are small and the library API already exists; the only real
design question is the gate model (autopilot default). **The best effort-to-strategic-value
ratio here** — rides the MCP wave.

## 4. Discord — *cheapest boundary re-proof*

**Interaction.** Near-identical to Slack: bot, mention or slash command, thread/channel =
run. Discord has buttons, select menus, **modals**, and rich embeds.

**Boundary mapping.** A `DiscordThreadUI` over the shared `transport/chatops` core — swap only
the transport; reuse runner, intent, notifier, delivery, store. `DiscordInterviewer` renders
buttons/modals; the conformance suite validates it.

**Auth/deploy.** Bot token + Gateway (WebSocket — **no public endpoint**, like Socket Mode).

**Effort: S.** Mostly a `ThreadUI` swap — this is the test of whether the shared-core refactor
pays off. **Watch-outs:** interaction tokens expire (~15 min) → long gates need followup
webhooks; slightly different threading model.

## 5. Microsoft Teams — *enterprise unlock*

**Interaction.** The enterprise Slack. A Teams bot (Bot Framework), @mention or message
extension, **Adaptive Cards** (rich, form-like — excellent for the live status card and
interview forms).

**Boundary mapping.** Same as Slack over the shared core: `TeamsThreadUI` renders Adaptive
Cards for gates and status; notifier → channel; RunManager per conversation.

**Auth/deploy.** Azure Bot Service registration + Bot Framework + OAuth/SSO. Heavier ceremony
than Slack; needs a public messaging endpoint.

**Effort: M.** The transport pattern is identical; the Azure/Bot-Framework setup is the cost.
**Payoff:** a whole Microsoft-shop buyer segment.

## 6. Email + mobile — *universal + pocket approvals*

**Email.** Send a request to `run@…`, get results back; reply to a gate email to answer.
Async, zero-install, great for non-technical approvers ("reply APPROVE").
- Boundary: `EmailInterviewer` sends gate questions as emails, parses replies (Message-ID
  threading); events → **digest** emails (not per-event); deliver results + attachments.
- Auth/deploy: inbound parsing via SES/Mailgun/Postmark webhooks + sender allowlist.
- **Effort: M.** Inbound parsing/threading is fiddly; gates are slow (async). **Watch-outs:**
  deliverability, reply-quote parsing, latency.

**Mobile.** Push notifications for gates → **approve from your phone**; a thin client over the
web dashboard's API. **Effort: L**, but mostly a client — **defer until the web backend (#2)
exists**, then it's cheap.

---

## Cross-cutting

- Every transport runs `transport/conformance` `RunInterviewerSuite` and inherits the core
  invariants (one terminal event, panic containment, per-run isolation, durable resume) for
  free — it only chooses presentation and identity.
- Each keys `RunManager` on its own session id (PR number, web session, MCP call, Discord
  thread, Teams conversation, email thread).

## Recommended sequence

| Order | Transport | Effort | Why now |
|---|---|---|---|
| 0 | Shared `transport/chatops` core | S | Makes 4/5/6 cheap |
| 1 | **MCP server** | S–M | Cheapest, strategic — Tracker as agent infra |
| 2 | **Discord** | S | Proves the boundary/shared-core actually pays off |
| 3 | **GitHub** | M–L | Highest dev leverage — meet devs in the PR |
| 4 | **Web dashboard** | L | The platform surface; unlocks mobile |
| 5 | Teams / Email / Mobile | M / M / L | Market-driven; mobile after web |
