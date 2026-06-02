// ABOUTME: Cross-platform pure Go helpers for the writable_paths fs-jail (issue #272).
// ABOUTME: ValidateWritablePaths runs at session setup before any syscall jail mechanism.
package exec

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"strings"
)

// ValidateWritablePaths is the cross-platform gate that runs at session
// setup before the jail is wired. It catches three classes of refusal:
//
//  1. working_dir escapes tracker's process cwd / session root (the
//     "working_dir: /tmp/atk relocation" attack described in the spec § 8.1).
//  2. The glob list is empty (fail-closed; a present-but-empty value is
//     already rejected by dippin's parser, but tracker backstops).
//  3. A glob entry is bad in some way (absolute, starts with ~, escapes via
//     parent traversal, uses an unsupported shape, etc.). These are mostly
//     caught by dippin DIP142, but tracker is the runtime backstop.
//
// Error sentinels:
//
//   - ErrPathEscape wraps class-1 errors AND the escape-flavored class-3
//     subclass: absolute / `~` / parent-traversal / Windows-absolute / inward
//     `..` segments / non-existent intermediate. errors.Is(err, ErrPathEscape)
//     answers "did the operator (or attacker) try to point the jail outside
//     the session root?"
//   - Plain (non-sentinel) errors carry the malformed-pattern class-3 cases:
//     empty / brace usage / multiple ** / metachars before ** / glued
//     `foo/**bar` / unbalanced character classes. These are "bad glob shape"
//     rather than "escape attempt" — the codergen handler refuses to start
//     on either, but the sentinel distinction lets upstream consumers
//     differentiate if they care.
//   - Class-2 (empty list) is a plain error too.
func ValidateWritablePaths(workingDir string, globs []string, processCwd string) error {
	if err := validateWorkingDirEscape(workingDir, processCwd); err != nil {
		return err
	}
	if len(globs) == 0 {
		return fmt.Errorf("writable_paths is empty (fail-closed)")
	}
	for _, g := range globs {
		if err := validateGlobEntry(g); err != nil {
			return err
		}
	}
	return nil
}

// validateWorkingDirEscape rejects a working_dir that resolves outside
// processCwd. Catches both absolute paths (e.g. "/tmp/atk") and parent
// escapes (e.g. "../../etc"), plus symlink relocation: a working_dir of
// "link" where "link" is a symlink to a directory outside processCwd
// would pass the string-only Clean/HasPrefix check, but the kernel
// follows it at runtime and the jail anchor relocates outside the
// intended session root (#275 review, Copilot jail.go:56).
//
// String check first (catches the absolute and ../-escape cases without
// touching the filesystem); then if the path exists, filepath.EvalSymlinks
// resolves any symlink chain and the containment check runs against the
// real target. ErrNotExist is the safe case — no symlink can have been
// placed at a path that doesn't exist yet, and the runtime creates the
// dir under the (already-validated) string-cleaned anchor.
func validateWorkingDirEscape(workingDir, processCwd string) error {
	cleanedCwd := filepath.Clean(processCwd)
	// Resolve processCwd's own symlinks FIRST. If tracker was invoked from
	// a symlinked path (e.g. /home -> /var/home on some distros), the
	// string-cleaned containment check would reject perfectly valid
	// working_dir values expressed as the real path. EvalSymlinks falls
	// back to the cleaned form on error — if we can't resolve tracker's
	// own cwd we accept the existing string-only behavior rather than
	// fail-closing on an unrelated FS issue (#275 review, Copilot
	// jail.go:67).
	realCwd, evalCwdErr := filepath.EvalSymlinks(cleanedCwd)
	if evalCwdErr != nil {
		realCwd = cleanedCwd
	}
	var resolved string
	if filepath.IsAbs(workingDir) {
		resolved = filepath.Clean(workingDir)
	} else {
		resolved = filepath.Clean(filepath.Join(cleanedCwd, workingDir))
	}
	// Quick string-cleaned check against both the cleaned and the
	// symlink-resolved cwd. Containment under either form is enough — the
	// per-component symlink-aware re-check below catches escapes the
	// string layer can't see.
	if !isSubpathOf(resolved, cleanedCwd) && !isSubpathOf(resolved, realCwd) {
		return fmt.Errorf("%w: working_dir %q resolves to %q which escapes the tracker process cwd %q (real path %q)",
			ErrPathEscape, workingDir, resolved, cleanedCwd, realCwd)
	}
	// Symlink-aware re-check: if the path exists on disk, follow any
	// symlinks and re-verify containment against the real target.
	realResolved, evalErr := filepath.EvalSymlinks(resolved)
	if evalErr != nil {
		if errors.Is(evalErr, fs.ErrNotExist) {
			// The full path doesn't exist yet, but an intermediate
			// component may be a symlink that relocates the eventual
			// working_dir outside the jail (e.g. working_dir "link/new"
			// where "link" -> /etc). EvalSymlinks on the whole path
			// returns ErrNotExist because the final suffix is missing,
			// hiding that escape. Re-check containment against the
			// deepest *existing* ancestor instead.
			return validateExistingAncestorEscape(resolved, cleanedCwd, workingDir)
		}
		return fmt.Errorf("evaluate working_dir %q: %w", workingDir, evalErr)
	}
	if !isSubpathOf(realResolved, realCwd) {
		return fmt.Errorf("%w: working_dir %q evaluates to %q via symlinks which escapes %q",
			ErrPathEscape, workingDir, realResolved, realCwd)
	}
	return nil
}

// validateExistingAncestorEscape handles the case where the cleaned
// working_dir does not yet exist on disk. It walks from the missing leaf up
// toward processCwd, finds the deepest ancestor that actually exists, and
// re-checks that its symlink-resolved real path stays inside processCwd. This
// rejects an intermediate symlink (e.g. working_dir "link/new" where "link" ->
// /etc) that would otherwise be hidden by EvalSymlinks returning ErrNotExist
// for the missing final suffix. The not-yet-existing suffix is created under
// that resolved ancestor at runtime, so a contained ancestor means the eventual
// working_dir is contained too.
func validateExistingAncestorEscape(resolved, cleanedCwd, workingDir string) error {
	realCwd, err := filepath.EvalSymlinks(cleanedCwd)
	if err != nil {
		// Same fallback as the caller: if tracker can't resolve its own cwd,
		// the cleaned form is no worse than the pre-existing string check.
		realCwd = cleanedCwd
	}
	for ancestor := filepath.Dir(resolved); isSubpathOf(ancestor, cleanedCwd); ancestor = filepath.Dir(ancestor) {
		real, err := filepath.EvalSymlinks(ancestor)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue // this ancestor is also missing — keep walking up
			}
			return fmt.Errorf("evaluate working_dir ancestor %q: %w", ancestor, err)
		}
		if !isSubpathOf(real, realCwd) {
			return fmt.Errorf("%w: working_dir %q has existing ancestor %q resolving to %q via symlinks which escapes %q",
				ErrPathEscape, workingDir, ancestor, real, realCwd)
		}
		return nil // deepest existing ancestor is contained — safe
	}
	return nil
}

// isSubpathOf reports whether child is the same as parent or a descendant.
// Both paths must be already cleaned + absolute.
func isSubpathOf(child, parent string) bool {
	if child == parent {
		return true
	}
	sep := string(filepath.Separator)
	if !strings.HasSuffix(parent, sep) {
		parent += sep
	}
	return strings.HasPrefix(child, parent)
}

// validateGlobEntry catches absolute, ~, parent-escape, and malformed-brace
// entries. dippin's DIP142 also catches these; we backstop at runtime.
func validateGlobEntry(g string) error {
	if g == "" {
		return fmt.Errorf("writable_paths entry is empty (fail-closed)")
	}
	if strings.HasPrefix(g, "/") || strings.HasPrefix(g, "~") {
		return fmt.Errorf("%w: writable_paths entry %q is absolute / ~ (must be workspace-relative)",
			ErrPathEscape, g)
	}
	if isWindowsAbsolute(g) {
		return fmt.Errorf("%w: writable_paths entry %q is a Windows absolute path", ErrPathEscape, g)
	}
	cleaned := filepath.Clean(g)
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return fmt.Errorf("%w: writable_paths entry %q escapes via parent traversal (cleaned: %q)",
			ErrPathEscape, g, cleaned)
	}
	// Reject ANY ".." segment in the original (uncleaned) entry. The cleaned
	// check above only catches outward escapes — but configureJail normalizes
	// stored globs with path.Clean (round 6 fix for the `./workspace/**`
	// matcher mismatch), and a clever author-input like "workspace/../**"
	// cleans to "**", which would silently grant write across the entire
	// anchor instead of the workspace subtree the author probably meant
	// to deny themselves out of. Refuse any `..` segment fail-closed
	// (#275 review, Copilot jail.go:162).
	for _, seg := range strings.Split(g, "/") {
		if seg == ".." {
			return fmt.Errorf("%w: writable_paths entry %q contains `..` segment; rejected fail-closed because path.Clean would collapse it to a broader glob than authored",
				ErrPathEscape, g)
		}
	}
	// Brace expansion is unsupported by the matcher (matchOneGlob /
	// path.Match neither expand `{a,b}`). Accepting balanced braces
	// would silently never match anything at runtime — surprising
	// denials on what looks like a legitimate pattern. Refuse all
	// brace usage with an actionable hint (#275 review, Copilot
	// jail.go:95).
	if strings.ContainsAny(g, "{}") {
		return fmt.Errorf("malformed writable_paths entry %q: brace expansion `{a,b}` is not supported; enumerate each pattern as a separate comma-delimited entry (e.g. write `workspace/*.md,workspace/*.yaml` rather than `workspace/*.{md,yaml}`)", g)
	}
	if err := validateDoubleStarPlacement(g); err != nil {
		return err
	}
	// Probe with path.Match so malformed character classes (e.g. "foo[") and
	// other Match-rejected shapes are caught at session setup rather than
	// becoming opaque runtime denials (#272 review, Copilot jail.go:91).
	// "**" is tracker's own metasyntax — path.Match would reject it as a
	// bad pattern, so we strip it before probing.
	probe := strings.ReplaceAll(g, "**", "x")
	if _, err := path.Match(probe, "x"); err != nil {
		return fmt.Errorf("malformed writable_paths entry %q: %v", g, err)
	}
	return nil
}

// isWindowsAbsolute matches `C:\foo` or `\\share\foo` shapes build-OS-independently.
func isWindowsAbsolute(s string) bool {
	if len(s) >= 2 && s[1] == ':' && ((s[0] >= 'A' && s[0] <= 'Z') || (s[0] >= 'a' && s[0] <= 'z')) {
		return true
	}
	if strings.HasPrefix(s, `\\`) {
		return true
	}
	return false
}

// validateDoubleStarPlacement refuses writable_paths entries whose `**`
// placement matchOneGlob doesn't honor. Supported shapes:
//
//   - bare `**`
//   - `prefix/**`
//   - `**/suffix`
//   - `prefix/**/suffix`
//
// Anything else (e.g. `foo/**bar`, multiple `**` segments, `**foo`) passes
// path.Match probing but silently never matches at runtime. Since
// writable_paths is a security boundary, refuse-to-start rather than
// silently misapply policy (#275 review, Copilot jail.go:101).
//
// Additionally, when `**` is present, the *prefix* part (segments before
// `/**`) must be a LITERAL path with no glob metachars (`*`, `?`, `[`).
// matchOneGlob compares the prefix as a literal string via strings.HasPrefix,
// while landlockDirForGlob derives RWDirs from the static-prefix-before-first-
// metachar — so `work*/**` would silently never match writes in-process while
// Landlock would grant write on the entire anchor (the static prefix is just
// `work`, and `landlockDirForGlob` collapses that to the anchor itself).
// That mismatch is broader than authored permissions; refuse fail-closed
// (#275 review, Copilot jail.go:225).
func validateDoubleStarPlacement(g string) error {
	doubleStarCount := 0
	doubleStarIdx := -1
	segs := strings.Split(g, "/")
	for i, seg := range segs {
		if seg == "**" {
			doubleStarCount++
			doubleStarIdx = i
			continue
		}
		if strings.Contains(seg, "**") {
			return fmt.Errorf("malformed writable_paths entry %q: `**` must be its own path segment (got segment %q); supported shapes are `**`, `prefix/**`, `**/suffix`, `prefix/**/suffix`",
				g, seg)
		}
	}
	if doubleStarCount > 1 {
		return fmt.Errorf("malformed writable_paths entry %q: only one `**` segment is supported per glob", g)
	}
	if doubleStarIdx >= 0 {
		for _, seg := range segs[:doubleStarIdx] {
			if strings.ContainsAny(seg, "*?[") {
				return fmt.Errorf("malformed writable_paths entry %q: glob metachars before `**` are not supported (segment %q) — matchOneGlob takes the prefix literally while Landlock derives a broader directory than authored, so the matcher and jail tiers would disagree; use a literal prefix or move `**` earlier",
					g, seg)
			}
		}
	}
	return nil
}
