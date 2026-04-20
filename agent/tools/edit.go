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
		return "", fmt.Errorf("old_string not found in %s\n\n%s\n\nHint: the file may have changed since you last read it — re-read with the read tool before retrying", path, nearbyContext(content, oldString))
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

// nearbyContext finds the closest matching region in content to help the agent understand
// what the file actually contains near where old_string was expected.
func nearbyContext(content, oldString string) string {
	lines := strings.Split(content, "\n")

	// Find the first non-empty line of old_string to use as a search anchor.
	firstLine := ""
	for _, l := range strings.Split(oldString, "\n") {
		if strings.TrimSpace(l) != "" {
			firstLine = l
			break
		}
	}

	// Locate the anchor line in the file to find the closest region.
	anchorLine := -1
	if firstLine != "" {
		for i, l := range lines {
			if strings.Contains(l, firstLine) {
				anchorLine = i
				break
			}
		}
	}

	const contextRadius = 5
	const fallbackLines = 20

	var start, end int
	if anchorLine >= 0 {
		start = anchorLine - contextRadius
		if start < 0 {
			start = 0
		}
		end = anchorLine + contextRadius + 1
		if end > len(lines) {
			end = len(lines)
		}
	} else {
		// No anchor found: show the first fallbackLines lines.
		start = 0
		end = len(lines)
		if end > fallbackLines {
			end = fallbackLines
		}
	}

	var sb strings.Builder
	sb.WriteString("Closest content near expected location:\n")
	for i := start; i < end; i++ {
		fmt.Fprintf(&sb, "%4d: %s\n", i+1, lines[i])
	}
	return sb.String()
}

// CachePolicy declares that edit is mutating and invalidates caches.
func (t *EditTool) CachePolicy() CachePolicy { return CachePolicyMutating }
