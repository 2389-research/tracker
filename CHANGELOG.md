# Changelog

All notable changes to tracker will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.10.2] - 2026-03-26

### Added

- **Deterministic failure detection in `tracker diagnose`**: When a tool node fails multiple times with identical errors, diagnose now flags it as a deterministic bug — "Failed 5 times with identical errors — this is a deterministic bug in the command, not a transient failure. Retrying won't help. Fix the tool command in the .dip file and re-run." Distinguishes deterministic failures (same error every time) from flaky failures (varying errors across retries).
- **Retry count in diagnose output**: Failed nodes now show "Retries: N attempts (all identical — deterministic failure)" or "Retries: N attempts" in the diagnosis, so the retry pattern is visible at a glance without reading suggestions.

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
