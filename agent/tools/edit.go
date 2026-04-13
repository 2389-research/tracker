// ABOUTME: EditFile tool performs exact search/replace on files in the working directory.
// ABOUTME: Requires unique match of old_string. Empty old_string with missing file creates a new file.
package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/2389-research/tracker/agent/exec"
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

	if params.OldString == "" {
		return t.handleCreateOrReject(ctx, params.Path, params.NewString, readErr)
	}

	return t.handleReplace(ctx, params.Path, params.OldString, params.NewString, content, readErr)
}

// handleCreateOrReject handles the case where old_string is empty:
// creates a new file if it doesn't exist, or returns an error if it does.
func (t *EditTool) handleCreateOrReject(ctx context.Context, path, newString string, readErr error) (string, error) {
	if readErr != nil {
		if !errors.Is(readErr, os.ErrNotExist) {
			return "", fmt.Errorf("cannot read file: %w", readErr)
		}
		if err := t.env.WriteFile(ctx, path, newString); err != nil {
			return "", err
		}
		return fmt.Sprintf("created %s", path), nil
	}
	return "", fmt.Errorf("old_string is empty but %s already exists; provide the text to replace", path)
}

// handleReplace performs a unique string replacement in an existing file.
func (t *EditTool) handleReplace(ctx context.Context, path, oldString, newString, content string, readErr error) (string, error) {
	if readErr != nil {
		return "", fmt.Errorf("cannot read file: %w", readErr)
	}
	count := strings.Count(content, oldString)
	if count == 0 {
		return "", fmt.Errorf("old_string not found in %s", path)
	}
	if count > 1 {
		return "", fmt.Errorf("old_string found %d times in %s (must be unique)", count, path)
	}
	newContent := strings.Replace(content, oldString, newString, 1)
	if err := t.env.WriteFile(ctx, path, newContent); err != nil {
		return "", err
	}
	return fmt.Sprintf("edited %s", path), nil
}

// CachePolicy declares that edit is mutating and invalidates caches.
func (t *EditTool) CachePolicy() CachePolicy { return CachePolicyMutating }
