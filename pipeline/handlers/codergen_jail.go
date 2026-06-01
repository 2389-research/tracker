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
)

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
	globs := append([]string(nil), cfg.WritablePaths...) // defensive copy

	env.CommandWrapper = func(c *osexec.Cmd) *osexec.Cmd {
		return execpkg.WrapBashCmd(c, anchor, globs)
	}
	env.WriteOpener = func(absPath string, perm os.FileMode) (*os.File, error) {
		// LocalEnvironment.WriteFile passes an absolute path; convert to
		// relative-to-anchor for OpenForWrite (which uses openat2 with
		// RESOLVE_BENEATH against the anchor FD).
		relPath, relErr := filepath.Rel(anchor, absPath)
		if relErr != nil || strings.HasPrefix(relPath, "..") {
			return nil, fmt.Errorf("%w: %q is outside anchor %q", execpkg.ErrPathEscape, absPath, anchor)
		}

		// Glob check: enforces the EXACT writable_paths globs per spec D2
		// two-tier semantic. RESOLVE_BENEATH only enforces anchor containment;
		// without this, a file-scoped glob like "workspace/out.md" would
		// degrade to directory-scoped behavior.
		// Check BEFORE OpenForWrite so no file is created on rejection.
		if !matchWritablePath(relPath, globs) {
			return nil, fmt.Errorf("%w: %q does not match any writable_paths glob (%v)",
				execpkg.ErrPathNotAllowed, relPath, globs)
		}

		return execpkg.OpenForWrite(anchor, relPath, perm)
	}
	return true, nil
}

// matchWritablePath returns true when relPath matches any of the writable
// glob patterns. Supports:
//   - "prefix/**" — matches relPath when it has prefix "prefix/"
//     (any depth descendant, file or directory).
//   - "exact/path.md" — exact match against relPath.
//   - "*.md", "foo/*.txt" — path.Match (single-segment globs).
//
// All globs are workspace-relative and use forward-slash separators.
func matchWritablePath(relPath string, globs []string) bool {
	for _, g := range globs {
		if matchOneGlob(relPath, g) {
			return true
		}
	}
	return false
}

func matchOneGlob(relPath, g string) bool {
	// Double-star prefix match. e.g. "workspace/**" matches anything under
	// "workspace/". Per spec D2, the static prefix is everything before the
	// first glob metachar; for "X/**" that's "X/".
	if strings.HasSuffix(g, "/**") {
		prefix := strings.TrimSuffix(g, "/**")
		return relPath == prefix || strings.HasPrefix(relPath, prefix+"/")
	}
	if g == "**" {
		return true
	}
	if strings.Contains(g, "*") || strings.Contains(g, "?") || strings.Contains(g, "[") {
		ok, _ := path.Match(g, relPath)
		return ok
	}
	return relPath == g
}
