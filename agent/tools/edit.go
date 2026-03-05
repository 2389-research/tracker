// ABOUTME: EditFile tool performs exact search/replace on files in the working directory.
// ABOUTME: Requires unique match of old_string. Empty old_string with missing file creates a new file.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/2389-research/mammoth-lite/agent/exec"
)

// EditTool implements the Tool interface for search/replace editing of files.
type EditTool struct {
	env exec.ExecutionEnvironment
}

// NewEditTool creates an EditTool backed by the given execution environment.
func NewEditTool(env exec.ExecutionEnvironment) *EditTool {
	return &EditTool{env: env}
}

func (t *EditTool) Name() string { return "edit" }

func (t *EditTool) Description() string {
	return "Edit a file by replacing an exact string match with new content. " +
		"The old_string must match exactly one location in the file. " +
		"If old_string is empty and the file does not exist, the file is created with new_string as content."
}

func (t *EditTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {
				"type": "string",
				"description": "The relative path of the file to edit."
			},
			"old_string": {
				"type": "string",
				"description": "The exact string to find and replace. Must match exactly once."
			},
			"new_string": {
				"type": "string",
				"description": "The replacement string."
			}
		},
		"required": ["path", "old_string", "new_string"]
	}`)
}

func (t *EditTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	var params struct {
		Path      string `json:"path"`
		OldString string `json:"old_string"`
		NewString string `json:"new_string"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if params.Path == "" {
		return "", fmt.Errorf("path is required")
	}

	content, readErr := t.env.ReadFile(ctx, params.Path)

	// When old_string is empty, create a new file or prepend to existing.
	if params.OldString == "" {
		if readErr != nil {
			if err := t.env.WriteFile(ctx, params.Path, params.NewString); err != nil {
				return "", err
			}
			return fmt.Sprintf("created %s", params.Path), nil
		}
		newContent := params.NewString + content
		if err := t.env.WriteFile(ctx, params.Path, newContent); err != nil {
			return "", err
		}
		return fmt.Sprintf("prepended to %s", params.Path), nil
	}

	if readErr != nil {
		return "", fmt.Errorf("cannot read file: %w", readErr)
	}

	count := strings.Count(content, params.OldString)
	if count == 0 {
		return "", fmt.Errorf("old_string not found in %s", params.Path)
	}
	if count > 1 {
		return "", fmt.Errorf("old_string found %d times in %s (must be unique)", count, params.Path)
	}

	newContent := strings.Replace(content, params.OldString, params.NewString, 1)
	if err := t.env.WriteFile(ctx, params.Path, newContent); err != nil {
		return "", err
	}

	return fmt.Sprintf("edited %s", params.Path), nil
}
