// ABOUTME: Interviewer resolution — selects the human-gate handler from Config.
// ABOUTME: Split out of tracker.go so the root file stays focused on the API surface (#450).
package tracker

import (
	"fmt"
	"log"

	"github.com/2389-research/tracker/agent"
	"github.com/2389-research/tracker/llm"
	"github.com/2389-research/tracker/pipeline/handlers"
)

// resolveInterviewer selects an interviewer based on Config.
// Returns nil if no automation is configured (interactive/default mode).
// Priority: Interviewer > AutoApprove > WebhookGate > Autopilot.
// When Backend is "claude-code", prefers ClaudeCodeAutopilotInterviewer.
//
// The return type is the base handlers.Interviewer: an injected Config.Interviewer
// may implement only Ask, and the human handler upgrades to the freeform/interview
// modes via type assertion. The automated interviewers below all satisfy the
// richer interfaces regardless.
func resolveInterviewer(cfg Config, client *llm.Client, completer agent.Completer) (handlers.Interviewer, error) {
	if cfg.Interviewer != nil {
		return cfg.Interviewer, nil
	}
	if cfg.AutoApprove {
		return &handlers.AutoApproveFreeformInterviewer{}, nil
	}
	if cfg.WebhookGate != nil {
		return resolveWebhookInterviewer(cfg.WebhookGate)
	}
	if cfg.Autopilot == "" {
		return nil, nil
	}
	return resolveAutopilot(cfg, client, completer)
}

// resolveWebhookInterviewer creates a WebhookInterviewer from a WebhookGateConfig.
// Returns an error if WebhookURL is not set.
func resolveWebhookInterviewer(wgc *WebhookGateConfig) (handlers.FreeformInterviewer, error) {
	if wgc.WebhookURL == "" {
		return nil, fmt.Errorf("WebhookGate.WebhookURL is required")
	}
	addr := wgc.CallbackAddr
	if addr == "" {
		addr = ":8789"
	}
	wi := handlers.NewWebhookInterviewer(wgc.WebhookURL, addr)
	if wgc.Timeout > 0 {
		wi.Timeout = wgc.Timeout
	}
	if wgc.TimeoutAction != "" {
		wi.DefaultAction = wgc.TimeoutAction
	}
	if wgc.AuthHeader != "" {
		wi.AuthHeader = wgc.AuthHeader
	}
	if wgc.RunID != "" {
		wi.RunID = wgc.RunID
	}
	return wi, nil
}

// resolveAutopilot builds an autopilot interviewer for the given persona and backend.
func resolveAutopilot(cfg Config, client *llm.Client, completer agent.Completer) (handlers.FreeformInterviewer, error) {
	persona, err := handlers.ParsePersona(cfg.Autopilot)
	if err != nil {
		return nil, fmt.Errorf("invalid autopilot persona %q: %w", cfg.Autopilot, err)
	}
	if cfg.Backend == "claude-code" {
		if iv, ccErr := handlers.NewClaudeCodeAutopilotInterviewer(persona); ccErr == nil {
			return iv, nil
		}
		log.Printf("[tracker] claude-code autopilot init failed, trying native")
	}
	client = resolveAutopilotClient(client, completer)
	if client == nil {
		return nil, fmt.Errorf("autopilot %q requires an LLM client (set Config.LLMClient or configure API keys)", cfg.Autopilot)
	}
	return handlers.NewAutopilotInterviewer(client, persona), nil
}

// resolveAutopilotClient returns the LLM client for native autopilot,
// trying a type assertion on completer if client is nil.
func resolveAutopilotClient(client *llm.Client, completer agent.Completer) *llm.Client {
	if client != nil {
		return client
	}
	if lc, ok := completer.(*llm.Client); ok {
		return lc
	}
	return nil
}
