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

### Engine correctness — *milestone: Engine correctness*
The engine must route and terminate exactly as authored. No silent
mis-routes, no phantom "Done" on an unresolved gate.
- **#348** — goal-gate retry re-runs the escalation tail, never the gate, so
  the pipeline reaches `Done` with the gate still at `outcome=fail`.

*(The v0.44.0 engine-correctness batch — #444/#445/#446/#447/#448 — and
#430 shipped and are closed; #348 is the remaining known routing defect.)*

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
  first non-TUI consumer that proves the boundary. Remaining: live-Slack
  verification against a staging workspace. See
  [`cmd/trackerbot/README.md`](cmd/trackerbot/README.md).

### Product & positioning
- **#460** — naming & discoverability: "tracker" is ungoogleable.
- **#461** — Dippin adoption path: editor support, pipeline gallery.
- **#463** — run-flag surface (~36 flags) needs presets / progressive
  disclosure.
- **#464** — `tracker-swebench` + `tracker-conformance` in-repo read as
  research clutter.

### The 1.0 question
- **#462** — publish a 1.0 roadmap with Go library API stability
  commitments. Benchmark cadence and the API-stability bar are the gating
  questions for v1.0.
