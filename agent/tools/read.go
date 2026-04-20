// ABOUTME: ReadFile tool reads the contents of a file in the working directory.
// ABOUTME: Accepts path, offset, and limit parameters and returns file contents as a string.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/2389-research/tracker/agent/exec"
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
	return "Read the contents of a file at the given path. Optionally specify offset (start line, 1-based) and limit (max lines) to read a range."
}

func (t *ReadTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"path": {
				"type": "string",
				"description": "The relative path of the file to read."
			},
			"offset": {
				"type": "integer",
				"description": "Line number to start reading from (1-based). Defaults to 1."
			},
			"limit": {
				"type": "integer",
				"description": "Maximum number of lines to return. Defaults to 0 (entire file)."
			}
		},
		"required": ["path"]
	}`)
}

func (t *ReadTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	var params struct {
		Path   string `json:"path"`
		Offset int    `json:"offset"`
		Limit  int    `json:"limit"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if params.Path == "" {
		return "", fmt.Errorf("path is required")
	}

	content, err := t.env.ReadFile(ctx, params.Path)
	if err != nil {
		return "", err
	}

	// No offset/limit requested — return full file unchanged (backward compatible).
	if params.Offset == 0 && params.Limit == 0 {
		return content, nil
	}

	// Trim trailing newline before splitting to avoid an inflated line count.
	// Most files end with "\n", and strings.Split would produce an extra empty element.
	lines := strings.Split(strings.TrimRight(content, "\n"), "\n")
	totalLines := len(lines)

	// Reject negative values to avoid slice-bounds panics.
	if params.Offset < 0 {
		return "", fmt.Errorf("offset must be >= 0, got %d", params.Offset)
	}
	if params.Limit < 0 {
		return "", fmt.Errorf("limit must be >= 0, got %d", params.Limit)
	}

	// Treat offset=0 as offset=1 (start of file).
	startLine := params.Offset
	if startLine == 0 {
		startLine = 1
	}

	if startLine > totalLines {
		return "", fmt.Errorf("offset %d is beyond end of file (%d lines)", startLine, totalLines)
	}

	// Convert to 0-based slice index.
	startIdx := startLine - 1
	endIdx := totalLines
	if params.Limit > 0 {
		remaining := totalLines - startIdx
		if params.Limit < remaining {
			endIdx = startIdx + params.Limit
		}
	}

	selected := lines[startIdx:endIdx]
	header := fmt.Sprintf("[showing lines %d-%d of %d]\n", startLine, startLine+len(selected)-1, totalLines)
	return header + strings.Join(selected, "\n"), nil
}

// CachePolicy declares that read results are cacheable for identical inputs.
func (t *ReadTool) CachePolicy() CachePolicy { return CachePolicyCacheable }
