# CLAUDE.md — Tracker Project Instructions

## Project Overview

Tracker is a pipeline orchestration engine for multi-agent LLM workflows.
Pipelines are defined in `.dip` files (Dippin language) and executed with
parallel agents via a TUI dashboard. Built by 2389.ai.

## Where to start

- System orientation, layer diagram, end-to-end sequence: [`ARCHITECTURE.md`](ARCHITECTURE.md)
- Subsystem deep dives: [`docs/architecture/README.md`](docs/architecture/README.md)
- Run loop, edge selection, escalation: `docs/architecture/engine.md`
- Handler catalog: `docs/architecture/handlers.md`
- `.dip` → Graph adapter: `docs/architecture/adapter.md`
- Context scoping and flow: `docs/architecture/context-flow.md`
- Backends (native / claude-code / acp): `docs/architecture/backends.md`

## Code map

- **Library API**: top-level `tracker.go`, `tracker_*.go`. Exported entry points include `Run`, `Diagnose` / `DiagnoseMostRecent`, `Doctor`, `Audit`, `ListRuns`, `Simulate`, `ExportBundle`, `ResolveRunDir`, `ResolveBudgetLimits`, `ResolveProviderBaseURL`, `ResolveActivityLogPath`, `NewNDJSONWriter`. Prefer these over shelling out to the CLI from embedded integrations.
- **CLI**: `cmd/tracker/` — `main.go` (entry, dispatches `__jail-exec` before flag parsing), `flags.go` (every flag), `run.go`, `resolve.go` (bare-name resolution), `doctor.go`, `diagnose.go`, `summary.go`.
- **Engine**: `pipeline/` — `engine.go`, `engine_edges.go` (edge selection), `graph.go` (shape → handler map), `handler.go` (Handler/Outcome contract), `node_config.go` (typed accessors), `dippin_adapter.go` (IR → Graph), `context.go`, `condition.go`, `checkpoint.go`, `budget.go`, `audit_path.go`.
- **Handlers**: `pipeline/handlers/` — one file per handler. **`registry.go` `NewDefaultRegistry` is the wire-up point** — add new handlers there and map the shape in `pipeline/graph.go`.
- **Agent**: `agent/` — `Session` turn loop, tool registry, context compaction. `agent/exec/` holds the Landlock jail (Linux).
- **LLM**: `llm/` — `Client`, middleware, token tracker; per-provider adapters under `llm/anthropic/`, `llm/openai/`, `llm/google/`, `llm/openaicompat/`.
- **TUI**: `tui/` — Bubble Tea dashboard, modal/review/interview content types.
- **Built-in workflows**: `examples/` (embedded into the binary).
- **Other binaries**: `cmd/tracker-conformance/` (release-shipped conformance harness), `cmd/tracker-swebench/` (SWE-bench runner with `agent-runner` subcommand).

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

### NEVER `go install` dippin-lang
- `go install github.com/2389-research/dippin-lang/cmd/dippin@...` overwrites the `dippin` binary on `PATH` (in `$GOBIN` or `$GOPATH/bin`) with a module-cache build, displacing the user's locally-built `dippin` from their development checkout. They want to keep using their local build.
- Update the Go module dependency with `go get github.com/2389-research/dippin-lang@vX.Y.Z` only
- If `dippin` is not on `PATH` for a verification step, ask the user — don't `go install`

### Tool node safety — LLM output as shell input
- NEVER `eval` content extracted from LLM-written files (arbitrary command execution).
- `tool_command` variable expansion has a safe-key allowlist for `ctx.*`: only `outcome`, `preferred_label`, `human_response`, `interview_answers` interpolate. All `graph.*` and `params.*` (author-controlled) are allowed. All LLM-origin `ctx.*` keys (`last_response`, `tool_stdout`, `tool_marker`, `tool_route`, `response.*`) are blocked. `manager_loop` steer values are namespaced under `steer.*` (#177) and never on the allowlist.
- Safe pattern: write LLM output to a file in a prior tool node, then read it (`cat .ai/output.json | jq ...`).
- Tool stdout/stderr capped at 64KB per stream (per-node `output_limit`, hard ceiling 10MB via `--max-output-limit`).
- Built-in denylist blocks `eval`, pipe-to-shell, `curl|sh`. Override with `--bypass-denylist` (avoid). Optional `--tool-allowlist` / `tool_commands_allow` narrows further but cannot override the denylist.
- Sensitive env vars (`*_API_KEY`, `*_SECRET`, `*_TOKEN`, `*_PASSWORD`) are stripped from tool subprocesses; override with `TRACKER_PASS_ENV=1`.
- Strip comments (`grep -v '^#'`) and blank lines from LLM-generated lists. Use flexible regex for markdown headers (LLMs vary `##` / `###` / colon use). Add empty-file guards after extracting from LLM-written files — fail loudly.

### Activity log integrity (#213)
- Live audit log path is computed by `pipeline.SecureActivityLogPath(runID)`; reads go through `tracker.ResolveActivityLogPath`. Resolution order (each step yields `<base>/<runID>/activity.jsonl`): `$TRACKER_AUDIT_DIR/<runID>/` → `$XDG_STATE_HOME/tracker/runs/<runID>/` → on Windows, `%LOCALAPPDATA%\tracker\runs\<runID>\` → `$HOME/.local/state/tracker/runs/<runID>/` → `os.TempDir()/tracker-audit/<runID>/` (last-resort when `$HOME` is unresolvable). File mode `0o600`, opened `O_NOFOLLOW`.
- Every runtime-written line is prefixed with sentinel `\x1f\x1e` (`pipeline.ActivityLogSentinel`). Lines lacking it count as `runtimeAnomalies.InjectedLines` and fire `SuggestionAuditLogInjection`. The sentinel is detection, not authentication.
- `TRACKER_AUDIT_DIR` and `XDG_STATE_HOME` MUST be absolute paths — relative values are silently ignored (`pipeline.absEnv`) so CWD can't re-anchor the secure log.
- RunIDs are validated by `pipeline.validateRunID` (rejects separators, `..`, `.`) so a tampered checkpoint can't escape the base.
- A sentinel-stripped snapshot is written to the legacy `<workDir>/.tracker/runs/<runID>/activity.jsonl` on close (best-effort, for `--export-bundle` / git_artifacts).
- Full threat model and residual risks: see [`docs/architecture/`](docs/architecture/) and the section in Architecture Gotchas below.

### Dippin-lang compatibility
- The dippin IR uses `ctx.` namespace prefix in conditions (`ctx.outcome = success`)
- Tracker's context stores bare keys (`outcome`). The condition evaluator strips `ctx.`, `context.`, and handles `internal.*`
- The adapter must synthesize implicit edges from `ParallelConfig.Targets` and `FanInConfig.Sources`
- `AgentConfig.ResponseFormat` and `AgentConfig.ResponseSchema` map to node attrs `response_format` and `response_schema`
- `AgentConfig.Params` is a generic pass-through map — typed fields take precedence over Params keys
- The adapter maps IR field names to tracker convention: `model` → `llm_model`, `provider` → `llm_provider`
- Provider name is `gemini` not `google`
- Provider base URL resolution goes through `tracker.ResolveProviderBaseURL(provider)`. Precedence: `<PROVIDER>_BASE_URL` env var wins; otherwise `TRACKER_GATEWAY_URL` is used with a per-kind suffix; otherwise empty (SDK default).
- Gateway kind dispatch via `TRACKER_GATEWAY_KIND` / `--gateway-kind` (default `cf-aig`). For `cf-aig` the suffixes are `/anthropic`, `/openai`, `/google-ai-studio`, `/compat`. For `bedrock` Anthropic gets `""` (SDK appends `/v1/messages`), OpenAI/Gemini get `/v1`, and `openai-compat` refuses to route. The strict path (`ResolveProviderBaseURLStrict`, used by adapter constructors) surfaces refusals — both unknown kinds and refused (kind, provider) pairs — as `ErrGatewayRouteRefused`; the lax `ResolveProviderBaseURL` returns `""` (back-compat — SDK default endpoint).
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
- **Merging a `release: vX.Y.Z` PR is NOT the release.** The release PR only ships CHANGELOG/README doc updates. The actual release is `git tag -a vX.Y.Z <merge-commit> -m "release: vX.Y.Z"` + `git push origin vX.Y.Z`. The tag push triggers `.github/workflows/release.yml` → GoReleaser (builds darwin/linux amd64/arm64 binaries for `tracker` and `tracker-conformance`, publishes the GitHub release). A release isn't done until `gh release view vX.Y.Z` shows the published entry with assets. v0.19.0 and v0.20.0 were back-tagged retroactively because of this; don't repeat.

### Version bumps
- Update go.mod module version on MAJOR bumps
- Keep dippin-lang dependency pinned to a tagged version, not a commit hash
- After updating dippin-lang, run `dippin doctor` on all example pipelines and verify scores

## Development Workflow

### Before committing
- `go build ./...` — must pass
- `go test ./... -short` — all packages must pass
- `dippin doctor examples/ask_and_execute.dip examples/build_product.dip examples/build_product_with_superspec.dip` — must be A grade
- If `dippin` is not on `PATH`, ask the user — they install it from a local dippin-lang checkout. Do not `go install` it (see Critical Rules).

### Before releasing
- Run `dippin doctor` on ALL example .dip files — aim for A grade across the board
- Run `dippin simulate -all-paths` on the three core pipelines
- Update CHANGELOG.md and README.md
- After the `release: vX.Y.Z` PR merges, tag the merge commit and push:
  - `git tag -a vX.Y.Z <merge-commit-sha> -m "release: vX.Y.Z"`
  - `git push origin vX.Y.Z`
  - The tag push triggers `.github/workflows/release.yml` → GoReleaser, which builds binaries and creates the GitHub release entry. Merging the release PR alone does not create a release (see Versioning and Releases § Releases).
- Refresh the website (`gh-pages` branch — see Project Infrastructure § Website).

### dippin-lang updates
- DO run `go get github.com/2389-research/dippin-lang@vX.Y.Z` to update the Go module dependency. The "never `go install`" rule lives in Critical Rules.
- After updating, verify: `go build ./... && go test ./... -short`

## Architecture Gotchas

### The adapter is the bridge
`pipeline/dippin_adapter.go` converts dippin IR to tracker's Graph model. Every naming mismatch between dippin and tracker conventions lives here — new IR fields land here first.

### Typed node-config accessors
Reads go through typed accessors on `*pipeline.Node`: `AgentConfig(graphAttrs)`, `ToolConfig()`, `HumanConfig()`, `ParallelConfig()`, `RetryConfig(graphAttrs)`. Defined in `pipeline/node_config.go`. When adding a new node attribute, extend the appropriate `NodeConfig` struct and its accessor — don't add fresh `node.Attrs[...]` reads. A handful of strict-parse helpers retain raw reads (see inline comments).

### Structured output (`response_format`)
`response_format: json_object` on agent nodes forces JSON output at the LLM API level. Path: `.dip` → `AgentConfig.ResponseFormat` → `node.Attrs["response_format"]` → `codergen.buildConfig` copies it onto `SessionConfig.ResponseFormat` → `session.buildResponseFormat()` → `llm.Request.ResponseFormat` → provider translator (Anthropic: system instruction via `appendResponseFormatInstruction`, OpenAI: native `json_object`, Gemini: `responseMimeType`). Use on any agent that must produce structured JSON.

### The engine doesn't know about parallel/fan-in
Engine treats every node uniformly (execute handler, select edge, advance). The parallel handler dispatches branches internally and hints the next node via `suggested_next_nodes` — no special-case engine code.

### Git artifacts and bundle export
`WithGitArtifacts(true)` makes the artifact run dir a git repo and commits after every terminal node. `ExportBundle(runDir, outPath)` (`tracker_bundle.go`) wraps `git bundle create --all` for a portable history. `Result.ArtifactRunDir` is the canonical run-dir locator. `--export-bundle` calls it post-run; failures warn, don't fail. Canonical hand-off pattern for a remote worker → `git clone <bundle>` on the user's machine.

### Checkpoint resume is fragile
Checkpoints store completed nodes, per-node edge selections, and context snapshots. **The restart counter is global** — a fix loop on milestone 1 consumes restart budget milestone 10 will need. Use per-milestone circuit breakers (e.g., a `fix_attempts` file on disk as in `build_product.dip`).

### Token usage flow
`llm.Usage` (per API call) → `agent.SessionResult.Usage` (per session) → `pipeline.SessionStats` (per trace entry, built in `pipeline/handlers/transcript.go`) → `EngineResult.Usage` (aggregated by `Trace.AggregateUsage`). The parallel handler folds branch stats into its own outcome. `cmd/tracker/summary.go` uses `llm.TokenTracker` (middleware-level, per-provider) and `EngineResult.Usage` (trace-level) as independent sources.

### Pipeline context isolation
Per-node scoping (`node.<nodeID>.<key>`) is a stable feature — after each node finishes its dirty writes are aliased under `node.<nodeID>.<key>` so later nodes can reference a specific upstream node's output. Declarative `writes:` extracts a typed JSON payload into first-class keys. Full model: [`docs/architecture/context-flow.md`](docs/architecture/context-flow.md).

### Cost governance
- `UsageSummary.ProviderTotals` carries tokens+cost; `tracker.Result.Cost` exposes dollar cost via `llm.TokenTracker.CostByProvider`.
- `pipeline.BudgetGuard` is evaluated between nodes after every `emitCostUpdate`. Breach → `OutcomeBudgetExceeded`, `EngineResult.BudgetLimitsHit`, `EventBudgetExceeded`.
- Configure via `Config.Budget`, `--max-tokens` / `--max-cost` (cents) / `--max-wall-time` CLI flags, or a `defaults:` block in the `.dip` workflow.
- Precedence: CLI flags / `Config.Budget` win; `defaults:` is the fallback. Folded in by `tracker.ResolveBudgetLimits`.

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

### Tool output capture
- `ctx.tool_stdout` / `ctx.tool_stderr` carry the **tail** (not head) of the stream up to the per-stream cap. Routing markers printed at end-of-output via `printf` survive truncation by construction.
- The captured value contains **only** the tail payload — no in-band truncation-marker suffix is appended (an earlier suffix could clobber routing tokens).
- Overflow emits `EventToolOutputTruncated{stream, limit, captured_bytes, dropped_bytes}`; `tracker diagnose` surfaces it as a suggestion.
- When a node has conditional edges that all evaluate false and routing falls through to an unconditional edge, the engine emits `EventConditionalFallthrough` with the missed conditions. Diagnose correlates this with truncation events to surface "your routing marker may have been dropped."

### Activity log threat model (full)
The Critical Rules entry above covers the operational contract. Background and residual risks:
- Pre-#213 the log lived at `<workDir>/.tracker/runs/<runID>/activity.jsonl` mode `0o644`. A tool subprocess running with `cmd.Dir = workDir` could append fake decision edges, truncate to suppress `tool_output_truncated`, or forge `pipeline_completed status=success` via relative-path shell redirection. Relocation to `$XDG_STATE_HOME/.../<runID>/` removes that reach.
- The sentinel detects casual injection (shell redirection, `tee -a`, `find ... -delete`). It does **not** detect a motivated forger who reads tracker's source and emits the sentinel bytes themselves. Per-line HMAC was considered (Option C) and dropped — key-management cost beats marginal gain. Operator-facing copy must not claim the runtime "prevents" forgery.
- Snapshot guards: the Close-time copy `Lstat`s `<artifactDir>` and `<artifactDir>/<runID>` before MkdirAll/open and refuses if either is a symlink. Residual TOCTOU between Lstat and MkdirAll (microsecond window) is accepted because the secure file remains authoritative.
- Legacy runs without a secure file (pre-#213 or archive-moved): `ResolveActivityLogPath` falls back to `<runDir>/activity.jsonl` without sentinel validation — absence of sentinel on the legacy path is not an injection signal.

### Agent backends

Three backends, all implementing `AgentBackend` (`pipeline/backend.go`). `CodergenHandler` selects via `selectBackend()`.

- **`native`** (default): wraps `agent.Session` — turn loop, tool registry, context compaction.
- **`claude-code`**: spawns `claude` as a subprocess, parses NDJSON. **API keys are stripped** from the subprocess env so the claude CLI uses subscription auth (Max/Pro OAuth) — override with `TRACKER_PASS_API_KEYS=1`. With `--backend claude-code` and no per-node override, non-Anthropic model names are stripped so the CLI uses its default.
- **`acp`**: ACP-protocol client (`backend_acp_client.go`) for headless external agents. The `writable_paths` jail refuses `acp` (out-of-process; tracker cannot apply Landlock to it).

Selection: per-node `backend:` attr wins over the global `--backend` flag (a node with `backend: native` stays native under `--backend claude-code`). The engine and TUI see the same `agent.Event` stream regardless of backend.

Error classification (`classifyError` in `backend_claudecode.go`): rate-limit and network → `OutcomeRetry`; auth, credit-balance, budget-limit, SIGKILL (exit 137) → `OutcomeFail`. Credit-balance also logs actionable guidance to unset `ANTHROPIC_API_KEY` for Max subscription auth. "Escalation" is a routing convention on top of `OutcomeFail` edges, not a distinct status — see `docs/architecture/engine.md#escalate`.

### Strict failure edges
- When a node's outcome is "fail" and all outgoing edges are unconditional, the pipeline stops
- This prevents tool nodes (Setup, Build) from silently continuing after failure
- Pipelines that intentionally handle failure must use `when ctx.outcome = fail` edges
- Nodes with ANY conditional edges are assumed to have intentional routing

### `tracker __jail-exec` internal subcommand (#272)
`writable_paths` on an agent node makes tracker re-exec itself via `/proc/self/exe __jail-exec -- <anchor> <globs> -- sh -c <cmd>`, applying Linux Landlock ABI v3 before `syscall.Exec` into `sh -c`. Dispatched in `cmd/tracker/main.go` **before** flag parsing. Operators MUST NOT invoke `__jail-exec` directly — the `__` prefix is the "internal" signal.

Two-tier enforcement: in-process tools (`Write`, `Edit`, `ApplyPatch`) hit `openat2(RESOLVE_BENEATH | RESOLVE_NO_SYMLINKS | RESOLVE_NO_MAGICLINKS)` against a session-root fd (no TOCTOU). Bash subprocess is bounded at the directory-ancestor of each glob's static prefix (Landlock is path-prefix on directories, not glob-aware).

Refuse-to-start gate in `pipeline/handlers/codergen_jail.go`: invalid `working_dir`, malformed globs (absolute / `~` / parent-escape / **any brace usage** / unsupported doublestar / malformed character classes), backend ∈ {claude-code, acp, unknown}, Landlock unavailable (kernel < 6.7 or non-Linux). Residual escape classes (not bounded): network egress, reads/exfil-by-read, anything inside an allowed path. Narrow globs are the strongest posture. Full design: [`docs/superpowers/specs/2026-06-01-issue-272-writable-paths-enforcement-design.md`](docs/superpowers/specs/2026-06-01-issue-272-writable-paths-enforcement-design.md).

## Project Infrastructure

### Website (GitHub Pages)
- Hosted at <https://2389-research.github.io/tracker/>. Source: `site/` directory on `main`, built with Hugo extended. The `gh-pages` branch is a build artifact — never edit by hand.
- Deploy: `.github/workflows/docs.yml` runs on every push to `main` that touches `site/**`, publishing `site/public/` to `gh-pages` via `peaceiris/actions-gh-pages` with `force_orphan: true`.
- Layout: hand-written HTML in `site/content/*.html`, shared layouts in `site/layouts/`, static assets in `site/static/`, nav data in `site/data/nav.yaml`.
- Local preview: `cd site && hugo server` (port 1313). `baseURL` in `hugo.toml` is the full production URL (`https://2389-research.github.io/tracker/`); pages are served under the `/tracker/` path. `uglyURLs = true` keeps URLs at `/tracker/<name>.html` (matching the pre-Hugo URL shape).
- Per-page front matter controls a11y metadata (`title`, `description`, `og_*`, optional `mermaid: true`, `jsonld:` block inlined as JSON-LD). Use `TechArticle` for inner pages, `SoftwareApplication` for home, `DefinedTermSet` for glossary.
- Adding a page: drop `site/content/<name>.html` with the front matter block (copy an existing page), add to `site/data/nav.yaml` if it should appear in the nav, push to `main`.
