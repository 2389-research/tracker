// ABOUTME: GrepSearch tool searches for regex patterns in files within the working directory.
// ABOUTME: Returns matching lines formatted as filepath:linenum:content, with a configurable result limit.
package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/2389-research/tracker/agent/exec"
)

// maxGrepResults caps the number of matching lines returned to avoid overwhelming output.
const maxGrepResults = 100

// GrepSearchTool searches for regex patterns across files in the working directory.
type GrepSearchTool struct {
	env exec.ExecutionEnvironment
}

// NewGrepSearchTool creates a GrepSearchTool bound to the given execution environment.
func NewGrepSearchTool(env exec.ExecutionEnvironment) *GrepSearchTool {
	return &GrepSearchTool{env: env}
}

func (t *GrepSearchTool) Name() string { return "grep_search" }

func (t *GrepSearchTool) Description() string {
	return "Search for a regex pattern in files. Returns matching lines with file path, line number, and content."
}

func (t *GrepSearchTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"pattern": {
				"type": "string",
				"description": "The regex pattern to search for."
			},
			"path": {
				"type": "string",
				"description": "File or directory to search in, relative to working directory. Defaults to '.' (entire working directory)."
			}
		},
		"required": ["pattern"]
	}`)
}

func (t *GrepSearchTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	var params struct {
		Pattern string `json:"pattern"`
		Path    string `json:"path"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if params.Pattern == "" {
		return "", fmt.Errorf("pattern is required")
	}
	if params.Path == "" {
		params.Path = "."
	}

	re, err := regexp.Compile(params.Pattern)
	if err != nil {
		return "", fmt.Errorf("invalid regex: %w", err)
	}

	searchRoot, err := t.safePath(params.Path)
	if err != nil {
		return "", err
	}

	info, err := os.Stat(searchRoot)
	if err != nil {
		return "", fmt.Errorf("path not found: %s", params.Path)
	}

	var matches []string
	truncated := false

	if info.IsDir() {
		matches, truncated, err = t.searchDir(ctx, searchRoot, re)
	} else {
		matches, truncated, err = t.searchFile(searchRoot, re)
	}
	if err != nil {
		return "", err
	}

	if len(matches) == 0 {
		return fmt.Sprintf("no matches for pattern %q", params.Pattern), nil
	}

	result := strings.Join(matches, "\n")
	if truncated {
		result += fmt.Sprintf("\n\n(results truncated, showing first %d of more matches)", maxGrepResults)
	}
	return result, nil
}

// safePath validates that a relative path resolves inside the working directory.
func (t *GrepSearchTool) safePath(rel string) (string, error) {
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("absolute paths not allowed: %s", rel)
	}

	joined := filepath.Join(t.env.WorkingDir(), rel)
	abs, err := filepath.Abs(joined)
	if err != nil {
		return "", err
	}

	workDir := t.env.WorkingDir()
	if !strings.HasPrefix(abs, workDir+string(filepath.Separator)) && abs != workDir {
		return "", fmt.Errorf("path escapes working directory: %s", rel)
	}

	return abs, nil
}

// searchDir walks a directory recursively, searching each regular file for matches.
func (t *GrepSearchTool) searchDir(ctx context.Context, root string, re *regexp.Regexp) ([]string, bool, error) {
	var matches []string
	truncated := false

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip unreadable entries
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if info.IsDir() {
			// Skip hidden directories.
			if strings.HasPrefix(info.Name(), ".") && path != root {
				return filepath.SkipDir
			}
			return nil
		}
		// Skip binary-looking files by checking for common binary extensions.
		if isBinaryExtension(info.Name()) {
			return nil
		}

		fileMatches, fileTruncated, err := t.searchFile(path, re)
		if err != nil {
			return nil // skip unreadable files
		}
		matches = append(matches, fileMatches...)
		if fileTruncated || len(matches) >= maxGrepResults {
			truncated = true
			matches = matches[:min(len(matches), maxGrepResults)]
			return fmt.Errorf("limit reached")
		}
		return nil
	})

	// "limit reached" is our sentinel, not a real error.
	if err != nil && err.Error() != "limit reached" && ctx.Err() == nil {
		return matches, truncated, err
	}

	return matches, truncated, nil
}

// searchFile scans a single file for lines matching the regex.
func (t *GrepSearchTool) searchFile(absPath string, re *regexp.Regexp) ([]string, bool, error) {
	f, err := os.Open(absPath)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = f.Close() }()

	relPath, err := filepath.Rel(t.env.WorkingDir(), absPath)
	if err != nil {
		relPath = absPath
	}

	var matches []string
	truncated := false
	scanner := bufio.NewScanner(f)
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := scanner.Text()
		if re.MatchString(line) {
			matches = append(matches, fmt.Sprintf("%s:%d:%s", relPath, lineNum, line))
			if len(matches) >= maxGrepResults {
				truncated = true
				break
			}
		}
	}

	return matches, truncated, scanner.Err()
}

// isBinaryExtension returns true for file extensions that are likely binary.
func isBinaryExtension(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	binaryExts := map[string]bool{
		".exe": true, ".bin": true, ".so": true, ".dylib": true, ".dll": true,
		".o": true, ".a": true, ".obj": true,
		".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".bmp": true, ".ico": true,
		".zip": true, ".tar": true, ".gz": true, ".bz2": true, ".xz": true, ".7z": true,
		".pdf": true, ".wasm": true,
	}
	return binaryExts[ext]
}

// CachePolicy declares that grep results are cacheable for identical inputs.
func (t *GrepSearchTool) CachePolicy() CachePolicy { return CachePolicyCacheable }
