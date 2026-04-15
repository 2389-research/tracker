# CLAUDE.md — Tracker Project Instructions

## Project Overview

Tracker is a pipeline orchestration engine for multi-agent LLM workflows.
Pipelines are defined in `.dip` files (Dippin language) and executed with
parallel agents via a TUI dashboard. Built by 2389.ai.

## Critical Rules

### NEVER use --no-verify
- `git commit --no-verify` and `git push --no-verify` are **forbidden** under all circumstances
- If pre-commit hooks fail, fix the root cause — do not bypass the hooks
- This applies even for merge commits, "pre-existing" issues, or any other justification
- The hooks (coverage, complexity, tests, lint, format) are safety gates — skipping them defeats their purpose

### Never silently swallow errors
- Provider errors (quota, auth, model not found) must hard-fail the pipeline, not retry
- Empty agent responses (0 tokens, 0 tool calls) are failures, not successes
- SSE stream errors (`error`, `response.failed` events) must be parsed and surfaced
- Condition evaluation on unresolved variables must warn, not silently return empty string

### Dippin-lang compatibility
- The dippin IR uses `ctx.` namespace prefix in conditions (`ctx.outcome = success`)
- Tracker's context stores bare keys (`outcome`). The condition evaluator strips `ctx.`, `context.`, and handles `internal.*`
- The adapter must synthesize implicit edges from `ParallelConfig.Targets` and `FanInConfig.Sources`
- `AgentConfig.ResponseFormat` and `AgentConfig.ResponseSchema` map to node attrs `response_format` and `response_schema` (v0.16.0)
- `AgentConfig.Params` is a generic pass-through map — typed fields take precedence over Params keys
- The adapter maps IR field names to tracker convention: `model` → `llm_model`, `provider` → `llm_provider`
- Provider name is `gemini` not `google`
- Variable expansion is single-pass — never re-scan resolved values
- `ensureStartExitNodes` only assigns passthrough start/exit handlers to nodes without handler-specific content. It checks for `prompt` (agent), `tool_command` (tool), and `mode` (human) attrs. Nodes with any of these keep their real handler. Bare nodes (none of the three) get the passthrough handler.

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

### Yes/No mode
- `mode: yes_no` on human nodes presents a fixed "Yes"/"No" choice
- Yes maps to `OutcomeSuccess`, No maps to `OutcomeFail`
- Pipelines route with `ctx.outcome = success` / `ctx.outcome = fail` conditions
- This is distinct from default choice mode, where outcome is always `success` and routing uses `PreferredLabel`
- `AutoApproveInterviewer` picks "Yes" (first choice) by default — forward progress semantics

### Interview mode
- `mode: interview` on human nodes enables structured multi-field form collection
- Upstream agent outputs JSON questions: `{"questions": [{"text": "...", "context": "...", "options": [...]}]}`
- Use `response_format: json_object` on question-generating agents to force JSON output at the API level
- `ParseStructuredQuestions` validates JSON first; falls back to `ParseQuestions` markdown heuristic parsing
- Questions with "other" variants in options are filtered — the UI always provides its own "Other" escape hatch
- One question shown at a time with progress bar, answered summary above, and selection feedback (filled dot + checkmark)
- Canceled interviews return `OutcomeFail`, not `OutcomeSuccess` — pipeline edges can route on cancellation
- `questions_key` defaults to `last_response`; `answers_key` defaults to `interview_answers`
- Zero parsed questions falls back to freeform with the node's `prompt` attribute
- Enter confirms selection and advances; Ctrl+S submits all; Esc cancels
- Empty API responses (0 content parts, 0 output tokens, 0 prior tool calls) trigger session-level retry with diagnostic logging

### Error surfacing
- Node failures (MsgNodeFailed) and retries (MsgNodeRetrying) must be shown inline in the activity log, not just in the sidebar icon
- Tool node stderr/stdout must be visible to the user — the `tracker diagnose` command reads status.json and activity.jsonl for this
- The "no providers configured" error must include actionable setup instructions, not just the raw error message

### TUI keyboard shortcuts (v0.13.0)
- `v` — cycle log verbosity (all/tools/errors/reasoning)
- `z` — toggle zen mode (full-width agent log, sidebar hidden)
- `/` — search agent log (n/N for next/prev match, Esc to exit)
- `?` — help overlay with all shortcuts
- `Enter` — drill down into selected node (arrow keys navigate, Esc to exit)
- `y` — copy visible log to clipboard
- `Ctrl+O` — expand/collapse tool output
- `q` — quit

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

### Structured output (`response_format`)
The `response_format: json_object` attribute on agent nodes forces JSON output
at the LLM API level. The path: `.dip` file → `AgentConfig.ResponseFormat` →
adapter → `node.Attrs["response_format"]` → `codergen.applyResponseFormat()` →
`SessionConfig.ResponseFormat` → `session.buildResponseFormat()` →
`llm.Request.ResponseFormat` → provider translator (Anthropic: system instruction,
OpenAI: native json_object, Gemini: responseMimeType). Use this on any agent
that must produce structured JSON (interview question generators, autopilot, etc.).

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

### Token usage flows through three layers
The `llm.Usage` struct tracks per-API-call tokens. `agent.SessionResult.Usage`
accumulates across turns within a session. `buildSessionStats()` in
`pipeline/handlers/transcript.go` copies usage into `pipeline.SessionStats`
on each trace entry. `Trace.AggregateUsage()` sums all trace entries into
`UsageSummary`, which lands on `EngineResult.Usage`.

For parallel execution, the parallel handler aggregates branch `SessionStats`
into its own outcome so the trace entry for the parallel node carries the
combined usage of all branches.

The CLI summary in `cmd/tracker/summary.go` uses `llm.TokenTracker` for
per-provider breakdowns (middleware-level) and `EngineResult.Usage` for
trace-level aggregation. These are independent data sources.

### Cost governance (v0.17.0+)

`UsageSummary.ProviderTotals` carries the per-provider rollup (tokens + cost),
and `tracker.Result.Cost` on the library API exposes dollar cost via
`llm.TokenTracker.CostByProvider`. The pipeline engine enforces optional
ceilings via `pipeline.BudgetGuard`, which is evaluated between nodes
after every `emitCostUpdate`. A breach halts the run with
`pipeline.OutcomeBudgetExceeded`, populates `EngineResult.BudgetLimitsHit`,
and fires `EventBudgetExceeded` carrying the final `CostSnapshot`.

Configure budgets via `tracker.Config.Budget` or the `--max-tokens`,
`--max-cost` (cents), `--max-wall-time` CLI flags. Reading them from
`.dip` workflow attrs is blocked on dippin-lang IR support (issue #67).

### OpenAI returns errors inside 200 SSE streams
The Responses API returns HTTP 200 and sends `error` / `response.failed`
as SSE event types. The adapter must handle these — they are NOT reflected
in the HTTP status code.

### CLI UX commands
- `tracker workflows` — lists all embedded built-in workflows with display names and goals.
- `tracker init <name>` — copies a built-in workflow to cwd for customization. Refuses to overwrite.
- `tracker doctor` — preflight health check (API keys, dippin binary, workdir). Run before first pipeline.
- `tracker diagnose [runID]` — deep failure analysis (reads status.json + activity.jsonl). Shows tool output, stderr, errors, timing anomalies, actionable suggestions. Without a run ID, analyzes the most recent run.
- `tracker update` — self-update to latest GitHub release. Detects install method (Homebrew/go install/binary), verifies SHA256 checksum, smoke-tests new binary, atomic swap with .bak rollback. Non-blocking update check runs on every `tracker run` (24h cache).
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
- Provider errors hard-fail per CLAUDE.md (only parse failures fall back to default)
- Two autopilot implementations: `AutopilotInterviewer` (native API) and `ClaudeCodeAutopilotInterviewer` (claude CLI subprocess)
- When `--backend claude-code`, autopilot routes through the claude subprocess — no API key needed
- Default model picks cheapest from configured provider via `Client.DefaultProvider()`
- `autopilotCfg` in `cmd/tracker/run.go` threads the config to `chooseInterviewer`

### Tool node safety — LLM output as shell input
- NEVER `eval` content extracted from LLM-written files (arbitrary command execution)
- Variable expansion in tool_command uses a safe-key allowlist for `ctx.*` keys: only `outcome`, `preferred_label`, `human_response`, `interview_answers` can be interpolated. All `graph.*` and `params.*` keys are always allowed (author-controlled). All LLM-origin `ctx.*` keys (`last_response`, `tool_stdout`, `response.*`, etc.) are blocked.
- The safe pattern: write LLM output to a file in a prior tool node, then read it in the command: `cat .ai/output.json | jq ...`
- Tool command output is capped at 64KB per stream by default (configurable via `output_limit` node attr, hard ceiling 10MB via `--max-output-limit`)
- A built-in denylist blocks common dangerous patterns (eval, pipe-to-shell, curl|sh). Use `--bypass-denylist` to override.
- An optional allowlist (`--tool-allowlist` CLI flag or `tool_commands_allow` graph attr) restricts commands to specific patterns. The allowlist cannot override the denylist.
- Sensitive environment variables (`*_API_KEY`, `*_SECRET`, `*_TOKEN`, `*_PASSWORD`) are stripped from tool subprocesses. Override with `TRACKER_PASS_ENV=1`.
- Always strip comments (`grep -v '^#'`) and blank lines from LLM-generated lists before using as patterns
- Use flexible regex for markdown headers LLMs write (they vary: `##`, `###`, with/without colons)
- Add empty-file guards after extracting content from LLM-written files — fail loudly, don't proceed with empty data
- Use `go test -skip` (Go 1.24+) instead of `(?!` negative lookahead which Go's regexp doesn't support
- The Decompose prompt explicitly instructs the agent on expected file formats

### Claude Code backend (v0.12.0, updated v0.13.0)
- `AgentBackend` interface in `pipeline/backend.go` — minimal contract: one method to execute a node, returns `agent.Event` stream
- `CodergenHandler` delegates to backends via `selectBackend()`, doesn't execute LLM calls directly
- `ClaudeCodeBackend` spawns `claude` as a subprocess, parses NDJSON stdout into `agent.Event` values
- `NativeBackend` wraps `agent.Session` — the existing turn loop with tool registry and context compaction (default)
- Per-node selection via `backend: claude-code` attribute in `.dip` files; global via `--backend claude-code` CLI flag
- Environment scoping: API keys (`ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, etc.) are **stripped** from the subprocess env so the claude CLI uses subscription auth (Max/Pro OAuth). Override with `TRACKER_PASS_API_KEYS=1`.
- Per-node `backend` attr takes priority over the global `--backend` flag: a node with `backend: native` uses native even when `--backend claude-code` is set globally
- With `--backend claude-code` and no per-node override, nodes route through the claude CLI — non-Anthropic model names are stripped so the CLI uses its default
- `buildLLMClient()` is lazy: failure is non-fatal with `--backend claude-code` (native client only needed for native backend nodes)
- Error classification: Claude CLI exit codes are mapped to pipeline outcomes (success, fail, escalate); credit balance errors logged with actionable guidance
- The engine and TUI see the same `agent.Event` stream regardless of backend — no special-case code needed
- All three built-in workflows are backend-agnostic: they work with both native and claude-code

### Strict failure edges (v0.13.0)
- When a node's outcome is "fail" and all outgoing edges are unconditional, the pipeline stops
- This prevents tool nodes (Setup, Build) from silently continuing after failure
- Pipelines that intentionally handle failure must use `when ctx.outcome = fail` edges
- Nodes with ANY conditional edges are assumed to have intentional routing

### Per-milestone circuit breakers
The `build_product.dip` pipeline uses a `fix_attempts` file on disk to limit
retries per milestone. This counter persists across pipeline restarts — if a
human says "retry" after escalation, the counter is already maxed. The counter
is only reset in `MarkMilestoneDone`. This is a design tradeoff, not a bug,
but users need to know about it (`tracker diagnose` surfaces this).
