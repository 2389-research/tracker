// ABOUTME: NativeBackend wraps agent.Session to implement the AgentBackend interface.
// ABOUTME: Translates AgentRunConfig into SessionConfig and forwards events via the emit callback.
package handlers

import (
	"context"

	"github.com/2389-research/tracker/agent"
	"github.com/2389-research/tracker/agent/exec"
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
	var sessionCfg agent.SessionConfig

	if sc, ok := cfg.Extra.(*agent.SessionConfig); ok && sc != nil {
		sessionCfg = *sc
	} else {
		sessionCfg = agent.DefaultConfig()
		if cfg.Model != "" {
			sessionCfg.Model = cfg.Model
		}
		if cfg.Provider != "" {
			sessionCfg.Provider = cfg.Provider
		}
		if cfg.MaxTurns > 0 {
			sessionCfg.MaxTurns = cfg.MaxTurns
		}
		if cfg.SystemPrompt != "" {
			sessionCfg.SystemPrompt = cfg.SystemPrompt
		}
		if cfg.WorkingDir != "" {
			sessionCfg.WorkingDir = cfg.WorkingDir
		}
	}

	handler := agent.EventHandlerFunc(func(evt agent.Event) {
		emit(evt)
	})

	opts := []agent.SessionOption{
		agent.WithEventHandler(handler),
	}
	if b.env != nil {
		opts = append(opts, agent.WithEnvironment(b.env))
	}

	sess, err := agent.NewSession(b.client, sessionCfg, opts...)
	if err != nil {
		return agent.SessionResult{}, err
	}

	return sess.Run(ctx, cfg.Prompt)
}
