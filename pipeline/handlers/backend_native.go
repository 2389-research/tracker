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
	"github.com/2389-research/tracker/agent/tools/sprintwriter"
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

	env, err := b.resolveRunEnv(&sessionCfg)
	if err != nil {
		return agent.SessionResult{}, err
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

	opts = appendCodegenTools(opts, b.client, sessionCfg, cfg, env)
	opts = appendSprintWriterTools(opts, b.client, sessionCfg, cfg, env)

	sess, err := agent.NewSession(b.client, sessionCfg, opts...)
	if err != nil {
		return agent.SessionResult{}, err
	}

	return sess.Run(ctx, cfg.Prompt)
}

// resolveRunEnv returns the execution environment for this run. Without a
// writable_paths declaration it is the shared backend env. When the session
// config declares writable_paths (#272), it builds a fresh *LocalEnvironment
// so the per-session jail hooks don't leak into the shared b.env, keeps
// sessionCfg.WorkingDir in sync with the resolved jail anchor, and returns the
// jailed env. configureJail also refuses-to-start when the backend,
// working_dir, paths, or kernel support are bad.
func (b *NativeBackend) resolveRunEnv(sessionCfg *agent.SessionConfig) (execpkg.ExecutionEnvironment, error) {
	if !sessionCfg.WritablePathsSet {
		return b.env, nil
	}
	localEnv, ok := b.env.(*execpkg.LocalEnvironment)
	if !ok {
		return nil, fmt.Errorf("writable_paths requires a *LocalEnvironment exec environment; got %T (issue #272)", b.env)
	}
	sessionRoot, err := jailSessionRoot(localEnv)
	if err != nil {
		return nil, err
	}
	jailedWorkDir := resolveJailedWorkDir(sessionCfg.WorkingDir, sessionRoot)
	// Keep SessionConfig.WorkingDir in sync with the resolved anchor so
	// configureJail validates against the same path the env is rooted at.
	sessionCfg.WorkingDir = jailedWorkDir
	jailedEnv := execpkg.NewLocalEnvironment(jailedWorkDir)
	if _, err := configureJail(sessionCfg, jailedEnv, sessionRoot); err != nil {
		return nil, err
	}
	return jailedEnv, nil
}

// jailSessionRoot resolves the "session root" for jail validation: the backend
// env's WorkingDir (the resolved --workdir flag, or AgentRunConfig.WorkingDir
// when set). Process CWD is wherever the user happened to invoke tracker from;
// using it as the validation base would either let a node-level working_dir
// relocate the jail anchor outside the session root (escape) or reject valid
// absolute --workdir values that sit outside the user's shell cwd (#275
// review, Copilot backend_native.go:78). Falls back to the process CWD only
// when the env has no WorkingDir.
func jailSessionRoot(localEnv *execpkg.LocalEnvironment) (string, error) {
	if sessionRoot := localEnv.WorkingDir(); sessionRoot != "" {
		return sessionRoot, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get tracker cwd for writable_paths jail: %w", err)
	}
	return cwd, nil
}

// resolveJailedWorkDir picks the fresh env's root: the session's working_dir
// when set (respecting per-node overrides so cmd.Dir and the jail anchor stay
// aligned — #272 review, coderabbitai backend_native.go:57), else the session
// root. Empty workDir defers to sessionRoot rather than filepath.Join(root,
// "") which silently relocates (#275 review, Copilot backend_native.go:62).
func resolveJailedWorkDir(workDir, sessionRoot string) string {
	if workDir == "" {
		workDir = sessionRoot
	}
	if !filepath.IsAbs(workDir) {
		workDir = filepath.Join(sessionRoot, workDir)
	}
	return workDir
}

// runWorkDir resolves the tool working directory: the session's WorkingDir,
// falling back to the run config's.
func runWorkDir(sessionCfg agent.SessionConfig, cfg pipeline.AgentRunConfig) string {
	if sessionCfg.WorkingDir != "" {
		return sessionCfg.WorkingDir
	}
	return cfg.WorkingDir
}

// appendCodegenTools registers the generate_code tool when a cheap model is
// configured via TRACKER_CODEGEN_MODEL; otherwise returns opts unchanged.
func appendCodegenTools(opts []agent.SessionOption, client agent.Completer, sessionCfg agent.SessionConfig, cfg pipeline.AgentRunConfig, env execpkg.ExecutionEnvironment) []agent.SessionOption {
	cheapModel := os.Getenv("TRACKER_CODEGEN_MODEL")
	if cheapModel == "" {
		return opts
	}
	cheapProvider := os.Getenv("TRACKER_CODEGEN_PROVIDER")
	if cheapProvider == "" {
		cheapProvider = "openai"
	}
	genOpts := []tools.GenerateCodeOption{
		tools.WithGenerateModel(cheapModel),
		tools.WithGenerateProvider(cheapProvider),
		// Route writes through the resolved env so the writable_paths fs-jail
		// (#272) intercepts generated files alongside Write / Edit /
		// ApplyPatch. `env` here is the JAILED env when
		// sessionCfg.WritablePathsSet — see resolveRunEnv. #275 audit pass.
		tools.WithGenerateEnv(env),
	}
	if workDir := runWorkDir(sessionCfg, cfg); workDir != "" {
		genOpts = append(genOpts, tools.WithGenerateWorkDir(workDir))
	}
	return append(opts, agent.WithTools(tools.NewGenerateCodeTool(client, genOpts...)))
}

// appendSprintWriterTools registers the write_enriched_sprint + dispatch_sprints
// tools when a sprint-writer model is configured via TRACKER_SPRINT_WRITER_MODEL;
// otherwise returns opts unchanged.
func appendSprintWriterTools(opts []agent.SessionOption, client agent.Completer, sessionCfg agent.SessionConfig, cfg pipeline.AgentRunConfig, env execpkg.ExecutionEnvironment) []agent.SessionOption {
	sprintModel := os.Getenv("TRACKER_SPRINT_WRITER_MODEL")
	if sprintModel == "" {
		return opts
	}
	sprintProvider := os.Getenv("TRACKER_SPRINT_WRITER_PROVIDER")
	if sprintProvider == "" {
		sprintProvider = "anthropic"
	}
	swOpts := []sprintwriter.WriteEnrichedSprintOption{
		sprintwriter.WithSprintWriterModel(sprintModel),
		sprintwriter.WithSprintWriterProvider(sprintProvider),
		// Same routing rationale as the generate_code tool above (#275 audit pass).
		sprintwriter.WithSprintWriterEnv(env),
	}
	workDir := runWorkDir(sessionCfg, cfg)
	if workDir != "" {
		swOpts = append(swOpts, sprintwriter.WithSprintWriterWorkDir(workDir))
	}
	writer := sprintwriter.NewWriteEnrichedSprintTool(client, swOpts...)
	opts = append(opts, agent.WithTools(writer))
	return append(opts, agent.WithTools(sprintwriter.NewDispatchSprintsTool(writer, workDir)))
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
