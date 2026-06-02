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
- Provider base URL resolution goes through `tracker.ResolveProviderBaseURL(provider)`: `<PROVIDER>_BASE_URL` env var wins; if unset, `TRACKER_GATEWAY_URL` is used with the provider suffix appended (`/anthropic`, `/openai`, `/google-ai-studio`, `/compat`); otherwise empty (SDK default). The `--gateway-url` CLI flag sets `TRACKER_GATEWAY_URL` before the LLM client is built. Use this for Cloudflare AI Gateway routing.
- Variable expansion is single-pass — never re-scan resolved values
- `ensureStartExitNodes` only assigns passthrough start/exit handlers to bare codergen (Agent) nodes with no `prompt` attribute. All other resolved handlers (`tool`, `wait.human`, `parallel`, `parallel.fan_in`, `conditional`, `subgraph`, `stack.manager_loop`, etc.) are preserved. The detection is based on `n.Handler`, not on enumerating handler-specific attributes, so future handler types are automatically covered.

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
- `questions_key` defaults to `interview_questions` (with `last_response` as a read-time fallback inside `resolveAgentOutput`); `answers_key` defaults to `interview_answers`
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
- **Merging a `release: vX.Y.Z` PR is NOT the release.** The release PR only ships CHANGELOG/README doc updates. The actual release requires `git tag -a vX.Y.Z <merge-commit> -m "release: vX.Y.Z"` followed by `git push origin vX.Y.Z`. The tag push triggers `.github/workflows/release.yml` → GoReleaser, which builds darwin/linux amd64/arm64 binaries for `tracker` and `tracker-conformance` and publishes the GitHub release. Do not consider a release done until the tag is pushed and `gh release view vX.Y.Z` returns the published entry with assets. This was missed on v0.19.0 and v0.20.0 originally — both had to be back-tagged retroactively.

### Website (GitHub Pages)
- Site is hosted on GitHub Pages at <https://2389-research.github.io/tracker/>
- Source: `site/` directory on `main`, built with Hugo extended v0.161.1. The `gh-pages` branch is a build artifact — never edit it by hand.
- Layout: Hugo project at `site/` with hand-written HTML in `site/content/*.html` (home, workflows, architecture, cli, changelog, glossary), shared layouts in `site/layouts/`, static assets (style.css, highlight.js, AGENTS.md, llms.txt) in `site/static/`, nav data in `site/data/nav.yaml`.
- Deploy: `.github/workflows/docs.yml` runs Hugo on every push to `main` that touches `site/**` or the workflow itself, then publishes `site/public/` to `gh-pages` via `peaceiris/actions-gh-pages` with `force_orphan: true` (single-commit history). No manual `gh-pages` edits needed.
- Local preview: `cd site && hugo server` (port 1313 by default). Site is served under `/tracker/` per `baseURL` in `site/hugo.toml`. The `public/` directory is gitignored.
- Per-page front matter controls a14y metadata: `title`, `description`, `og_title`, `og_description`, `mermaid: true` (only on pages with diagrams), and a `jsonld:` block that the head partial inlines as `<script type="application/ld+json">`. Use `TechArticle` for inner pages, `SoftwareApplication` for home, `DefinedTermSet` for glossary.
- Adding a new page: drop `site/content/<name>.html` with the front matter block (see existing pages for the pattern), add an entry to `site/data/nav.yaml` if it should appear in the top nav, push to `main`. The workflow handles the rest.
- `uglyURLs = true` in `hugo.toml` keeps URLs at `/tracker/<name>.html` (matching the pre-Hugo URL shape — preserves external links and search-indexed paths).

### Version bumps
- Update go.mod module version on MAJOR bumps
- Keep dippin-lang dependency pinned to a tagged version, not a commit hash
- After updating dippin-lang, run `dippin doctor` on all example pipelines and verify scores

## Development Workflow

### Before committing
- `go build ./...` — must pass
- `go test ./... -short` — all 17 packages must pass
- `dippin doctor examples/ask_and_execute.dip examples/build_product.dip examples/build_product_with_superspec.dip` — must be A grade

### Before releasing
- Run `dippin doctor` on ALL example .dip files — aim for A grade across the board
- Run `dippin simulate -all-paths` on the three core pipelines
- Update CHANGELOG.md and README.md
- After the `release: vX.Y.Z` PR merges, tag the merge commit and push:
  - `git tag -a vX.Y.Z <merge-commit-sha> -m "release: vX.Y.Z"`
  - `git push origin vX.Y.Z`
  - The tag push triggers `.github/workflows/release.yml` → GoReleaser, which builds binaries and creates the GitHub release entry. Merging the release PR alone does not create a release (see Critical Rules § Releases).
- Refresh the website (`gh-pages` branch — see Critical Rules § Website)

### dippin-lang updates
- NEVER run `go install github.com/2389-research/dippin-lang/cmd/dippin@...` — the user installs dippin from their local development checkout
- DO run `go get github.com/2389-research/dippin-lang@vX.Y.Z` to update the Go module dependency
- After updating, verify: `go build ./... && go test ./... -short`

## Architecture Gotchas

### The adapter is the bridge
`pipeline/dippin_adapter.go` converts dippin IR to tracker's Graph model.
Every naming mismatch between dippin conventions and tracker conventions
lives here. When dippin-lang adds new IR fields, the adapter needs updating.

### Typed node-config accessors (v0.22.0)
Node configuration reads go through typed accessors on `*pipeline.Node`:
`AgentConfig(graphAttrs)`, `ToolConfig()`, `HumanConfig()`, `ParallelConfig()`,
`RetryConfig(graphAttrs)`. Defined in `pipeline/node_config.go`. Every handler
that previously read `node.Attrs[...]` directly now calls the typed accessor
(a handful of strict-parse helpers retain raw reads — look for inline
comments). When adding a new node attribute, extend the appropriate
`NodeConfig` struct and its accessor; don't add new `node.Attrs[...]` reads.

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

### Git artifacts and bundle export
`WithGitArtifacts(true)` initializes the artifact run directory as a git repo
and commits after every terminal node. `ExportBundle(runDir, outPath)` in
`tracker_bundle.go` wraps `git bundle create <outPath> --all` to produce a
portable, self-contained bundle of the run's full history (commits + tags).
`Result.ArtifactRunDir` is the canonical way for callers to locate the run
directory. The `--export-bundle` CLI flag calls `ExportBundle` as a post-run
step; failures are warnings and do not affect the exit code. `ExportBundle`
is the canonical way for a remote factory-worker to hand a completed run
back to the user — clone with `git clone <bundle>` on any machine.

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

### Pipeline context isolation
For the user-facing model of data flow between nodes, context scoping, and fidelity levels, see **[Pipeline Context Flow](docs/architecture/context-flow.md)**. Per-node scoping (`node.<nodeID>.<key>`) is a stable feature — after each node finishes, its dirty writes are aliased under `node.<nodeID>.<key>` so later nodes can reference a specific upstream node's output by name. Declarative `writes:` (v0.21.0) builds on this: agent/tool/interview-human nodes can declare the keys they produce, and a typed JSON payload is extracted into first-class context keys.

### Cost governance (v0.17.0+)

`UsageSummary.ProviderTotals` carries the per-provider rollup (tokens + cost),
and `tracker.Result.Cost` on the library API exposes dollar cost via
`llm.TokenTracker.CostByProvider`. The pipeline engine enforces optional
ceilings via `pipeline.BudgetGuard`, which is evaluated between nodes
after every `emitCostUpdate`. A breach halts the run with
`pipeline.OutcomeBudgetExceeded`, populates `EngineResult.BudgetLimitsHit`,
and fires `EventBudgetExceeded` carrying the final `CostSnapshot`.

Configure budgets via `tracker.Config.Budget`, the `--max-tokens`,
`--max-cost` (cents), `--max-wall-time` CLI flags, or a `defaults:` block
in the `.dip` workflow (v0.19.0, closed #67). The path:
`WorkflowDefaults.MaxTotalTokens` / `MaxCostCents` / `MaxWallTime` →
`extractWorkflowDefaults` in `pipeline/dippin_adapter.go` →
`graph.Attrs["max_total_tokens" | "max_cost_cents" | "max_wall_time"]` →
`tracker.ResolveBudgetLimits(cfg, graph)` folds the graph values in as
fallbacks for any field left zero on `Config.Budget` / CLI. CLI flags and
`Config.Budget` always win over `defaults:`.

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

### Library API equivalents (for embedded integrations)
- Prefer `tracker.Diagnose(ctx, runDir)` / `tracker.DiagnoseMostRecent(ctx, workDir)` over shelling out to `tracker diagnose` and scraping stdout.
- Use `tracker.Doctor(ctx, cfg, opts...)` for structured preflight checks in services/tests.
- Use `tracker.Audit(ctx, runDir)` and `tracker.ListRuns(workDir, ...)` for run inspection.
- Use `tracker.NewNDJSONWriter(io.Writer)` to get the same stream shape as `tracker --json`.

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
- For fully headless execution without an LLM judge, use `--webhook-url` instead — gates are POSTed to an external service and blocked on a callback (Closes #63, #86)

### Tool output capture (v0.25.2+)

- `ctx.tool_stdout` and `ctx.tool_stderr` carry the **tail** of the
  stream up to the per-stream cap (default 64KB, `output_limit` per-node
  override). Head bytes are elided when the stream overflows. Routing
  markers emitted at end-of-output via `printf` survive truncation by
  construction. This matches the documented "right-trimmed so edge
  conditions match reliably" contract that pre-#208 the runtime
  violated by keeping the head.
- When a stream overflows, the runtime emits `EventToolOutputTruncated`
  with `TruncationDetail{stream, limit, captured_bytes, dropped_bytes}`.
  `tracker diagnose` surfaces this as a suggestion. The captured value
  itself contains **only** the tail payload — no in-band marker string
  is appended (pre-#208 a `"...(output truncated at N bytes)"` suffix
  was embedded, which could clobber routing-relevant trailing tokens).
- When a node's outgoing edges include conditional predicates AND all
  conditionals evaluate false AND routing falls through to an
  unconditional edge, the engine emits `EventConditionalFallthrough`
  with the list of conditions that missed. This does NOT fire on
  intentional unconditional-only routing — it specifically marks
  "stated routing intent missed." `tracker diagnose` correlates this
  with truncation events to surface "your routing marker may have been
  dropped" when both fire on the same node.

### Tool node safety — LLM output as shell input
- NEVER `eval` content extracted from LLM-written files (arbitrary command execution)
- Variable expansion in tool_command uses a safe-key allowlist for `ctx.*` keys: only `outcome`, `preferred_label`, `human_response`, `interview_answers` can be interpolated. All `graph.*` and `params.*` keys are always allowed (author-controlled). All LLM-origin `ctx.*` keys (`last_response`, `tool_stdout`, `tool_marker`, `tool_route`, `response.*`, etc.) are blocked. `manager_loop` steer_context keys are namespaced under `steer.*` (#177 option B) so they can never collide with the safe-allowlisted bare keys above — even if a future feature lets steer_context values come from LLM output, those values reach the child as `steer.<key>` and are blocked from tool_command expansion by virtue of not being on the allowlist.
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

### Activity log integrity (#213)
- Threat model: tool subprocesses run with `cmd.Dir = workDir`. Pre-#213 the audit log lived at `<workDir>/.tracker/runs/<runID>/activity.jsonl` mode `0o644` — an LLM-controlled subprocess could append fake `decision_edge` lines, truncate to suppress `tool_output_truncated`, or forge a `pipeline_completed status=success`. Same threat shape as the tool_command safe-key allowlist above, applied to the log file instead of the command string.
- Where the log lives now: live writes go to `$XDG_STATE_HOME/tracker/runs/<runID>/activity.jsonl` (default `$HOME/.local/state/tracker/runs/<runID>/`, mode `0o600`). Override via `TRACKER_AUDIT_DIR`. Windows fallback `%LOCALAPPDATA%\tracker\runs\<runID>\`. The path is computed by `pipeline.SecureActivityLogPath(runID)`.
- Snapshot for bundles: on `JSONLEventHandler.Close()` a sentinel-stripped copy is written to the legacy `<workDir>/.tracker/runs/<runID>/activity.jsonl` (mode `0o644`, `O_NOFOLLOW` on unix). This keeps `--export-bundle` / git_artifacts working — they pick up the snapshot. The snapshot is best-effort; the secure file remains authoritative.
- Sentinel scheme: every line the runtime writes is prefixed with `\x1f\x1e` (`pipeline.ActivityLogSentinel`). Diagnose/Audit prefer the secure path via `tracker.ResolveActivityLogPath` and validate the sentinel; lines lacking it count as `runtimeAnomalies.InjectedLines` and fire `SuggestionAuditLogInjection`.
- What the scheme defends: casual injection — LLM-driven shell redirection (`>>`, `> file`), `tee -a`, `find . -name activity.jsonl -delete`, anything using relative paths from the project root. Relocation removes the relative-path reach entirely; sentinel makes any reach-via-absolute-path attempt detectable.
- What the scheme does NOT defend: a motivated forger who reads tracker's source (open source) and emits the sentinel bytes themselves. The sentinel is detection, not authentication. Option C (per-line HMAC) is the next tier and is explicitly out of scope per the issue — the key-management cost is too high for the marginal gain. Document this honestly in any operator-facing copy: do not claim the runtime "prevents" forgery.
- Migration: pre-#213 runs and runs whose secure file was lost (archive/move) have no secure file. `ResolveActivityLogPath` falls back to `<runDir>/activity.jsonl`, parses it without sentinel validation, and Diagnose surfaces no false-positive injection signal (absence of sentinel isn't a signal on the legacy path).
- Env var contract: `TRACKER_AUDIT_DIR` and `XDG_STATE_HOME` MUST be absolute paths. A relative value would re-anchor the secure log to the process CWD — defeating the relocation defense — so `pipeline.absEnv` silently ignores non-absolute values and falls through to the next resolver candidate.
- RunID contract: `pipeline.validateRunID` rejects runIDs containing path separators, `..`, or `.`. Tampered checkpoints supplying `"run_id": "../etc/something"` would otherwise escape the secure base. Fresh runs get 6-byte hex from `generateRunID` (safe by construction); the validation guards the resume path where `runID` comes from `<workDir>/.tracker/runs/<runID>/checkpoint.json` (attacker-reachable).
- Symlink defenses: secure file open uses `O_NOFOLLOW` (unix builds) so the final component can't be a pre-planted symlink. The Close-time snapshot Lstats `<artifactDir>` and `<artifactDir>/<runID>` before MkdirAll/open and refuses if either is a symlink — guards the intermediate-component attack where a tool subprocess swaps the run dir for a symlink during the run. Residual risk: TOCTOU between Lstat and MkdirAll (microsecond window) and symlinks at deeper intermediates inside attacker-writable territory; both are acceptable because the secure file remains authoritative and the snapshot is best-effort.

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
- Error classification: `classifyError` in `backend_claudecode.go` maps Claude CLI stderr/exit codes to `OutcomeSuccess` / `OutcomeFail` / `OutcomeRetry` — rate-limit and network errors classify as `retry`; auth, credit-balance, budget-limit, and SIGKILL (exit 137) classify as `fail`; credit-balance errors also log actionable guidance to unset `ANTHROPIC_API_KEY` for Max subscription auth. "Escalation" is a routing convention built on top of `OutcomeFail` edges, not a distinct outcome status — see `docs/architecture/engine.md#escalate`.
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

### `tracker __jail-exec` internal subcommand (#272)

When `writable_paths` is set on an agent node, tracker re-execs itself via
`/proc/self/exe __jail-exec -- <anchor> <globs> -- sh -c <cmd>` to apply
Linux Landlock to Bash subprocesses. The dispatch happens in `cmd/tracker/main.go`
BEFORE flag parsing. Operators MUST NOT invoke `__jail-exec` directly — the
`__` prefix signals "internal." The helper applies Landlock ABI v3, then
`syscall.Exec`s into `sh -c <cmd>`, preserving Landlock through exec so the
agent's bash and all descendants (cargo, rustc, child shells) are bounded by
the writable_paths globs at the kernel layer.

Two-tier semantic: in-process tools (`Write`, `Edit`, `ApplyPatch`) enforce
the exact globs via `openat2(RESOLVE_BENEATH | RESOLVE_NO_SYMLINKS | RESOLVE_NO_MAGICLINKS)`
against a session-root fd — symlink chains rejected at the kernel syscall, no
TOCTOU window. Bash subprocess is bounded at the directory ancestor of each
glob's static prefix because Landlock is path-prefix on directories, not
glob-aware.

Refuse-to-start: invalid `working_dir` (escapes process cwd; the check is
symlink-aware and walks the deepest existing ancestor when the leaf does not
yet exist), malformed globs (absolute / `~` / parent-escape / **any brace
usage** since the matcher does not expand `{a,b}` / unsupported doublestar
shapes / malformed character classes), backend ∈ {claude-code, acp, unknown}
(out-of-process; tracker cannot sandbox — gated both inside
`NativeBackend.Run` and at the `CodergenHandler.Execute` dispatcher),
Landlock unavailable (kernel < 6.7 or non-Linux). Configure-jail gate in
`pipeline/handlers/codergen_jail.go`.

Residual escape classes (not bounded): network egress (cargo crate fetches,
curl), reads / exfil-by-read, content within an allowed path. Narrow globs are
the strongest posture. See
`docs/superpowers/specs/2026-06-01-issue-272-writable-paths-enforcement-design.md`
for the full design.
