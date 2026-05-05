// ABOUTME: Canonical Completer interface used by tools that need direct LLM
// ABOUTME: access (generate_code, write_enriched_sprint). Re-exported by the
// ABOUTME: agent package as a type alias so the two are identical, not parallel.
package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/2389-research/tracker/llm"
)

// Completer is the minimal LLM-call surface a tool needs to invoke the
// model directly (e.g. for an audit pass or per-file generation). The
// agent package re-exports this as a type alias so the two interfaces
// are literally the same type, preventing silent divergence.
type Completer interface {
	Complete(ctx context.Context, req *llm.Request) (*llm.Response, error)
}

// resolveUnderRoot resolves rel against root and verifies the cleaned path
// stays within root, including after symlink evaluation. An absolute rel is
// accepted as long as it points inside root — the goal is "no path escape,"
// not "no absolute path" (which would break tests using t.TempDir()).
//
// Symlinks are evaluated for both root and the candidate so a symlink inside
// root pointing outside the workspace can't be used to escape. If the
// candidate doesn't yet exist (common for write paths), its parent directory
// is symlink-resolved instead and the cleaned filename is reattached.
//
// Used by tools that read or write files based on LLM-supplied paths
// (write_enriched_sprint, generate_code) to prevent directory traversal
// and accidental writes/reads outside the working tree.
func resolveUnderRoot(root, rel string) (string, error) {
	if root == "" {
		return "", fmt.Errorf("root directory is empty")
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve root: %w", err)
	}
	rootEval, err := evalSymlinksLeniently(rootAbs)
	if err != nil {
		return "", fmt.Errorf("resolve root symlinks: %w", err)
	}

	var candidate string
	if filepath.IsAbs(rel) {
		candidate = filepath.Clean(rel)
	} else {
		candidate = filepath.Clean(filepath.Join(rootAbs, rel))
	}

	candidateEval, err := evalSymlinksLeniently(candidate)
	if err != nil {
		return "", fmt.Errorf("resolve path symlinks: %w", err)
	}

	if candidateEval != rootEval && !strings.HasPrefix(candidateEval, rootEval+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes working directory: %s", rel)
	}
	return candidateEval, nil
}

// evalSymlinksLeniently is filepath.EvalSymlinks plus a fallback for paths
// that don't yet exist: resolve the longest existing prefix and re-attach the
// missing tail. This is required for write paths (the file isn't there yet)
// while still catching symlink-based escapes for any directory that does
// exist along the way.
func evalSymlinksLeniently(p string) (string, error) {
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return resolved, nil
	}

	// Walk up until we find an existing directory, then re-append the rest.
	parent := p
	tail := ""
	for parent != "" && parent != filepath.Dir(parent) {
		if _, err := os.Stat(parent); err == nil {
			resolved, err := filepath.EvalSymlinks(parent)
			if err != nil {
				return "", err
			}
			return filepath.Clean(filepath.Join(resolved, tail)), nil
		}
		tail = filepath.Join(filepath.Base(parent), tail)
		parent = filepath.Dir(parent)
	}
	// Path has no existing ancestor — return the cleaned absolute form.
	return filepath.Clean(p), nil
}
