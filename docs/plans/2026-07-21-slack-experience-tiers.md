# Slack Experience — Four-Tier Expansion Plan

**Date:** 2026-07-21
**Status:** proposed
**Goal:** take `cmd/trackerbot` from *functional* (mentions, buttons, notifications) to
*delightful* — a live, touchable run you watch happen. Grounded in the existing files
(`slack.go`, `interviewer.go`, `notify.go`, `runner.go`, `delivery.go`, `store.go`,
`main.go`) and the transport boundary (event streams, `RunManager`, `Simulate`,
the steering channel).

## Small core additions this needs (do alongside Tier 1–2)

Most of this is Slack-side, but a few boundary bits should be lifted into the core:
- **`Config.SteeringChan chan map[string]string`** — surface the engine's existing
  `WithSteeringChan` on `tracker.Config` so a transport can inject steering mid-run (Tier 2).
- **`tracker.EstimateRun(source, cfg)`** — a thin wrapper over `Simulate` (all-paths) + the
  cost model returning `{steps, minEstimate, maxEstimate, expected}` for the pre-run estimate
  (Tier 1). Logic exists; expose it as one call.
- **Budget-bump resume** — confirm `RunManager` can relaunch a checkpointed run with a raised
  `Config.Budget` (it already resumes from `CheckpointDir`; just thread a new budget).

Everything else is Block Kit + Slack Web API on top of streams already emitted.

Effort key: **S** ≈ ~1 wk · **M** ≈ 2–4 wk.

---

## Tier 1 — Flagship delighters *(the emotional arc; effort M–L)*

The journey: **it tells me the cost → I watch it work → it hands me exactly what I wanted.**

### 1.1 Live, self-updating run card *(the centerpiece)*
One message per run, edited in place via `chat.update` — it morphs from "starting…" → a live
dashboard → the results card.
- **Track** the status message `ts` per run (in the `store` / `SlackInterviewer`).
- **Renderer:** a Block Kit builder that turns run state into the card (pipeline lamps from
  `stage_started/completed`, cost meter from `cost_updated`, current-node line from
  `agent.Event`, elapsed).
- **Driver:** feed the card from the existing `notify.go` event handler; **throttle**
  `chat.update` to ~1/sec (Slack rate limits) with a trailing update on terminal.
- **Controls row:** `[⏸ Pause] [🎯 Steer] [🛑 Cancel]` buttons (wired in Tier 2).
- **Milestones:** (a) render + update lamps/cost/elapsed; (b) collapse today's per-event spam
  into the card (keep only gates + terminal as separate posts); (c) morph to a results card on
  finish.

### 1.2 Pre-run cost + time estimate
On a mention, before `RunManager.Start`, call `tracker.EstimateRun` and post:
*"~18 steps, ~$3–8, ~10 min. `[Run it]` `[Tweak budget]` `[Cancel]`"* — a confirm gate.
- **Tasks:** the `EstimateRun` core wrapper; a confirm-gate Block Kit message; on "Tweak",
  a modal to set budget/params. **Differentiator** — no competitor tells you the bill first.
- **Config:** `TRACKERBOT_CONFIRM=always|over_cost|never` (default: confirm when estimate > a
  threshold).

### 1.3 Delivery that lands the plane
Extend `delivery.go` with a `DeliveryStrategy` chosen by what was built:
- **Code** → push branch + open PR (`WithGitArtifacts`/`ExportBundle`) → `[View PR]` button.
- **Deploy** → detect a URL in output → big `[🚀 Open]` button.
- **Docs/files** → `files.upload` the key artifacts straight into the thread.
- **Screenshot** → if the product has a UI, attach a render.
- Always: a **results card** — what was built, cost, duration, artifacts as buttons,
  `[Run again] [Iterate]`.
- **Milestones:** (a) results card + file upload; (b) PR/branch delivery; (c) URL/deploy button.

### 1.4 Interview gates as a Slack modal
Replace one-question-at-a-time-in-thread with a real modal (`views.open` off the gate's
`trigger_id`): dropdowns/inputs/radios in one form, `views.submit` → resolve.
- **Tasks:** modal builder from `handlers.Question[]`; `view_submission` handler → `Resolve`;
  fall back to the current in-thread flow when no `trigger_id` (event-triggered gates).

---

## Tier 2 — Interactivity & control *(power-user delight; effort M)*

### 2.1 Steer mid-run
`[🎯 Steer]` on the card → modal → inject into `Config.SteeringChan` (core addition above).
The engine's `manager_loop`/steering merges it between nodes.
- **Tasks:** `Config.SteeringChan` wiring; steer modal; namespace values under `steer.*`.

### 2.2 Budget bump on breach
On `EventBudgetExceeded`, the card shows *"Spent $5.00. `[+$5 & continue]` `[Stop]`"*.
- **Tasks:** relaunch the checkpointed run with a raised `Config.Budget` via `RunManager`;
  audit the bump (who approved).

### 2.3 Smart failure recovery
On terminal fail, results card buttons: `[Retry] [Retry with a hint] [Escalate to @lead]
[Diagnose]`. "Retry with a hint" → modal → steering/param; `[Diagnose]` posts the existing
`Diagnose` summary.

### 2.4 Reaction controls
Subscribe to `reaction_added`; 🛑 on the status card → cancel, 👍 on a pending gate → approve
its default. Playful, fast.

### 2.5 Pause / autopilot toggle
`[⏸ Pause]` (cancel-safe checkpoint) and a card toggle to flip a gate-heavy run to
`Config.Autopilot` (hands-off) mid-flight.

**Milestones:** (a) steer + budget-bump (needs the core additions); (b) failure-recovery
buttons; (c) reactions + pause/autopilot.

---

## Tier 3 — Slack-native surface area *(discoverability & stickiness; effort M)*

### 3.1 Slash commands
Register `/tracker` → subcommands `run` / `status` / `cancel` / `runs` / `workflows`. Works
anywhere without an @mention. (Socket Mode delivers `slash_commands`.)

### 3.2 App Home tab
On `app_home_opened`, `views.publish` a personal dashboard: your active runs (live state),
recents, **quick-launch buttons** for favorite workflows, month-to-date spend.

### 3.3 Shortcuts
- **Global** ("Run a workflow" from the ⚡ menu) → modal picker.
- **Message action** ("Send this thread to Tracker") → start a run with the *thread as
  context* — magic for support/eng channels.

### 3.4 Link unfurling
On `link_shared` for a run URL, `chat.unfurl` a rich preview card (status, cost, artifacts).

### 3.5 Multi-approver gates
Gate config: require N approvals or a specific approver; the card shows who signed off. Slack's
collaboration superpower — a CLI can't do this.

**Milestones:** (a) slash commands + App Home; (b) shortcuts (global + message action);
(c) unfurl + multi-approver.

---

## Tier 4 — Config & power features *(effort M)*

### 4.1 Per-channel config
`/tracker config` (or an App Home form) sets per-channel defaults: workflow, budget, backend,
autopilot persona, allowed workflows. New store keyed by `channel_id`.

### 4.2 Named presets
Save a run config → `/tracker run nightly-audit`. Store of `{name → {workflow, params,
budget, backend}}`.

### 4.3 Scheduled runs
`/tracker schedule "0 9 * * 1-5" audit → #eng` — a cron scheduler that launches runs and posts
to a channel. (Reuse the run path; add a scheduler goroutine + persisted schedule store.)

### 4.4 RBAC
Extend the existing `Authorizer` from a flat allowlist to per-action / per-workflow roles:
who can run, who can approve gates, who can bump budget.

### 4.5 Per-user prefs
DM vs thread for critical events; default backend; notification verbosity. Store keyed by
`user_id`.

**Milestones:** (a) per-channel config + presets; (b) RBAC + per-user prefs; (c) scheduled runs.

---

## Notification polish (threaded through all tiers)

Quiet by default: @mention the requester only on gate-needed / done / failed; ✅-react the
original message when finished; a "Tracker is working…" presence; optional DM for the moments
that need eyes. Less spam = more trust. Implemented in `notify.go`'s notable-event predicate.

## Rollout sequence

| Order | Slice | Effort | Why |
|---|---|---|---|
| 1 | Core additions (`SteeringChan`, `EstimateRun`, budget-bump resume) | S | Unblocks Tier 1–2 |
| 2 | **Tier 1.1 live card + 1.2 estimate + 1.3 delivery** | M–L | The whole emotional arc — the WOW |
| 3 | **Tier 1.4 modals + Tier 2 control** | M | Interactivity on top of the card |
| 4 | **Tier 3 surface area** (slash, App Home, shortcuts) | M | Discoverability + stickiness |
| 5 | **Tier 4 config/power** | M | Teams operationalize it |

**Recommended first build:** the core additions + **Tier 1.1–1.3** as one "trackerbot 2.0"
arc — it's mostly rendering on top of streams already emitted, and it's the moment customers
exclaim with delight.
