# Durable Recovery — Crash Recovery & Precise Resume

**Date:** 2026-07-20
**Status:** proposed
**Ship:** 1 (atomic writes) / 2 (checkpoint-before-gate)
**Depends on:** Phase 0 (panic containment prevents a *mass* re-run; this recovers a true
process kill)
**Related:** #213 (activity-log integrity), checkpoint/resume subsystem

## Question this answers

> If the bot crashes, can the system fully recover and resume precisely where it left off?

**Today: yes, at *node* granularity — with two durability bugs that can defeat recovery
entirely, plus a semantic caveat that in-flight nodes/gates re-run.** This workstream
closes the bugs and tightens the granularity.

## How recovery works today (verified)

1. **Intent persisted** — `store` writes `thread_ts → {channel, workflow, params}` to
   `RunsBase/trackerbot-state.json` on launch (`store.put`), removes on completion
   (`store.remove`). `cmd/trackerbot/store.go`.
2. **Deterministic paths** — workdir + `checkpoint.json` derive from `thread_ts`, so a
   restart finds the same checkpoint with no run-ID bookkeeping. `runner.runPaths`.
3. **Startup resume** — `st.list()` → `go runner.Resume(rec)` per orphan.
   `main.go:56-61`.
4. **Engine checkpoints per node** — completed nodes, per-node edge selections, and a
   context snapshot are saved at each node boundary (~14 `saveCheckpoint` sites in
   `engine_run.go`/`engine.go`). Resume skips completed nodes and continues.

So a `kill -9` / OOM / deploy mid-run resumes from the **last completed node**.

## What "precisely where it left off" does NOT mean

Checkpointing is at **node boundaries**, not per-LLM-turn. On resume, anything in flight
at crash time re-runs from that node's start:
- an agent node N tool-calls deep restarts from the top (and **re-spends** those tokens);
- a **pending human gate** re-asks (its node wasn't checkpointed), orphaning the old
  Slack message under a new gate id — a user may see the question twice;
- side effects are **not idempotent by the engine** — a tool node that already pushed to
  git / deployed / emailed repeats it on re-run.

## Findings (CONFIRMED)

### FD.1 — Checkpoint write is non-atomic (HIGH for recovery)
`pipeline/checkpoint.go:258-273`. `SaveCheckpoint` does `os.WriteFile(path, data, 0o600)`
directly — no temp-file + `os.Rename`. A crash *during* the write truncates
`checkpoint.json`; `LoadCheckpoint` (`checkpoint.go:276`) then fails to unmarshal and
resume cannot restart from it. The one file whose entire job is crash recovery is written
unsafely — the highest-value, lowest-risk fix in the whole plan.

### FD.2 — State file has the same flaw and fails silently *and globally* (HIGH)
`cmd/trackerbot/store.go:83-95`. `store.flush` also uses `os.WriteFile`. A torn write
corrupts `trackerbot-state.json`, and `openStore` (`store.go:32-43`) swallows the
`json.Unmarshal` error into an **empty** store — so *every* orphaned run is silently
dropped and no thread resumes. Worse than FD.1 (all-or-nothing) and silent (the coverage
review flagged this exact swallow as untested).

### FD.3 — Pending gate loses upstream work on resume (MEDIUM)
Because a gate node is only checkpointed on completion, a crash while awaiting a human
answer re-runs the gate's entire node — including any expensive upstream agent turn that
produced the gate's question — rather than resuming at "waiting for answer."

### FD.4 — Start/persist window (LOW)
`runner.launch` does `rm.Start` → `register` → `store.put`. A crash in that window leaves
the run in-memory-only and unpersisted → not resumed. But no checkpoint exists yet either
(node 0 not done), so nothing is lost except the ack. Minor; note only.

## Tasks

### TD.1 — Atomic checkpoint write
`SaveCheckpoint`: write to `path + ".tmp"` (same dir/filesystem), `fsync`, then
`os.Rename` over `path` (atomic on POSIX same-fs). On Windows, `os.Rename` over an
existing file needs a remove-then-rename or `ReplaceFile` — handle per-GOOS.
- Files: `pipeline/checkpoint.go`.

### TD.2 — Atomic state-file write + non-silent corruption handling
`store.flush`: same temp + rename. `openStore`: on `json.Unmarshal` error, do **not**
silently empty — log loudly and (D-3 decision) either refuse to start so an operator can
recover the file, or move the corrupt file aside (`.corrupt-<n>`) and continue empty with
a prominent warning. Never a silent drop (CLAUDE.md: never swallow errors).
- Files: `cmd/trackerbot/store.go`. Add a corrupt-file test (currently untested).

### TD.3 — Checkpoint before blocking on a human gate
Have the human handler (or the engine around it) save a checkpoint that marks the run as
"awaiting gate at node X" **before** blocking on the interviewer, so a crash resumes at
the gate — re-asking only the question, not re-running the upstream node that produced it.
Store enough to re-post the gate (prompt, choices, gate kind) so resume can re-render it.
- Files: `pipeline/handlers/human.go`, checkpoint schema (`pipeline/checkpoint.go`),
  `cmd/trackerbot/interviewer.go` (re-render on resume).

### TD.4 — Idempotency guidance (docs, not code)
Document that re-run of an in-flight node repeats side effects, and the authoring
patterns that make workflows crash-safe (idempotent file writes; guard external effects
behind a "already done?" check; keep irreversible steps in their own late node so the
window is small). Cross-link the checkpoint-resume fragility note in CLAUDE.md.
- Files: `docs/architecture/` (artifacts/engine), CLAUDE.md cross-ref.

### TD.5 (stretch) — Duplicate-message suppression on gate re-ask
When TD.3 lands, on resume detect a still-open prior gate message for the thread and edit
it in place (or note the re-ask) instead of posting a fresh duplicate.
- Files: `cmd/trackerbot/interviewer.go`, `slack.go` (message-ts bookkeeping in `store`).

## Verification / gates

- `go build ./...`, `go test ./... -short` green.
- New tests: a checkpoint written, then a simulated torn write (truncated tmp) — the
  committed `checkpoint.json` is either the old-complete or new-complete version, never a
  corrupt partial; corrupt `trackerbot-state.json` is surfaced, not silently emptied;
  a run that crashes at a gate resumes at the gate without re-running its upstream node
  (TD.3).
- Manual: kill the bot mid-run between nodes → restart → thread resumes from the next
  node; kill during a gate → gate re-asked (TD.3: without upstream re-run).

## Recovery matrix (target state after this workstream)

| Scenario | Today | After |
|---|---|---|
| Kill between nodes | resumes from last node | unchanged (already good) |
| Kill mid-node | re-runs node from start | unchanged (node granularity is the design) |
| Kill awaiting a gate | re-runs gate node incl. upstream | **resumes at the gate** (TD.3) |
| Kill during checkpoint/state write | can corrupt → run lost | **atomic → never lost** (TD.1/2) |
| Handler panic | process dies (all runs) | contained (Phase 0) → clean resume |
