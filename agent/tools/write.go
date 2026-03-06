// ABOUTME: WriteFile tool creates or overwrites a file in the working directory.
// ABOUTME: Accepts path and content parameters, creates parent directories as needed.
package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/2389-research/tracker/agent/exec"
)

// WriteTool implements the Tool interface for writing file contents.
type WriteTool struct {
	env exec.ExecutionEnvironment
}

// NewWriteTool creates a WriteTool backed by the given execution environment.
func NewWriteTool(env exec.ExecutionEnvironment) *WriteTool {
	return &WriteTool{env: env}
}

func (t *WriteTool) Name() string { return "write" }

func (t *WriteTool) Description() string {
	return "Create or overwrite a file with the given content. Creates parent directories as needed."
}

func (t *WriteTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {
				"type": "string",
				"description": "The relative path of the file to write."
			},
			"content": {
				"type": "string",
				"description": "The content to write to the file."
			}
		},
		"required": ["path", "content"]
	}`)
}

func (t *WriteTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	var params struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if params.Path == "" {
		return "", fmt.Errorf("path is required")
	}

	if err := t.env.WriteFile(ctx, params.Path, params.Content); err != nil {
		return "", err
	}

	return fmt.Sprintf("wrote %d bytes to %s", len(params.Content), params.Path), nil
}
