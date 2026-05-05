// ABOUTME: NativeBackend wraps agent.Session to implement the AgentBackend interface.
// ABOUTME: Translates AgentRunConfig into SessionConfig and forwards events via the emit callback.
package handlers

import (
	"context"
	"os"

	"github.com/2389-research/tracker/agent"
	"github.com/2389-research/tracker/agent/exec"
	"github.com/2389-research/tracker/agent/tools"
	"github.com/2389-research/tracker/pipeline"
)

// NativeBackend implements pipeline.AgentBackend using the built-in agent.Session.
type NativeBackend struct {
	client agent.Completer
	env    exec.ExecutionEnvironment
}

// NewNativeBackend creates a NativeBackend that runs agent sessions with the
// given LLM completer and execution environment.
func NewNativeBackend(client agent.Completer, env exec.ExecutionEnvironment) *NativeBackend {
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

	handler := agent.EventHandlerFunc(func(evt agent.Event) {
		emit(evt)
	})

	opts := []agent.SessionOption{
		agent.WithEventHandler(handler),
	}
	if b.env != nil {
		opts = append(opts, agent.WithEnvironment(b.env))
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
func (b *NativeBackend) buildSessionConfig(cfg pipeline.AgentRunConfig) agent.SessionConfig {
	if sc, ok := cfg.Extra.(*agent.SessionConfig); ok && sc != nil {
		return *sc
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
	return base
}
