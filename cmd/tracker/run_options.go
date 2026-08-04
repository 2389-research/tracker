// ABOUTME: runOptions — the explicit per-invocation config value threaded from
// ABOUTME: flag parsing (executeRun) into run/runTUI, replacing the active* package globals.
package main

import (
	"maps"

	"github.com/2389-research/tracker/pipeline"
	"github.com/2389-research/tracker/pipeline/handlers"
)

// gitPreflightCfg holds the --git / --allow-init values for a run. Consumed by
// applyGitPreflight in run()/runTUI() just after applyRunParamOverrides.
type gitPreflightCfg struct {
	policy    string
	allowInit bool
}

// runOptions is the explicit, per-invocation configuration for a single pipeline
// run. It is built once in executeRun (from the parsed runConfig plus resolved
// resume metadata) and threaded into run/runTUI and their helpers. It replaces
// the former active* package-level globals — configuration is passed, not
// smuggled through mutable package state.
type runOptions struct {
	// Invocation basics (formerly positional args on run/runTUI).
	pipelineFile string
	workdir      string
	checkpoint   string // resolved checkpoint path; "" for a new run
	format       string
	backend      string
	verbose      bool
	jsonOut      bool

	// Config formerly smuggled via active* globals.
	autopilot      autopilotCfg
	webhookGate    *webhookGateCfg // nil when no webhook gate is configured
	budget         pipeline.BudgetLimits
	params         map[string]string
	exportBundle   string
	artifactDir    string
	toolSafety     handlers.ToolHandlerConfig
	gatewayURL     string
	gatewayKind    string
	git            gitPreflightCfg
	failOnOverride bool
	resume         resumeInfo
}

// newRunOptions builds the runOptions for one run from the parsed flags (cfg)
// and the resolved resume metadata. cfg.failOnOverride is expected to already
// reflect the TRACKER_FAIL_ON_OVERRIDE env fallback (applied by executeRun
// before this is called). The params map is cloned so downstream mutation
// cannot reach the caller's runConfig.
func newRunOptions(cfg runConfig, resume resumeInfo) *runOptions {
	return &runOptions{
		pipelineFile: cfg.pipelineFile,
		workdir:      cfg.workdir,
		checkpoint:   resume.CheckpointPath,
		format:       cfg.format,
		backend:      cfg.backend,
		verbose:      cfg.verbose,
		jsonOut:      cfg.jsonOut,
		autopilot:    autopilotCfg{persona: cfg.autopilot, autoApprove: cfg.autoApprove},
		webhookGate:  buildWebhookGateConfig(cfg),
		budget: pipeline.BudgetLimits{
			MaxTotalTokens: cfg.maxTokens,
			MaxCostCents:   cfg.maxCostCents,
			MaxWallTime:    cfg.maxWallTime,
			SleepAware:     cfg.sleepAware,
		},
		params:       maps.Clone(cfg.params),
		exportBundle: cfg.exportBundle,
		artifactDir:  cfg.artifactDir,
		toolSafety: handlers.ToolHandlerConfig{
			BypassDenylist: cfg.bypassDenylist,
			Allowlist:      append([]string(nil), cfg.toolAllowlist...),
			DenylistAdd:    append([]string(nil), cfg.toolDenylistAdd...),
			MaxOutputLimit: cfg.maxOutputLimit,
		},
		gatewayURL:     cfg.gatewayURL,
		gatewayKind:    cfg.gatewayKind,
		git:            gitPreflightCfg{policy: cfg.git, allowInit: cfg.allowInit},
		failOnOverride: cfg.failOnOverride,
		resume:         resume,
	}
}
