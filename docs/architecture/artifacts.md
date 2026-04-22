# Artifacts and Run Directory Layout

Every tracker run leaves behind a self-describing directory on disk. This directory is the single source of truth for:

- **Audit** — `tracker audit` / `tracker.Audit` builds a timeline, retry list, and error summary from it.
- **Diagnose** — `tracker diagnose` / `tracker.Diagnose` inspects it to explain why a run failed.
- **Resume** — the checkpoint file lets a new run pick up where the previous one stopped.
- **Handoff** — with `WithGitArtifacts(true)`, the directory is a git repo and `ExportBundle` packages it as a portable `.bundle` file.

Everything about a run — prompts, responses, event stream, per-node outcomes, edge selections, restart counter — lives here. Callers do not need to hold on to in-memory state; the on-disk layout is durable and debuggable.

## Purpose

- Record **what the LLM saw** (`prompt.md`) and **what it produced** (`response.md`) per node so post-hoc review and retro-prompting are possible.
- Record **how the engine decided** via the JSONL activity log — every edge choice, context update, and outcome.
- Provide a **resumable** representation of run state via `checkpoint.json`.
- Optionally provide a **git-versioned** history of all of the above, suitable for bundle export to another machine.

## Layout

```text
.tracker/
  runs/
    <runID>/                      ← one directory per run
      activity.jsonl              ← append-only event stream (pipeline + agent + llm events)
      checkpoint.json             ← completed nodes, retry counts, context snapshot, edge selections
      <nodeID>/                   ← per-node artifact directory
        prompt.md                 ← the resolved prompt sent to the LLM / tool
        response.md               ← the final response or command output
        status.json               ← outcome, preferred label, suggested next nodes, context updates
      <anotherNode>/
        ...
      .git/                       ← only when WithGitArtifacts(true)
      .gitignore                  ← created when the run is a git repo
```

Subdirectory names are node IDs verbatim. Subgraph children appear under the parent's node ID (e.g. `<parent>/<child>/`) — see [tui.md §Node list](./tui.md) for how the TUI mirrors this structure.

## Files written per node

[pipeline/artifacts.go](../../pipeline/artifacts.go) is the single writer:

```go
WriteStageArtifacts(rootDir, nodeID, prompt, response, outcome) // prompt.md + response.md + status.json
WriteStatusArtifact(rootDir, nodeID, outcome)                   // status.json only (tool nodes without response)
```

`prompt.md` and `response.md` are plain UTF-8 markdown — line-for-line what the LLM or tool produced. They are meant to be cat-able and grep-able.

`status.json` is a structured snapshot of the outcome:

```json
{
  "outcome": "success",
  "preferred_next_label": "approve",
  "suggested_next_ids": ["NextNode"],
  "context_updates": {
    "last_response": "…",
    "response.Plan": "…"
  }
}
```

This is what `tracker diagnose` reads to reconstruct per-node status and what `tracker audit` cross-references against the activity log.

## Event log (`activity.jsonl`)

One JSON object per line, written by `pipeline/events_jsonl.go:JSONLEventHandler`. The handler opens `<artifactDir>/<runID>/activity.jsonl` lazily on the first event and appends for the life of the run.

Sources:

- `source: "pipeline"` — engine lifecycle events (stage started/completed/failed/retrying, edge decisions, checkpoint saved/failed, cost updated, budget exceeded, pipeline completed/failed).
- `source: "agent"` — `agent.Event` values forwarded via `WriteAgentEvent` (tool calls, text deltas, LLM request/finish, errors).
- `source: "llm"` — provider-level trace (`TraceRequestStart`, `TraceFinish`, etc.).

Each entry carries `ts`, `type`, optional `run_id` / `node_id` / `message` / `error`, and type-specific fields:

- **Edge decisions**: `edge_from`, `edge_to`, `edge_condition`, `edge_priority`, `condition_match`, `outcome_status`, `context_snapshot`, `context_updates`, `restart_count`, `cleared_nodes`.
- **Cost snapshots**: `total_tokens`, `total_cost_usd`, `provider_totals`, `wall_elapsed_ms`.
- **Tokens per request**: `token_input`, `token_output`.
- **Tool output**: `tool_name`, `content`, `provider`, `model`.

`tracker.ParseActivityLine` ([tracker_activity.go](../../tracker_activity.go)) is the canonical decoder. It handles two timestamp formats (RFC3339Nano and `2006-01-02T15:04:05.000Z07:00`) because the file has both historically — do not feed `ActivityEntry` through `json.Unmarshal` directly.

## Checkpoint (`checkpoint.json`)

Defined in [pipeline/checkpoint.go](../../pipeline/checkpoint.go):

```go
type Checkpoint struct {
    RunID          string
    CurrentNode    string
    CompletedNodes []string
    RetryCounts    map[string]int
    Context        map[string]string
    Timestamp      time.Time
    RestartCount   int
    EdgeSelections map[string]string // nodeID → selected edge target
    FallbackTaken  map[string]bool   // goal-gate fallback tracking
}
```

Written after every terminal node outcome via `engine.saveCheckpoint`. On the next `tracker run --resume <runID>` the engine loads this file and picks up at `CurrentNode`, skipping any node already in `CompletedNodes` (completed set rebuilt lazily for O(1) lookup).

`EdgeSelections` is what makes resume deterministic. Conditions that depended on context values from the previous attempt might evaluate differently after restart, so the engine replays the stored edge choice instead of re-evaluating. See CLAUDE.md §Checkpoint resume is fragile for the restart-counter caveat.

## Sequence: what happens when a node completes

```mermaid
sequenceDiagram
    participant Engine as pipeline.Engine
    participant Handler
    participant Artifacts as artifacts.go
    participant Git as gitArtifactRepo
    participant JSONL as events_jsonl.go
    participant CP as checkpoint.go

    Handler-->>Engine: Outcome{status, context_updates, ...}
    Engine->>Artifacts: WriteStageArtifacts(rootDir, nodeID, prompt, response, outcome)
    Artifacts->>Artifacts: write prompt.md, response.md, status.json
    Engine->>JSONL: emit EventStageCompleted / Failed
    JSONL->>JSONL: append jsonlLogEntry
    opt WithGitArtifacts
        Engine->>Git: CommitNode(nodeID, handler, status, traceEntry)
        Git->>Git: git add . ; git commit -m "node(...): ..."
        opt terminal outcome
            Engine->>Git: TagCheckpoint(nodeID)
            Git->>Git: git tag -f checkpoint/<runID>/<nodeID>
        end
    end
    Engine->>CP: saveCheckpoint(cp, pctx.Snapshot())
    CP->>CP: write checkpoint.json (atomic rename)
```

The order is: artifacts first (so the commit captures them), then JSONL, then git, then checkpoint. Git and checkpoint failures do not halt the run — they log a warning via `EventCheckpointFailed` and continue.

## Git artifacts (opt-in)

`WithGitArtifacts(true)` ([pipeline/engine.go](../../pipeline/engine.go)) enables the full git-backed workflow implemented in [pipeline/git_artifacts.go](../../pipeline/git_artifacts.go).

Initialization (`gitArtifactRepo.Init`):

1. Verify `git` is in `PATH`.
2. `mkdir -p` the artifact dir.
3. `git init --quiet` if `.git` is absent. Any non-`ErrNotExist` error on the stat is fatal (we do not silently skip init).
4. `git config user.name tracker` / `user.email tracker@local` locally (no global pollution).
5. Create `.gitignore` (`*.tmp`, `checkpoint.json`) if absent.
6. If `HEAD` does not exist (fresh repo), stage everything and make an initial empty commit "tracker: run <runID> started". If `HEAD` already exists (this is a resume against an existing dir), skip the initial commit so we don't accumulate noise.

After every terminal node outcome, `CommitNode` runs:

```
git add .
git commit --allow-empty -m "node(<id>): <handler> outcome=<status>

duration: <d>
edge_to: <to>
tokens: <n> cost: $<usd>
"
```

Failures here are logged but do not mark the repo as `failed=true` — one bad commit should not take down the whole run.

At checkpoint save points, `TagCheckpoint(nodeID)` creates a lightweight tag `checkpoint/<runID>/<nodeID>` at `HEAD`. Tags are idempotent (`-f`) so retrying the same node replaces the previous tag. These tags are the basis for future Layer-2 checkpoint-replay support (issue #77).

Environment safety: `gitSafeEnv()` strips `*_API_KEY`, `*_SECRET`, `*_TOKEN`, `*_PASSWORD` from the git subprocess env, mirroring the tool-handler logic and respecting the `TRACKER_PASS_ENV=1` escape hatch.

## Bundle export

`tracker.ExportBundle(runDir, outPath)` in [tracker_bundle.go](../../tracker_bundle.go):

```go
git -C <runDir> bundle create <outPath> --all
```

Captures every commit and every tag — including checkpoint tags — as a single portable file. On the receiving side:

```bash
git clone run.bundle recovered-run
cd recovered-run
git log --oneline
git show checkpoint/<runID>/<finalNode>
```

The bundle is stand-alone: no network access, no remote repo. This is the canonical way for a factory-worker instance to hand a completed run back to the user. The `--export-bundle` CLI flag calls this as a post-run step — failures are warnings and do not affect the exit code.

`Result.ArtifactRunDir` on the library `tracker.Result` is the canonical path callers pass to `ExportBundle`. It is populated when `WithArtifactDir` is set (directly via `Config.ArtifactDir` or indirectly via the default auto-generated directory).

## Use cases

- **Live view**: `tracker run --follow <runID>` tails `activity.jsonl` and renders the TUI.
- **Diagnose**: `tracker diagnose` (or `tracker diagnose <runID>`) reads `checkpoint.json` + `status.json` files + `activity.jsonl`, correlates `stage_failed` events with node status, classifies identical-retries (deterministic bug vs flaky), and emits actionable suggestions.
- **Audit**: `tracker audit` produces an `AuditReport` with timeline, retries, errors, and durations from the same sources.
- **Resume**: `tracker run --resume <runID>` loads `checkpoint.json`, skips completed nodes, replays stored `EdgeSelections`.
- **Retro**: `cat .tracker/runs/<runID>/<nodeID>/prompt.md` to re-inspect exactly what the LLM saw.
- **Factory handoff**: `tracker run --export-bundle /path/run.bundle`, ship the bundle, `git clone run.bundle`.
- **Forensics**: `git log --all --grep='outcome=fail'` inside an exported run shows every failing node in commit order.

## Integration points

- **Write side** — [pipeline/engine.go](../../pipeline/engine.go) (checkpoint save, git commit hooks), [pipeline/artifacts.go](../../pipeline/artifacts.go) (per-node files), [pipeline/events_jsonl.go](../../pipeline/events_jsonl.go) (activity log), [pipeline/git_artifacts.go](../../pipeline/git_artifacts.go) (git repo and commits), [pipeline/handlers/transcript.go](../../pipeline/handlers/transcript.go) (session-level transcript buffering inside codergen).
- **Read side** — [tracker_activity.go](../../tracker_activity.go) (resolve run dir, load activity log), [tracker_audit.go](../../tracker_audit.go) (`AuditReport` shape), [tracker_diagnose.go](../../tracker_diagnose.go) (`DiagnoseReport` shape), [tracker_bundle.go](../../tracker_bundle.go) (bundle export).
- **CLI** — `cmd/tracker/audit.go`, `cmd/tracker/diagnose.go`, `cmd/tracker/run.go` (sets `WithArtifactDir`, `WithCheckpointPath`, optional `WithGitArtifacts`).

## Gotchas and invariants

- **`status.json` writes happen even on failure.** `WriteStageArtifacts` is called on every terminal outcome, success or failure; diagnose depends on this.
- **`activity.jsonl` is append-only.** Never rewrite or truncate during a run — callers tailing the file rely on monotonically growing offsets. The file is opened with `O_APPEND`.
- **`checkpoint.json` is overwritten in place.** `SaveCheckpoint` writes atomically via temp-file-then-rename so a crash mid-write cannot corrupt the file.
- **Fresh-vs-resume detection uses `git rev-parse --verify HEAD`.** An existing HEAD means "resume" and the initial commit is skipped. Do not add new init steps above this check or the resume semantics change.
- **`.gitignore` excludes `checkpoint.json` and `*.tmp`.** This keeps commits focused on prompts / responses / status and avoids reserializing run state. If checkpoint.json changes need to be durable, export via bundle (which captures the tree at each commit, not the checkpoint).
- **`EdgeSelections` makes resume deterministic.** Without it, a condition over `ctx.last_response` (which may have changed in a later run) would re-evaluate and possibly pick a different edge than the original run. Do not bypass this map when implementing new edge types.
- **`FallbackTaken` persists across checkpoint saves.** Goal-gate fallback/escalation is one-shot per node per run — even across resumes.
- **Per-milestone circuit breakers are separate.** `build_product.dip` uses an on-disk `fix_attempts` file counter that **is not reset by resume**. This is a deliberate design tradeoff; CLAUDE.md §Per-milestone circuit breakers documents it.
- **Git operations are best-effort.** `gitArtifactRepo.failed=true` after init failure turns subsequent ops into no-ops. Callers should not depend on `git show` succeeding — use `tracker diagnose` on the JSONL/status files instead.
- **Sensitive env is stripped from git subprocesses.** Anything matching `*_API_KEY`, `*_SECRET`, `*_TOKEN`, `*_PASSWORD` is filtered unless `TRACKER_PASS_ENV=1`. Do not reintroduce these implicitly.
- **Timestamps in the log come in two formats.** Parse via `ParseActivityLine` / `parseActivityTimestamp`; do not write `time.Time` fields directly.
- **`ArtifactRunDir` is the canonical path for external callers.** Construct it from `Result.ArtifactRunDir`, not from manual string manipulation. Not every path in tracker includes `/runs/` — embedded subdirectories may differ.

## Files

- [pipeline/artifacts.go](../../pipeline/artifacts.go) — `WriteStageArtifacts`, `WriteStatusArtifact`, `stageStatus` JSON shape.
- [pipeline/checkpoint.go](../../pipeline/checkpoint.go) — `Checkpoint`, `SaveCheckpoint`, `LoadCheckpoint`, edge-selection helpers.
- [pipeline/events_jsonl.go](../../pipeline/events_jsonl.go) — `JSONLEventHandler`, `jsonlLogEntry`, `buildLogEntry`, `WriteAgentEvent`.
- [pipeline/git_artifacts.go](../../pipeline/git_artifacts.go) — `gitArtifactRepo`, `Init`, `CommitNode`, `TagCheckpoint`, `gitSafeEnv`.
- [pipeline/engine.go](../../pipeline/engine.go) — `WithArtifactDir`, `WithCheckpointPath`, `WithGitArtifacts`, `WithBudgetGuard`.
- [pipeline/engine_checkpoint.go](../../pipeline/engine_checkpoint.go) — `saveCheckpoint`, `loadOrCreateCheckpoint`, `clearDownstream`.
- [pipeline/handlers/transcript.go](../../pipeline/handlers/transcript.go) — per-node transcript buffer feeding `response.md`.
- [tracker_bundle.go](../../tracker_bundle.go) — `ExportBundle`.
- [tracker_activity.go](../../tracker_activity.go) — `ResolveRunDir`, `MostRecentRunID`, `LoadActivityLog`, `ParseActivityLine`.
- [tracker_audit.go](../../tracker_audit.go) — `Audit`, `AuditReport`, `TimelineEntry`, `RetryRecord`, `ActivityError`.
- [tracker_diagnose.go](../../tracker_diagnose.go) — `Diagnose`, `DiagnoseReport`, `NodeFailure`, `Suggestion`, budget-halt detection.

## See also

- [../ARCHITECTURE.md](../../ARCHITECTURE.md) — where the artifact layer sits in the stack.
- [./engine.md](./engine.md) — engine lifecycle that emits events written here.
- [./handlers.md](./handlers.md) — handlers that produce node-level prompts and responses.
- [../pipeline-context-flow.md](../pipeline-context-flow.md) — context snapshot model (the same snapshot written to `checkpoint.json`).
- [./tui.md](./tui.md) — the TUI reads the same event stream live via the `tea.Program.Send` path.
