// ABOUTME: Canonical Completer interface used by tools that need direct LLM
// ABOUTME: access (generate_code, write_enriched_sprint). Re-exported by the
// ABOUTME: agent package as a type alias so the two are identical, not parallel.
package tools

import (
	"context"
	"fmt"
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
// stays within root. An absolute rel is accepted as long as it points
// inside root — the goal is "no path escape," not "no absolute path"
// (which would break tests that use t.TempDir()).
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

	var candidate string
	if filepath.IsAbs(rel) {
		candidate = filepath.Clean(rel)
	} else {
		candidate = filepath.Clean(filepath.Join(rootAbs, rel))
	}

	if candidate != rootAbs && !strings.HasPrefix(candidate, rootAbs+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes working directory: %s", rel)
	}
	return candidate, nil
}
