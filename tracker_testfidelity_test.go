// ABOUTME: Tests the duplicate-test-body detector — the #489 "byte-for-byte
// ABOUTME: duplicate required test" evidence and near-duplicates that differ only in a literal.
package tracker

import (
	"os"
	"path/filepath"
	"testing"
)

func writeGo(t *testing.T, dir, name, src string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

// The #489 case: two "distinct" required tests with byte-identical bodies.
const dupTestsSrc = `package x

import "testing"

func TestNoPRSkip(t *testing.T) {
	got := skipReason("no_pr")
	if got != "skipped" {
		t.Errorf("want skipped, got %q", got)
	}
	if len(got) == 0 {
		t.Fatal("empty")
	}
}

func TestAmbiguousPRSkip(t *testing.T) {
	got := skipReason("no_pr")
	if got != "skipped" {
		t.Errorf("want skipped, got %q", got)
	}
	if len(got) == 0 {
		t.Fatal("empty")
	}
}

func TestDistinct(t *testing.T) {
	x := compute(1, 2)
	if x != 3 {
		t.Errorf("want 3, got %d", x)
	}
	if x < 0 {
		t.Fatal("negative")
	}
}
`

func TestAnalyzeTestFidelity_DetectsByteDuplicate(t *testing.T) {
	dir := t.TempDir()
	writeGo(t, dir, "x_test.go", dupTestsSrc)

	rep, err := AnalyzeTestFidelity(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.DuplicateGroups) != 1 {
		t.Fatalf("expected 1 duplicate group, got %d: %+v", len(rep.DuplicateGroups), rep.DuplicateGroups)
	}
	g := rep.DuplicateGroups[0]
	if g.Kind != "identical" {
		t.Errorf("kind = %q, want identical", g.Kind)
	}
	names := map[string]bool{}
	for _, tl := range g.Tests {
		names[tl.Name] = true
	}
	if !names["TestNoPRSkip"] || !names["TestAmbiguousPRSkip"] {
		t.Errorf("expected the two duplicate tests, got %+v", g.Tests)
	}
	if names["TestDistinct"] {
		t.Error("a genuinely distinct test was flagged")
	}
}

// Near-duplicate: same structure, differs only in literal values — the
// "copy the test, tweak the fixture string" pattern.
const nearDupSrc = `package x

import "testing"

func TestAlpha(t *testing.T) {
	got := classify("alpha")
	if got != "A" {
		t.Errorf("want A, got %q", got)
	}
	if got == "" {
		t.Fatal("empty")
	}
}

func TestBeta(t *testing.T) {
	got := classify("beta")
	if got != "B" {
		t.Errorf("want B, got %q", got)
	}
	if got == "" {
		t.Fatal("empty")
	}
}
`

func TestAnalyzeTestFidelity_DetectsNearDuplicate(t *testing.T) {
	dir := t.TempDir()
	writeGo(t, dir, "y_test.go", nearDupSrc)

	rep, err := AnalyzeTestFidelity(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.DuplicateGroups) != 1 || rep.DuplicateGroups[0].Kind != "near-identical" {
		t.Fatalf("expected 1 near-identical group, got %+v", rep.DuplicateGroups)
	}
}

func TestAnalyzeTestFidelity_CleanTreeNoFindings(t *testing.T) {
	dir := t.TempDir()
	writeGo(t, dir, "z_test.go", `package x

import "testing"

func TestOne(t *testing.T) {
	if add(1, 1) != 2 { t.Fatal("math is broken") }
	if add(2, 2) != 4 { t.Fatal("still broken") }
	if add(0, 0) != 0 { t.Fatal("zero") }
}

func TestTwo(t *testing.T) {
	s := greet("world")
	if s != "hello world" { t.Errorf("got %q", s) }
	if len(s) == 0 { t.Fatal("empty") }
	if s[0] != 'h' { t.Fatal("no h") }
}
`)
	rep, err := AnalyzeTestFidelity(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.DuplicateGroups) != 0 {
		t.Errorf("clean tree should have no findings, got %+v", rep.DuplicateGroups)
	}
}

// A trivial stub body (below the statement threshold) must not be flagged even
// when two are identical — that would be noise.
func TestAnalyzeTestFidelity_IgnoresTrivialStubs(t *testing.T) {
	dir := t.TempDir()
	writeGo(t, dir, "s_test.go", `package x

import "testing"

func TestStubA(t *testing.T) { t.Skip("todo") }
func TestStubB(t *testing.T) { t.Skip("todo") }
`)
	rep, err := AnalyzeTestFidelity(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.DuplicateGroups) != 0 {
		t.Errorf("trivial stubs should not be flagged, got %+v", rep.DuplicateGroups)
	}
}
