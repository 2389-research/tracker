# Tracker Roadmap

A rolling engineering roadmap in three tiers — no dates:

- **Now** — the current focus. Every Now workstream has a GitHub milestone with its
  issues attached; the milestone closing is the workstream finishing.
- **Next** — queued. A Next workstream starts (and gets a milestone) when a Now
  workstream closes.
- **Later** — parked. Revisited at releases; promoted when it starts blocking real work.

Refreshed in every release PR (see CLAUDE.md → Before releasing).
Last updated: 2026-07-05, after v0.42.0.

## Now

### Engine correctness

**Goal:** a pipeline can never report `Done` while a goal gate sits at
`outcome=fail`, and packed `.dipx` runs behave exactly like source-tree runs.

- #348 (P1) goal-gate retry re-runs the escalation tail, never the gate —
  pipeline completes `Done` with the gate at `outcome=fail`
- #430 (P2) `graph.workflow_dir` is empty in packed `.dipx` runs

**Done when:** the gate re-arms on retry with a regression test, and `.dipx`
runs populate `workflow_dir`. Milestone: **Engine correctness**.

### Epic #308 closeout

**Goal:** finish Phase 3 of build_product hardening and close the epic.
Phases 0–2 (silent-halt fix, never-lose-work, language-native quality gates)
shipped through v0.42.0.

- #304 engine: budget by cost + no-progress detector; per-node turn count
  becomes a backstop
- #307 docs+refactor: build_product vs superspec, backport the spec-coherence
  preflight, resolve examples/ vs workflows/ duplication

**Done when:** #304 and #307 close, then #308 closes. Milestone: **Epic #308 closeout**.

### SWE-bench: first scored run

**Goal:** turn the merged harness (`cmd/tracker-swebench/`) into a published
SWE-bench Verified score, joining AttractorBench (0.805) and Terminal-Bench
2.0 (56.2%) on the board.

- #465 root-cause the empty `model_patch` smoke run, full Verified run on
  aibox01, publish the score

**Done when:** a full Verified run scores with the official evaluation harness
and the number is recorded. Milestone: **SWE-bench first score**.

## Next

### Parallel-first resilience

**Goal:** parallel milestone execution becomes first-class — a fix loop on one
branch can't starve another, and a killed run resumes mid-node instead of
replaying completed turns.

- #420 branch-scoped retry, context, and fix-attempt counters (retires the
  global-restart-counter gotcha)
- #427 sub-node turn checkpointing for mid-node resume (#422 follow-up)

### Cost & efficiency

**Goal:** no single reviewer or branch can silently burn a disproportionate
share of a run's budget.

- #353 review fan-out cost asymmetry — one reviewer burned 32% of run cost
  (2.6M uncached input tokens) to duplicate a 42-second finding; builds on
  #304's budget machinery and v0.41's memoization + diff-scoped review panels

### Code health — load-bearing tier

**Goal:** the refactors that make future engine/CLI work cheaper and safer.

- #396 replace 11 config-smuggling package globals in cmd/tracker with an
  explicit `runOptions` struct
- #393 codergen claude-code/ACP parsers go through typed `AgentNodeConfig`
  accessors (9 raw `node.Attrs` reads today)

## Later

### Sandbox breadth

- #281 per-OS `writable_paths` enforcement: macOS Sandbox / FreeBSD Capsicum —
  the macOS half is the likeliest promotion, since development happens on
  macOS and the jail currently refuses everything non-Linux
- #280 file-scoped Bash enforcement investigation
- #279 rescope `overrideAlreadyRecorded` to checkpoint generation

### Code health — cosmetic tier

- #395 collapse pervasive near-identical duplication (engine emits, llm
  adapter scaffolding, tui content types)
- #398 extract inline `prompt:`/`command:` bodies into testable sidecar files

### Security docs & process

- #284 Linux security primitives reference doc
- #285 9-class audit checklist for `writable_paths` changes
- #286 "freeze and prove" pattern for security PRs

### Benchmark cadence & the v1.0 question

- Terminal-Bench 2.0 iteration beyond 56.2% (failure-class triage from the
  first run)
- AttractorBench re-runs as new models land
- Define what v1.0 means for tracker: API stability surface, docs bar,
  deprecation policy

## How this file is maintained

- A Now workstream finishes → close its milestone → promote a workstream from
  Next (create its milestone, attach issues) → update this file, all in one PR.
- Only Now-tier workstreams get milestones; Next/Later live here only.
- Every release PR refreshes this file (CLAUDE.md → Before releasing).
- New issues land in a tier when triaged; anything unlisted is implicitly Later.
