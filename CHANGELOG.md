# Changelog

All notable changes to tracker will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- **Subgraph Event Propagation**: Child pipeline engines emit events visible to the parent TUI
  - `NodeScopedPipelineHandler` prefixes child node IDs with parent path (e.g., `SubgraphNode/ChildAgent`)
  - `SubgraphHandler` accepts `PipelineEventHandler` + `RegistryFactory` for scoped child registries
  - TUI dynamically inserts and indents subgraph child nodes in the node list
  - Recursive subgraph nesting supported (events compose: `A/B/C`)

- **Per-Branch Parallel Config**: Parallel fan-out nodes can override target node attributes per branch
  - `branch.N.llm_model`, `branch.N.llm_provider`, `branch.N.fidelity` attrs on parallel nodes
  - Each branch runs its target with overridden config; original node stays unchanged

- **Per-Node Working Directory**: `working_dir` attribute on agent and tool nodes
  - Enables git worktree isolation for parallel implementations
  - CodergenHandler overrides `SessionConfig.WorkingDir`
  - ToolHandler prepends `cd` to shell commands

- **Variable Interpolation**: Full `${namespace.key}` syntax in prompts and node attributes
  - `${ctx.*}` for runtime pipeline context
  - `${params.*}` for subgraph parameters
  - `${graph.*}` for workflow-level attributes
  - Lenient mode by default; strict mode for development

- **Pipeline Examples**:
  - `ask_and_execute.dip`: Competitive 3-way implementation with git worktree isolation, cross-critique, and winner selection
  - `build_product.dip`: Sequential milestone builds with verification loops and cross-review
  - `build_product_with_superspec.dip`: Parallel stream execution with dependency phases, quality gates, and traceability audit

### Changed

- **dippin-lang updated** to latest main (subgraph params, compaction, fidelity, parallel branches, stylesheets)
- **Dippin adapter**: synthesizes implicit edges from `ParallelConfig.Targets` and `FanInConfig.Sources`
- **Dippin adapter**: maps `model`/`provider` to `llm_model`/`llm_provider` (matches codergen handler)
- **Dippin adapter**: maps new IR fields: `BaseDelay`, `NodeIO`, `OnResume`, `BranchConfig`, `Stylesheet`, `Goal`
- **TUI activity log**: rewritten with line-level styling (no glamour); append-only, stable viewport
- **TUI human input**: replaced single-line buffer with `bubbles/textarea` (wrapping, multiline, Ctrl+S submit)
- **Validate command**: registers all handler types (`wait.human`, `parallel`, `parallel.fan_in`, `manager_loop`)

### Fixed

- **Condition evaluator**: resolves `ctx.` namespace prefix (dippin IR uses `ctx.outcome`, engine stores `outcome`)
- **Condition evaluator**: resolves `ctx.internal.*` and bare `internal.*` references via `GetInternal()`
- **Condition evaluator**: handles infix negation (`key not contains value`, `key not in value`)
- **Freeform human gates**: match response text against edge labels to set `PreferredLabel` for routing
- **Agent event routing**: `evt.SessionID` changed to `evt.NodeID` in TUI wiring
- **Thinking spinner**: `MsgThinkingStarted` now emitted from agent events (with nodeID) instead of global LLM trace
- **DIP104 lint rule**: checks `fallback_retry_target` (was checking wrong key `fallback_target`)
- **Activity log**: reserved stable line for activity indicator to prevent viewport shift between turns
- **Pipeline edges**: removed unconditional fallback edges that caused infinite gate/fix loops
- **Merge nodes**: route merge conflicts to human escalation instead of silently continuing

## [Previous Versions]
(No prior changelog entries - this is the initial CHANGELOG)
