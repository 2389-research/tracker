# Changelog

All notable changes to tracker will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

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
