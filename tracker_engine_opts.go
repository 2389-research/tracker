// ABOUTME: buildEngineOpts and its persistence helper: the Engine options a Config
// ABOUTME: implies (events, steering, budgets, bundle identity, checkpoints).
package tracker

import "github.com/2389-research/tracker/pipeline"

// buildEngineOpts constructs engine options from Config. When a config
// budget field is zero, buildEngineOpts falls back to the matching
// graph-level attr (max_total_tokens, max_cost_cents, max_wall_time)
// populated by the adapter from dippin WorkflowDefaults. Explicit
// Config.Budget values always win over the workflow fallback.
func buildEngineOpts(cfg Config, graph *pipeline.Graph) []pipeline.EngineOption {
	opts := appendPersistenceOpts(nil, cfg)
	if cfg.EventHandler != nil {
		opts = append(opts, pipeline.WithPipelineEventHandler(cfg.EventHandler))
	}
	if cfg.SteeringChan != nil {
		opts = append(opts, pipeline.WithSteeringChan(cfg.SteeringChan))
	}
	if len(cfg.Context) > 0 {
		opts = append(opts, pipeline.WithInitialContext(cfg.Context))
	}
	budget := ResolveBudgetLimits(cfg.Budget, graph)
	warnSubscriptionCeilings(budget, graph)
	if guard := pipeline.NewBudgetGuard(budget); guard != nil {
		opts = append(opts, pipeline.WithBudgetGuard(guard))
	}
	if cfg.BundleIdentity != "" {
		opts = append(opts, pipeline.WithBundleIdentity(cfg.BundleIdentity))
	}
	if cfg.ResumeFrom != "" || cfg.ResumeExact {
		opts = append(opts, pipeline.WithResumePolicy(pipeline.ResumePolicy{From: cfg.ResumeFrom, NoRewind: cfg.ResumeExact}))
	}
	opts = append(opts, pipeline.WithStylesheetResolution(true))
	return opts
}

// appendPersistenceOpts adds the checkpoint/artifact engine options derived from
// Config. GitArtifacts is a no-op in the engine unless ArtifactDir is also set.
func appendPersistenceOpts(opts []pipeline.EngineOption, cfg Config) []pipeline.EngineOption {
	if cfg.CheckpointDir != "" {
		opts = append(opts, pipeline.WithCheckpointPath(cfg.CheckpointDir))
	}
	if cfg.ArtifactDir != "" {
		opts = append(opts, pipeline.WithArtifactDir(cfg.ArtifactDir))
	}
	if cfg.GitArtifacts {
		opts = append(opts, pipeline.WithGitArtifacts(true))
	}
	return opts
}
