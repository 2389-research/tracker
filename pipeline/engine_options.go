// ABOUTME: The functional options (With...) that configure an Engine, split out of
// ABOUTME: engine.go to keep it inside the file-size ratchet; EngineOption stays there.
package pipeline

// WithPipelineEventHandler sets the event handler for pipeline lifecycle events.
func WithPipelineEventHandler(h PipelineEventHandler) EngineOption {
	return func(e *Engine) {
		e.eventHandler = h
	}
}

// WithCheckpointPath enables checkpoint save/resume at the given file path.
func WithCheckpointPath(path string) EngineOption {
	return func(e *Engine) {
		e.checkpointPath = path
	}
}

// WithStylesheetResolution enables model stylesheet resolution on nodes before execution.
func WithStylesheetResolution(enabled bool) EngineOption {
	return func(e *Engine) {
		e.resolveStylesheet = enabled
	}
}

// WithArtifactDir sets the base dir for run artifacts (<artifactDir>/<nodeID>/).
func WithArtifactDir(dir string) EngineOption {
	return func(e *Engine) {
		e.artifactDir = dir
	}
}

// WithInitialContext pre-populates the pipeline context with the given values.
// Used by subgraph execution to pass parent context into child pipelines.
func WithInitialContext(ctx map[string]string) EngineOption {
	return func(e *Engine) {
		e.initialContext = ctx
	}
}

// WithBudgetGuard attaches a BudgetGuard evaluated after every terminal
// node outcome. Nil guards are no-ops.
func WithBudgetGuard(guard *BudgetGuard) EngineOption {
	return func(e *Engine) { e.budgetGuard = guard }
}

// WithBaselineUsage pre-loads the engine's BudgetGuard with usage already
// consumed by a parent run. Used by subgraph execution so the child's guard
// check sees parent spend + child trace combined, preventing the "subgraph
// sandbox" escape where an operator's --max-tokens / --max-cost ceiling
// would otherwise be silently non-binding for nodes nested in a subgraph.
// Nil baselines are no-ops.
func WithBaselineUsage(baseline *UsageSummary) EngineOption {
	return func(e *Engine) { e.baselineUsage = baseline }
}

// WithGitArtifacts enables git-backed artifact tracking. When enabled, the
// artifact dir is initialized as a git repo at run start, and each terminal
// node outcome produces one commit capturing the artifact state at that
// point. Checkpoint saves made via saveCheckpointWithTag (not all
// saveCheckpoint call sites) also create a lightweight git tag of the form
// checkpoint/<runID>/<nodeID> pointing at the most recent node-outcome commit,
// intended as the basis for future checkpoint-replay support (Layer 2 of
// issue #77 — not wired up by this option).
//
// Requires git in PATH. Silently no-ops if artifactDir is not set.
func WithGitArtifacts(enabled bool) EngineOption {
	return func(e *Engine) { e.gitArtifacts = enabled }
}

// WithSteeringChan provides a channel for injecting context updates into the
// pipeline between node executions. The engine drains pending updates after
// each node's outcome is applied, making steered values visible to edge
// selection and the next node's prompt expansion. Nil channels are no-ops.
func WithSteeringChan(ch <-chan map[string]string) EngineOption {
	return func(e *Engine) { e.steeringCh = ch }
}

// WithBundleIdentity stamps every PipelineEvent the engine emits with the
// given content-addressed identity string (typically "sha256:<hex>"). Used
// to thread .dipx bundle identity into the activity log so every line of
// activity.jsonl carries provenance. Empty string (the default) is a no-op
// and matches the behavior for plain .dip runs.
func WithBundleIdentity(id string) EngineOption {
	return func(e *Engine) { e.bundleIdentity = id }
}

// WithAnswerCommandFlags sets the -w / --artifact-dir flags the gate_paused
// event's `tracker answer` command carries, so it works from any directory.
func WithAnswerCommandFlags(flags string) EngineOption {
	return func(e *Engine) { e.answerFlags = flags }
}
