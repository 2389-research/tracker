// ABOUTME: Cross-platform property/invariant tests for the writable_paths jail
// ABOUTME: validation surface (issue #282, follow-up to #275/#272) using pgregory.net/rapid.
package exec

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"pgregory.net/rapid"
)

// These tests pin first-principles properties of the cross-platform jail
// validation surface (ValidateWritablePaths / validateGlobEntry and their
// helpers) so a regression in any documented contract fails fast with a
// shrunk counter-example rather than being rediscovered in review (the #275
// review took 13 rounds / ~30 findings on exactly these properties).
//
// The example-based table in jail_test.go stays as-is; these generalize it.

// genLiteralSegment draws a single path segment that is a pure literal: no
// glob metachars (*?[]{}), no separators, no `.`/`..`, no `~`/`:`/`\`. Starts
// with a letter so it can never be empty or collide with a Windows drive or a
// dot-special. Shared by every property in package exec (this file has no
// build constraint, so jail_linux_test.go and jail_other_test.go see it too).
func genLiteralSegment(t *rapid.T, label string) string {
	return rapid.StringMatching(`^[a-z][a-z0-9_-]{0,7}$`).Draw(t, label)
}

// genLiteralSegments draws between min and max literal segments.
func genLiteralSegments(t *rapid.T, label string, min, max int) []string {
	return rapid.SliceOfN(rapid.StringMatching(`^[a-z][a-z0-9_-]{0,7}$`), min, max).Draw(t, label)
}

// genLiteralPath draws a workspace-relative literal path of 1-4 segments joined
// by `/` (e.g. "workspace/sub/out"). Always valid input to validateGlobEntry.
func genLiteralPath(t *rapid.T, label string) string {
	return strings.Join(genLiteralSegments(t, label, 1, 4), "/")
}

// genHappyGlob draws a writable_paths entry that validateGlobEntry MUST accept:
// every documented supported shape.
func genHappyGlob(t *rapid.T, label string) string {
	switch rapid.IntRange(0, 5).Draw(t, label+"_shape") {
	case 0:
		return "**"
	case 1:
		return genLiteralPath(t, label+"_p") + "/**"
	case 2:
		return "**/" + genLiteralPath(t, label+"_s")
	case 3:
		return genLiteralPath(t, label+"_p") + "/**/" + genLiteralPath(t, label+"_s")
	case 4:
		// Plain literal path (static glob → literal match at runtime).
		return genLiteralPath(t, label+"_lit")
	default:
		// Single-segment `*` glob (path.Match territory, not `**`).
		return genLiteralPath(t, label+"_dir") + "/*"
	}
}

// genEscapeGlob draws a writable_paths entry that MUST be refused with the
// ErrPathEscape sentinel ("tried to point the jail outside the session root").
func genEscapeGlob(t *rapid.T, label string) string {
	base := genLiteralPath(t, label+"_base")
	switch rapid.IntRange(0, 5).Draw(t, label+"_kind") {
	case 0:
		return "/" + base // absolute
	case 1:
		return "~/" + base // home-relative
	case 2:
		return "~" + base // bare tilde prefix
	case 3:
		// Windows drive-absolute, build-OS-independent.
		drive := rapid.StringMatching(`^[A-Za-z]$`).Draw(t, label+"_drive")
		return drive + `:\` + base
	case 4:
		return `\\` + base // Windows UNC
	default:
		// A `..` segment anywhere — rejected fail-closed because path.Clean
		// would collapse it into a broader glob than authored.
		pre := genLiteralSegments(t, label+"_pre", 0, 3)
		post := genLiteralSegments(t, label+"_post", 0, 3)
		all := append(append(append([]string{}, pre...), ".."), post...)
		return strings.Join(all, "/")
	}
}

// genMalformedGlob draws a writable_paths entry that MUST be refused with a
// PLAIN (non-sentinel) error: a bad glob shape rather than an escape attempt.
func genMalformedGlob(t *rapid.T, label string) string {
	switch rapid.IntRange(0, 4).Draw(t, label+"_kind") {
	case 0:
		// Brace usage (balanced or not) — the matcher never expands it.
		return genLiteralPath(t, label+"_brace") + "{"
	case 1:
		// `**` glued to other chars in the same segment.
		return genLiteralPath(t, label+"_glue") + "/**" + genLiteralSegment(t, label+"_glueleaf")
	case 2:
		// More than one `**` segment.
		return genLiteralSegment(t, label+"_a") + "/**/" + genLiteralSegment(t, label+"_b") + "/**/" + genLiteralSegment(t, label+"_c")
	case 3:
		// Glob metachars in the literal prefix before `**`.
		return genLiteralSegment(t, label+"_meta") + "*/**"
	default:
		// Unbalanced character class — path.Match rejects it.
		return genLiteralPath(t, label+"_cc") + "["
	}
}

func TestValidateGlobEntry_HappyShapes_Rapid(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		g := genHappyGlob(t, "g")
		if err := validateGlobEntry(g); err != nil {
			t.Fatalf("validateGlobEntry(%q) = %v, want nil (documented supported shape)", g, err)
		}
	})
}

func TestValidateGlobEntry_EscapeClass_Rapid(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		g := genEscapeGlob(t, "g")
		err := validateGlobEntry(g)
		if !errors.Is(err, ErrPathEscape) {
			t.Fatalf("validateGlobEntry(%q) = %v, want errors.Is(err, ErrPathEscape)", g, err)
		}
	})
}

func TestValidateGlobEntry_MalformedClass_NotEscape_Rapid(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		g := genMalformedGlob(t, "g")
		err := validateGlobEntry(g)
		if err == nil {
			t.Fatalf("validateGlobEntry(%q) = nil, want a malformed-shape error", g)
		}
		// The class invariant: malformed shapes are PLAIN errors, never the
		// escape sentinel. Upstream consumers distinguish "bad shape" from
		// "escape attempt" via errors.Is — that distinction must hold.
		if errors.Is(err, ErrPathEscape) {
			t.Fatalf("validateGlobEntry(%q) = %v, must NOT be classified as ErrPathEscape (malformed != escape)", g, err)
		}
	})
}

// TestValidateWritablePaths_PreservesGlobClass_Rapid pins that the PUBLIC entry
// point (not just the lowercase helper) preserves the escape-vs-malformed
// sentinel partition for whichever bad glob appears in the list.
func TestValidateWritablePaths_PreservesGlobClass_Rapid(t *testing.T) {
	cwd := t.TempDir()
	rapid.Check(t, func(t *rapid.T) {
		// Valid contained working_dir so only the glob class is under test.
		const wd = "work"
		if rapid.Bool().Draw(t, "escape") {
			g := genEscapeGlob(t, "g")
			if err := ValidateWritablePaths(wd, []string{g}, cwd); !errors.Is(err, ErrPathEscape) {
				t.Fatalf("ValidateWritablePaths(glob=%q) = %v, want errors.Is ErrPathEscape", g, err)
			}
		} else {
			g := genMalformedGlob(t, "g")
			err := ValidateWritablePaths(wd, []string{g}, cwd)
			if err == nil {
				t.Fatalf("ValidateWritablePaths(glob=%q) = nil, want malformed error", g)
			}
			if errors.Is(err, ErrPathEscape) {
				t.Fatalf("ValidateWritablePaths(glob=%q) = %v, malformed must not be ErrPathEscape", g, err)
			}
		}
	})
}

// TestValidateWritablePaths_HappyGlobs_Rapid: a contained working_dir plus any
// list of supported glob shapes validates clean.
func TestValidateWritablePaths_HappyGlobs_Rapid(t *testing.T) {
	cwd := t.TempDir()
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(1, 4).Draw(t, "n")
		globs := make([]string, n)
		for i := range globs {
			globs[i] = genHappyGlob(t, "g")
		}
		if err := ValidateWritablePaths("work", globs, cwd); err != nil {
			t.Fatalf("ValidateWritablePaths(work, %v) = %v, want nil", globs, err)
		}
	})
}

// TestValidateWorkingDir_ContainedRelative_Rapid: any relative working_dir
// built from literal segments resolves under the process cwd → accepted. The
// path need not exist; EvalSymlinks returning ErrNotExist is the safe case.
func TestValidateWorkingDir_ContainedRelative_Rapid(t *testing.T) {
	cwd := t.TempDir()
	rapid.Check(t, func(t *rapid.T) {
		rel := genLiteralPath(t, "rel")
		if err := ValidateWritablePaths(rel, []string{"**"}, cwd); err != nil {
			t.Fatalf("ValidateWritablePaths(workingDir=%q) = %v, want nil (contained relative path)", rel, err)
		}
	})
}

// TestValidateWorkingDir_AbsoluteEscape_Rapid: an absolute working_dir that is
// not under the process cwd is refused with ErrPathEscape.
func TestValidateWorkingDir_AbsoluteEscape_Rapid(t *testing.T) {
	cwd := t.TempDir()
	cleanCwd := filepath.Clean(cwd)
	rapid.Check(t, func(t *rapid.T) {
		abs := "/" + genLiteralPath(t, "abs")
		// Guard against the astronomically unlikely case the random absolute
		// path lands inside the temp cwd; that case is not an escape.
		if isSubpathOf(filepath.Clean(abs), cleanCwd) {
			t.Skip("generated absolute path happens to be under cwd")
		}
		err := ValidateWritablePaths(abs, []string{"**"}, cwd)
		if !errors.Is(err, ErrPathEscape) {
			t.Fatalf("ValidateWritablePaths(workingDir=%q, cwd=%q) = %v, want errors.Is ErrPathEscape", abs, cwd, err)
		}
	})
}

// TestIsWindowsAbsolute_MatchesSpec_Rapid pins isWindowsAbsolute against its
// documented predicate over arbitrary input, so dropping either the
// drive-letter or UNC branch diverges immediately.
func TestIsWindowsAbsolute_MatchesSpec_Rapid(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		s := rapid.StringN(0, 12, 12).Draw(t, "s")
		want := (len(s) >= 2 && s[1] == ':' && ((s[0] >= 'A' && s[0] <= 'Z') || (s[0] >= 'a' && s[0] <= 'z'))) ||
			strings.HasPrefix(s, `\\`)
		if got := isWindowsAbsolute(s); got != want {
			t.Fatalf("isWindowsAbsolute(%q) = %v, want %v", s, got, want)
		}
	})
}

// TestIsSubpathOf_Rapid pins the three containment invariants: reflexivity, any
// literal descendant is contained, and a sibling sharing a string prefix but
// not a path-component boundary is NOT contained (the bug class isSubpathOf's
// separator guard exists to prevent).
//
// isSubpathOf compares using string(filepath.Separator) and operates on cleaned
// OS paths, so the test builds native-separator paths via filepath.Join rather
// than hard-coded "/" — otherwise the descendant case would mismatch on Windows
// (`/a/b/c` vs prefix `/a/b\`). (Globs elsewhere are forward-slash by spec, so
// those generators legitimately keep "/".)
func TestIsSubpathOf_Rapid(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		sep := string(filepath.Separator)
		parent := filepath.Join(append([]string{sep}, genLiteralSegments(t, "p", 1, 4)...)...)

		if !isSubpathOf(parent, parent) {
			t.Fatalf("isSubpathOf(%q, %q) = false, want true (reflexive)", parent, parent)
		}

		child := filepath.Join(parent, genLiteralPath(t, "sub"))
		if !isSubpathOf(child, parent) {
			t.Fatalf("isSubpathOf(%q, %q) = false, want true (descendant)", child, parent)
		}

		// Sibling sharing a string prefix but NOT a path-component boundary
		// (e.g. "/a/b" vs "/a/bx") — appended WITHOUT a separator on purpose.
		sibling := parent + genLiteralSegment(t, "suffix")
		if isSubpathOf(sibling, parent) {
			t.Fatalf("isSubpathOf(%q, %q) = true, want false (prefix-but-not-child)", sibling, parent)
		}
	})
}
