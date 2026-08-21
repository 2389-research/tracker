# Positioning Research: Trust as Tracker's Sharpest Differentiator (#459)

> Research dossier. Purpose: decide whether — and how — to reposition tracker's
> marketing around **built-in trust** (hard budget caps, tamper-evident audit
> log, `tracker diagnose`, `writable_paths` sandbox) rather than a feature list.
> Every product claim below is scoped to what tracker *actually* does today,
> with honest caveats, so the positioning can't overclaim.

---

## 0. TL;DR

Tracker's real wedge is not "multi-agent orchestration" (a crowded lane) — it's
that the **guardrails you'd otherwise have to build yourself are shipped in the
box.** LangGraph/CrewAI/AutoGen give you a framework and leave budget control,
audit, and sandboxing as an exercise for the user. Tracker answers the one
question that actually blocks autonomous/overnight runs: *"will it blow my
budget and leave me with no idea what happened?"*

**Recommended framing:** *"Safe to let run."* Lead with trust — the budget cap
+ the git-committed audit trail + `tracker diagnose` + the optional filesystem
sandbox — as the headline, with "multi-agent pipelines" as the *what* and
"local-first, every run a git commit" as the *how*.

The "trust / won't-surprise-you-on-the-bill" lane is **largely open** among the
agent frameworks — nobody in LangGraph/CrewAI/AutoGen leads with it (see §2).
That is the opening.

---

## 1. Ground truth: what tracker actually ships for each trust pillar

This section is the honesty backbone. The positioning may only claim what is
listed here, at the scope listed here.

### 1.1 Hard budget caps — **strong, honest, ship-worthy as a headline**

- **What it is:** `pipeline.BudgetLimits` with three independent dimensions:
  `MaxTotalTokens` (int), `MaxCostCents` (int), `MaxWallTime` (duration), plus an
  opt-in `StallTimeout`. Source: `pipeline/budget.go`.
- **CLI surface:** `--max-tokens`, `--max-cost` (cents), `--max-wall-time`.
  Also declarable inline in a workflow's `defaults:` block
  (`max_total_tokens` / `max_cost_cents` / `max_wall_time`). Precedence:
  CLI flags / `Config.Budget` win; `defaults:` is the fallback
  (`tracker.ResolveBudgetLimits`).
- **Enforcement:** `BudgetGuard` is evaluated **between nodes**, after every
  `emitCostUpdate`. Breach → terminal status `budget_exceeded`,
  `EngineResult.BudgetLimitsHit`, `EventBudgetExceeded`. A halted run prints a
  `HALTED: budget exceeded` section naming the tripped dimension, and the WIP is
  preserved (it's a clean terminal, not a crash).
- **Cost accuracy:** dollar cost comes from `llm.EstimateCost`, which resolves
  model prices from `dippin-lang/pricing` (canonical pricing data), not a stale
  hand-maintained tracker table. A model dippin doesn't price → $0 + one-time
  warning (never a hard fail).
- **Honest caveat:** the check is **between nodes**, not mid-token-stream — a
  single very expensive node can overshoot the cap by up to that node's cost
  before the guard fires. This is "kill it at $5.50, not $500," not "hard stop
  at exactly $5.0000." Marketing must say *halts between steps on breach*, not
  *never exceeds by a cent*. (The current homepage bullet "Kill a runaway agent
  at $5, not $500" is honest and well-calibrated — keep it.)

### 1.2 Tamper-**evident** activity log — **real, but caveat-heavy; say "evident," never "proof"**

- **What it is (#213):** every run writes an append-only `activity.jsonl`
  decision log (edge selections with priority + context snapshot, condition
  evaluations, node outcomes with token counts, restart detections). The live
  log lives at an integrity-protected path outside the tool-reachable workdir:
  `$TRACKER_AUDIT_DIR/<id>/` → `$XDG_STATE_HOME/tracker/runs/<id>/` →
  `$HOME/.local/state/tracker/runs/<id>/` → temp. File mode `0600`, opened
  `O_NOFOLLOW`. RunIDs validated (`validateRunID`) so a tampered checkpoint
  can't escape the base.
- **The sentinel:** every runtime-written line is prefixed with the sentinel
  `\x1f\x1e`. Lines lacking it are counted as `runtimeAnomalies.InjectedLines`
  and fire `SuggestionAuditLogInjection`. This detects **casual injection** —
  shell redirection, `tee -a`, `find … -delete`-style tampering by a tool
  subprocess that tried to forge decision edges or fake a
  `pipeline_completed status=success`.
- **Checkpoint (#559)** got the same relocation: `checkpoint.json` (authoritative
  for resume) now lives in the secure state dir; only a non-authoritative
  snapshot sits under the artifact dir.
- **HONEST LIMITS — must not be softened in copy:**
  - It is tamper-**EVIDENT**, not tamper-**PROOF**. The sentinel is *detection,
    not authentication.*
  - It does **not** stop a motivated forger who reads tracker's source and emits
    the sentinel bytes themselves. Per-line HMAC was considered and dropped
    (key-management cost > marginal gain).
  - It counts **injected** lines, not **deleted** ones — silent line-deletion is
    out of scope by construction.
  - Relocation defeats the *relative-path* (`cmd.Dir=workDir`) vector only. A
    **same-UID** unjailed tool subprocess inherits `HOME` + `TRACKER_RUN_ID` and
    can reconstruct the secure path and truncate/`sed -i`/delete it. A process at
    tracker's UID can always reach tracker's own state files.
  - The real boundary for an untrusted tool node is the `writable_paths`
    Landlock jail (§1.4), not the sentinel.
  - Legacy/archived runs without a secure file fall back to the workdir path with
    no sentinel validation — absence of sentinel there is not an injection signal.
- **Copy rule:** claim "tamper-evident audit trail — casual tampering is
  flagged," never "tamper-proof" or "cryptographically verified."

### 1.3 `tracker diagnose` — **strong, under-marketed; promote it**

- **What it is:** deterministic post-run triage. Reads `status.json` +
  `activity.jsonl` and surfaces, per failed node: tool stdout/stderr, error
  messages, timing anomalies (suspiciously-fast completions), deterministic-vs-
  flaky retry classification, stalled loops, escalation patterns, and actionable
  suggestions — including correlating `EventConditionalFallthrough` with
  `tool_output_truncated` to flag "your routing marker may have been dropped."
- **Library parity:** `tracker.Diagnose(ctx, runDir)` /
  `tracker.DiagnoseMostRecent(ctx, workDir)` return a structured
  `DiagnoseReport` — embedders don't scrape stdout.
- **Current marketing gap:** it appears in the install snippet and one "what you
  get" card, but it is the *answer to "what happened?"* — it deserves to be a
  named trust pillar, not step 3 of a code block.
- **Honest caveat:** it's a *reader/analyzer*, not a guarantee of correctness —
  it explains what the log recorded. Its quality is bounded by log fidelity
  (which is high: run capture v0.50 records verbatim provider request bodies and
  per-call/turn/session identity into `run.json`).

### 1.4 `writable_paths` filesystem sandbox — **real, powerful, but Linux-only; scope every claim to the OS**

- **What it is (#272):** declaring `writable_paths` on an agent node makes
  tracker re-exec itself (`/proc/self/exe __jail-exec`) and apply **Linux
  Landlock (ABI v3)** before `exec`ing the shell, bounding *all* file mutations —
  in-process `Write`/`Edit`/`ApplyPatch` AND the Bash subprocess and its whole
  descendant tree (cargo → rustc, etc.) — to the declared globs. In-process
  tools enforce the exact globs via `openat2(RESOLVE_BENEATH |
  RESOLVE_NO_SYMLINKS | RESOLVE_NO_MAGICLINKS)` against a session-root fd
  (no TOCTOU); the Bash subprocess is bounded at the directory ancestors of each
  glob's static prefix.
- **Refuse-to-start gate** (fails closed, does not silently no-op): invalid
  `working_dir`, malformed globs (absolute / `~` / parent-escape / any brace /
  bad char class), backend ∈ {claude-code, acp}, or Landlock unavailable.
- **HONEST LIMITS:**
  - **Linux-only.** Requires a Landlock-capable kernel (ABI v3). On
    macOS / Windows / older Linux, a workflow that declares `writable_paths`
    **refuses to start** with `ErrLandlockUnavailable` — it does not run
    unprotected. macOS (Seatbelt/Sandbox) and FreeBSD (Capsicum) enforcement is
    **not yet built** — tracked in **#281** (see `ROADMAP.md`).
  - Bounds **writes only.** Residual escape classes NOT bounded: network egress,
    reads / exfiltration-by-read, and anything inside an allowed path. Narrow
    globs are the strongest posture.
  - Only applies to the **native** backend (tracker's own tools/subprocess).
    claude-code and acp backends are out-of-process and refuse-to-start under
    `writable_paths`.
- **Copy rule:** "optional Landlock filesystem sandbox on Linux — bounds an
  agent's writes to the paths you name," always OS-qualified. Never imply
  cross-platform sandboxing.

### 1.5 Supporting trust surface (secondary proof points)

- **Every run is a git commit / portable bundle:** `WithGitArtifacts(true)`
  commits after each terminal node; `ExportBundle` → `git bundle create --all`
  for a self-contained, replayable history. Reinforces "diff what happened."
- **Secret inputs never leak:** a `secret` input's value is staged to a `0600`
  file; `${inputs.<name>}` resolves to the path only — the secret never enters a
  prompt, the provider wire, the trace, or the checkpoint (#555). `.tracker/` is
  git-excluded so staged secrets never reach a commit/bundle.
- **Tool-command safety:** built-in denylist (blocks `eval`, pipe-to-shell,
  `curl|sh`), sensitive env vars (`*_API_KEY`/`*_SECRET`/`*_TOKEN`/`*_PASSWORD`)
  stripped from tool subprocesses, safe-key allowlist on `tool_command` variable
  expansion (LLM-origin `ctx.*` keys blocked).
- **Fail-closed provider errors:** auth / model-not-found hard-fail rather than
  silently retrying; billing exhaustion pauses into a resumable terminal.

### 1.6 Where these live on the site *today* (the marketing gap)

- `_index.html` (homepage) hero **does** already carry the budget-cap message
  ("won't surprise you on the bill" + the `--max-cost` bullet) and the
  git-commit message. Good — the bones of a trust story are there.
- **`tracker diagnose`** appears only as install-snippet step 3 and one card —
  under-weighted for what it does.
- **The tamper-evident audit log and the `writable_paths` sandbox appear NOWHERE
  on the homepage** (confirmed: `grep` for sandbox/landlock/tamper/audit-log/
  integrity in `site/content/_index.html` returns nothing). Two of the four
  trust pillars are invisible to a first-time visitor. This is the core problem
  #459 names.

---

## 2. Competitive messaging landscape

> Question: does anyone in the agent-orchestration / durable-execution space
> **lead** with cost-control, budget caps, audit, or sandbox? Is "trust / you
> won't get a surprise bill" an occupied or open lane?

*Findings from live homepages/docs, August 2026. Quotes verbatim.*

### 2.1 Agent frameworks (tracker's nearest neighbors)

| Tool | Verbatim hero | Leads with | Cost caps in hero? |
|---|---|---|---|
| **LangChain** | "Observe, Evaluate, and Deploy Reliable AI Agents" | observability + govern | **Closest** — "Control model calls, spend, and sensitive data with LLM Gateway" is a *govern feature*, not the headline; "Run agent-generated code in isolated sandboxes" present. |
| **LangGraph** | "Gain control with LangGraph to design agents that reliably handle complex tasks" | control + durability; trust via logos (Klarna, Uber, J.P. Morgan) | No. Observability is a pointer to LangSmith, not native. |
| **CrewAI** | "The Enterprise Agent Build & Runtime for the work your business runs on" / "…the control to govern them" | enterprise governance/control | No named spend-cap/audit/sandbox in hero. |
| **AutoGen** | "A framework for building AI agents and applications" | dev framework only | No. Only a Docker code-exec extension. **Now in maintenance mode** — positioning frozen; MS pushes Agent Framework as successor. |
| **Mastra** | "Build AI agents" / "Build, observe, and improve agents that run for days" | **built-in observability** | No — cost is one tracked metric, explicitly *not* spend-control. |
| **Griptape** | "Unleash Your AI Creative Superpowers" | pivoted to creative/no-code | No — security only as supporting "Off-Prompt™" claim. |

### 2.2 Durable-execution engines (adjacent category)

| Tool | Verbatim hero | Leads with | Cost caps? |
|---|---|---|---|
| **Temporal** | "The world's best AI runs on Temporal" | durability/reliability, social proof ("as reliable as gravity," 9 yrs in prod) | **No** — only "$1,000 free credits." |
| **Inngest** | "Unbreakable Agents. Invisible Infra." | durability + zero-infra; strong secondary audit/replay ("Trace everything, replay anything") + SOC2/HIPAA | **No.** |
| **Dagger** | "A better way to ship" | repeatability + observability ("an output you can trust") | **No.** |
| **Restate** | "Build innately resilient distributed apps" | durability/resilience | **No.** |

### 2.3 Who *does* claim the "won't blow your budget" lane?

- **Nobody in the workflow-engine / agent-framework category leads with it.** The
  durable-execution lane is crowded and homogeneous — Temporal, Inngest, Restate
  all say some flavor of "survives failure / unbreakable / resilient" and **none
  touches cost.**
- **The only deliberate claimant found is Portal26** (a governance product, not an
  orchestration engine): a landing page literally titled *"Your AI Agents Are
  Burning Budget,"* marketing an *"Agentic Token Control module – the first of
  its kind,"* promising to "eliminate surprise budget overruns." It's a topic
  landing page, adjacent category — not a competing runner.
- **LLM gateways** (Portkey, TrueFoundry, LiteLLM, Helicone) *have* budget caps
  but bury them as sub-features; their heroes lead on observability/govern
  ("Observe, govern, and secure every AI interaction"). LiteLLM does per-key/team
  budget limits with request rejection — a feature, not a headline.
- Most "no bill shock / cost guardrail" language lives in **FinOps
  thought-leadership** (CloudZero, Finout, Ramp), not product hero copy.

### 2.4 Landscape conclusion — the lane is open

- **"Trust / safe-to-run / won't-surprise-you-on-the-bill" is an OPEN lane** for a
  local-first agent runner. The closest occupant, **LangChain**, bundles
  observe + govern + spend + sandbox — but (a) it's cloud/LangSmith-coupled, (b)
  spend is a govern sub-feature not the headline, and (c) it's the maximalist
  platform, not a local-first single binary. That leaves clear daylight.
- **"Observability" is contested** (Mastra, LangChain, Inngest, Dagger all claim
  some of it) — a reason *not* to make Option D the lead.
- **"Durability/reliability" is saturated** (Temporal/Inngest/Restate) and isn't
  tracker's story anyway (tracker is local-first, not a hosted durable runtime).
- The **combination** tracker ships — hard budget caps + tamper-evident git audit
  + one-command diagnose + optional Linux sandbox, all local, all in one binary,
  none requiring a cloud account — is not assembled by any single competitor at
  the hero level. That combination is the defensible position.

---

## 3. Positioning frameworks applied

### 3.1 April Dunford — "Obviously Awesome" (5 components)

1. **Competitive alternatives** (what a user does if tracker didn't exist):
   - Build the pipeline in **LangGraph/LangChain or CrewAI/AutoGen** and wire up
     their own budget tracking, logging/observability (LangSmith, Langfuse,
     custom), and sandboxing (Docker, firejail). "Roll your own guardrails."
   - Use a **general durable-execution engine** (Temporal / Inngest / Restate)
     that has retries + observability but no model/token/dollar awareness and no
     LLM-specific gates.
   - **Just run a shell script / raw agent loop overnight** and hope.
2. **Unique attributes** (what tracker has that they don't, out of the box):
   - Budget caps in three dimensions enforced by the engine.
   - Tamper-evident, git-committed decision audit trail.
   - `tracker diagnose` — a triage command, not a dashboard you build.
   - Optional Landlock filesystem sandbox (Linux).
   - Local-first: every run is a git commit you can diff/replay/bundle.
3. **Value** (what those attributes let you do): run an autonomous/overnight
   multi-agent job and be **confident it will halt inside a budget and leave a
   diffable record of exactly what it did** — without building any of that
   scaffolding yourself.
4. **Target customer who cares most:** the engineer/small-team running
   autonomous coding or research pipelines who has been (or fears being) burned
   by a runaway agent bill or an opaque failure. See §3.2.
5. **Market frame** (the context you file tracker under): not "a better
   LangGraph" and not "Temporal for AI" — **"the multi-agent pipeline runner you
   can trust to run unattended."** Trust is the frame; orchestration is the
   category.

### 3.2 Jobs-to-be-Done

- **Primary job:** *"When I kick off a multi-agent build/research run and walk
  away, help me trust it won't blow my budget or leave me with no idea what
  happened — so I can actually let it run unattended."*
- **Ideal customer profile:** solo devs, small AI-eng teams, and "factory /
  agent-platform" builders (the trackerbot / webhook-gate / library-embedding
  audience) who run *many* autonomous runs and for whom a surprise bill or an
  unauditable failure is a real, felt cost.
- **Under-served need the alternatives leave on the table:** the frameworks
  optimize for *expressiveness* (build any agent graph); tracker optimizes for
  *safety-to-run-unattended*. That is a different job and a different buyer
  mood.

---

## 4. Positioning options (3–5 framings tracker could adopt)

Each: implied hero headline · proof points · who it wins · who it loses ·
honesty risk.

### Option A — **"Safe to let run" / trust-first** *(recommended, see §5)*
- **Headline:** *"Multi-agent pipelines you can trust to run unattended."*
- **Proof points:** hard budget caps (3 dims) · tamper-evident git-committed
  audit trail · `tracker diagnose` triage · optional Linux filesystem sandbox.
- **Wins:** the overnight-run / autonomous-agent buyer; platform builders
  embedding tracker; anyone burned by a runaway bill.
- **Loses:** people shopping for the most *expressive* agent framework (they'll
  read "guardrails" as "opinionated/limiting"); pure-research notebook users.
- **Honesty risk:** "trust" invites overclaim — must be pinned to *evident-not-
  proof* audit and *Linux-only* sandbox, or it backfires with security-literate
  readers. Manageable with disciplined copy (§1 rules).

### Option B — **"Won't surprise you on the bill"** *(cost-first — a subset of A)*
- **Headline:** *"Multi-agent LLM pipelines that won't surprise you on the
  bill."* (This is literally the current hero.)
- **Proof points:** `--max-cost` / `--max-tokens` / `--max-wall-time`; halts
  mid-run with a snapshot; per-provider cost breakdown.
- **Wins:** the exact "I got a $400 overnight bill" pain; very concrete, very
  legible.
- **Loses:** narrower than A — leaves audit + sandbox + diagnose on the floor;
  a competitor could neutralize it by adding one spend-cap flag.
- **Honesty risk:** low, *if* "between-nodes" enforcement is stated. Risk is
  strategic, not honesty: it's a single-feature moat, easy to copy.

### Option C — **"Local-first — every run is a git commit"**
- **Headline:** *"Every agent run is a git commit you can diff and replay."*
- **Proof points:** git artifacts, checkpoint tags, `ExportBundle`, replay from
  any checkpoint.
- **Wins:** the reproducibility/provenance-minded engineer; the "I don't want my
  pipeline in someone's cloud" buyer.
- **Loses:** doesn't speak to the *budget* fear (the sharpest one); "git commit"
  reads as a mechanism, not an outcome, to non-power-users.
- **Honesty risk:** low. But it under-sells the budget + sandbox story.

### Option D — **"Observability-first / no dashboards to wire up"**
- **Headline:** *"See every token, tool call, and decision — no observability
  stack to wire up."*
- **Proof points:** live TUI, `activity.jsonl`, `tracker diagnose`, run capture
  → `run.json`.
- **Wins:** the LangSmith/Langfuse-fatigued buyer; teams tired of gluing
  telemetry.
- **Loses:** "observability" is a crowded, enterprise-coded word; invites
  comparison to mature APM/LLM-obs vendors tracker won't win on features.
- **Honesty risk:** medium — "observability" sets an expectation of dashboards/
  retention/alerting tracker doesn't provide.

### Option E — **"Guardrails included / the batteries-included agent runner"**
- **Headline:** *"The multi-agent runner with the guardrails already built in."*
- **Proof points:** all four pillars framed as "you'd build these yourself in
  LangGraph — here they're in the box."
- **Wins:** direct framework-switchers who feel the wiring-it-up tax.
- **Loses:** "guardrails" can read as restrictive; requires the reader to
  already know the LangGraph pain to land.
- **Honesty risk:** low-medium; must not imply the sandbox is on by default or
  cross-platform.

---

## 5. Recommendation + execution plan

### 5.1 Recommendation: **Option A ("Safe to let run"), keeping Option B's bill
line as the lead proof point.**

**Why A over B/C/D/E:**
- B is *already* the hero and is good, but it's a single-feature story a
  competitor can copy with one flag. A **absorbs** B (the bill line stays as
  proof point #1) while widening the moat to the *combination* of four
  guardrails — which is far harder to copy and matches the real product.
- The competitive scan (§2) shows the **trust/safe-to-run lane is open** among
  agent frameworks — they compete on expressiveness and integrations. Owning
  "trust" is a positioning land-grab, not a feature fight.
- A directly answers the JTBD ("let it run unattended") and speaks to the
  highest-value buyer (platform/overnight-run users), where C (provenance) and
  D (observability) address narrower or more crowded needs.
- A is *true*: all four pillars ship today. The only discipline required is
  honest scoping — which §1 already codifies.

### 5.2 Exact hero rewrite

> **Eyebrow:** Wire it up. Let it rip — safely.
>
> **Headline:** *Multi-agent pipelines you can trust to run unattended.*
>
> **Subhead:** Define workflows in a small text file and run them locally with
> the guardrails already built in: a hard budget cap, a tamper-evident audit
> trail in git, and one command to explain any run. No observability stack to
> wire up, no surprise bill.
>
> **Proof bullets (3):**
> 1. **Halt at $5, not $500.** Hard caps on cost, tokens, and wall-time
>    (`--max-cost 500 --max-tokens 100000 --max-wall-time 30m`). The engine
>    checks between every step and stops with a snapshot on breach.
> 2. **Know exactly what happened.** Every decision, token, and tool call lands
>    in a git-committed, tamper-evident `activity.jsonl`. `tracker diagnose`
>    reads it back and tells you which node burned the budget or broke.
> 3. **Bound what an agent can touch.** *(Linux)* Declare `writable_paths` and a
>    Landlock sandbox confines every write — the agent and its subprocesses — to
>    the paths you name.

*(Note: bullet 3 must carry a visible "(Linux)" qualifier. If leading with a
Linux-only feature in the hero feels too caveated, demote it to the trust
section and make bullet 3 the git-commit/replay line instead — both are honest.)*

### 5.3 Which trust features to surface, and where

1. **New "Trust" section** on the homepage, directly under the hero (before
   "How is this different?"), with four cards — one per pillar:
   - *Budget caps* (exists as a card — promote it up).
   - *Tamper-evident audit trail* (**new** — currently absent).
   - *`tracker diagnose`* (**promote** from install-step to named pillar).
   - *Filesystem sandbox — Linux* (**new** — currently absent), OS-qualified.
2. **Reframe the "What you get" grid** so the first three cards are the trust
   pillars, then the capability cards (parallel worktrees, typed JSON, etc.).
3. **Add a short "honest limits" line** under the sandbox and audit cards
   (e.g., "Tamper-evident, not tamper-proof — casual tampering is flagged;
   full threat model in the docs"). Counter-intuitively this *builds* trust with
   the security-literate buyer and inoculates against "well, actually" critics.
4. **Add a `/security` or `/trust` page** linking the threat-model docs (#213
   sentinel limits, #272 residual escape classes, #281 roadmap). This is where
   the caveats live in full so the homepage cards can stay tight.

### 5.4 Honest per-OS caveats (copy the site MUST carry)

- **Budget caps:** all OSes. Enforced *between nodes*; a single node can overshoot
  by its own cost. Say "halts between steps on breach."
- **Audit log:** all OSes. **Tamper-EVIDENT, not tamper-proof.** Detects casual
  injection; a same-UID process can still delete/forge. Never "cryptographically
  verified."
- **`tracker diagnose`:** all OSes. It analyzes the recorded log; it doesn't
  guarantee correctness.
- **`writable_paths` sandbox:** **Linux only** (Landlock ABI v3 kernel); native
  backend only; bounds writes only (not network/reads). Refuses to start on
  macOS/Windows/old Linux (macOS/FreeBSD tracked in #281). Never imply
  cross-platform.

### 5.5 Credibility signals to add

- **GitHub stars badge** + a link to Discussions (social proof; the site
  currently sends people to GitHub with no traction signal).
- **The SWE-bench Verified number** — **once #465 lands.** It is *not scored
  yet* (ROADMAP #465 = "first scored run; debug the empty run"), so it must NOT
  appear on the site until there's a real number. Reserve a "Benchmarks" slot;
  fill it when #465 is green. Do not preannounce.
- **A "why you can trust the audit log" doc link** (the threat model) — turns a
  potential objection into a credibility asset.
- **Concrete before/after cost anecdote** ("a runaway fix-loop that would have
  spent $X halted at $Y") if a real run can back it — honest, specific numbers
  only.
- **Version + changelog cadence** is already visible (v0.63.x) — keep it; a live
  changelog signals an active project.

### 5.6 What NOT to do (honesty guardrails)

- Do **not** say "tamper-proof," "cryptographically verified," "secure by
  default," or "sandboxed" without the Linux qualifier.
- Do **not** claim the budget cap is exact-to-the-cent.
- Do **not** put a SWE-bench number up before #465 produces one.
- Do **not** imply the sandbox protects against network exfiltration or reads.

---

## Appendix: source pointers

- Budget: `pipeline/budget.go`, `tracker.ResolveBudgetLimits`, README §Cost
  Governance.
- Audit log / sentinel (#213) + checkpoint (#559): CLAUDE.md "Activity log
  integrity" + "Activity log threat model (full)"; README §Decision Audit Trail.
- Sandbox (#272) + #281 roadmap: `docs/superpowers/specs/2026-06-01-issue-272-*`,
  `docs/architecture/linux-security-primitives.md`, `ROADMAP.md` #281.
- `tracker diagnose`: README §Troubleshooting; `tracker.Diagnose` /
  `DiagnoseMostRecent`.
- Current site: `site/content/_index.html`.
- SWE-bench status: `ROADMAP.md` #465 (not yet scored).
