// ABOUTME: Structural near-duplicate (#532 H1) tests — known-hollow archetypes
// ABOUTME: that MUST flag and known-good tests that MUST NOT (false-positive eval).
package tracker

import "testing"

// findStructural returns the structural group containing the named test, or nil.
func findStructural(rep *TestFidelityReport, name string) *StructuralTestGroup {
	for i := range rep.StructuralGroups {
		for _, tl := range rep.StructuralGroups[i].Tests {
			if tl.Name == name {
				return &rep.StructuralGroups[i]
			}
		}
	}
	return nil
}

func analyzeSrc(t *testing.T, src string) *TestFidelityReport {
	t.Helper()
	dir := t.TempDir()
	writeGo(t, dir, "x_test.go", src)
	rep, err := AnalyzeTestFidelity(dir)
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

// KNOWN-HOLLOW: renamed locals + reordered statements calling the same production
// function. The exact/literal-strip hashes miss it (statement order differs); H1
// must catch it.
const structRenamedReordered = `package x

import "testing"

func TestParseA(t *testing.T) {
	cfg := parseConfig("a=1")
	if cfg.Name != "a" {
		t.Errorf("want a, got %q", cfg.Name)
	}
	if cfg.Val != 1 {
		t.Fatalf("want 1, got %d", cfg.Val)
	}
	if cfg.Val < 0 {
		t.Fatal("negative")
	}
	if cfg.Name == "" {
		t.Fatal("empty name")
	}
}

func TestParseB(t *testing.T) {
	result := parseConfig("b=2")
	if result.Val != 2 {
		t.Fatalf("want 2, got %d", result.Val)
	}
	if result.Name != "b" {
		t.Errorf("want b, got %q", result.Name)
	}
	if result.Name == "" {
		t.Fatal("empty name")
	}
	if result.Val < 0 {
		t.Fatal("negative")
	}
}
`

func TestStructural_FlagsRenamedReordered(t *testing.T) {
	rep := analyzeSrc(t, structRenamedReordered)
	g := findStructural(rep, "TestParseA")
	if g == nil {
		t.Fatalf("expected TestParseA/TestParseB flagged as structural near-dupe, groups=%+v", rep.StructuralGroups)
	}
	if len(g.Tests) != 2 {
		t.Errorf("expected 2 tests in the cluster, got %d", len(g.Tests))
	}
	found := false
	for _, c := range g.SharedCalls {
		if c == "parseConfig" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected parseConfig in SharedCalls, got %v", g.SharedCalls)
	}
	// It must NOT be double-reported as an exact/near-identical duplicate.
	if len(rep.DuplicateGroups) != 0 {
		t.Errorf("reordered bodies should not be exact/near-identical dupes, got %+v", rep.DuplicateGroups)
	}
}

// KNOWN-GOOD (primary guard): two structurally identical tests that call DIFFERENT
// production functions are testing different things — MUST NOT be flagged.
const structDistinctTargets = `package x

import "testing"

func TestConfig(t *testing.T) {
	v := parseConfig("a=1")
	if v.Name != "a" {
		t.Errorf("want a, got %q", v.Name)
	}
	if v.Val != 1 {
		t.Fatalf("want 1, got %d", v.Val)
	}
	if v.Val < 0 {
		t.Fatal("negative")
	}
	if v.Name == "" {
		t.Fatal("empty name")
	}
}

func TestHeader(t *testing.T) {
	v := parseHeader("a=1")
	if v.Name != "a" {
		t.Errorf("want a, got %q", v.Name)
	}
	if v.Val != 1 {
		t.Fatalf("want 1, got %d", v.Val)
	}
	if v.Val < 0 {
		t.Fatal("negative")
	}
	if v.Name == "" {
		t.Fatal("empty name")
	}
}
`

func TestStructural_IgnoresDistinctTargets(t *testing.T) {
	rep := analyzeSrc(t, structDistinctTargets)
	if len(rep.StructuralGroups) != 0 {
		t.Errorf("tests calling different production functions must not be flagged, got %+v", rep.StructuralGroups)
	}
}

// KNOWN-GOOD: genuinely different bodies that happen to call the same production
// function. Structure diverges well below threshold — MUST NOT be flagged.
const structSameCallDifferentBody = `package x

import "testing"

func TestEncode(t *testing.T) {
	out, err := encode(map[string]int{"a": 1, "b": 2})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("empty output")
	}
	if out[0] != '{' {
		t.Errorf("want json object, got %q", out)
	}
}

func TestEncodeRoundTrip(t *testing.T) {
	for _, tc := range []int{1, 2, 3, 4, 5} {
		s, err := encode(map[string]int{"n": tc})
		if err != nil {
			t.Fatalf("case %d: %v", tc, err)
		}
		back := mustDecode(t, s)
		if back["n"] != tc {
			t.Errorf("case %d: round trip lost value", tc)
		}
	}
}
`

func TestStructural_IgnoresSameCallDifferentBody(t *testing.T) {
	rep := analyzeSrc(t, structSameCallDifferentBody)
	if len(rep.StructuralGroups) != 0 {
		t.Errorf("genuinely different bodies must not be flagged, got %+v", rep.StructuralGroups)
	}
}

// KNOWN-GOOD: table-driven / harness-shape tests that share structure but exercise
// no production symbol in common (each drives its own function). MUST NOT flag.
const structHarnessShape = `package x

import "testing"

func TestValidateName(t *testing.T) {
	cases := []struct{ in, want string }{{"a", "A"}, {"b", "B"}}
	for _, c := range cases {
		got := validateName(c.in)
		if got != c.want {
			t.Errorf("validateName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestValidateEmail(t *testing.T) {
	cases := []struct{ in, want string }{{"x", "X"}, {"y", "Y"}}
	for _, c := range cases {
		got := validateEmail(c.in)
		if got != c.want {
			t.Errorf("validateEmail(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
`

func TestStructural_IgnoresHarnessShapeDistinctTargets(t *testing.T) {
	rep := analyzeSrc(t, structHarnessShape)
	if len(rep.StructuralGroups) != 0 {
		t.Errorf("harness-shape tests with distinct targets must not be flagged, got %+v", rep.StructuralGroups)
	}
}

// KNOWN-GOOD: assert-only tests that call no production function are not
// corroborated and must never be flagged (no call-target overlap possible).
const structAssertOnly = `package x

import "testing"

func TestConstantsA(t *testing.T) {
	if maxRetries != 3 {
		t.Errorf("maxRetries = %d", maxRetries)
	}
	if minDelay != 1 {
		t.Errorf("minDelay = %d", minDelay)
	}
	if maxDelay != 10 {
		t.Errorf("maxDelay = %d", maxDelay)
	}
	if backoff != 2 {
		t.Errorf("backoff = %d", backoff)
	}
	if jitter != 0 {
		t.Errorf("jitter = %d", jitter)
	}
}

func TestConstantsB(t *testing.T) {
	if defaultPort != 8080 {
		t.Errorf("defaultPort = %d", defaultPort)
	}
	if defaultHost != "x" {
		t.Errorf("defaultHost = %q", defaultHost)
	}
	if defaultPath != "/" {
		t.Errorf("defaultPath = %q", defaultPath)
	}
	if defaultUser != "root" {
		t.Errorf("defaultUser = %q", defaultUser)
	}
	if defaultScheme != "https" {
		t.Errorf("defaultScheme = %q", defaultScheme)
	}
}
`

func TestStructural_IgnoresAssertOnlyNoProductionCall(t *testing.T) {
	rep := analyzeSrc(t, structAssertOnly)
	if len(rep.StructuralGroups) != 0 {
		t.Errorf("assert-only tests with no production call must not be flagged, got %+v", rep.StructuralGroups)
	}
}
