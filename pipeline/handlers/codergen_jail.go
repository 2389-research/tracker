// ABOUTME: configureJail wires the writable_paths fs-jail into the agent's exec environment.
// ABOUTME: Three refuse-to-start gates: bad paths, unsupported backend, Landlock unavailable.
package handlers

import (
	"fmt"
	"os"
	osexec "os/exec"
	"path"
	"path/filepath"
	"strings"

	"github.com/2389-research/tracker/agent"
	execpkg "github.com/2389-research/tracker/agent/exec"
	"github.com/2389-research/tracker/pipeline"
)

// refuseWritablePathsOnUnsupportedBackend is the dispatcher-layer
// counterpart to configureJail's G2 gate. configureJail only runs inside
// NativeBackend.Run; the claude-code and acp backends never invoke it
// because buildRunConfig switches Extra away from *agent.SessionConfig
// for them, so the writable_paths signal is dropped before any gate fires.
// Without an earlier check a node that declares writable_paths but selects
// a non-native backend (either via `backend: claude-code` / `backend: acp`
// on the node OR via the global --backend default with no node-level
// override) starts unjailed (#275 review, Copilot codergen.go:647).
//
// The detection is by type assertion against the backend instance: anything
// that isn't *NativeBackend cannot run inside the jail, regardless of how
// it got selected.
func refuseWritablePathsOnUnsupportedBackend(node *pipeline.Node, backend pipeline.AgentBackend) error {
	if node == nil {
		return nil
	}
	cfg := node.AgentConfig(nil)
	if !cfg.WritablePathsSet {
		return nil
	}
	if _, ok := backend.(*NativeBackend); ok {
		return nil
	}
	return fmt.Errorf("writable_paths refuses backend %T (only native enforces; out-of-process backends cannot be sandboxed; see issue #272)", backend)
}

// configureJail consults cfg.WritablePathsSet and wires the jail into env
// when the flag is set. Returns (enabled, err):
//   - (false, nil) when WritablePathsSet is false — no jail, env unchanged.
//   - (false, err) when a refuse-to-start gate fires — session creation halts.
//   - (true, nil) when the jail is fully wired — env.CommandWrapper and
//     env.WriteOpener are populated.
//
// Refusal gates (per spec § 8.4):
//
//	G1. ValidateWritablePaths returns an error (covers bad working_dir, bad
//	    globs, empty list — Task 8 unifies all three classes).
//	G2. Backend is claude-code or acp (out-of-process; jail can't enforce)
//	    OR unknown (fail-closed).
//	G3. ProbeLandlock fails (non-Linux, kernel < 6.7, syscall denied).
//
// The handoff: Task 15's codergen buildConfig calls this immediately before
// agent.NewSession. Any refuse returned here surfaces as EventNodeFailed
// pre-LLM-token; the session never starts.
func configureJail(cfg *agent.SessionConfig, env *execpkg.LocalEnvironment, processCwd string) (bool, error) {
	if !cfg.WritablePathsSet {
		return false, nil
	}

	// G1: validate the working_dir + glob shape. Catches empty list, malformed
	// glob, working_dir escape — all three classes per Task 8.
	if err := execpkg.ValidateWritablePaths(cfg.WorkingDir, cfg.WritablePaths, processCwd); err != nil {
		return false, fmt.Errorf("writable_paths validation failed: %w", err)
	}

	// G2: refuse unsupported backends. claude-code and acp run out-of-process,
	// so the jail can't intercept their writes. Unknown names also refuse —
	// safer to fail-closed than to ship a silent no-op on a future backend.
	switch cfg.Backend {
	case "", "native":
		// ok
	case "claude-code", "acp":
		return false, fmt.Errorf("writable_paths is not supported on backend %q (only native enforces; see issue #272)", cfg.Backend)
	default:
		return false, fmt.Errorf("writable_paths refuses unknown backend %q (only native enforces; see issue #272)", cfg.Backend)
	}

	// G3: probe Landlock support.
	if err := execpkg.ProbeLandlock(); err != nil {
		return false, fmt.Errorf("writable_paths requires Landlock: %w", err)
	}

	// Wire the env. The anchor is the absolute resolved WorkingDir.
	var anchor string
	if filepath.IsAbs(cfg.WorkingDir) {
		anchor = filepath.Clean(cfg.WorkingDir)
	} else {
		anchor = filepath.Clean(filepath.Join(processCwd, cfg.WorkingDir))
	}
	// Normalize the stored globs to the same canonical (path.Clean) form the
	// validator checked. ValidateWritablePaths Cleans each entry before its
	// escape/shape checks, but the runtime matcher (matchOneGlob) and the
	// Landlock dir computation (landlockDirForGlob) both consume the stored
	// string literally. Without normalizing here, an entry like "./workspace/**"
	// passes validation yet makes matchOneGlob compare the literal prefix
	// "./workspace" against "workspace/..." and deny every write under
	// workspace/ — a fail-closed surprise (Copilot codergen_jail.go:71).
	globs := make([]string, len(cfg.WritablePaths))
	for i, g := range cfg.WritablePaths {
		globs[i] = path.Clean(g)
	}

	env.CommandWrapper = func(c *osexec.Cmd) *osexec.Cmd {
		return execpkg.WrapBashCmd(c, anchor, globs)
	}
	env.WriteOpener = func(absPath string, perm os.FileMode) (*os.File, error) {
		relPath, err := relPathForJail(anchor, absPath)
		if err != nil {
			return nil, err
		}
		if !matchWritablePath(relPath, globs) {
			return nil, fmt.Errorf("%w: %q does not match any writable_paths glob (%v)",
				execpkg.ErrPathNotAllowed, relPath, globs)
		}
		// Policy approved — create the parent dir via the symlink-safe
		// walker then open via openat2. SafeMkdirAll uses openat2 with
		// RESOLVE_BENEATH | RESOLVE_NO_SYMLINKS on every intermediate
		// component so an agent who pre-creates "workspace" (or any
		// ancestor) as a symlink cannot redirect the MkdirAll outside
		// the jail (#275 review, Copilot codergen_jail.go:92). Order
		// matters: mkdir runs AFTER the glob check so a rejected write
		// leaves no empty directories.
		if err := execpkg.SafeMkdirAll(anchor, filepath.Dir(relPath), 0755); err != nil {
			return nil, err
		}
		return execpkg.OpenForWrite(anchor, relPath, perm)
	}
	env.Remover = func(absPath string) error {
		relPath, err := relPathForJail(anchor, absPath)
		if err != nil {
			return err
		}
		if !matchWritablePath(relPath, globs) {
			return fmt.Errorf("%w: %q does not match any writable_paths glob (%v)",
				execpkg.ErrPathNotAllowed, relPath, globs)
		}
		// SafeRemove resolves the parent dir via openat2 + unlinkat so
		// an agent who pre-creates any intermediate component as a
		// symlink cannot redirect the delete outside the jail (#275
		// review, Copilot codergen_jail.go:103).
		return execpkg.SafeRemove(anchor, relPath)
	}
	return true, nil
}

// relPathForJail validates that absPath sits beneath anchor and returns the
// relative path used by the jail's policy and openat2 layers. Rejects only
// real parent traversal: relPath == ".." or starts with "../" — bare names
// like "..foo" or "...cache" stay below the anchor and must be allowed
// through (#272 review, coderabbitai codergen_jail.go:82).
func relPathForJail(anchor, absPath string) (string, error) {
	relPath, relErr := filepath.Rel(anchor, absPath)
	if relErr != nil {
		return "", fmt.Errorf("%w: cannot relativize %q against anchor %q: %v",
			execpkg.ErrPathEscape, absPath, anchor, relErr)
	}
	if relPath == ".." || strings.HasPrefix(relPath, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: %q is outside anchor %q", execpkg.ErrPathEscape, absPath, anchor)
	}
	return relPath, nil
}

// matchWritablePath returns true when relPath matches any of the writable
// glob patterns. Supports:
//   - "**" — matches anything.
//   - "prefix/**" — matches the prefix itself and any descendant.
//   - "**/suffix" — matches at any depth, including the top level.
//   - "prefix/**/suffix" — matches "prefix/.../suffix" at any intermediate depth.
//   - "*.md", "foo/*.txt" — path.Match (single-segment globs).
//   - "exact/path.md" — literal match.
//
// All globs are workspace-relative and use forward-slash separators.
// Closes the #272 review gap (coderabbitai codergen_jail.go:129) where the
// previous matcher only honored trailing "/**" and bare "**".
func matchWritablePath(relPath string, globs []string) bool {
	for _, g := range globs {
		if matchOneGlob(relPath, g) {
			return true
		}
	}
	return false
}

func matchOneGlob(relPath, g string) bool {
	// Static glob: literal match.
	if !strings.ContainsAny(g, "*?[") {
		return relPath == g
	}
	if g == "**" {
		return true
	}
	if strings.Contains(g, "**") {
		// Split on the first "/**" boundary. Note: we deliberately don't
		// support multiple "**" in one glob — that's a doublestar feature
		// we don't need for the documented adopters.
		i := strings.Index(g, "/**")
		switch {
		case i == 0:
			// "/**suffix" — same shape as "**/suffix" after stripping the
			// leading "/". Treat both as "any-path-prefix + suffix".
			rest := strings.TrimPrefix(g[3:], "/")
			return rest == "" || matchSuffixAtAnyDepth(relPath, rest)
		case i < 0:
			// No "/**" present but contains "**" elsewhere (e.g. leading
			// "**/x"). Handle the "**/" prefix case here.
			if strings.HasPrefix(g, "**/") {
				return matchSuffixAtAnyDepth(relPath, strings.TrimPrefix(g, "**/"))
			}
			// Fallback: anything else with embedded ** is not supported.
			// path.Match doesn't understand **, so this would mis-match;
			// reject.
			return false
		default:
			// "prefix/**" or "prefix/**/suffix".
			prefix := g[:i]
			rest := strings.TrimPrefix(g[i+3:], "/")
			if !strings.HasPrefix(relPath+"/", prefix+"/") {
				return false
			}
			if rest == "" {
				return relPath == prefix || strings.HasPrefix(relPath, prefix+"/")
			}
			after := strings.TrimPrefix(relPath, prefix)
			after = strings.TrimPrefix(after, "/")
			return matchSuffixAtAnyDepth(after, rest)
		}
	}
	// Single-segment glob (*, ?, []). path.Match treats "/" as a separator.
	ok, _ := path.Match(g, relPath)
	return ok
}

// matchSuffixAtAnyDepth returns true when suffix matches relPath after
// trimming zero or more leading path components. Each trim step delegates
// to path.Match so single-segment globs in the suffix still work
// (e.g. matchSuffixAtAnyDepth("a/b/c.md", "*.md") = true via the "c.md" step).
func matchSuffixAtAnyDepth(relPath, suffix string) bool {
	for {
		if ok, _ := path.Match(suffix, relPath); ok {
			return true
		}
		idx := strings.Index(relPath, "/")
		if idx < 0 {
			return false
		}
		relPath = relPath[idx+1:]
	}
}
