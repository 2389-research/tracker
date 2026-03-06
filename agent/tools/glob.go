// ABOUTME: Glob tool searches for files matching a pattern in the working directory.
// ABOUTME: Returns matching file paths separated by newlines, or a "no matches" message.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/2389-research/tracker/agent/exec"
)

// GlobTool finds files matching a glob pattern relative to the working directory.
type GlobTool struct {
	env exec.ExecutionEnvironment
}

// NewGlobTool creates a GlobTool bound to the given execution environment.
func NewGlobTool(env exec.ExecutionEnvironment) *GlobTool {
	return &GlobTool{env: env}
}

func (t *GlobTool) Name() string { return "glob" }

func (t *GlobTool) Description() string {
	return "Search for files matching a glob pattern relative to the working directory."
}

func (t *GlobTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"pattern": {
				"type": "string",
				"description": "The glob pattern to match (e.g. '*.go', 'src/**/*.ts')."
			}
		},
		"required": ["pattern"]
	}`)
}

func (t *GlobTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	var params struct {
		Pattern string `json:"pattern"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if params.Pattern == "" {
		return "", fmt.Errorf("pattern is required")
	}

	matches, err := t.env.Glob(ctx, params.Pattern)
	if err != nil {
		return "", err
	}

	if len(matches) == 0 {
		return fmt.Sprintf("no matches for pattern %q", params.Pattern), nil
	}

	return strings.Join(matches, "\n"), nil
}
