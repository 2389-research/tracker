// ABOUTME: NativeBackend wraps agent.Session to implement the AgentBackend interface.
// ABOUTME: Translates AgentRunConfig into SessionConfig and forwards events via the emit callback.
package handlers

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/2389-research/tracker/agent"
	execpkg "github.com/2389-research/tracker/agent/exec"
	"github.com/2389-research/tracker/agent/tools"
	"github.com/2389-research/tracker/pipeline"
)

// NativeBackend implements pipeline.AgentBackend using the built-in agent.Session.
type NativeBackend struct {
	client agent.Completer
	env    execpkg.ExecutionEnvironment
}

// NewNativeBackend creates a NativeBackend that runs agent sessions with the
// given LLM completer and execution environment.
func NewNativeBackend(client agent.Completer, env execpkg.ExecutionEnvironment) *NativeBackend {
	return &NativeBackend{
		client: client,
		env:    env,
	}
}

// Run builds a SessionConfig from the AgentRunConfig, creates an agent.Session,
// and executes the agentic loop. Events are forwarded to the emit callback.
// If cfg.Extra contains an *agent.SessionConfig (set by CodergenHandler), it is
// used directly to preserve all config fields (reasoning effort, caching, etc.).
func (b *NativeBackend) Run(ctx context.Context, cfg pipeline.AgentRunConfig, emit func(agent.Event)) (agent.SessionResult, error) {
	sessionCfg := b.buildSessionConfig(cfg)

	// writable_paths fs-jail (#272): when the session config declares it,
	// build a fresh *LocalEnvironment so the per-session jail hooks don't
	// leak into the shared b.env. configureJail also refuses-to-start when
	// the backend, working_dir, paths, or kernel support are bad.
	env := b.env
	if sessionCfg.WritablePathsSet {
		localEnv, ok := b.env.(*execpkg.LocalEnvironment)
		if !ok {
			return agent.SessionResult{}, fmt.Errorf("writable_paths requires a *LocalEnvironment exec environment; got %T (issue #272)", b.env)
		}
		processCwd, err := os.Getwd()
		if err != nil {
			return agent.SessionResult{}, fmt.Errorf("get tracker cwd for writable_paths jail: %w", err)
		}
		// Fresh env rooted at the session's working_dir when set, falling
		// back to the existing backend env's WorkingDir, then processCwd.
		// Using the session's working_dir respects per-node overrides so
		// cmd.Dir and the jail anchor stay aligned (#272 review,
		// coderabbitai backend_native.go:57). The fallback to the backend
		// env's WorkingDir preserves the effective working dir for callers
		// that set AgentRunConfig.WorkingDir but leave SessionConfig.WorkingDir
		// empty — without it, filepath.Join(processCwd, "") would silently
		// relocate the session to tracker's cwd (#275 review, Copilot
		// backend_native.go:62).
		jailedWorkDir := sessionCfg.WorkingDir
		if jailedWorkDir == "" {
			jailedWorkDir = localEnv.WorkingDir()
		}
		if !filepath.IsAbs(jailedWorkDir) {
			jailedWorkDir = filepath.Join(processCwd, jailedWorkDir)
		}
		// Keep SessionConfig.WorkingDir in sync with the resolved anchor so
		// configureJail validates against the same path the env is rooted
		// at — without this, an empty SessionConfig.WorkingDir would make
		// configureJail anchor on processCwd while the env runs from the
		// (potentially different) backend WorkingDir.
		sessionCfg.WorkingDir = jailedWorkDir
		jailedEnv := execpkg.NewLocalEnvironment(jailedWorkDir)
		if _, err := configureJail(&sessionCfg, jailedEnv, processCwd); err != nil {
			return agent.SessionResult{}, err
		}
		env = jailedEnv
	}

	handler := agent.EventHandlerFunc(func(evt agent.Event) {
		emit(evt)
	})

	opts := []agent.SessionOption{
		agent.WithEventHandler(handler),
	}
	if env != nil {
		opts = append(opts, agent.WithEnvironment(env))
	}

	// Register generate_code tool if a cheap model is configured via env.
	if cheapModel := os.Getenv("TRACKER_CODEGEN_MODEL"); cheapModel != "" {
		cheapProvider := os.Getenv("TRACKER_CODEGEN_PROVIDER")
		if cheapProvider == "" {
			cheapProvider = "openai"
		}
		genOpts := []tools.GenerateCodeOption{
			tools.WithGenerateModel(cheapModel),
			tools.WithGenerateProvider(cheapProvider),
		}
		workDir := sessionCfg.WorkingDir
		if workDir == "" {
			workDir = cfg.WorkingDir
		}
		if workDir != "" {
			genOpts = append(genOpts, tools.WithGenerateWorkDir(workDir))
		}
		opts = append(opts, agent.WithTools(tools.NewGenerateCodeTool(b.client, genOpts...)))
	}

	// Register write_enriched_sprint tool if a sprint-writer model is configured via env.
	if sprintModel := os.Getenv("TRACKER_SPRINT_WRITER_MODEL"); sprintModel != "" {
		sprintProvider := os.Getenv("TRACKER_SPRINT_WRITER_PROVIDER")
		if sprintProvider == "" {
			sprintProvider = "anthropic"
		}
		swOpts := []tools.WriteEnrichedSprintOption{
			tools.WithSprintWriterModel(sprintModel),
			tools.WithSprintWriterProvider(sprintProvider),
		}
		workDir := sessionCfg.WorkingDir
		if workDir == "" {
			workDir = cfg.WorkingDir
		}
		if workDir != "" {
			swOpts = append(swOpts, tools.WithSprintWriterWorkDir(workDir))
		}
		writer := tools.NewWriteEnrichedSprintTool(b.client, swOpts...)
		opts = append(opts, agent.WithTools(writer))
		opts = append(opts, agent.WithTools(tools.NewDispatchSprintsTool(writer, workDir)))
	}

	sess, err := agent.NewSession(b.client, sessionCfg, opts...)
	if err != nil {
		return agent.SessionResult{}, err
	}

	return sess.Run(ctx, cfg.Prompt)
}

// buildSessionConfig returns the SessionConfig to use for a run.
// If cfg.Extra carries a pre-built *agent.SessionConfig it is used directly;
// otherwise a default config is built from the AgentRunConfig fields.
//
// tool_access enforcement (issue #258): regardless of whether Extra carries
// a pre-built SessionConfig or we build fresh, the directive on the
// AgentRunConfig must end up on the SessionConfig — direct callers (tests,
// integrators) that construct AgentRunConfig manually would otherwise
// bypass enforcement because Extra is nil and applyRunConfigOverrides
// previously dropped the field.
func (b *NativeBackend) buildSessionConfig(cfg pipeline.AgentRunConfig) agent.SessionConfig {
	if sc, ok := cfg.Extra.(*agent.SessionConfig); ok && sc != nil {
		out := *sc
		// Inherit cfg.ToolAccess whenever the pre-built SessionConfig
		// is not already restricted under the canonical (whitespace-
		// trimmed) check. Using `out.ToolAccess == ""` alone would
		// treat a whitespace-only value like " " as "set" — but
		// IsToolAccessRestricted considers it unrestricted, so the
		// AgentRunConfig directive should override.
		if cfg.ToolAccess != "" && !out.IsToolAccessRestricted() {
			out.ToolAccess = cfg.ToolAccess
		}
		return out
	}
	return applyRunConfigOverrides(agent.DefaultConfig(), cfg)
}

// applyRunConfigOverrides copies non-zero AgentRunConfig fields onto base.
func applyRunConfigOverrides(base agent.SessionConfig, cfg pipeline.AgentRunConfig) agent.SessionConfig {
	if cfg.Model != "" {
		base.Model = cfg.Model
	}
	if cfg.Provider != "" {
		base.Provider = cfg.Provider
	}
	if cfg.MaxTurns > 0 {
		base.MaxTurns = cfg.MaxTurns
	}
	if cfg.SystemPrompt != "" {
		base.SystemPrompt = cfg.SystemPrompt
	}
	if cfg.WorkingDir != "" {
		base.WorkingDir = cfg.WorkingDir
	}
	// tool_access enforcement (issue #258): propagate the directive to the
	// SessionConfig so direct AgentRunConfig callers (not just CodergenHandler)
	// get the empty-registry / ToolChoice=none defenses.
	if cfg.ToolAccess != "" {
		base.ToolAccess = cfg.ToolAccess
	}
	return base
}
