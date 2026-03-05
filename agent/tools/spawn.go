// ABOUTME: Built-in tool that spawns a child agent session for delegated subtasks.
// ABOUTME: The child runs to completion and its final text output becomes the tool result.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
)

// SessionRunner abstracts the ability to run a child agent session.
// This interface breaks the circular dependency between agent/tools and agent packages:
// the tools package defines what it needs, and the agent package provides the implementation.
type SessionRunner interface {
	RunChild(ctx context.Context, task string, systemPrompt string, maxTurns int) (string, error)
}

// SpawnAgentTool delegates a subtask to a child agent session and returns its output.
type SpawnAgentTool struct {
	runner SessionRunner
}

// NewSpawnAgentTool creates a SpawnAgentTool backed by the given SessionRunner.
func NewSpawnAgentTool(runner SessionRunner) *SpawnAgentTool {
	return &SpawnAgentTool{runner: runner}
}

func (t *SpawnAgentTool) Name() string { return "spawn_agent" }

func (t *SpawnAgentTool) Description() string {
	return "Spawn a child agent to handle a subtask. Returns the child's final text output."
}

func (t *SpawnAgentTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"task": {
				"type": "string",
				"description": "The task description for the child agent to perform."
			},
			"system_prompt": {
				"type": "string",
				"description": "Optional system prompt for the child agent session."
			},
			"max_turns": {
				"type": "integer",
				"description": "Maximum number of turns for the child session (default 10)."
			}
		},
		"required": ["task"]
	}`)
}

func (t *SpawnAgentTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	var params struct {
		Task         string `json:"task"`
		SystemPrompt string `json:"system_prompt"`
		MaxTurns     int    `json:"max_turns"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if params.Task == "" {
		return "", fmt.Errorf("task is required")
	}
	if params.MaxTurns <= 0 {
		params.MaxTurns = 10
	}

	return t.runner.RunChild(ctx, params.Task, params.SystemPrompt, params.MaxTurns)
}
