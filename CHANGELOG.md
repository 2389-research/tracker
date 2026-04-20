# Changelog

All notable changes to tracker will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- **Library API hardening for v1.0** (#102, #103, #104, #106, #109):
  - Typed enum-like strings for `CheckStatus` and `SuggestionKind` so consumers can switch-exhaust. Existing constants (`SuggestionRetryPattern`, etc.) retain their underlying string values.
  - `tracker.WithVersionInfo(version, commit)` functional option replaces the CLI-only `DoctorConfig.TrackerVersion` / `TrackerCommit` fields.
  - `DiagnoseConfig.LogWriter` / `AuditConfig.LogWriter` — optional `io.Writer` for non-fatal parse warnings. Nil is treated as `io.Discard` so library callers no longer see stray warnings on `os.Stderr`. The `tracker` CLI sets this to `io.Discard` for user-facing commands. `Doctor` has no warnings to suppress so it deliberately does not carry a `LogWriter` field.
  - `Doctor`, `Diagnose`, `DiagnoseMostRecent`, `Audit`, `Simulate` now accept `context.Context`, honored by provider probes and binary version lookups. `getBinaryVersion` now uses `exec.CommandContext` with a 5-second timeout, matching `getDippinVersion`.
  - Provider probe error bodies are now sanitized (API keys and bearer tokens stripped) before they land in `CheckDetail.Message`.
  - `NDJSON` handler closures (pipeline, agent, LLM trace) now `recover()` from panics in the underlying writer so a misbehaving sink cannot crash the caller goroutine. Panic suppression is per-`NDJSONWriter` instance (not package-level), so one misbehaving sink cannot silence unrelated writers in the same process.
  - `Diagnose` now streams `activity.jsonl` with `bufio.Scanner` instead of `os.ReadFile` → `strings.Split`, matching `LoadActivityLog` and avoiding a memory spike on large runs. Scanner errors (1 MB line-length overflow, I/O) and `ctx.Err()` now propagate out of `Diagnose` as a real error — partial reports are never returned as success, so automation with deadlines can distinguish complete from truncated analysis.
- **Workflow params via `${params.*}` with CLI/library overrides** (closes #81): top-level Dippin `vars` now map to graph attrs under `params.<key>`, making them available in agent prompts, tool commands, and edge conditions through `${params.key}` interpolation. Added repeatable `--param key=value` on the CLI plus `tracker.Config.Params` for library callers; overrides hard-fail on unknown keys at startup and run summaries print effective overridden params. New lint rules DIP120 (undeclared `${params.*}` reference) and DIP121 (declared but unused var).
- **Per-human-gate timeout / timeout_action in `.dip`** (closes #112): the dippin-lang v0.21.0 IR exposes `HumanConfig.Timeout` and `HumanConfig.TimeoutAction`; the adapter copies them into `node.Attrs["timeout"]` / `node.Attrs["timeout_action"]` where `pipeline/handlers/human.go` already consumed them. The `examples/human_gate_test_suite.dip` Makefile lint skip is removed.
- **Workflow-level budget ceilings from `.dip`** (closes #67): dippin-lang v0.21.0 adds `WorkflowDefaults.MaxTotalTokens`, `WorkflowDefaults.MaxCostCents`, and `WorkflowDefaults.MaxWallTime`. The adapter now maps them to `graph.Attrs["max_total_tokens"]` / `["max_cost_cents"]` / `["max_wall_time"]`, and `tracker.resolveBudgetLimits` uses them as a fallback when `Config.Budget` and the matching `--max-*` CLI flags are zero. Explicit config values still win.

### Changed

- **dippin-lang dependency bumped from `v0.20.0` → `v0.21.0`.** Picks up three upstream fixes tracked as dippin-lang#18/#20/#21 (PRs #22/#23) plus release issue #25. `PinnedDippinVersion` constant updated to match. Closes tracker#75 transitively — dippin lint now recognizes `${ctx.node.<id>.*}` scoped reads as valid without tracker-side changes.
- **BREAKING** (library):
  - `tracker.Doctor(cfg)` → `tracker.Doctor(ctx, cfg, opts...)`.
  - `tracker.Diagnose(runDir)` → `tracker.Diagnose(ctx, runDir, opts...)`.
  - `tracker.DiagnoseMostRecent(workdir)` → `tracker.DiagnoseMostRecent(ctx, workdir, opts...)`.
  - `tracker.Audit(runDir)` → `tracker.Audit(ctx, runDir)`. (No config struct — Audit emits no suppressible warnings. Use `ListRuns` + `AuditConfig{LogWriter}` for bulk enumeration.)
  - `tracker.Simulate(source)` → `tracker.Simulate(ctx, source)`.
  - `tracker.ListRuns(workdir)` now accepts optional `...AuditConfig`.
  - `tracker.NDJSONEvent` → `tracker.StreamEvent`. Wire-format JSON tags unchanged.
  - `NDJSONWriter.Write` now returns `error` so callers can detect a broken stream. First failure is still logged to `os.Stderr` once (unchanged behavior); subsequent failures are surfaced via the return value.
  - `DoctorConfig.TrackerVersion` and `DoctorConfig.TrackerCommit` removed — use `tracker.WithVersionInfo(version, commit)` instead.
  - `CheckResult.Status` and `CheckDetail.Status` are now typed as `tracker.CheckStatus` (underlying string). Untyped string literal comparisons (`status == "ok"`) keep working.
  - `Suggestion.Kind` is now typed as `tracker.SuggestionKind` (underlying string).
- `tracker diagnose` suggestion order is now deterministic (alphabetical by node ID). Previously suggestions printed in Go map-iteration order, which varied between runs.

### Fixed

- **OpenAI Responses API: `function_call_output` and `function_call` items now always serialize required fields** (closes #114). Previously the shared `openaiInput` struct used `omitempty` on every field, so a tool returning an empty-string result produced `{"type":"function_call_output","call_id":"..."}` with no `output` field, and a no-argument tool call produced `function_call` with no `arguments`. OpenAI's endpoint tolerated this, but OpenRouter's strict Zod validator rejected the requests with `invalid_prompt` / `invalid_union` errors, symptomatic on GLM, Qwen, and Kimi via OpenRouter. Fixed by replacing the `omitempty`-tagged single struct with a `MarshalJSON` method that emits only fields valid per item type, with required fields always present. Reported by @Nopik.

## [0.18.0] - 2026-04-17

### Added

- **CLI↔library feature parity — Phase 1 (NDJSON) + Phase 2** (#76, PR #101). Four CLI commands (`diagnose`, `audit`, `doctor`, `simulate`) and the NDJSON event writer are now public library APIs. Library consumers can reuse the CLI's behavior without shelling to a binary and parsing printed output.
  - `tracker.NewNDJSONWriter(io.Writer)` — public NDJSON event writer producing the same wire format as `tracker --json`. Factory methods `PipelineHandler`, `AgentHandler`, `TraceObserver` return handlers that plug into `Config.EventHandler`, `Config.AgentEvents`, and the LLM trace hook. Closes Phase 1.
  - `tracker.Diagnose(runDir)` / `tracker.DiagnoseMostRecent(workDir)` — structured `*DiagnoseReport` with node failures, budget halt, and typed suggestions (`Kind: "retry_pattern" | "escalate_limit" | "no_output" | "shell_command" | "go_test" | "suspicious_timing" | "budget"`).
  - `tracker.Audit(runDir)` — structured `*AuditReport` with timeline, retries, errors, and recommendations.
  - `tracker.ListRuns(workDir)` — sorted `[]RunSummary` for enumerating past runs (newest first).
  - `tracker.Doctor(cfg)` — structured `*DoctorReport` for preflight health checks. `ProbeProviders` defaults to false; set true to make real API calls for auth verification. `CheckDetail.Status` has four values: `"ok"`, `"warn"`, `"error"`, and `"hint"` (informational sub-items such as optional providers not configured).
  - `tracker.Simulate(source)` — structured `*SimulateReport` with nodes, edges, execution plan, graph attributes, and unreachable-node list.
  - `tracker.ResolveRunDir(workDir, runID)` / `tracker.MostRecentRunID(workDir)` — exposed run-directory resolution helpers.
  - `tracker.ActivityEntry` / `tracker.LoadActivityLog(runDir)` / `tracker.ParseActivityLine(line)` / `tracker.SortActivityByTime(entries)` — shared activity.jsonl parsing used by CLI and library.

- **SWE-bench harness (`cmd/tracker-swebench`)**: a new orchestrator binary that evaluates tracker's agent against the SWE-bench dataset. Includes a Dockerfile and build script for the base image, container lifecycle management with SIGTERM handling and orphan cleanup, dataset JSONL parsing, results writer with resumability, container resource limits (CPU/memory) and `--platform` pinning, secure `--env-file` for API keys (replacing `-e` flags), instance-ID validation + scoped container names, integration test for the dataset-to-results pipeline, and an in-container `agent-runner` binary that captures all changes via `git diff` (including new files).

- **`WithExtraHeaders` option for Anthropic and OpenAI adapters**: injects custom HTTP headers (e.g., `cf-aig-token`) for gateway auth. Used by the swebench harness to forward `CF_AIG_TOKEN` from the host through the container to the agent-runner.

### Fixed

- `classifyStatus` now correctly returns `"fail"` for budget-halted runs (runs with a `budget_exceeded` activity event were previously mis-classified as `"success"`).
- `NDJSONWriter.AgentHandler` now preserves the original `agent.Event.Timestamp` instead of re-stamping with `time.Now()`, preventing event reordering in the NDJSON stream.
- `simBFSNodeOrder` now sorts orphan nodes by ID before appending, making `SimulateReport.Nodes` ordering deterministic.
- `ResolveRunDir` now always returns an absolute path via `filepath.Abs`, matching its documented contract.
- `MostRecentRunID` no longer writes to `os.Stderr` from a library function; invalid checkpoint directories are silently skipped.
- `checkWorkdirLib` now correctly propagates `warn` details to the section-level `Status` field.
- `checkProvidersLib` now propagates individual provider `error` details to the section-level `Status` (was always `"ok"` when any provider was configured).
- `getDippinVersion` now uses `exec.CommandContext` with a 5-second timeout to prevent hangs on unresponsive dippin binaries.
- `PinnedDippinVersion` constant updated to `v0.20.0` to match the `go.mod` requirement.
- `checkPipelineFileLib` no longer warns when the pipeline file has a `.dot` extension (both `.dip` and `.dot` are valid input formats).
- Fixed ineffectual assignment to `suffix` in `cmd/tracker/doctor.go` `maybeFixGitignore`.
- `checkDiskSpaceLib` moved to platform-specific files (`tracker_doctor_unix.go` / `tracker_doctor_windows.go`) to avoid a Windows build failure from `syscall.Statfs`.
- `enrichFromEntryNF` and `updateFailureTimingNF` now guard against zero timestamps to prevent incorrect duration calculations in `DiagnoseReport`.
- `claude-sonnet-4-6` added to the LLM model catalog — the model was in `pricing.go` but missing from `catalog.go`, causing `GetModelInfo` to return nil and cost reporting to show `$0.00` for the swebench harness default model.
- ACP backend: `validatePathInWorkDir` now resolves symlinks on both `path` and `workDir`. On macOS `/var` is a symlink to `/private/var`, which was causing path validation to reject files inside `t.TempDir()`.

### Changed

- `cmd/tracker/diagnose.go`, `audit.go`, `doctor.go`, `simulate.go` are now thin printers over the new library APIs. CLI stdout and `--json` wire format are byte-identical. Closes Phase 2 of #76.
- `dippin-lang` dependency bumped from `v0.19.1` → `v0.20.0`. CI installs the matching CLI version (was stale at `v0.10.0`). `examples/human_gate_test_suite.dip` renamed `default_choice:` → `default:` to match the IR field. The file is temporarily skipped from `make lint` because v0.20.0's stricter parser rejects `timeout:` / `timeout_action:` on human nodes — tracker supports those attrs at the node level but dippin-lang's `HumanConfig` IR doesn't expose them yet. Tracked upstream at dippin-lang#18.

- **Structured reflection prompt on tool failure** (issue #93): when a tool call returns an error, the agent session now automatically injects a user-role reflection message before the next LLM turn. The prompt asks the model to identify what went wrong, what assumption was incorrect, and what minimal change will fix it — matching the pattern used by top SWE-bench agents (~10-15% recovery improvement). The feature is enabled by default (`ReflectOnError: true` in `DefaultConfig()`) and capped at three consecutive reflection turns to prevent infinite loops; the counter resets after any clean (no-error) turn. Pipeline authors can opt individual nodes out via `reflect_on_error: false` in their `.dip` file.
- **Verify-after-edit loop with auto-test** (closes #94): agent sessions can now automatically run tests after any turn that includes file-edit tool calls (`write`, `edit`, `apply_patch`, `notebook_edit`). Modelled on top SWE-bench agent behaviour (~15-20% improvement on benchmark), this transparent inner loop catches regressions before the LLM moves on.
  - `SessionConfig.VerifyAfterEdit bool` — opt-in flag (default: false).
  - `SessionConfig.VerifyCommand string` — explicit command; if empty, auto-detection runs: `go.mod` → `go test ./...`, `Cargo.toml` → `cargo test`, `package.json` → `npm test`, `Makefile` with `test:` target → `make test`, `pytest.ini`/`pyproject.toml[tool.pytest]` → `pytest`.
  - `SessionConfig.MaxVerifyRetries int` — max verify→repair cycles per edit turn (default: 2). After exhaustion the session proceeds without blocking.
  - Repair turns do NOT count toward `MaxTurns` — they are a transparent sub-loop.
  - Verification output is capped at 4 KB (tail kept — most relevant errors appear at the end).
  - Pipeline nodes wire the feature via `verify_after_edit`, `verify_command`, and `max_verify_retries` node attributes. `verify_command` can also be set at graph level as a default for all nodes.
  - New file `agent/verify.go`; 8 new tests in `agent/verify_test.go` and `agent/session_test.go`.

## [0.17.0] - 2026-04-16

### Added

- **Library API for workflow catalog and resolution** (partial #76 — Phase 1): library consumers can now list, open, and resolve built-in workflows without shelling out to the CLI.
  - `tracker.Workflows() []WorkflowInfo` returns every embedded workflow sorted by name.
  - `tracker.LookupWorkflow(name) (WorkflowInfo, bool)` looks up a single built-in by bare name.
  - `tracker.OpenWorkflow(name) ([]byte, WorkflowInfo, error)` returns the raw `.dip` source for a built-in.
  - `tracker.ResolveSource(name, workDir) (source, WorkflowInfo, error)` mirrors the CLI's bare-name resolution — filesystem first, then embedded — and returns the actual source bytes.
  - `tracker.ResolveCheckpoint(workDir, runID) (path, error)` resolves a run ID (or unique prefix) to its `checkpoint.json` path under `.tracker/runs/<runID>/`.
  - `tracker.Config.ResumeRunID` lets library consumers set `cfg.ResumeRunID = "abc123"` and `NewEngine` resolves it to `CheckpointDir` automatically — equivalent to the CLI's `-r/--resume` flag. An explicit `CheckpointDir` on the same config still wins as a manual override.
  - Embedded workflow files moved from `cmd/tracker/workflows/` to top-level `workflows/` so they can be shared by both the tracker library and the CLI binary. The CLI continues to embed them via thin wrappers over the library functions.

- **`ExportBundle(runDir, outPath string) error` library API and `--export-bundle` CLI flag** (issue #77, Layer 2): after a run completes, `ExportBundle` calls `git bundle create <outPath> --all` against the artifact run directory to produce a single portable `.bundle` file capturing every commit and tag (including `checkpoint/*` tags) produced by `WithGitArtifacts`. The bundle can be cloned on any machine with `git clone <bundle>` and inspected with `git log`. `Result.ArtifactRunDir` is now populated when `Config.ArtifactDir` is set, giving callers a direct path to the run directory. `Result.BundlePath` is available for callers that populate it after calling `ExportBundle`. The CLI `--export-bundle <path>` flag invokes `ExportBundle` as a post-run step; failures print a warning and do not affect the run's exit code. No new dependencies — implemented with `os/exec`.
- **`WithGitArtifacts(bool)` engine option** (issue #77, Layer 1): when enabled alongside `WithArtifactDir`, the artifact run directory is initialized as a (non-bare) git repository at run start and a commit is created after every terminal-outcome node — including success, fail, retry-exhausted, goal-gate fallback, and goal-gate unsatisfied paths. Commits carry a structured message (`node(<id>): <handler> outcome=<status>`) plus duration, edge, and token/cost metadata. `git log` gives a human-readable audit trail of execution order. Successful node advances also create lightweight checkpoint tags (`checkpoint/<runID>/<nodeID>`) enabling future replay support. On checkpoint resume, `Init()` detects an existing HEAD and skips the "run started" commit so replay doesn't add noise. All git operations are best-effort — git failures emit `EventWarning` events and do not crash the engine. Requires `git` in PATH; silently no-ops if `artifactDir` is unset or git is missing.

### Fixed

- **`tracker doctor` robustness fixes** (PR #83 review round 2):
  - Writability probes now use `os.CreateTemp` instead of fixed filenames (`.tracker_test_write`, `.tracker_write_probe`) — probes can't collide with real user files and are always cleaned up.
  - `checkProviders` no longer emits ✗ lines for unconfigured providers when at least one provider is already configured. Missing providers are shown as an informational hint line (e.g. "not configured: OpenAI, Gemini (optional)"). The ✗ lines appear only when zero providers are configured.
  - `checkGitignore` parses the `.gitignore` file line-by-line with exact (trimmed, slash-stripped) comparison instead of `strings.Contains` to prevent false positives (`runsheet` → `runs`, `my.tracker.bak` → `.tracker`).
  - Removed spurious `TRACKER_ARTIFACT_DIR` check — that env var is not wired into any CLI code path; checking it was misleading.
  - Disk space threshold confirmed at 10 GB (was already correct in code and CHANGELOG; the initial PR description saying 100 MB was wrong and has been corrected).
  - `resolveProviderBaseURL` in `doctor.go` was a duplicate of the canonical function. The duplicate is removed; `doctor.go` now calls the exported `tracker.ResolveProviderBaseURL`. The Gemini gateway suffix is corrected to `/google-ai-studio` (was `/gemini`).
  - `parseDoctorFlags` now validates `--backend` against the allowed set (`native`, `claude-code`, `acp`), consistent with `parseRunFlags`.

- **Per-node backend selection now overrides global `--backend` flag** (issue #70): A node with `backend: native` always uses the native LLM client even when `--backend claude-code` is set globally, enabling mixed-backend pipelines (e.g. some nodes on claude-code subscription, others on OpenAI native API). The `selectBackend` priority is now documented: per-node attr > global flag > default native. The registry also registers the CodergenHandler when per-node backend attrs are present in the graph, even if the global default is native and no `--backend` flag is passed. Error messages for missing native client when using `--backend claude-code` now include actionable guidance.
- **Start/exit node handler overwrite broadened fix**: `ensureStartExitNodes` previously checked only the `prompt` attribute to decide whether to preserve a node's handler, which meant tool nodes (`tool_command`) and human nodes (`mode`) designated as start/exit would still have their handlers silently overwritten. The helper now bases the decision on the resolved `Handler` field: any handler other than `codergen` is always preserved; only a bare `codergen` node with no `prompt` gets the passthrough. This fixes cases like `parallel` with `parallel_targets`, `parallel.fan_in` with `fan_in_sources`, `conditional`, `subgraph`, `stack.manager_loop`, and `wait.human` nodes used as start/exit. Closes #69.

### Added

- **Cloudflare AI Gateway support** (`TRACKER_GATEWAY_URL` env var, `--gateway-url` CLI flag): set one gateway root URL and tracker routes every provider through Cloudflare's AI Gateway — Anthropic, OpenAI, Gemini, OpenAI-compat — avoiding 429 rate limits and enabling gateway-side analytics, caching, and model routing. The new `ResolveProviderBaseURL(provider)` helper resolves the per-provider base URL with priority `<PROVIDER>_BASE_URL` > `TRACKER_GATEWAY_URL` + provider suffix > empty (SDK default), so per-provider env var overrides still work. Closes #64.
- **`tracker doctor` comprehensive preflight checks** (closes #61): `tracker doctor` now runs a structured series of checks with clear pass/warn/fail status, actionable fix messages, and documented exit codes (0=all pass, 1=any failure, 2=warnings only). New checks include:
  - Per-provider API key validation with format hints (key prefix, length)
  - `--probe` flag for live auth validation (makes a minimal 1-token API call per configured provider; offline-safe by default). The probe adapters honor `<PROVIDER>_BASE_URL` env vars (and `TRACKER_GATEWAY_URL`) so probing through a Cloudflare gateway works.
  - `dippin` binary version detection; `checkVersionCompat` compares the installed CLI's major.minor against the `go.mod`-pinned version (`v0.18.0`) and warns on divergence.
  - `.ai/` subdirectory writability check (note: `TRACKER_ARTIFACT_DIR` env var is not checked — it is not wired into the CLI and was removed to avoid misleading output)
  - Disk space warning (warn if < 10 GB free — threshold confirmed in code; the initial PR description that said 100 MB was incorrect)
  - `.gitignore` check for `.tracker/`, `runs/`, and `.ai/` entries (line-by-line exact match — no more false positives from substrings like `runsheet`)
  - Environment variable warnings for dangerous override keys (`TRACKER_PASS_ENV`, `TRACKER_PASS_API_KEYS`)
  - `--backend claude-code` awareness: hard-fails (exit 1) if the `claude` CLI is not found; without `--backend` the missing binary is a warning only.
  - `tracker doctor [pipeline.dip]`: optional positional arg validates the pipeline file with full lint (same as `tracker validate`)
  - Human-readable composite result lines per check group (providers, binaries, dirs)
  - `-w/--workdir` and `--backend` flags on `tracker doctor` so `tracker -w /path doctor` and `tracker --backend claude-code doctor` work as expected.
  - OpenAI-Compat provider now has a real `--probe` implementation (previously silently skipped).
  - Probe default models updated to current catalog entries: Anthropic → `claude-haiku-4-5`, Gemini → `gemini-2.0-flash`.
  - Exit code 2 is emitted when doctor finishes with warnings but no hard failures (was always 0). `DoctorWarningsError` sentinel returned from `runDoctorWithConfig`; `main.go` maps it to `os.Exit(2)`.

- **Webhook-based human gates for headless execution** (Closes #63, Closes #86): new `tracker.Config.WebhookGate` library field and matching CLI flags wire a `WebhookInterviewer` that POSTs gate prompts to a user-configured webhook URL and blocks on a callback. The interviewer starts a local HTTP server on a configurable address, tracks pending gates by UUID with per-gate shared-secret tokens (`X-Tracker-Gate-Token`) to authenticate inbound callbacks (mismatches return 401), supports a per-gate timeout with configurable action (`fail` / `success`), optional `Authorization` header for outbound requests, server-side HTTP timeouts (`ReadHeaderTimeout` 10s / `ReadTimeout` 30s / `WriteTimeout` 30s / `IdleTimeout` 60s), 64 KB callback body cap via `http.MaxBytesReader`, wildcard-address rewrite (`0.0.0.0` / `[::]` → `127.0.0.1`) so the outbound payload carries a dialable callback URL, and an explicit `Cancel()` that closes the server and unblocks pending gates. Implements both `FreeformInterviewer` and `LabeledFreeformInterviewer` so it drops into existing pipeline flows unchanged. CLI flags added: `--webhook-url` (required to enable), `--gate-callback-addr` (default `:8789`), `--gate-timeout` (default `10m`), `--gate-timeout-action` (`fail`/`success`), `--webhook-auth` (outbound `Authorization` header). Mutual exclusion with `--autopilot` and `--auto-approve` is enforced at parse time. Validation rejects invalid `--gate-timeout-action` values at parse time.
- **Per-node context scoping** (`PipelineContext.ScopeToNode`): after each node's handler completes, the engine copies every key written during that node's execution into a `node.<nodeID>.<key>` namespace. Downstream nodes can read `node.MyAgent.last_response` to get a specific upstream node's output without being affected by later writes to the bare `last_response` key. Bare keys retain their last-writer-wins global semantics for full backward compatibility. New convenience method `GetScoped(nodeID, key)`. Closes #32.
- `pipeline.ContextKeyNodePrefix` constant (`"node."`), the namespace prefix for per-node scoped keys.

- `Result.Cost` on the library API with per-provider rollup (`map[string]llm.ProviderCost`) and `TotalUSD`. Populated from the `llm.TokenTracker` middleware and priced via `llm.EstimateCost`. Closes #62.
- `pipeline.BudgetGuard` enforcing `MaxTotalTokens`, `MaxCostCents`, and `MaxWallTime` limits. Halts the run with `pipeline.OutcomeBudgetExceeded` when any dimension trips. Closes #17.
- New `tracker.Config.Budget` field (type `pipeline.BudgetLimits`) for library consumers.
- New CLI flags on `tracker run`: `--max-tokens`, `--max-cost` (cents), `--max-wall-time`.
- New pipeline events `cost_updated` (streaming per-node cost snapshots) and `budget_exceeded` (fired on halt). Both carry a `CostSnapshot` payload with `TotalTokens`, `TotalCostUSD`, `ProviderTotals`, and `WallElapsed`.
- `tracker diagnose` surfaces a "Budget halt detected" section when a run halts on budget.
- `UsageSummary.ProviderTotals` (per-provider token and cost rollup) on `pipeline.Trace.AggregateUsage()` output.

### Notes

- Reading budget limits from `.dip` workflow attrs is blocked on dippin-lang IR support; tracked in #67.

## [0.16.4] - 2026-04-09

### Fixed

- **Turn-limit exhaustion treated as success**: Agents that exhausted their turn limit (or entered a tool call loop) were silently treated as `OutcomeSuccess`, allowing pipelines to advance past nodes that wrote zero files. Now returns `OutcomeFail` so the engine routes through explicit `when ctx.outcome = fail` edges (or stops via strict-failure-edge when no failure edge exists).
- **Loop detection produces distinct diagnostic**: `turn_limit_msg` context key now distinguishes "agent entered tool call loop" from "agent exhausted turn limit" for clearer `tracker diagnose` output.

### Added

- **`ContextKeyTurnLimitMsg` constant**: New `pipeline.ContextKeyTurnLimitMsg` context key for turn-limit and loop-detection diagnostics. Added to `reservedContextKeys()` for linter recognition.
- **Turn-limit and loop-detection tests**: `TestCodergenHandlerMaxTurnsExhaustedIsFail`, `TestCodergenHandlerMaxTurnsWithAutoStatusSuccess`, `TestCodergenHandlerMaxTurnsWithAutoStatusFail`, `TestCodergenHandlerLoopDetectedMessage`.

## [0.16.3] - 2026-04-06

### Fixed

- **Thinking signature dropped in streaming**: The Anthropic SSE handler now captures `signature_delta` events. Previously, thinking block signatures were silently lost during streaming, causing multi-turn sessions with extended thinking (Opus 4.6) to crash with `messages.N.content: Input should be a valid list` when the API rejected the signature-less thinking block on the next turn.
- **Redacted thinking blocks dropped in streaming**: The SSE handler now captures `redacted_thinking` content blocks and round-trips them through the `StreamAccumulator`. Previously, these opaque blocks were silently dropped, breaking conversation continuity.
- **Nil message content serialized as `null`**: `translateMessage` now initializes content as an empty slice so JSON serializes to `[]` instead of `null` when all content parts are skipped.

## [0.16.2] - 2026-04-05

### Added

- **Comprehensive human gate test suite**: `examples/human_gate_test_suite.dip` exercises all 4 gate modes (choice, yes_no, freeform, interview) plus timeout, default_choice, ctx.outcome routing, hybrid freeform, and interview cancel. 100 simulated paths, all reaching Exit.
- **Backend selection precedence test**: Verifies node attr overrides global `--backend` CLI flag.

### Changed

- **dippin-lang v0.18.0**: Updated from v0.17.0. Adds `flatten` package for inlining subgraph refs into a single flat workflow.

### Fixed

- **human_gate_showcase.dip**: EchoFreeform agent no longer asks follow-up questions that conflict with the next gate's choices.

## [0.16.1] - 2026-04-04

### Fixed

- **`mode: yes_no` human gate outcome mapping**: Yes now returns `OutcomeSuccess`, No returns `OutcomeFail`. Previously, `yes_no` fell through to choice mode which always returned `OutcomeSuccess` regardless of selection, causing `ctx.outcome = fail` conditions to never match. Pipelines using `mode: yes_no` with `ctx.outcome` edge conditions now route correctly.

### Added

- **`executeYesNo` handler**: Dedicated handler for `mode: yes_no` human gates. Presents fixed "Yes"/"No" choices and maps selection to outcome status. Comprehensive test coverage for all four human gate modes (choice, yes_no, freeform, interview).

## [0.16.0] - 2026-04-04

### Added

- **ACP (Agent Client Protocol) backend**: Third execution backend alongside native and claude-code. Spawns ACP-compatible coding agents as subprocesses via JSON-RPC 2.0 over stdio using `github.com/coder/acp-go-sdk`. Per-node selection via `backend: acp` + `acp_agent` params in .dip files, global override via `--backend acp` CLI flag.
- **ACP agent routing**: Provider-based binary mapping (`anthropic` → `claude-agent-acp`, `openai` → `codex-acp`, `gemini` → `gemini --acp`). The `acp_agent` node attribute overrides provider-based selection.
- **ACP model bridging**: `mapModelToBridge` maps tracker model names (e.g. `claude-sonnet-4-6`) to bridge model IDs via substring matching against `NewSession` advertised models.
- **ACP environment scoping**: API keys and base URLs stripped from subprocess environment by default so agents use native auth (subscription/OAuth). Override with `TRACKER_PASS_API_KEYS=1`.
- **ACP terminal management**: Full `CreateTerminal`, `TerminalOutput`, `KillTerminalCommand`, `ReleaseTerminal` implementation with process group isolation (`Setpgid`) and goroutine-safe output buffering.
- **ACP file operations**: `ReadTextFile` and `WriteTextFile` handlers scoped to the node's working directory.
- **`ACPConfig` type**: Backend-specific config carrying explicit agent binary name, extracted from `params.acp_agent` in .dip files.
- **`--backend acp` CLI flag**: Routes all agent nodes through ACP without per-node attrs.

### Fixed

- **ACP data race on empty response check**: `handler.mu` now locked before reading `textParts`/`toolCount` after prompt completes.
- **ACP terminal output data race**: Replaced `bytes.Buffer` with `syncBuffer` (mutex-protected writer) for subprocess stdout/stderr.
- **ACP protocol version validation**: `InitializeResponse.ProtocolVersion` checked against `ProtocolVersionNumber` with warning on mismatch.
- **ACP empty Cwd fallback**: `os.Getwd()` used when `WorkingDir` is empty, preventing ACP SDK validation failure.
- **ACP process kill safety**: `Pid > 0` guard before `syscall.Kill(-pid, SIGKILL)` at all 3 call sites to prevent killing pid 0 process group.
- **`TRACKER_PASS_API_KEYS` truthiness**: Changed from `!= ""` to `== "1"` so `"false"` and `"0"` correctly strip keys.

## [0.15.0] - 2026-04-03

### Added

- **Per-node response context keys**: Codergen and human handlers now write `response.<nodeID>` alongside `last_response`/`human_response`, enabling downstream nodes to reference specific upstream outputs instead of only the most recent. (#24)
- **Parallel concurrency limits**: `max_concurrency` attr on parallel nodes limits concurrent branch goroutines via semaphore. Context-aware acquisition aborts on cancellation. (#27)
- **Parallel branch timeout**: `branch_timeout` attr on parallel nodes sets per-branch context deadline. Slow branches fail without blocking fan-in. (#27)
- **Human gate timeout**: `timeout` attr on human nodes with `timeout_action` (default/fail) and `default_choice` fallback. Applied to freeform, choice, and interview modes. (#30)
- **Edge adjacency indexes**: `OutgoingEdges`/`IncomingEdges` now use O(1) map lookup via adjacency indexes built by `AddEdge`, with O(E) fallback for graphs built without `AddEdge`. Returns defensive copies. (#31)
- **Format constants**: `FormatDip` and `FormatDOT` typed constants for pipeline format identification. (#9)
- **Pipeline package documentation**: `pipeline/doc.go` with package overview and dual-format documentation. (#12)

### Fixed

- **P0: Goal-gate infinite fallback loop**: `FallbackTaken` guard persisted in checkpoint prevents one-shot fallback/escalation from looping. Separate fallback routing path in `handleExitNode` doesn't increment retry counts. (#15)
- **P0: Parallel branch context loss on fan-in**: `PipelineContext.DiffFrom()` captures side effects from parallel branches. (#20)
- **Adapter nil pointer guards**: Nil checks for IR nodes, edges, and all 6 pointer config types in `extractNodeAttrs`. Also guards in `synthesizeImplicitEdges` and `buildFanInSourceMap`. (#38)
- **Adapter sentinel errors**: `ErrNilWorkflow`, `ErrMissingStart`, `ErrMissingExit`, `ErrUnknownNodeKind`, `ErrUnknownConfig` with `%w` wrapping for `errors.Is` support. (#33)
- **Deterministic map iteration**: `extractSubgraphAttrs` and `serializeStylesheet` sort keys before iteration via `slices.Sorted(maps.Keys(...))`. (#8)
- **Workflow.Version mapping**: `ir.Workflow.Version` now mapped to `g.Attrs["version"]`. (#25)
- **Validation bypass removed**: Deleted `DippinValidated` field — all 5 structural validation checks always run for defense-in-depth. (#4)
- **Library stderr cleanup**: Replaced `fmt.Fprintf(os.Stderr, ...)` with `log.Printf(...)` in library code (tracker.go, condition.go, autopilot handlers). (#7)
- **Case-insensitive auto_status**: `parseAutoStatus` now matches STATUS prefix case-insensitively and skips STATUS lines inside code fences. (#23)
- **Word-boundary fidelity truncation**: `truncateAtWordBoundary` cuts at whitespace (unicode.IsSpace) instead of mid-word, with `...` suffix and named `DefaultTruncateLimit` constant. (#34)
- **Condition parser hardening**: Support `==` operator (space-delimited), strip surrounding double quotes from values in `=`/`==`/`!=` comparisons. (#21)
- **Consensus pipeline parallelized**: `consensus_task.dip` now uses parallel fan-out/fan-in for DoD, Planning, and Review phases. (#26)
- **CLI format detection default**: Unknown extensions now default to `.dip` instead of `.dot`, with case-insensitive extension matching. (#9)
- **Empty API response retry**: Empty API responses (0 output tokens, 0 tool calls) now trigger `OutcomeRetry` instead of hard-failing. (#23)
- **POSIX build constraint**: `//go:build !windows` on `agent/exec/local.go`. (#28)
- **ConsoleInterviewer IsYesNo priority**: Yes/no check now runs before option list check, matching TUI behavior. (#48 review)
- **Test rename**: `TestListBuiltinWorkflowsReturnsThree` → `ReturnsFour`. (#48 review)

### Changed

- **Retry backoff jitter**: `ExponentialBackoff` and `LinearBackoff` now apply ±25% random jitter to prevent thundering herd when multiple pipelines retry simultaneously. (#29)
- **Code cleanup**: Unexported `NodeKindToShape`, removed `make([]*Edge, 0)`, replaced custom `contains` helper with `strings.Contains`, replaced bubble sort with `slices.SortFunc`. (#10)

### Deprecated

- **DOT format support**: `ParseDOT` is deprecated. Use `.dip` format with `FromDippinIR` instead. DOT support will be removed in v1.0. (#12)

## [0.14.0] - 2026-03-31

### Added

- **Interview mode for human gates**: New `mode: interview` on human nodes enables structured multi-field form collection. An upstream agent generates markdown questions; the interview handler parses them into individual fields (select with inline options, yes/no confirm, freeform textarea). Answers are stored as JSON at a configurable context key and as a markdown summary at `human_response`. Supports retry pre-fill, cancellation with partial answers, and 0-question fallback to freeform.
- **Interview question parser**: `ParseQuestions()` extracts structured questions from agent markdown — numbered items, bulleted questions, imperative prompts. Trailing parentheticals like `(option1, option2)` become select field options. Yes/no patterns auto-detected. Fenced code blocks skipped.
- **TUI interview modal**: Fullscreen one-question-at-a-time form with progress bar, answered summary, selection feedback (filled dot + checkmark), elaboration textareas (Tab), submit (Ctrl+S), cancel (Esc), and PgUp/PgDn jump navigation. Pre-fills from previous answers on retry.
- **Interview autopilot support**: `AutopilotInterviewer`, `ClaudeCodeAutopilotInterviewer`, and `AutopilotTUIInterviewer` all implement `AskInterview`. LLM-backed autopilot sends all questions in a single prompt, parses JSON response, retries once on parse failure, hard-fails on double failure.
- **Console interview support**: `ConsoleInterviewer.AskInterview` presents questions one at a time with option selection by name or number, blank-line skip, and previous-answer hints on retry.
- **`deep_review` built-in workflow**: Interview-driven codebase review pipeline with 3 structured interview gates (scope, findings, priority), parallel analysis (correctness, security, design), and remediation plan generation. Run with `tracker deep_review`.
- **`interview-loop.dip` subgraph**: Reusable interview loop pattern (ask → answer → assess → loop) in `examples/subgraphs/`. Parameterized with `topic` and `focus` for embedding via `subgraph` nodes.
- **Structured JSON question format**: `ParseStructuredQuestions()` parses JSON questions from agent output with validation. Handles code fences, preamble text, and extracts `{"questions": [...]}` objects. Falls back to markdown heuristic parsing. "Other" option variants are auto-filtered since the UI always provides its own.
- **One-question-at-a-time TUI**: Interview form shows one question with full context, progress bar, answered summary, and remaining count. Selection feedback with filled dot and checkmark. Enter confirms and advances.
- **`response_format` support**: Agent nodes can set `response_format: json_object` or `response_format: json_schema` with `response_schema:` to force structured output at the LLM API level. Plumbed from `.dip` files through dippin IR → adapter → codergen → agent session → all three providers (Anthropic, OpenAI, Gemini).
- **Agent `params` map**: Generic key-value pass-through from `.dip` files via `AgentConfig.Params` (dippin-lang v0.16.0). Enables runtime features like `backend: claude-code` without IR schema changes.
- **Empty API response diagnostics**: Anthropic adapter logs raw response body, HTTP status, stop_reason, model, and request-id when API returns 0 output tokens. Session layer retries completely empty responses with diagnostic event emission.
- **EngineResult.Usage**: Pipeline runs now expose aggregated token counts and cost via `EngineResult.Usage` (`*UsageSummary`). Downstream consumers can read `TotalInputTokens`, `TotalOutputTokens`, `TotalTokens`, `TotalCostUSD`, and `SessionCount` directly from the result.
- **Per-node token tracking in SessionStats**: `InputTokens`, `OutputTokens`, `TotalTokens`, `CostUSD`, `ReasoningTokens`, `CacheReadTokens`, `CacheWriteTokens` fields on `SessionStats` in trace entries.
- **Parallel branch stats aggregation**: Parallel handler now collects and aggregates `SessionStats` from branch outcomes into its own trace entry.
- **Consistent JSON tags**: All fields on `SessionStats`, `TraceEntry`, and `Trace` now have `json:"snake_case"` tags for consistent serialization.

### Fixed

- **Interview cancellation returns OutcomeFail**: Canceled interviews now return `fail` status instead of `success`, allowing pipeline edges to route canceled interviews differently from completed ones.
- **ClaudeCode autopilot hard-fails on parse error**: `ClaudeCodeAutopilotInterviewer.AskInterview` now retries once on JSON parse failure and hard-fails on double failure, matching the native autopilot behavior. Previously silently fell back to first-option defaults.
- **SerializeInterviewResult enforced**: Panics on marshal failure instead of silently returning empty string, preventing downstream deserialization corruption.
- **Goroutine leak in autopilot flash**: `flashDecision` goroutine now exits immediately when the caller unblocks via a `done` channel, instead of sleeping for the full 2-second timer. Includes `defer/recover` for panic safety per CLAUDE.md.
- **Mode 1 tea.Cmd propagation**: All three TUI runner types (choice, freeform, interview) now propagate `tea.Cmd` from `content.Update()` instead of discarding it.
- **Context leak in retry loop**: `ClaudeCodeAutopilotInterviewer.AskInterview` uses explicit `cancel()` calls instead of `defer cancel()` inside a for loop, preventing context timer goroutine leaks on retry.
- **Empty API response guard**: Agent sessions that receive completely empty responses (0 content parts, 0 output tokens, no prior tool calls) now retry with a continuation prompt instead of silently succeeding with empty `last_response`. Codergen handler also fails the node when the session produces empty text with zero tool calls.
- **Start/exit agent nodes preserved**: `ensureStartExitNodes` no longer overwrites the `codergen` handler on agent nodes designated as start or exit. Agent start/exit nodes now execute their LLM prompts instead of being silently replaced with no-op passthroughs. (Closes #42)
- **DecisionDetail token mapping**: `TokenInput`/`TokenOutput` in pipeline events now correctly map from `InputTokens`/`OutputTokens` instead of `CacheHits`/`CacheMisses`.
- **Native backend double-counting**: Token usage from the native backend is no longer reported twice to the `TokenTracker`.
- **Cancel/fail EndTime**: Cancelled and retry-exhausted runs now set `trace.EndTime` so the run summary shows duration.
- **failResult atomicity**: `failResult()` now accepts a `*Trace` parameter and sets both `Trace` and `Usage` internally, preventing silent data loss.
- **Built-in pipeline prompts**: Removed trivial placeholder prompts from Start/Done nodes in built-in workflows that were causing unnecessary LLM calls.

## [0.13.0] - 2026-03-28

### Added

- **TUI: Progress bar with ETA**: Amber ASCII bar (`━━━──────`) in the status bar shows completed/total nodes. ETA appears after 2+ real LLM nodes complete, based on rolling average of node durations.
- **TUI: Desktop notification**: Fires OS-native notification on pipeline completion (macOS `osascript`, Linux `notify-send`). Disable with `TRACKER_NO_NOTIFY=1`.
- **TUI: Log verbosity cycling (`v`)**: Cycle through All → Tools → Errors → Reasoning. View-level filter only — all lines always stored (append-only per CLAUDE.md).
- **TUI: Zen mode (`z`)**: Hide sidebar, agent log gets full terminal width. Status bar and modal gates still work.
- **TUI: Help overlay (`?`)**: Modal showing all keyboard shortcuts in a styled two-column table.
- **TUI: Agent log search (`/`)**: Inline search bar with real-time highlighting. `n`/`N` jump between matches. Search intersects with verbosity filter.
- **TUI: Per-node cost tracking**: Shows cost badge on completed nodes in the sidebar. Uses delta snapshots from `TokenTracker`. Parallel branches show `~` prefix (approximate). Max subscription shows "usage" not "cost".
- **TUI: Node drill-down (`Enter`)**: Arrow keys navigate the node list, Enter focuses the log on that node, Esc returns to full view.
- **TUI: Copy to clipboard (`y`)**: Copies visible (filtered) log text. Uses `pbcopy`/`xclip`. Error message includes diagnostic on failure.
- **TUI: Status bar flash**: "Copied!" confirmation that auto-clears after 2 seconds.
- **Claude-code autopilot**: New `ClaudeCodeAutopilotInterviewer` routes autopilot gate decisions through the `claude` CLI subprocess instead of direct API calls. No API key needed for `--autopilot` with `--backend claude-code`.
- **`--auto-approve` works with TUI**: No longer forces `--no-tui`. Gates auto-dismiss in the dashboard.

### Changed

- **Claude-code env: API keys stripped**: `buildEnv()` strips `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `GEMINI_API_KEY` from the subprocess environment so the `claude` CLI uses Max/Pro subscription auth instead of consuming API credits. Override with `TRACKER_PASS_API_KEYS=1`.
- **Lazy LLM client**: `buildLLMClient()` failure is non-fatal with `--backend claude-code`. The native client is only required when something actually needs it (native backend nodes, native autopilot).
- **Claude-code backend handles all providers**: With `--backend claude-code`, nodes with `provider: openai` or `provider: gemini` also route through the claude CLI. Non-Anthropic model names are stripped so the CLI uses its default.
- **Max subscription cost labeling**: Header, sidebar, and exit summary show "~$X.XX usage" instead of "$X.XX" when all usage is from `claude-code` provider. Exit summary adds "(Max subscription — no actual charge)".
- **Strict failure edges**: When a node's outcome is "fail" and all outgoing edges are unconditional, the pipeline now stops instead of silently continuing. Pipelines that intentionally handle failure must use explicit `when ctx.outcome = fail` edges.
- **Status bar hints**: Updated to show all new shortcuts (`v filter  z zen  / search  ? help  q quit`).

### Fixed

- **TUI: Sidebar connector alignment**: Connectors (`│`) now align with node lamps when selection mode is active.
- **TUI: Scroll follows selection**: Up/Down navigation scrolls the node list viewport to keep the selected node visible.
- **Search: `formatMatchStatus` bug**: Rune arithmetic broke for 10+ matches. Now uses `fmt.Sprintf`.
- **Search: Match consistency with filters**: Search matches against the filtered view, not the full line buffer.
- **Verbosity: Separators preserved**: Node separator lines pass through all verbosity filters for structural context.
- **Zen mode: `relayout()` fix**: Terminal resize in zen mode now gives the agent log full width.
- **Exit hang**: `runTUI()` waits at most 5 seconds for the pipeline goroutine after the TUI closes.
- **Notification zombie**: `SendNotification` uses `cmd.Run()` in a goroutine instead of `cmd.Start()` without `Wait()`.

## [0.12.1] - 2026-03-27

### Fixed

- **Claude Code subprocess killed after 10 seconds**: `exec.CommandContext` + `WaitDelay` created a race where Go's process management sent SIGKILL to the Claude Code subprocess after exactly 10 seconds, despite no context cancellation. Switched to plain `exec.Command`.
- **Claude Code auth failure from stripped environment**: The minimal env allowlist prevented Claude Code from finding its OAuth token / config directory. Now passes the full parent environment.
- **NDJSON unmarshal error on subagent results**: Claude Code's subagent tool results return `content` as an array of blocks, not a string. The parser now handles both formats.

### Added

- **Autopilot runs inside the TUI**: `--autopilot` no longer forces `--no-tui`. Gate decisions flash in a modal for 2 seconds showing "AUTOPILOT" header, the prompt, and the chosen option in green. Press Enter to dismiss early.
- **Backend and autopilot tags in TUI header**: Orange tag for `claude-code`, purple tag for autopilot persona — always visible next to the pipeline name.
- **"Agent backend:" startup message**: Prints the active backend before the TUI starts (visible in `--no-tui` mode).

## [0.12.0] - 2026-03-27

### Added

- **Claude Code backend**: Pluggable `AgentBackend` interface with `--backend claude-code` flag. Spawns the `claude` CLI as a subprocess, parses NDJSON output, and maps exit codes to pipeline outcomes. Per-node via `backend: claude-code` in `.dip` files, or global via CLI flag. Includes environment scoping, token tracking, and retryable init.
- **`tracker update`**: Self-update command downloads the latest GitHub release, verifies SHA256 checksum, extracts the binary, smoke-tests it, and atomically replaces the current binary with a `.bak` rollback. Detects install method (Homebrew → advises `brew upgrade`, go install → advises `go install @latest`, binary → self-replaces).
- **Non-blocking update check**: On every `tracker run`, a background goroutine checks for new releases (24h file-based cache). Prints a one-line hint to stderr if an update is available. Disabled in CI (`CI` env) or with `TRACKER_NO_UPDATE_CHECK`.

### Changed

- Upgraded dippin-lang dependency v0.10.0 → v0.12.0 (preferred_label fix, immediately_after assertions, tool command lint, subgraph validation, test coverage)
- Tightened 5 dippin test assertions with `immediately_after` for stricter edge verification

## [0.11.2] - 2026-03-27

### Fixed

- **PickNextMilestone silent skip**: Flexible milestone header matching now handles `## Milestone 1: Title`, `### Milestone 1 — Setup`, and other LLM formatting variations. Fails loudly if no milestones found or extraction produces an empty file.
- **Removed `eval` of LLM-generated verify commands**: TestMilestone no longer evals commands extracted from milestone specs — this was arbitrary code execution from free-form LLM text. Verification is now the Implement agent's responsibility.
- **TestMilestone known_failures parsing**: Strip comments and blank lines, use `go test -skip` instead of unsupported `(?!` negative lookahead.
- **PickBest winner parsing hardened**: Uses `grep -ioE 'claude|codex|gemini'` regardless of markdown formatting.

## [0.11.1] - 2026-03-27

### Fixed

- Provider errors hard-fail per CLAUDE.md (autopilot review fixes)
- Default autopilot model picks cheapest from configured provider
- Autopilot forces `--no-tui`, `matchChoice` uses longest-match, `decide()` returns errors

## [0.11.0] - 2026-03-26

### Added

- **`--autopilot <persona>`**: Replace all human gates with LLM-backed decisions. Four personas encode different risk tolerances:
  - **lax**: Bias toward forward progress. Approves plans, marks done on escalation, accepts reviews.
  - **mid**: Balanced engineering judgment. The default persona if none specified.
  - **hard**: High quality bar. Pushes back on gaps, demands fixes, retries before accepting.
  - **mentor**: Approves forward progress but writes detailed constructive feedback.
- **`--auto-approve`**: Deterministic auto-approval of all human gates. No LLM calls — always picks the default or first option. For testing pipeline flow and CI.
- Uses the pipeline's existing LLM client with low temperature (0.1) for consistent decisions. Structured JSON output with fallback-to-default on error.

## [0.10.3] - 2026-03-26

### Fixed

- **Signature collision in retry detection**: Failure signatures now use null byte separator instead of pipe, preventing false "identical" matches when error strings contain `|`.
- **Duration label clarity**: Shows "Duration (last):" instead of "Duration:" when a node had multiple retries, so users know the value is the last attempt's duration, not total.

## [0.10.2] - 2026-03-26

### Added

- **Deterministic failure detection in `tracker diagnose`**: When a tool node fails multiple times with identical errors, diagnose now flags it as a deterministic bug — "Failed 5 times with identical errors — this is a deterministic bug in the command, not a transient failure. Retrying won't help. Fix the tool command in the .dip file and re-run." Distinguishes deterministic failures (same error every time) from flaky failures (varying errors across retries).
- **Retry count in diagnose output**: Failed nodes now show "Attempts: N failures (all identical — deterministic)" in the diagnosis, so the retry pattern is visible at a glance without reading suggestions.

## [0.10.1] - 2026-03-26

### Changed

- **README rewritten**: Added v0.10.0 features (workflows, init, bare names), mermaid diagrams for build_product milestone loop and architecture layers, full CLI reference section, development section with `dippin test`.
- **CLAUDE.md updated**: Fixed stale `EscalateToHuman` reference in edge routing rules, added `tracker workflows`/`tracker init` docs and bare name resolution section.

### Fixed

- **`suggested_next_nodes` string literal**: Extracted `ContextKeySuggestedNextNodes` constant in `pipeline/context.go`, eliminating 6 scattered string literals across engine and handler code.
- **`enrichFromActivity` cognitive complexity (34 → 18)**: Extracted `enrichFromEntry()` helper for per-line processing.
- **`printDiagnoseSuggestions` cyclomatic complexity (16 → 8)**: Extracted `suggestionsForFailure()` helper. All functions now pass complexity thresholds.

## [0.10.0] - 2026-03-26

### Added

- **Embedded built-in workflows**: The 3 flagship pipelines (`ask_and_execute`, `build_product`, `build_product_with_superspec`) are now embedded in the binary via `go:embed`. Users who install via `brew` or `go install` can run them without cloning the repo.
- **`tracker workflows`**: Lists all built-in workflows with their display names and goals.
- **`tracker init <workflow>`**: Copies a built-in workflow to the current directory for customization. Refuses to overwrite existing files.
- **Bare name resolution**: `tracker build_product`, `tracker validate build_product`, and `tracker simulate build_product` all work with bare workflow names. Local `.dip` files always take precedence over built-ins.
- **`make sync-workflows` / `make check-workflows`**: Makefile targets to keep embedded copies in sync with `examples/`. CI enforces sync.

### Changed

- **Split `EscalateToHuman` into two context-specific gates** in `build_product.dip`:
  - `EscalateMilestone` (mid-build): offers **mark done** (override test, continue to next milestone), **retry** (re-implement from scratch), **accept** (skip to cleanup), **abandon**. Defaults to "mark done".
  - `EscalateReview` (post-build): offers **accept** (ship it), **retry** (back to Decompose), **abandon**. Defaults to "accept".
- **Escalation gates now have `prompt:` blocks** with rich context explaining each option (requires dippin-lang v0.9.0+).

### Fixed

- **TestMilestone early-exit bug**: Previously, the attempt counter was checked *before* running tests. A milestone that was genuinely fixed on attempt 4 would escalate instead of succeeding. Tests now run first; the counter is only checked on failure.
- **Milestone escalation was a dead end**: `EscalateToHuman` had no edge back into the build loop. Choosing "accept" ended the entire build instead of continuing to the next milestone. `EscalateMilestone -> MarkMilestoneDone` now enables "mark done and move on."

### Tests

- **23 dippin simulation tests** for `build_product.dip` covering every edge from both escalation gates, all human gate label selections, fix loop mechanics, and cross-review routing. Uses dippin-lang v0.9.0 features: `preferred_label`, `immediately_after`, and `prompt:` blocks on human gates.
- **18 Go unit tests** for the embedded workflow system: catalog lookup, resolution order (filesystem > local .dip > embedded > error), flag parsing for `workflows`/`init`, init file creation and overwrite protection.

## [0.9.2] - 2026-03-26

### Added

- **`tracker diagnose [runID]`**: Deep failure analysis for pipeline runs. Reads per-node status files and activity logs to surface tool stdout/stderr, error messages, and timing anomalies. Provides actionable suggestions (e.g., stale fix_attempts counter, suspiciously fast execution, missing tools). Without a run ID, analyzes the most recent run.
- **`tracker doctor`**: Preflight health check verifying LLM provider API keys (masked in output), dippin binary availability, and working directory access. Shows actionable hints for every failure.
- **Provider status in `tracker version`**: Shows which LLM providers have API keys configured, or prompts `tracker setup` if none are found.
- **VCS-aware local builds**: `go install` builds now show the git commit hash and build timestamp via Go's embedded VCS metadata, instead of `unknown`. GoReleaser ldflags still take precedence for release builds.
- **Freeform "other" option in review hybrid**: ReviewHybridContent now includes an "other (provide feedback)" option with a textarea, so users can provide custom retry instructions at labeled escalation gates — not just pick from predefined labels.
- **Runtime error surfacing in TUI**: The activity log now shows `FAILED:` and `RETRYING:` messages inline when nodes fail or retry. Previously, tool node failures only updated the sidebar icon with no details visible.

### Fixed

- **ReviewHybridContent phantom cursor**: `totalOptions()` returned `len(labels)+1` creating an unreachable dead-end cursor position. Now correctly bounded to label count + 1 (for "other").
- **Glamour rendering in review hybrid**: The prompt label portion was rendered with plain lipgloss bold, bypassing glamour. Now the full prompt (label + context) goes through glamour so headings, code blocks, and lists render correctly in the viewport.
- **Actionable "no providers" error**: The bare `error: create LLM client: no providers configured` message is replaced with specific env var names and a `tracker setup` hint.

## [0.9.1] - 2026-03-25

### Fixed

- **ReviewHybridContent phantom cursor position**: `totalOptions()` returned `len(labels)+1` creating an unreachable "other" slot with no textarea — cursor could land on a dead-end position that couldn't be submitted. Now correctly bounded to label count only.
- **RadioHeight off-by-one in review hybrid**: Viewport height calculation reserved space for a non-existent "other" option line, wasting a terminal row.

## [0.9.0] - 2026-03-25

### Added

- **Subgraph Loading**: CLI now loads and executes subgraph references from `.dip` files. Path resolution tries relative to parent file, with `.dip` extension auto-appended, recursive loading with cycle detection
- **Hybrid Radio+Freeform Gate**: Human gates with labeled outgoing edges present a radio list of labels plus an "other" option for custom freeform feedback
- **Split-Pane Review View**: Long human gate prompts (20+ lines) use a fullscreen split-pane with glamour-rendered scrollable viewport and textarea
- **Upfront Subgraph Validation**: Every subgraph node is validated at load time — missing refs, empty refs, and circular refs all fail immediately with clear messages

### Fixed

- **Subgraph handler was never wired**: The CLI had SubgraphHandler and WithSubgraphs but never called either — subgraph nodes always failed at runtime with "subgraph not found"
- **Child registry used wrong graph for human gates**: RegistryFactory now overrides WithInterviewer with the child graph so human gates inside subgraphs see the correct edge labels
- **Circular subgraph refs caused runtime stack overflow**: Now detected at load time via absolute-path cycle detection
- **Concurrent subgraph executions shared mutable state**: InjectParamsIntoGraph now deep-clones Attrs, Edges, and NodeOrder instead of sharing pointers
- **Gate deadlocks on cancel**: Ctrl+C and Esc close reply channels on all gate types (Choice, Freeform, Hybrid, Review)
- **Labels hidden by long prompt**: Labeled gates always use hybrid radio view regardless of prompt length
- **Activity log indicator pushed off viewport**: Fixed terminal row budget calculation
- **67 root-level analysis markdown files removed**: Cleaned repo of stale LLM analysis artifacts

## [0.8.0] - 2026-03-25

### Added

- **Decision Audit Trail**: Engine emits structured decision events to activity.jsonl
  - `decision_edge`: which edge was selected, at what priority level, with context snapshot
  - `decision_condition`: every condition evaluated with match result and context values
  - `decision_outcome`: node outcome status, context updates, token counts
  - `decision_restart`: restart count, cleared nodes, context snapshot
- **Skipped Node State**: Unvisited nodes show ⊘ (dim) when pipeline completes
- **Topological Node Ordering**: TUI sidebar uses execution order (Kahn's algorithm), not declaration order or BFS
- **Complexity Enforcement**: Makefile targets and pre-commit hooks enforce cyclomatic ≤ 15, cognitive ≤ 25, file size ≤ 500 LOC
- **Pre-commit Quality Gates**: Format, vet, build, test, race detector, coverage, dippin lint — all enforced on every commit
- **Pipeline Test Scenarios**: `.test.json` files for all three core pipelines with happy path and failure scenarios
- **CLAUDE.md**: Project rules, versioning policy, and architecture gotchas for AI-assisted development
- **Subgraph Event Propagation**: Child pipeline engines emit events visible to the parent TUI
- **Per-Branch Parallel Config**: Parallel fan-out nodes can override target node attributes per branch
- **Per-Node Working Directory**: `working_dir` attribute on agent and tool nodes for git worktree isolation
- **Variable Interpolation**: Full `${namespace.key}` syntax — `ctx.*`, `params.*`, `graph.*` namespaces
- **Pipeline Examples**: `ask_and_execute.dip`, `build_product.dip`, `build_product_with_superspec.dip`

### Changed

- **Major complexity refactoring**: 35 cyclomatic violations → 0, 30 cognitive violations → 0, 7 oversized files → 0
  - `engine.go` (1002 lines, cyclomatic 61) → 4 files, max cyclomatic 12
  - `main.go` (1228 lines) → 8 focused files, max 378 lines
  - All 3 LLM adapters, codergen handler, parallel handler, condition evaluator, dippin adapter decomposed
- **dippin-lang upgraded** to v0.8.0 (explain, unused, graph, test commands; DIP121/DIP122 lint rules; exhaustive condition detection; model catalog with verified pricing)
- **GoReleaser**: quality gates in before hooks, grouped changelog (Features/Fixes/Other)
- **CI workflow**: full gate suite (format, vet, build, test, race, coverage, lint, doctor, complexity)
- **TUI activity log**: rewritten — per-node streams, line-level styling (no glamour), append-only with 10k line cap
- **TUI human input**: bubbles/textarea with wrapping, multiline, Ctrl+S submit, Esc cancel
- **Build product pipeline**: opus fix agent with 50 turns, per-milestone circuit breaker (3 attempts then escalate), known test failures support

### Fixed

- **OpenAI SSE error handling**: `error` and `response.failed` events parsed and surfaced as typed errors (was silently dropped)
- **Non-retryable provider errors**: quota, auth, model not found now crash immediately (was `OutcomeRetry`)
- **Empty agent responses**: zero-output sessions return `OutcomeFail` (was `OutcomeSuccess`)
- **Parallel handler**: navigates to join node via `suggested_next_nodes`; dispatches only branch targets; panic recovery in goroutines; emits stage events per branch
- **Condition evaluator**: resolves `ctx.*`, `context.*`, `internal.*` prefixes; handles infix negation; warns on unresolved variables
- **Variable expansion**: single-pass prevents infinite loops; malformed tokens skipped instead of stopping all expansion
- **Freeform human gates**: match response text against edge labels for routing
- **Thinking spinner**: emitted from agent events (with nodeID) not global LLM trace
- **Activity log viewport**: counts terminal rows, reserves indicator line, stable rendering
- **Pipeline routing**: removed unconditional fallbacks that caused infinite loops; merge conflicts escalate to human; ReadSpec/Decompose gated on success
- **Provider naming**: `gemini` not `google` everywhere
- **Checkpoint**: save failures use correct event type; per-node edge selections for deterministic resume
- **All 25 example pipelines**: grade A on `dippin doctor` (was 10 F's)

## [0.7.0] - 2026-03-25

(See GitHub release for v0.7.0 changelog)

## [Previous Versions]

See [GitHub releases](https://github.com/2389-research/tracker/releases) for earlier versions.
