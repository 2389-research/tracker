// ABOUTME: ReadFile tool reads the contents of a file in the working directory.
// ABOUTME: Accepts a path parameter and returns file contents as a string.
package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/2389-research/mammoth-lite/agent/exec"
)

// ReadTool implements the Tool interface for reading file contents.
type ReadTool struct {
	env exec.ExecutionEnvironment
}

// NewReadTool creates a ReadTool backed by the given execution environment.
func NewReadTool(env exec.ExecutionEnvironment) *ReadTool {
	return &ReadTool{env: env}
}

func (t *ReadTool) Name() string { return "read" }

func (t *ReadTool) Description() string {
	return "Read the contents of a file at the given path."
}

func (t *ReadTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {
				"type": "string",
				"description": "The relative path of the file to read."
			}
		},
		"required": ["path"]
	}`)
}

func (t *ReadTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	var params struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if params.Path == "" {
		return "", fmt.Errorf("path is required")
	}
	return t.env.ReadFile(ctx, params.Path)
}
