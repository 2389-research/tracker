package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/2389-research/tracker/agent/exec"
)

type ApplyPatchTool struct {
	env exec.ExecutionEnvironment
}

func NewApplyPatchTool(env exec.ExecutionEnvironment) *ApplyPatchTool {
	return &ApplyPatchTool{env: env}
}

func (t *ApplyPatchTool) Name() string { return "apply_patch" }

func (t *ApplyPatchTool) Description() string {
	return "Apply a patch in apply_patch format to create, update, or delete files."
}

func (t *ApplyPatchTool) Parameters() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"patch": {
				"type": "string",
				"description": "The apply_patch payload to execute."
			}
		},
		"required": ["patch"]
	}`)
}

func (t *ApplyPatchTool) Execute(ctx context.Context, input json.RawMessage) (string, error) {
	var params struct {
		Patch string `json:"patch"`
	}
	if err := json.Unmarshal(input, &params); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if params.Patch == "" {
		return "", fmt.Errorf("patch is required")
	}

	ops, err := parsePatch(params.Patch)
	if err != nil {
		return "", err
	}

	for _, op := range ops {
		if err := t.applyOp(ctx, op); err != nil {
			return "", err
		}
	}

	return fmt.Sprintf("applied patch (%d file(s) changed)", len(ops)), nil
}

// applyOp applies a single patch operation (add, update, or delete).
func (t *ApplyPatchTool) applyOp(ctx context.Context, op patchOperation) error {
	switch op.kind {
	case patchAdd:
		return t.env.WriteFile(ctx, op.path, op.content)
	case patchUpdate:
		return t.applyUpdateOp(ctx, op)
	case patchDelete:
		return t.applyDeleteOp(op)
	default:
		return fmt.Errorf("unsupported patch operation")
	}
}

// applyUpdateOp reads, patches, and writes an updated file, handling optional moves.
func (t *ApplyPatchTool) applyUpdateOp(ctx context.Context, op patchOperation) error {
	content, err := t.env.ReadFile(ctx, op.path)
	if err != nil {
		return err
	}
	updated, err := applyHunks(content, op.hunks)
	if err != nil {
		return fmt.Errorf("update %s: %w", op.path, err)
	}
	targetPath := op.path
	if op.moveTo != "" {
		targetPath = op.moveTo
	}
	if err := t.env.WriteFile(ctx, targetPath, updated); err != nil {
		return err
	}
	if op.moveTo != "" && op.moveTo != op.path {
		path, err := safePatchPath(t.env.WorkingDir(), op.path)
		if err != nil {
			return err
		}
		return os.Remove(path)
	}
	return nil
}

// applyDeleteOp removes a file safely within the working directory.
func (t *ApplyPatchTool) applyDeleteOp(op patchOperation) error {
	path, err := safePatchPath(t.env.WorkingDir(), op.path)
	if err != nil {
		return err
	}
	return os.Remove(path)
}

type patchKind string

const (
	patchAdd    patchKind = "add"
	patchUpdate patchKind = "update"
	patchDelete patchKind = "delete"
)

type patchOperation struct {
	kind    patchKind
	path    string
	moveTo  string
	content string
	hunks   []patchHunk
}

type patchHunk struct {
	oldLines       []string
	newLines       []string
	noNewlineAtEOF bool
}

func parsePatch(patch string) ([]patchOperation, error) {
	lines := strings.Split(patch, "\n")
	if len(lines) == 0 || lines[0] != "*** Begin Patch" {
		return nil, fmt.Errorf("patch must begin with *** Begin Patch")
	}

	var ops []patchOperation
	for i := 1; i < len(lines); {
		line := lines[i]
		switch {
		case line == "*** End Patch":
			return ops, nil
		case strings.HasPrefix(line, "*** Add File: "):
			op, next, err := parseAddFile(lines, i)
			if err != nil {
				return nil, err
			}
			ops = append(ops, op)
			i = next
		case strings.HasPrefix(line, "*** Delete File: "):
			ops = append(ops, patchOperation{
				kind: patchDelete,
				path: strings.TrimPrefix(line, "*** Delete File: "),
			})
			i++
		case strings.HasPrefix(line, "*** Update File: "):
			op, next, err := parseUpdateFile(lines, i)
			if err != nil {
				return nil, err
			}
			ops = append(ops, op)
			i = next
		default:
			return nil, fmt.Errorf("unexpected patch line %q", line)
		}
	}

	return nil, fmt.Errorf("patch is missing *** End Patch")
}

// parseAddFile parses an "*** Add File:" block starting at line index i.
func parseAddFile(lines []string, i int) (patchOperation, int, error) {
	path := strings.TrimPrefix(lines[i], "*** Add File: ")
	i++
	var content []string
	for i < len(lines) && !strings.HasPrefix(lines[i], "*** ") {
		if lines[i] == "" && i == len(lines)-1 {
			break
		}
		if !strings.HasPrefix(lines[i], "+") {
			return patchOperation{}, 0, fmt.Errorf("add file lines must start with +")
		}
		content = append(content, strings.TrimPrefix(lines[i], "+"))
		i++
	}
	return patchOperation{
		kind:    patchAdd,
		path:    path,
		content: joinPatchLines(content, false),
	}, i, nil
}

// parseUpdateFile parses an "*** Update File:" block starting at line index i.
func parseUpdateFile(lines []string, i int) (patchOperation, int, error) {
	path := strings.TrimPrefix(lines[i], "*** Update File: ")
	i++
	moveTo := ""
	if i < len(lines) && strings.HasPrefix(lines[i], "*** Move to: ") {
		moveTo = strings.TrimPrefix(lines[i], "*** Move to: ")
		i++
	}
	hunks, i, err := parseUpdateHunks(lines, i)
	if err != nil {
		return patchOperation{}, 0, err
	}
	if len(hunks) == 0 {
		return patchOperation{}, 0, fmt.Errorf("update %s has no hunks", path)
	}
	return patchOperation{
		kind:   patchUpdate,
		path:   path,
		moveTo: moveTo,
		hunks:  hunks,
	}, i, nil
}

// parseUpdateHunks collects all @@ hunks within an "Update File" block.
func parseUpdateHunks(lines []string, i int) ([]patchHunk, int, error) {
	var hunks []patchHunk
	for i < len(lines) {
		if strings.HasPrefix(lines[i], "*** ") || lines[i] == "*** End Patch" {
			break
		}
		if !strings.HasPrefix(lines[i], "@@") {
			return nil, 0, fmt.Errorf("expected hunk header, got %q", lines[i])
		}
		i++
		hunk, next, err := parseHunk(lines, i)
		if err != nil {
			return nil, 0, err
		}
		hunks = append(hunks, hunk)
		i = next
	}
	return hunks, i, nil
}

func parseHunk(lines []string, start int) (patchHunk, int, error) {
	hunk := patchHunk{}
	i := start
	for i < len(lines) {
		advance, err := processHunkLine(lines, i, &hunk)
		if err != nil {
			return patchHunk{}, 0, err
		}
		if advance < 0 {
			// advance == -1 signals "stop, don't increment"
			// advance == -2 signals "stop, increment for EOF marker"
			if advance == -2 {
				i++
			}
			break
		}
		i += advance
	}
	return hunk, i, nil
}

// processHunkLine handles one line of a hunk scan.
// Returns (1, nil) to advance normally, (-1, nil) to stop without consuming, (-2, nil) to stop and consume (EOF marker), (0, err) on error.
func processHunkLine(lines []string, i int, hunk *patchHunk) (int, error) {
	line := lines[i]
	done, eofFlag := isHunkTerminator(line)
	if done {
		if eofFlag {
			hunk.noNewlineAtEOF = true
			return -2, nil
		}
		return -1, nil
	}
	if line == "" && i == len(lines)-1 {
		return -1, nil
	}
	if len(line) == 0 {
		return 0, fmt.Errorf("empty patch line in hunk")
	}
	if err := applyHunkLine(line, hunk); err != nil {
		return 0, err
	}
	return 1, nil
}

// isHunkTerminator returns (shouldStop, isEndOfFile) for a hunk scan line.
func isHunkTerminator(line string) (bool, bool) {
	if strings.HasPrefix(line, "@@") || line == "*** End Patch" {
		return true, false
	}
	if strings.HasPrefix(line, "*** ") {
		if line == "*** End of File" {
			return true, true
		}
		return true, false
	}
	return false, false
}

// applyHunkLine adds a single hunk line to the hunk based on its prefix character.
func applyHunkLine(line string, hunk *patchHunk) error {
	switch line[0] {
	case ' ':
		text := line[1:]
		hunk.oldLines = append(hunk.oldLines, text)
		hunk.newLines = append(hunk.newLines, text)
	case '-':
		hunk.oldLines = append(hunk.oldLines, line[1:])
	case '+':
		hunk.newLines = append(hunk.newLines, line[1:])
	default:
		return fmt.Errorf("unsupported hunk line %q", line)
	}
	return nil
}

func applyHunks(content string, hunks []patchHunk) (string, error) {
	updated := content
	for _, hunk := range hunks {
		oldText := joinPatchLines(hunk.oldLines, hunk.noNewlineAtEOF)
		newText := joinPatchLines(hunk.newLines, hunk.noNewlineAtEOF)

		switch {
		case oldText != "" && strings.Contains(updated, oldText):
			updated = strings.Replace(updated, oldText, newText, 1)
		case strings.Contains(updated, strings.TrimSuffix(oldText, "\n")):
			trimmedOld := strings.TrimSuffix(oldText, "\n")
			trimmedNew := strings.TrimSuffix(newText, "\n")
			updated = strings.Replace(updated, trimmedOld, trimmedNew, 1)
		default:
			return "", fmt.Errorf("hunk did not match target content")
		}
	}
	return updated, nil
}

func joinPatchLines(lines []string, noNewlineAtEOF bool) string {
	if len(lines) == 0 {
		return ""
	}
	joined := strings.Join(lines, "\n")
	if noNewlineAtEOF {
		return joined
	}
	return joined + "\n"
}

func safePatchPath(root, rel string) (string, error) {
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("absolute paths not allowed: %s", rel)
	}
	path := filepath.Join(root, rel)
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	if abs != rootAbs && !strings.HasPrefix(abs, rootAbs+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes working directory: %s", rel)
	}
	return abs, nil
}

// CachePolicy declares that apply_patch is mutating and invalidates caches.
func (t *ApplyPatchTool) CachePolicy() CachePolicy { return CachePolicyMutating }
