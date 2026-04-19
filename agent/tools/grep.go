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

// noiseDirs are directories skipped during recursive grep to reduce noise.
// Dot-prefixed dirs (e.g. .git, .tox, .venv) are already skipped by the
// HasPrefix(".") check in shouldSkipDir, so they are not listed here.
var noiseDirs = map[string]bool{
	"__pycache__":  true,
	"build":        true,
	"dist":         true,
	"node_modules": true,
	"venv":         true,
}

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
			},
			"context_lines": {
				"type": "integer",
				"description": "Number of context lines to show before and after each match. Default 0."
			}
		},
		"required": ["pattern"]
	}`)
}

func (t *GrepSearchTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	pattern, path, contextLines, err := parseGrepInput(input)
	if err != nil {
		return "", err
	}

	re, err := regexp.Compile(pattern)
	if err != nil {
		return "", fmt.Errorf("invalid regex: %w", err)
	}

	searchRoot, err := t.safePath(path)
	if err != nil {
		return "", err
	}

	matches, truncated, totalMatches, err := t.runSearch(ctx, searchRoot, path, re, contextLines)
	if err != nil {
		return "", err
	}

	return formatGrepResults(pattern, matches, truncated, totalMatches), nil
}

// parseGrepInput unmarshals the JSON input and validates required fields.
func parseGrepInput(input json.RawMessage) (pattern, path string, contextLines int, err error) {
	var params struct {
		Pattern      string `json:"pattern"`
		Path         string `json:"path"`
		ContextLines int    `json:"context_lines"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return "", "", 0, fmt.Errorf("invalid input: %w", err)
	}
	if params.Pattern == "" {
		return "", "", 0, fmt.Errorf("pattern is required")
	}
	if params.Path == "" {
		params.Path = "."
	}
	return params.Pattern, params.Path, params.ContextLines, nil
}

// runSearch performs the grep search on the resolved path.
// Returns matches, whether they were truncated, the total match count (>= len(matches)), and any error.
func (t *GrepSearchTool) runSearch(ctx context.Context, searchRoot, displayPath string, re *regexp.Regexp, contextLines int) ([]string, bool, int, error) {
	info, err := os.Stat(searchRoot)
	if err != nil {
		return nil, false, 0, fmt.Errorf("path not found: %s", displayPath)
	}
	if info.IsDir() {
		return t.searchDir(ctx, searchRoot, re, contextLines)
	}
	matches, truncated, err := t.searchFile(searchRoot, re, contextLines)
	if err != nil {
		return nil, false, 0, err
	}
	totalMatches := len(matches)
	if truncated {
		// Count the remaining matches beyond what searchFile collected.
		extra, countErr := t.countFileMatches(searchRoot, re)
		if countErr == nil {
			totalMatches = extra
		}
	}
	return matches, truncated, totalMatches, nil
}

// formatGrepResults builds the final output string from search matches.
// When truncated, totalMatches carries the full count across all files so
// the agent can decide whether to narrow the search pattern.
func formatGrepResults(pattern string, matches []string, truncated bool, totalMatches int) string {
	if len(matches) == 0 {
		return fmt.Sprintf("no matches for pattern %q", pattern)
	}
	result := strings.Join(matches, "\n")
	if truncated {
		result += fmt.Sprintf("\n\n(showing first %d of %d total matches — narrow your search pattern)", maxGrepResults, totalMatches)
	}
	return result
}

// safePath validates that a relative path resolves inside the working directory.
func (t *GrepSearchTool) safePath(rel string) (string, error) {
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("absolute paths not allowed: %s (use a relative path like %q instead)", rel, filepath.Base(rel))
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
// Returns matches (capped at maxGrepResults), whether truncated, total match count, and any error.
func (t *GrepSearchTool) searchDir(ctx context.Context, root string, re *regexp.Regexp, contextLines int) ([]string, bool, int, error) {
	var matches []string
	truncated := false
	totalMatches := 0
	capReached := false

	err := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return nil // skip unreadable entries
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if info.IsDir() {
			return shouldSkipDir(info, path, root)
		}
		if isBinaryExtension(info.Name()) {
			return nil
		}

		if !capReached {
			// Still collecting matches.
			fileMatches, fileTruncated, err := t.searchFile(path, re, contextLines)
			if err != nil {
				return nil // skip unreadable files
			}
			matches = append(matches, fileMatches...)
			totalMatches += len(fileMatches)
			if fileTruncated || len(matches) >= maxGrepResults {
				truncated = true
				capReached = true
				matches = matches[:min(len(matches), maxGrepResults)]
				// For a truncated single file, countFileMatches gives the true total.
				if fileTruncated {
					if n, err := t.countFileMatches(path, re); err == nil {
						totalMatches = totalMatches - len(fileMatches) + n
					}
				}
			}
		} else {
			// Cap already hit — only count remaining matches.
			if n, err := t.countFileMatches(path, re); err == nil {
				totalMatches += n
			}
		}
		return nil
	})

	if err != nil && ctx.Err() == nil {
		return matches, truncated, totalMatches, err
	}

	return matches, truncated, totalMatches, nil
}

// countFileMatches counts matching lines in a file without collecting them.
func (t *GrepSearchTool) countFileMatches(absPath string, re *regexp.Regexp) (int, error) {
	f, err := os.Open(absPath)
	if err != nil {
		return 0, err
	}
	defer func() { _ = f.Close() }()

	count := 0
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		if re.MatchString(scanner.Text()) {
			count++
		}
	}
	return count, scanner.Err()
}

// shouldSkipDir returns filepath.SkipDir for hidden directories and known noise directories (except the root).
func shouldSkipDir(info os.FileInfo, path, root string) error {
	if path == root {
		return nil
	}
	name := info.Name()
	if strings.HasPrefix(name, ".") {
		return filepath.SkipDir
	}
	if noiseDirs[name] {
		return filepath.SkipDir
	}
	if strings.HasSuffix(name, ".egg-info") {
		return filepath.SkipDir
	}
	return nil
}

// searchFile scans a single file for lines matching the regex.
// When contextLines > 0, it buffers the whole file first and emits match groups with context.
func (t *GrepSearchTool) searchFile(absPath string, re *regexp.Regexp, contextLines int) ([]string, bool, error) {
	f, err := os.Open(absPath)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = f.Close() }()

	relPath, err := filepath.Rel(t.env.WorkingDir(), absPath)
	if err != nil {
		relPath = absPath
	}

	if contextLines <= 0 {
		return searchFileNoContext(f, relPath, re)
	}
	return searchFileWithContext(f, relPath, re, contextLines)
}

// searchFileNoContext performs a simple line-by-line scan with no context buffering.
func searchFileNoContext(f *os.File, relPath string, re *regexp.Regexp) ([]string, bool, error) {
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

// searchFileWithContext buffers the entire file and emits match lines plus N context lines
// before and after each match. Context lines use `filepath-linenum-content` format;
// match lines use `filepath:linenum:content`. Overlapping windows are merged and
// non-contiguous groups are separated by `--`.
func searchFileWithContext(f *os.File, relPath string, re *regexp.Regexp, n int) ([]string, bool, error) {
	scanner := bufio.NewScanner(f)
	var allLines []string
	for scanner.Scan() {
		allLines = append(allLines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return nil, false, err
	}

	total := len(allLines)

	// Collect 0-based indices of matching lines.
	var matchIndices []int
	for i, line := range allLines {
		if re.MatchString(line) {
			matchIndices = append(matchIndices, i)
		}
	}
	if len(matchIndices) == 0 {
		return nil, false, nil
	}

	// Build non-overlapping groups of [start, end] inclusive (0-based).
	type group struct{ start, end int }
	var groups []group
	cur := group{
		start: max(0, matchIndices[0]-n),
		end:   min(total-1, matchIndices[0]+n),
	}
	for _, idx := range matchIndices[1:] {
		wStart := max(0, idx-n)
		wEnd := min(total-1, idx+n)
		if wStart <= cur.end+1 {
			// Overlapping or adjacent — merge.
			if wEnd > cur.end {
				cur.end = wEnd
			}
		} else {
			groups = append(groups, cur)
			cur = group{start: wStart, end: wEnd}
		}
	}
	groups = append(groups, cur)

	// Build a set of match indices for quick lookup.
	matchSet := make(map[int]bool, len(matchIndices))
	for _, idx := range matchIndices {
		matchSet[idx] = true
	}

	var out []string
	truncated := false
	matchCount := 0

	for i, g := range groups {
		if i > 0 {
			out = append(out, "--")
		}
		for lineIdx := g.start; lineIdx <= g.end; lineIdx++ {
			lineNum := lineIdx + 1 // 1-based
			content := allLines[lineIdx]
			if matchSet[lineIdx] {
				out = append(out, fmt.Sprintf("%s:%d:%s", relPath, lineNum, content))
				matchCount++
				if matchCount >= maxGrepResults {
					return out, true, nil
				}
			} else {
				out = append(out, fmt.Sprintf("%s-%d-%s", relPath, lineNum, content))
			}
		}
	}

	return out, truncated, nil
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
