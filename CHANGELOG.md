# Changelog

All notable changes to tracker will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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
