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

	changed := 0
	for _, op := range ops {
		switch op.kind {
		case patchAdd:
			if err := t.env.WriteFile(ctx, op.path, op.content); err != nil {
				return "", err
			}
		case patchUpdate:
			content, err := t.env.ReadFile(ctx, op.path)
			if err != nil {
				return "", err
			}
			updated, err := applyHunks(content, op.hunks)
			if err != nil {
				return "", fmt.Errorf("update %s: %w", op.path, err)
			}
			targetPath := op.path
			if op.moveTo != "" {
				targetPath = op.moveTo
			}
			if err := t.env.WriteFile(ctx, targetPath, updated); err != nil {
				return "", err
			}
			if op.moveTo != "" && op.moveTo != op.path {
				path, err := safePatchPath(t.env.WorkingDir(), op.path)
				if err != nil {
					return "", err
				}
				if err := os.Remove(path); err != nil {
					return "", err
				}
			}
		case patchDelete:
			path, err := safePatchPath(t.env.WorkingDir(), op.path)
			if err != nil {
				return "", err
			}
			if err := os.Remove(path); err != nil {
				return "", err
			}
		default:
			return "", fmt.Errorf("unsupported patch operation")
		}
		changed++
	}

	return fmt.Sprintf("applied patch (%d file(s) changed)", changed), nil
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
			path := strings.TrimPrefix(line, "*** Add File: ")
			i++
			var content []string
			for i < len(lines) && !strings.HasPrefix(lines[i], "*** ") {
				if lines[i] == "" && i == len(lines)-1 {
					break
				}
				if !strings.HasPrefix(lines[i], "+") {
					return nil, fmt.Errorf("add file lines must start with +")
				}
				content = append(content, strings.TrimPrefix(lines[i], "+"))
				i++
			}
			ops = append(ops, patchOperation{
				kind:    patchAdd,
				path:    path,
				content: joinPatchLines(content, false),
			})
		case strings.HasPrefix(line, "*** Delete File: "):
			ops = append(ops, patchOperation{
				kind: patchDelete,
				path: strings.TrimPrefix(line, "*** Delete File: "),
			})
			i++
		case strings.HasPrefix(line, "*** Update File: "):
			path := strings.TrimPrefix(line, "*** Update File: ")
			i++
			moveTo := ""
			if i < len(lines) && strings.HasPrefix(lines[i], "*** Move to: ") {
				moveTo = strings.TrimPrefix(lines[i], "*** Move to: ")
				i++
			}
			var hunks []patchHunk
			for i < len(lines) {
				switch {
				case strings.HasPrefix(lines[i], "*** "), lines[i] == "*** End Patch":
					goto doneUpdate
				case strings.HasPrefix(lines[i], "@@"):
					i++
					hunk, next, err := parseHunk(lines, i)
					if err != nil {
						return nil, err
					}
					hunks = append(hunks, hunk)
					i = next
				default:
					return nil, fmt.Errorf("expected hunk header, got %q", lines[i])
				}
			}
		doneUpdate:
			if len(hunks) == 0 {
				return nil, fmt.Errorf("update %s has no hunks", path)
			}
			ops = append(ops, patchOperation{
				kind:   patchUpdate,
				path:   path,
				moveTo: moveTo,
				hunks:  hunks,
			})
		default:
			return nil, fmt.Errorf("unexpected patch line %q", line)
		}
	}

	return nil, fmt.Errorf("patch is missing *** End Patch")
}

func parseHunk(lines []string, start int) (patchHunk, int, error) {
	hunk := patchHunk{}
	i := start
	for i < len(lines) {
		line := lines[i]
		if strings.HasPrefix(line, "@@") || line == "*** End Patch" {
			break
		}
		if strings.HasPrefix(line, "*** ") {
			if line == "*** End of File" {
				hunk.noNewlineAtEOF = true
				i++
				break
			}
			break
		}
		if line == "" && i == len(lines)-1 {
			break
		}
		if len(line) == 0 {
			return patchHunk{}, 0, fmt.Errorf("empty patch line in hunk")
		}
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
			return patchHunk{}, 0, fmt.Errorf("unsupported hunk line %q", line)
		}
		i++
	}
	return hunk, i, nil
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
