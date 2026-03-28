// ABOUTME: Claude-code-backed autopilot interviewer for gate decisions.
// ABOUTME: Routes autopilot decisions through the claude CLI subprocess instead of direct API calls.
package handlers

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
	// parseDecision, matchChoice, personaPrompts, buildUserPrompt
	// are reused from autopilot.go in this package.
)

// ClaudeCodeAutopilotInterviewer implements LabeledFreeformInterviewer by
// spawning lightweight claude CLI subprocesses for gate decisions. This avoids
// requiring a funded ANTHROPIC_API_KEY — the subprocess uses the user's
// Max/Pro subscription OAuth instead.
type ClaudeCodeAutopilotInterviewer struct {
	persona    Persona
	claudePath string
}

// NewClaudeCodeAutopilotInterviewer creates an autopilot interviewer that
// routes decisions through the claude CLI.
func NewClaudeCodeAutopilotInterviewer(persona Persona) (*ClaudeCodeAutopilotInterviewer, error) {
	path, err := resolveClaudePath()
	if err != nil {
		return nil, fmt.Errorf("claude-code autopilot: %w", err)
	}
	return &ClaudeCodeAutopilotInterviewer{
		persona:    persona,
		claudePath: path,
	}, nil
}

// Ask handles choice-mode gates by selecting from the given options.
func (a *ClaudeCodeAutopilotInterviewer) Ask(prompt string, choices []string, defaultChoice string) (string, error) {
	return a.decide(prompt, choices, defaultChoice)
}

// AskFreeform handles pure freeform gates by generating a text response.
func (a *ClaudeCodeAutopilotInterviewer) AskFreeform(prompt string) (string, error) {
	decision, err := a.callClaude(prompt, nil, "")
	if err != nil {
		fmt.Fprintf(os.Stderr, "WARNING: claude-code autopilot freeform call failed (%v), using 'auto-approved'\n", err)
		return "auto-approved", nil
	}
	if decision.Reasoning != "" {
		return decision.Reasoning, nil
	}
	return decision.Choice, nil
}

// AskFreeformWithLabels handles hybrid gates with labeled edges.
func (a *ClaudeCodeAutopilotInterviewer) AskFreeformWithLabels(prompt string, labels []string, defaultLabel string) (string, error) {
	return a.decide(prompt, labels, defaultLabel)
}

// decide is the core decision-making logic.
func (a *ClaudeCodeAutopilotInterviewer) decide(prompt string, options []string, defaultOption string) (string, error) {
	decision, err := a.callClaude(prompt, options, defaultOption)
	if err != nil {
		return "", fmt.Errorf("claude-code autopilot gate decision failed: %w", err)
	}

	choice := matchChoice(decision.Choice, options)
	if choice == "" {
		fmt.Fprintf(os.Stderr, "WARNING: claude-code autopilot chose %q which doesn't match any option, using default\n", decision.Choice)
		return a.fallback(options, defaultOption), nil
	}

	return choice, nil
}

// callClaude spawns a claude CLI subprocess to make a gate decision.
func (a *ClaudeCodeAutopilotInterviewer) callClaude(prompt string, options []string, defaultOption string) (*autopilotDecision, error) {
	systemPrompt := personaPrompts[a.persona]
	userPrompt := buildUserPrompt(prompt, options, defaultOption)
	fullPrompt := systemPrompt + "\n\n" + userPrompt

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, a.claudePath,
		"--print",
		"-p", fullPrompt,
		"--max-turns", "1",
		"--output-format", "text",
	)
	cmd.Env = buildEnv()

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("claude CLI: %w (stderr: %s)", err, strings.TrimSpace(stderr.String()))
	}

	responseText := strings.TrimSpace(stdout.String())
	if responseText == "" {
		return nil, fmt.Errorf("claude CLI returned empty response")
	}

	decision, parseErr := parseDecision(responseText)
	if parseErr != nil {
		// If the response isn't valid JSON, try to extract a choice directly.
		// The claude CLI sometimes returns plain text instead of JSON.
		if len(options) > 0 {
			for _, opt := range options {
				if strings.Contains(strings.ToLower(responseText), strings.ToLower(opt)) {
					return &autopilotDecision{
						Choice:    opt,
						Reasoning: responseText,
					}, nil
				}
			}
		}
		return nil, fmt.Errorf("claude-code autopilot: %w (response: %.200s)", parseErr, responseText)
	}

	return decision, nil
}

// fallback returns the default option, or the first option, or empty string.
func (a *ClaudeCodeAutopilotInterviewer) fallback(options []string, defaultOption string) string {
	if defaultOption != "" {
		return defaultOption
	}
	if len(options) > 0 {
		return options[0]
	}
	return ""
}
