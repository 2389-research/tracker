# Tracker Roadmap

A rolling engineering roadmap organized into thematic workstreams across
**Now / Next / Later** tiers. Priorities, not dates. The GitHub milestones
mirror the **Now** tier and are the source of truth for what's actively in
flight; everything below Now is directional and will churn.

> **Maintenance contract:** a workstream finishes → close its milestone →
> promote the next workstream up a tier → update this file, all in the same
> PR. Only **Now**-tier workstreams get GitHub milestones. Refreshed at every
> release (see the *Before releasing* checklist in `CLAUDE.md`).

---

## Now

The three active milestones. These are what we're building next.

### Failure UX & recovery — *epic #493: every failure leads with excellence + empathy*
Shipped a batch of this epic:
- **#487** — ✅ resolved (P0): billing/quota exhaustion is now a *recoverable*
  `paused_billing` terminal (checkpoint + preserved WIP + `tracker -r` resume)
  with an account-attributed message (provider + env var + masked key + billing
  URL), not a fatal abort.
- **#492** — ✅ resolved: failures lead with a classified cause + remediation
  (`💳 Billing…`, `🔑 Auth…`, `⏳ Rate limited…`) via `tracker.ClassifyFailure`,
  not the `handler error at node "X"` wrapper.
- **#488** — partial: the "in-flight work not preserved" warning now leads with
  what was lost/safe + recovery; preserve-by-default is the remaining part.
- Still open in the epic: **#489** (verify-milestone test-fidelity), **#486**
  (provider/model failover), and the #488/#487 refinements.

### Engine correctness — *milestone: Engine correctness*
The engine must route and terminate exactly as authored. No silent
mis-routes, no phantom "Done" on an unresolved gate.
- **#348** — ✅ resolved: goal-gate retry now re-enters the gate node (via a
  persisted `GateRecheckPending` flag) so a remediated tree is re-judged, and a
  human "accept" marks the gate `validation_overridden` (#271) rather than
  ending in a silent success with an unsatisfied gate. Regression-tested in
  `pipeline/engine_goal_gate_recheck_test.go` + `_override_test.go`.

*(The v0.44.0 engine-correctness batch — #444/#445/#446/#447/#448 — #430, and
#348 shipped and are closed. No known routing defects remain open.)*

### Epic #308 closeout — *milestone: Epic #308 closeout*
Harden `build_product` against the structural and process gaps surfaced by
the case-study runs.
- **#308** — the epic: structural & process gaps beyond #233.
- **#304** — budget by cost + a no-progress detector; per-node turn count
  becomes a backstop rather than the primary guard.
- **#307** — document `build_product` vs `superspec`, backport the
  spec-coherence preflight, resolve the `examples/` vs `workflows/`
  duplication (#256).

### SWE-bench first score — *milestone: SWE-bench first score*
Get a real, published benchmark number.
- **#465** — first scored SWE-bench Verified run: debug the empty
  `model_patch` smoke run → full Verified run → publish the score.

---

## Next

Directional. Expected to promote to Now as the milestones above close.

### Run capture & cost correctness — ✅ shipped (v0.50.0)
Landed the tracker-runner run-capture PR (#519): executed spec + verbatim
provider request bodies + per-call/turn/session identity + a `run.json` manifest
(`tracker run-json` backfills archived runs). A deep-dive review of that work
found and fixed a batch of cost-accuracy and security issues, all shipped in the
same release: provider-aware cache-write pricing (#522), mixed-backend token
undercount in `run.json` (#523), session cost priced with the response model so
`--max-cost` survives a failover (#524), the world-readable run-dir mirror (#525),
and capture identity on the live `--json` wire (#526). The capture-file security
hardening followed in v0.50.1 (#521/#528/#529: atomic 0600 + O_NOFOLLOW writes,
and `.tracker/` excluded from WIP/bundle staging), and v0.51.0 closed out the
remaining follow-ups: the `Config.Capture` embedder seam + docs (#530),
`TokenTracker` per-(provider,model) pricing (#527), and the dual
cost-field-name convergence (#520). The only open item from the arc is #518
(pricing source-of-truth + drift detection) — an `unpriced`/`--max-cost` signal
plus an optional scheduled pricing-page staleness check.

### Transport boundary — ✅ shipped (v0.46.0)
The core is now fully UI-agnostic: TUI, Slack, web, and mobile are first-class
transport peers on one `tracker.Config` → `Engine` path. Shipped #472/#474/#475/
#476/#477/#478/#479 (absorbing #396/#450/#451): the `Config.Interviewer` seam,
event-stream completeness (authoritative terminal-status, start snapshot,
cost-as-events), N-concurrent-run safety, the `tui/render` relocation, full
CLI→library unification, and the transport-neutral `RunManager`. Followed by a
hardening pass — engine/RunManager panic containment, atomic checkpoint/state
writes, `trackerbot` authz/budget/lifecycle, and a `transport/conformance` suite
that a new transport runs to prove correctness. Boundary contract:
[`docs/architecture/transport-boundary.md`](docs/architecture/transport-boundary.md).

### Policy, hygiene & coherence — ✅ shipped (v0.48.0)
Built on the v0.47.0 embedding surface. Shipped a fail-closed pre-execution
tool-call guardrail hook (#506) — an opt-in `GuardrailPolicy` that gates every
agent tool call on `(tool, args, context)` and returns the denial reason to the
model; an injectable diagnostic sink (#449, `tracker.SetDiagnosticLogger`) so the
library no longer writes to the global logger; gate identity on the interviewer
callback (`GateAware`), sharing the `gate_opened` id so an out-of-process
transport can correlate the blind `Ask*` callback with the event stream; and
three fixes for cross-cutting gaps a post-release audit found between the v0.47.0
event/wire landings — per-turn agent usage now round-trips through `activity.jsonl`,
the bounded event handler never splits a gate pair under a lossy overflow policy,
and every handler-originated event carries `run_id`. Follow-ups filed: #514
(`tracker audit` classes a paused run as failed), #516 (resume-in-capacity-window
race), #517 (diagnose blank-line injection counting).

### Embedding surface completeness — ✅ shipped (v0.47.0)
The event surface an out-of-process, event-sourced control plane
(`tracker-runner`) drives Tracker through is now complete. Shipped in v0.47.0
(sourced from a tracker-runner enablement audit): `turn_metrics` attribution
(#508) and gate lifecycle events (#509) so per-turn cost and gate history
reconstruct from the stream alone; NDJSON `StreamEvent` payload parity with
`activity.jsonl` and a lossless `ActivityEntry` reader, both held to the private
schema by a mechanical field-name guard; `paused_billing` as a first-class
resumable `RunManager.RunPaused` state (#487) instead of `RunFailed`; submit-time
variable-availability validation (#505); and a bounded/async event-handler seam
so a slow subscriber cannot block the engine. The follow-on enablement work has
since shipped: #449 and #506 in v0.48.0, and the coverage-hole golden fixtures +
#462 (API stability policy + surface snapshot) in v0.49.0.

### Coverage & API stability — ✅ shipped (v0.49.0)
Closed the embedding batch. Golden-trace fixtures now pin the five previously
unverified handler/terminal contracts (`validation_overridden`, `subgraph`,
`stack.manager_loop`, `interview`, `paused_billing`); `docs/api-stability.md` plus
an exported-surface golden snapshot (`api_surface_test.go`) guard the root
`tracker` package against accidental signature change; and three audit follow-ups
landed — #514 (`tracker audit` classes a paused run as `paused`, not `failed`),
#516 (spurious resume-time `ErrAtCapacity`), #517 (diagnose blank-line injection
counting).

### Parallel-first resilience
First-class parallel milestone execution, so branches retry and resume
independently instead of sharing global counters.
- **#420** — branch-scoped retry, context, and fix-attempt counters.
- **#427** — sub-node turn checkpointing for mid-node resume.

### Cost & efficiency
- **#353** — review fan-out cost asymmetry: one reviewer burned 32% of a run
  duplicating a 42-second finding. Dedup / cap the fan-out.

### First-run & product polish (from the audit)
The things a brand-new user hits first.
- **#456** — first run fails: `build_product` hard-exits without `SPEC.md`;
  ship a graceful path.
- **#457** — README information architecture: release-note walls before
  examples.
- **#458** — show the TUI: screenshot / GIF in the README and homepage hero.
- **#459** — positioning: lead with the trust story (budget caps,
  tamper-evident audit log).

### Load-bearing refactors
Untangle the accessors and package seams that slow every future change.
- **#393** — claude-code / ACP parsers bypass typed `AgentNodeConfig`
  accessors (9 raw `node.Attrs` reads).
- **#449** — route ~140 raw `log.Printf`/`fmt.Printf` diagnostics through a
  real logger.
- *(#396, #450, #451 moved up into the Transport boundary workstream, which
  depends on them.)*

---

## Later

Backlog. Real, but not scheduled.

### Sandbox breadth
- **#279** — rescope `overrideAlreadyRecorded` to checkpoint generation.
- **#280** — file-scoped Bash enforcement for `writable_paths`.
- **#281** — per-OS enforcement on macOS (Sandbox) / FreeBSD (Capsicum).

### Security documentation & process
- **#284** — Linux security primitives reference doc.
- **#285** — 9-class audit checklist for `writable_paths` changes.
- **#286** — "freeze and prove" pattern for security PRs.

### Structural & cosmetic refactors
- **#395** — collapse pervasive near-identical duplication (engine emits,
  llm adapters).
- **#398** — extract inline `prompt:` / `command:` bodies into testable
  sidecar files.
- **#452** — `write_enriched_sprint.go` (1,250 lines) is a domain workflow
  embedded in `agent/`.
- **#453** — split the 1,687-line `tracker_doctor.go` into unit-testable
  checks.
- **#454** — group handler-specific `Outcome` fields into sub-structs.
- **#455** — repo hygiene sweep.

### Transports
- **#473** — ✅ shipped (v0.46.0): Slack transport (`cmd/trackerbot`) — drive
  Tracker from Slack via Socket Mode; `@trackerbot` starts runs, threads receive
  notifications and gate questions, results land back in the thread. All four
  gate modes, natural-language intent, control commands, per-thread concurrency,
  failure diagnosis (#480–#485), **durable resume across restarts**, authz +
  fail-closed budget + workdir lifecycle, and conformance-suite coverage. The
  first non-TUI consumer that proves the boundary. See
  [`cmd/trackerbot/README.md`](cmd/trackerbot/README.md).
  Experience layer (v0.46.0): a live status card, up-front cost `estimate` +
  confirm-over-threshold gate, richer delivery, and the `retry` / `bump` /
  `steer` / `workflows` commands + workflow suggestions; plus the Tier-3
  `/tracker` slash command and App Home tab. Remaining: live-Slack verification
  of the visual surfaces + the slash/App-Home plumbing against a staging
  workspace.
- **Mid-run steer** — ✅ shipped (v0.46.0): `Config.SteeringChan` forwards
  external context updates into a running pipeline (drained between nodes);
  `trackerbot`'s `steer <text>` command is the first consumer.
- **CLI REPL (`cmd/trackerchat`)** — ✅ shipped (v0.46.0): a terminal front-end,
  the **second** boundary consumer — reuses all of `transport/chatops`, adding
  only a terminal `ThreadUI` + a stdin loop (`transport/cli`). Concrete proof the
  boundary is I/O-only. See [`cmd/trackerchat/README.md`](cmd/trackerchat/README.md).
- Next transports (from the expansion plan): web dashboard, Discord, Teams,
  email, GitHub/GitLab bot — each a `transport/chatops` (or event-stream)
  consumer. See [`docs/plans/2026-07-21-transport-expansion.md`](docs/plans/2026-07-21-transport-expansion.md).

### Product & positioning
- **#460** — naming & discoverability: "tracker" is ungoogleable.
- **#461** — Dippin adoption path: editor support, pipeline gallery.
- **#463** — run-flag surface (~36 flags) needs presets / progressive
  disclosure.
- **#464** — `tracker-swebench` + `tracker-conformance` in-repo read as
  research clutter.

### The 1.0 question
- **#462** — API stability policy shipped: [`docs/api-stability.md`](docs/api-stability.md)
  states the supported root-`tracker` surface, the open-enum rule, and the pre-1.0
  deprecation contract, mechanically guarded by an exported-surface golden snapshot
  (`api_surface_test.go` + `testdata/api_surface.golden`). Remaining for v1.0: the
  actual no-breaking-changes commitment, benchmark cadence, and extending the
  surface snapshot to the supported sub-package types (handlers/pipeline/llm).
