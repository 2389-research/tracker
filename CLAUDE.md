# CLAUDE.md — Tracker Project Instructions

## Project Overview

Tracker is a pipeline orchestration engine for multi-agent LLM workflows.
Pipelines are defined in `.dip` files (Dippin language) and executed with
parallel agents via a TUI dashboard. Built by 2389.ai.

## Critical Rules

### Never silently swallow errors
- Provider errors (quota, auth, model not found) must hard-fail the pipeline, not retry
- Empty agent responses (0 tokens, 0 tool calls) are failures, not successes
- SSE stream errors (`error`, `response.failed` events) must be parsed and surfaced
- Condition evaluation on unresolved variables must warn, not silently return empty string

### Dippin-lang compatibility
- The dippin IR uses `ctx.` namespace prefix in conditions (`ctx.outcome = success`)
- Tracker's context stores bare keys (`outcome`). The condition evaluator strips `ctx.`, `context.`, and handles `internal.*`
- The adapter must synthesize implicit edges from `ParallelConfig.Targets` and `FanInConfig.Sources`
- The adapter maps IR field names to tracker convention: `model` → `llm_model`, `provider` → `llm_provider`
- Provider name is `gemini` not `google`
- Variable expansion is single-pass — never re-scan resolved values

### Parallel execution
- The parallel handler dispatches branches from `parallel_targets` attr, NOT from outgoing graph edges
- After dispatch, the handler sets `suggested_next_nodes` pointing to the fan-in join node
- Branch goroutines must have defer/recover for panic safety
- The parallel handler emits EventStageStarted/Completed per branch for TUI visibility

### Edge routing — no unconditional fallbacks to loop targets
- NEVER add an unconditional edge to the same target as a conditional edge (causes infinite loops)
- Safe fallbacks go to an escalation gate or Done, not to FixX or the same gate
- In `build_product.dip`, use `EscalateMilestone` for mid-build failures and `EscalateReview` for post-build failures
- Conditions `outcome=success` + `outcome=fail` are exhaustive — no fallback needed on those

### Human gate UX
- Freeform gates with labeled edges use the hybrid radio+freeform modal (HybridContent), NOT plain FreeformContent
- Labeled gates with long context (>200 chars or >5 lines after `---`) use ReviewHybridContent — fullscreen glamour viewport + radio labels + freeform "other" option
- Long prompts (20+ lines) without labels use the split-pane ReviewContent with glamour-rendered viewport
- Both HybridContent and ReviewHybridContent have an "other" option with a textarea for custom freeform input
- The full prompt (label + context) must go through glamour — never render markdown with plain lipgloss
- All modal content types must implement Cancellable — Ctrl+C calls Cancel() to close reply channels and prevent goroutine hangs
- Never block a pipeline handler goroutine on a channel send/receive without a cancellation path

### Error surfacing
- Node failures (MsgNodeFailed) and retries (MsgNodeRetrying) must be shown inline in the activity log, not just in the sidebar icon
- Tool node stderr/stdout must be visible to the user — the `tracker diagnose` command reads status.json and activity.jsonl for this
- The "no providers configured" error must include actionable setup instructions, not just the raw error message

### TUI stability
- The activity log is append-only with line-level styling — no glamour markdown rendering
- Each line is styled once on newline and never re-rendered
- The activity indicator line is always reserved (space when idle) to prevent viewport shift
- Per-node streams for parallel execution with separators on node change
- Count actual terminal rows (not entries) when budgeting the viewport

## Versioning and Releases

### Changelog
- Keep CHANGELOG.md updated with every feature, fix, and breaking change
- Use [Keep a Changelog](https://keepachangelog.com/) format
- Group entries under Added, Changed, Fixed, Removed
- Update the changelog in the same PR as the code change, not after

### Releases
- Tag releases on GitHub with semantic versioning (vMAJOR.MINOR.PATCH)
- Create GitHub releases with release notes derived from CHANGELOG.md
- Tag after a coherent batch of work, not after every commit
- Breaking changes bump MAJOR, new features bump MINOR, fixes bump PATCH

### Version bumps
- Update go.mod module version on MAJOR bumps
- Keep dippin-lang dependency pinned to a tagged version, not a commit hash
- After updating dippin-lang, run `dippin doctor` on all example pipelines and verify scores

## Development Workflow

### Before committing
- `go build ./...` — must pass
- `go test ./... -short` — all 14 packages must pass
- `dippin doctor examples/ask_and_execute.dip examples/build_product.dip examples/build_product_with_superspec.dip` — must be A grade

### Before releasing
- Run `dippin doctor` on ALL example .dip files — aim for A grade across the board
- Run `dippin simulate -all-paths` on the three core pipelines
- Update CHANGELOG.md and README.md
- Tag and push: `git tag vX.Y.Z && git push origin vX.Y.Z`
- Create GitHub release

### dippin-lang updates
- NEVER run `go install github.com/2389-research/dippin-lang/cmd/dippin@...` — the user installs dippin from their local development checkout
- DO run `go get github.com/2389-research/dippin-lang@vX.Y.Z` to update the Go module dependency
- After updating, verify: `go build ./... && go test ./... -short`

## Architecture Gotchas

### The adapter is the bridge
`pipeline/dippin_adapter.go` converts dippin IR to tracker's Graph model.
Every naming mismatch between dippin conventions and tracker conventions
lives here. When dippin-lang adds new IR fields, the adapter needs updating.

### The engine doesn't know about parallel/fan-in
The engine treats every node the same: execute handler, select edge, advance.
The parallel handler does concurrent dispatch internally and hints the engine
where to go next via `suggested_next_nodes`. The engine has no special-case
code for parallel execution.

### Checkpoint resume is fragile
Checkpoints store completed nodes and context snapshots. Edge selections are
stored per-node for deterministic replay. But the restart counter is global
across the entire run — a fix loop on milestone 1 consumes restart budget
that milestone 10 needs. Use per-milestone circuit breakers in the pipeline
design (e.g., attempt counter file).

### OpenAI returns errors inside 200 SSE streams
The Responses API returns HTTP 200 and sends `error` / `response.failed`
as SSE event types. The adapter must handle these — they are NOT reflected
in the HTTP status code.

### CLI UX commands
- `tracker workflows` — lists all embedded built-in workflows with display names and goals.
- `tracker init <name>` — copies a built-in workflow to cwd for customization. Refuses to overwrite.
- `tracker doctor` — preflight health check (API keys, dippin binary, workdir). Run before first pipeline.
- `tracker diagnose [runID]` — deep failure analysis (reads status.json + activity.jsonl). Shows tool output, stderr, errors, timing anomalies, actionable suggestions. Without a run ID, analyzes the most recent run.
- `tracker version` — shows commit hash, build time, and which providers are configured. Uses Go VCS metadata for `go install` builds, GoReleaser ldflags for releases.

### Bare name resolution
Running `tracker build_product` (no path, no extension) resolves in order:
1. `build_product.dip` in cwd (local file wins)
2. `build_product` as a file in cwd
3. Built-in embedded workflow by name
4. Error with list of available built-ins

This applies to `tracker validate`, `tracker simulate`, and `tracker run` uniformly via `resolvePipelineSource()` in `cmd/tracker/resolve.go`.

### Autopilot mode
- `--autopilot <persona>` replaces all human gates with LLM-backed decisions
- Four personas: `lax` (forward progress), `mid` (balanced, default), `hard` (high bar), `mentor` (approve with feedback)
- `--auto-approve` is deterministic (no LLM) — always picks default/first option
- The `AutopilotInterviewer` in `pipeline/handlers/autopilot.go` implements `LabeledFreeformInterviewer`
- Uses structured JSON output: `{"choice": "...", "reasoning": "..."}`
- Falls back to default edge on LLM error (with warning to stderr)
- The autopilot reuses the pipeline's LLM client — no separate config needed
- `activeRunConfig` in `cmd/tracker/run.go` threads the config to `chooseInterviewer`

### Per-milestone circuit breakers
The `build_product.dip` pipeline uses a `fix_attempts` file on disk to limit
retries per milestone. This counter persists across pipeline restarts — if a
human says "retry" after escalation, the counter is already maxed. The counter
is only reset in `MarkMilestoneDone`. This is a design tradeoff, not a bug,
but users need to know about it (`tracker diagnose` surfaces this).
