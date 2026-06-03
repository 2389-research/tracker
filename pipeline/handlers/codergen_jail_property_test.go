// ABOUTME: Property/invariant tests for the writable_paths jail wiring in
// ABOUTME: codergen_jail.go (issue #282, follow-up to #275/#272) using pgregory.net/rapid.
package handlers

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/2389-research/tracker/agent"
	execpkg "github.com/2389-research/tracker/agent/exec"
	"github.com/2389-research/tracker/pipeline"

	"pgregory.net/rapid"
)

// These tests pin the matcher, path-relativization, refuse-to-start gates, and
// the WriteOpener/Remover closures that configureJail installs. They generalize
// the example-based tables in codergen_jail_test.go (which stay as-is).
//
// fakeBackendForGate and NativeBackend are defined in codergen_jail_test.go;
// this file shares them (same package).

// drawSeg / drawPath generate literal (metachar-free) path segments and paths.
func drawSeg(t *rapid.T, label string) string {
	return rapid.StringMatching(`^[a-z][a-z0-9_-]{0,7}$`).Draw(t, label)
}

func drawPath(t *rapid.T, label string) string {
	return strings.Join(rapid.SliceOfN(rapid.StringMatching(`^[a-z][a-z0-9_-]{0,7}$`), 1, 4).Draw(t, label), "/")
}

// drawValidGlob draws an entry in one of the documented supported shapes.
func drawValidGlob(t *rapid.T, label string) string {
	switch rapid.IntRange(0, 4).Draw(t, label+"_shape") {
	case 0:
		return "**"
	case 1:
		return drawPath(t, label+"_p") + "/**"
	case 2:
		return "**/" + drawPath(t, label+"_s")
	case 3:
		return drawPath(t, label+"_lit") // static literal path
	default:
		return drawPath(t, label+"_dir") + "/*" // single-segment star
	}
}

// --- matchOneGlob / matchWritablePath ---

// TestMatchOneGlob_PrefixDoublestar_Rapid: `prefix/**` matches the prefix
// directory itself and every descendant (the headline example in issue #282).
func TestMatchOneGlob_PrefixDoublestar_Rapid(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		prefix := drawPath(t, "prefix")
		g := prefix + "/**"
		if !matchOneGlob(prefix, g) {
			t.Fatalf("matchOneGlob(%q, %q) = false, want true (prefix dir itself)", prefix, g)
		}
		full := prefix + "/" + drawPath(t, "leaf")
		if !matchOneGlob(full, g) {
			t.Fatalf("matchOneGlob(%q, %q) = false, want true (descendant)", full, g)
		}
	})
}

// TestMatchOneGlob_BareDoublestar_Rapid: bare `**` matches any relative path.
func TestMatchOneGlob_BareDoublestar_Rapid(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		p := drawPath(t, "p")
		if !matchOneGlob(p, "**") {
			t.Fatalf("matchOneGlob(%q, \"**\") = false, want true", p)
		}
	})
}

// TestMatchOneGlob_SuffixDoublestar_Rapid: `**/suffix` matches any path that
// ends in the suffix component(s) at any depth, including the top level.
func TestMatchOneGlob_SuffixDoublestar_Rapid(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		sufSegs := rapid.SliceOfN(rapid.StringMatching(`^[a-z][a-z0-9_-]{0,7}$`), 1, 3).Draw(t, "suf")
		suffix := strings.Join(sufSegs, "/")
		g := "**/" + suffix
		preSegs := rapid.SliceOfN(rapid.StringMatching(`^[a-z][a-z0-9_-]{0,7}$`), 0, 3).Draw(t, "pre")
		full := strings.Join(append(append([]string{}, preSegs...), sufSegs...), "/")
		if !matchOneGlob(full, g) {
			t.Fatalf("matchOneGlob(%q, %q) = false, want true (suffix at any depth)", full, g)
		}
	})
}

// TestMatchOneGlob_PrefixSuffixDoublestar_Rapid: `prefix/**/suffix` matches
// `prefix/<any-intermediate>/suffix`.
func TestMatchOneGlob_PrefixSuffixDoublestar_Rapid(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		prefix := drawPath(t, "prefix")
		suf := drawSeg(t, "suf")
		mid := rapid.SliceOfN(rapid.StringMatching(`^[a-z][a-z0-9_-]{0,7}$`), 0, 3).Draw(t, "mid")
		g := prefix + "/**/" + suf
		parts := append([]string{prefix}, mid...)
		parts = append(parts, suf)
		full := strings.Join(parts, "/")
		if !matchOneGlob(full, g) {
			t.Fatalf("matchOneGlob(%q, %q) = false, want true (prefix/**/suffix)", full, g)
		}
	})
}

// TestMatchOneGlob_PrefixDoublestar_RejectsOtherTop_Rapid: a path under a
// different top-level directory does NOT match `prefix/**`. Distinct "pre"/"oth"
// tags guarantee the two top dirs differ without needing rejection sampling.
func TestMatchOneGlob_PrefixDoublestar_RejectsOtherTop_Rapid(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		prefix := "pre" + drawSeg(t, "prefix")
		g := prefix + "/**"
		other := "oth" + drawSeg(t, "other")
		full := other + "/" + drawPath(t, "leaf")
		if matchOneGlob(full, g) {
			t.Fatalf("matchOneGlob(%q, %q) = true, want false (different top dir)", full, g)
		}
		if matchOneGlob(other, g) {
			t.Fatalf("matchOneGlob(%q, %q) = true, want false (sibling top dir)", other, g)
		}
	})
}

// TestMatchOneGlob_LiteralExact_Rapid: a static (metachar-free) glob matches
// only the exact path equal to it.
func TestMatchOneGlob_LiteralExact_Rapid(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		lit := drawPath(t, "lit")
		if !matchOneGlob(lit, lit) {
			t.Fatalf("matchOneGlob(%q, %q) = false, want true (literal self-match)", lit, lit)
		}
		other := lit + drawSeg(t, "extra") // strictly longer, so != lit
		if matchOneGlob(other, lit) {
			t.Fatalf("matchOneGlob(%q, %q) = true, want false (literal matches only itself)", other, lit)
		}
	})
}

// TestMatchWritablePath_IsUnion_Rapid: matchWritablePath is exactly the OR of
// matchOneGlob over the list. Pins the union contract against refactors.
func TestMatchWritablePath_IsUnion_Rapid(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(1, 4).Draw(t, "n")
		globs := make([]string, n)
		for i := range globs {
			globs[i] = drawValidGlob(t, "g")
		}
		p := drawPath(t, "p")
		want := false
		for _, g := range globs {
			if matchOneGlob(p, g) {
				want = true
				break
			}
		}
		if got := matchWritablePath(p, globs); got != want {
			t.Fatalf("matchWritablePath(%q, %v) = %v, want %v (union of matchOneGlob)", p, globs, got, want)
		}
	})
}

// --- relPathForJail ---

// TestRelPathForJail_ContainedRoundTrips_Rapid: for any literal relative path,
// relPathForJail(anchor, anchor/rel) returns rel with no error.
func TestRelPathForJail_ContainedRoundTrips_Rapid(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		anchor := "/" + drawPath(t, "anchor")
		rel := drawPath(t, "rel")
		abs := filepath.Join(anchor, rel)
		got, err := relPathForJail(anchor, abs)
		if err != nil {
			t.Fatalf("relPathForJail(%q, %q) = err %v, want nil", anchor, abs, err)
		}
		if got != rel {
			t.Fatalf("relPathForJail(%q, %q) = %q, want %q", anchor, abs, got, rel)
		}
	})
}

// TestRelPathForJail_RejectsParentEscape_Rapid: the anchor's own parent
// directory relativizes to ".." and must be refused with ErrPathEscape.
func TestRelPathForJail_RejectsParentEscape_Rapid(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		anchor := "/" + drawPath(t, "anchor")
		parent := filepath.Dir(anchor) // Rel(anchor, parent) == ".."
		_, err := relPathForJail(anchor, parent)
		if !errors.Is(err, execpkg.ErrPathEscape) {
			t.Fatalf("relPathForJail(%q, %q) = %v, want errors.Is ErrPathEscape", anchor, parent, err)
		}
	})
}

// TestRelPathForJail_AllowsDotDotPrefixName_Rapid: a bare leaf name that merely
// STARTS with ".." (e.g. "..cache") stays below the anchor and must be allowed
// — only a true ".."/"../" traversal escapes (#272 review nuance).
func TestRelPathForJail_AllowsDotDotPrefixName_Rapid(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		anchor := "/" + drawPath(t, "anchor")
		name := ".." + drawSeg(t, "name") // "..foo", never exactly ".."
		abs := filepath.Join(anchor, name)
		got, err := relPathForJail(anchor, abs)
		if err != nil {
			t.Fatalf("relPathForJail(%q, %q) = err %v, want nil (bare ..name is below anchor)", anchor, abs, err)
		}
		if got != name {
			t.Fatalf("relPathForJail(%q, %q) = %q, want %q", anchor, abs, got, name)
		}
	})
}

// --- configureJail refusal gates ---

// TestConfigureJail_G1_RefusesInvalidGlobs_Rapid: any invalid glob causes
// configureJail to refuse (G1) without wiring any env hooks. Cross-platform —
// G1 runs before the Landlock probe.
func TestConfigureJail_G1_RefusesInvalidGlobs_Rapid(t *testing.T) {
	cwd := t.TempDir()
	rapid.Check(t, func(t *rapid.T) {
		env := execpkg.NewLocalEnvironment(cwd)
		cfg := agent.SessionConfig{
			WorkingDir:       "work",
			WritablePaths:    []string{drawInvalidGlob(t, "g")},
			WritablePathsSet: true,
			Backend:          "native",
		}
		enabled, err := configureJail(&cfg, env, cwd)
		if err == nil {
			t.Fatalf("configureJail accepted invalid glob %v, want refuse", cfg.WritablePaths)
		}
		if enabled {
			t.Fatalf("configureJail enabled=true on G1 refusal")
		}
		if env.CommandWrapper != nil || env.WriteOpener != nil || env.Remover != nil {
			t.Fatalf("env hooks wired despite G1 refusal")
		}
	})
}

// TestConfigureJail_G2_RefusesNonNativeBackend_Rapid: with valid paths, any
// non-native backend is refused by name (G2) before any hooks are wired. G2
// runs before the Landlock probe, so this holds on every OS.
func TestConfigureJail_G2_RefusesNonNativeBackend_Rapid(t *testing.T) {
	cwd := t.TempDir()
	rapid.Check(t, func(t *rapid.T) {
		backend := drawNonNativeBackend(t)
		env := execpkg.NewLocalEnvironment(cwd)
		cfg := agent.SessionConfig{
			WorkingDir:       "work",
			WritablePaths:    []string{drawValidGlob(t, "g")},
			WritablePathsSet: true,
			Backend:          backend,
		}
		enabled, err := configureJail(&cfg, env, cwd)
		if err == nil {
			t.Fatalf("configureJail accepted backend %q, want refuse", backend)
		}
		if enabled {
			t.Fatalf("configureJail enabled=true on G2 refusal for %q", backend)
		}
		if !strings.Contains(err.Error(), backend) {
			t.Fatalf("configureJail err %v does not name backend %q", err, backend)
		}
		if env.CommandWrapper != nil || env.WriteOpener != nil || env.Remover != nil {
			t.Fatalf("env hooks wired despite G2 refusal")
		}
	})
}

// drawInvalidGlob draws a glob that ValidateWritablePaths must reject (mix of
// escape-class and malformed-class).
func drawInvalidGlob(t *rapid.T, label string) string {
	switch rapid.IntRange(0, 3).Draw(t, label+"_kind") {
	case 0:
		return "/" + drawPath(t, label+"_abs") // absolute → escape
	case 1:
		return "~/" + drawPath(t, label+"_tilde") // tilde → escape
	case 2:
		return drawPath(t, label+"_brace") + "/*.{md" // brace → malformed
	default:
		return drawSeg(t, label+"_meta") + "*/**" // metachar before ** → malformed
	}
}

// drawNonNativeBackend draws a backend name configureJail's G2 must refuse:
// the two known out-of-process backends plus an arbitrary unknown name.
func drawNonNativeBackend(t *rapid.T) string {
	switch rapid.IntRange(0, 2).Draw(t, "backend_kind") {
	case 0:
		return "claude-code"
	case 1:
		return "acp"
	default:
		return "xb" + drawSeg(t, "unknown") // never "", "native", "claude-code", "acp"
	}
}

// TestRefuseWritablePathsOnUnsupportedBackend_Matrix is the exhaustive
// dispatcher-layer table: {nil, unset, writable} × {native, non-native}. The
// gate keys ONLY on the concrete *NativeBackend type, so acp / claude-code /
// unknown all collapse to the single "non-native" column here; their distinct
// refusal *messages* are exercised by TestConfigureJail_G2_RefusesNonNativeBackend_Rapid.
func TestRefuseWritablePathsOnUnsupportedBackend_Matrix(t *testing.T) {
	nodeWritable := &pipeline.Node{Attrs: map[string]string{"writable_paths": "workspace/**"}}
	nodeUnset := &pipeline.Node{Attrs: map[string]string{}}
	native := &NativeBackend{}
	nonNative := fakeBackendForGate{}

	cases := []struct {
		name    string
		node    *pipeline.Node
		backend pipeline.AgentBackend
		wantErr bool
	}{
		{"nil node + native", nil, native, false},
		{"nil node + non-native", nil, nonNative, false},
		{"unset + native", nodeUnset, native, false},
		{"unset + non-native", nodeUnset, nonNative, false},
		{"writable + native", nodeWritable, native, false},
		{"writable + non-native", nodeWritable, nonNative, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := refuseWritablePathsOnUnsupportedBackend(tc.node, tc.backend)
			if (err != nil) != tc.wantErr {
				t.Fatalf("refuseWritablePathsOnUnsupportedBackend = %v, wantErr=%v", err, tc.wantErr)
			}
		})
	}
}

// --- WriteOpener / Remover closures (Landlock-gated) ---

// TestConfigureJail_Closures_Rapid exercises the installed WriteOpener/Remover
// over random paths in three classes:
//   - allowed (matches the glob): write+remove succeed, file really created;
//   - denied by glob (under anchor, no glob match): ErrPathNotAllowed, and the
//     parent dir is NOT created (mkdir runs AFTER the glob check — a rejected
//     write must leave no empty directories);
//   - escaping the anchor: ErrPathEscape.
//
// Requires a working Landlock (the closures call the real Linux openat2 seam);
// skips otherwise.
func TestConfigureJail_Closures_Rapid(t *testing.T) {
	if err := execpkg.ProbeLandlock(); err != nil {
		t.Skipf("Landlock unavailable: %v", err)
	}
	anchor := t.TempDir()
	env := execpkg.NewLocalEnvironment(anchor)
	cfg := agent.SessionConfig{
		WorkingDir:       ".",
		WritablePaths:    []string{"workspace/**"},
		WritablePathsSet: true,
		Backend:          "native",
	}
	enabled, err := configureJail(&cfg, env, anchor)
	if err != nil || !enabled {
		t.Fatalf("configureJail setup: enabled=%v err=%v", enabled, err)
	}
	if env.WriteOpener == nil || env.Remover == nil {
		t.Fatal("configureJail did not wire WriteOpener/Remover")
	}

	rapid.Check(t, func(rt *rapid.T) {
		leaf := drawSeg(rt, "leaf") + ".txt"
		switch rapid.IntRange(0, 2).Draw(rt, "case") {
		case 0: // allowed
			abs := filepath.Join(anchor, "workspace", leaf)
			f, err := env.WriteOpener(abs, 0o644)
			if err != nil {
				rt.Fatalf("allowed write %q rejected: %v", abs, err)
			}
			_ = f.Close()
			if _, e := os.Stat(abs); e != nil {
				rt.Fatalf("allowed file %q not created: %v", abs, e)
			}
			if err := env.Remover(abs); err != nil {
				rt.Fatalf("allowed remove %q failed: %v", abs, err)
			}
			if _, e := os.Stat(abs); !os.IsNotExist(e) {
				rt.Fatalf("file %q persisted after Remover (stat err=%v)", abs, e)
			}
		case 1: // denied by glob
			topDir := "deny" + drawSeg(rt, "top") // never "workspace"
			abs := filepath.Join(anchor, topDir, leaf)
			_, err := env.WriteOpener(abs, 0o644)
			if !errors.Is(err, execpkg.ErrPathNotAllowed) {
				rt.Fatalf("denied write %q = %v, want errors.Is ErrPathNotAllowed", abs, err)
			}
			// mkdir ordering: the rejected write must not have created the dir.
			if _, e := os.Stat(filepath.Join(anchor, topDir)); e == nil {
				rt.Fatalf("rejected write created parent dir %q (mkdir ran before glob check)", topDir)
			}
			if err := env.Remover(abs); !errors.Is(err, execpkg.ErrPathNotAllowed) {
				rt.Fatalf("denied remove %q = %v, want errors.Is ErrPathNotAllowed", abs, err)
			}
		default: // escapes anchor
			abs := filepath.Join(filepath.Dir(anchor), "escape"+drawSeg(rt, "e"), leaf)
			_, err := env.WriteOpener(abs, 0o644)
			if !errors.Is(err, execpkg.ErrPathEscape) {
				rt.Fatalf("escape write %q = %v, want errors.Is ErrPathEscape", abs, err)
			}
			if err := env.Remover(abs); !errors.Is(err, execpkg.ErrPathEscape) {
				rt.Fatalf("escape remove %q = %v, want errors.Is ErrPathEscape", abs, err)
			}
		}
	})
}
