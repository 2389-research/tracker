# Changelog

All notable changes to tracker will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- **Decision Audit Trail**: Engine emits structured decision events to activity.jsonl
  - `decision_edge`: which edge was selected, at what priority level (condition/label/suggested/weight/lexical), with context snapshot
  - `decision_condition`: every condition evaluated with match result and context values
  - `decision_outcome`: node outcome status, context updates, token counts
  - `decision_restart`: restart count, cleared nodes, context snapshot

- **Skipped Node State**: Unvisited nodes show ⊘ (dim) when pipeline completes, distinguishing "not needed" from "not yet reached"

- **Node Declaration Order**: TUI sidebar uses source file declaration order (not BFS), so Done appears at the bottom

- **Subgraph Event Propagation**: Child pipeline engines emit events visible to the parent TUI
  - `NodeScopedPipelineHandler` prefixes child node IDs with parent path (e.g., `SubgraphNode/ChildAgent`)
  - `SubgraphHandler` accepts `PipelineEventHandler` + `RegistryFactory` for scoped child registries
  - TUI dynamically inserts and indents subgraph child nodes in the node list

- **Per-Branch Parallel Config**: Parallel fan-out nodes can override target node attributes per branch
  - `branch.N.llm_model`, `branch.N.llm_provider`, `branch.N.fidelity` attrs on parallel nodes

- **Per-Node Working Directory**: `working_dir` attribute on agent and tool nodes
  - Enables git worktree isolation for parallel implementations
  - Validated against path traversal and shell metacharacters

- **Variable Interpolation**: Full `${namespace.key}` syntax in prompts and node attributes
  - `${ctx.*}`, `${params.*}`, `${graph.*}` namespaces
  - Single-pass expansion prevents recursive expansion attacks

- **Pipeline Examples**:
  - `ask_and_execute.dip`: Competitive 3-way implementation with git worktree isolation
  - `build_product.dip`: Sequential milestone builds with opus fix agent (50 turns)
  - `build_product_with_superspec.dip`: Parallel stream execution with dependency phases

### Changed

- **dippin-lang updated** to latest main (subgraph params, compaction, fidelity, parallel branches, stylesheets)
- **Dippin adapter**: synthesizes implicit edges from `ParallelConfig.Targets` and `FanInConfig.Sources`; parallel nodes link to fan-in join via `parallel_join` attr
- **Dippin adapter**: preserves node declaration order in `Graph.NodeOrder`
- **Dippin adapter**: maps `model`/`provider` to `llm_model`/`llm_provider` (matches codergen handler)
- **TUI activity log**: rewritten from scratch — per-node streams, line-level styling (no glamour), append-only with 10k line cap, multi-node activity indicators for parallel execution
- **TUI human input**: bubbles/textarea with wrapping, multiline, Ctrl+S submit, Esc cancel
- **Validate command**: registers all handler types (`wait.human`, `parallel`, `parallel.fan_in`, `manager_loop`)
- **Build product pipeline**: implement and fix agents upgraded to opus with 50 max_turns

### Fixed

- **OpenAI SSE error handling**: `error` and `response.failed` SSE events now parsed and surfaced as typed errors (was silently dropped, causing empty responses on quota/auth failures)
- **Non-retryable provider errors**: quota exceeded, auth failure, model not found now crash the pipeline immediately instead of retrying (was `OutcomeRetry`)
- **Empty agent responses**: sessions producing zero output now return `OutcomeFail` instead of `OutcomeSuccess`
- **Parallel handler fan-in routing**: parallel handler navigates to join node via `suggested_next_nodes` instead of re-entering completed branch nodes
- **Parallel handler branch dispatch**: uses `parallel_targets` attr to dispatch only branch nodes, not the fan-in join
- **Parallel handler panic recovery**: goroutine panics caught and converted to `OutcomeFail`
- **Parallel handler stage events**: emits `EventStageStarted`/`EventStageCompleted` per branch so TUI shows them as running
- **Condition evaluator**: resolves `ctx.` and `context.` namespace prefixes
- **Condition evaluator**: resolves `ctx.internal.*` and bare `internal.*` via `GetInternal()`
- **Condition evaluator**: handles infix negation (`key not contains value`)
- **Condition evaluator**: warns on unresolved variables to stderr
- **Variable expansion**: single-pass rewrite prevents infinite loops on self-referential values; malformed tokens skipped instead of stopping all expansion
- **Freeform human gates**: match response text against edge labels for routing; Esc with empty cancels
- **Thinking spinner**: emitted from agent events (with nodeID) not global LLM trace
- **Activity log viewport**: counts actual terminal rows (not entries) to prevent indicator being pushed off screen
- **Pipeline edges**: removed unconditional fallback edges that caused infinite gate/fix loops
- **Merge nodes**: route merge conflicts to human escalation
- **Provider name**: `gemini` not `google` in all `.dip` files and DIP108 lint rule
- **Checkpoint events**: save failures emit `EventCheckpointFailed` (was misleadingly `EventCheckpointSaved`)
- **Checkpoint resume**: stores per-node edge selections for deterministic replay
- **Unknown outcome status**: emits warning event before treating as success
- **Edge tiebreaker**: emits diagnostic when lexical ordering resolves equal-weight edges
- **DIP104 lint**: checks correct key `fallback_retry_target`
- **SubgraphHandler**: panics at construction if both registry and factory are nil
- **AgentLog**: resets `inCodeBlock` on flush; cleans up completed node streams; caps at 10k lines
- **gpt-5.4**: added to model catalog for cost tracking

## [Previous Versions]
(No prior changelog entries - this is the initial CHANGELOG)
