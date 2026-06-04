// ABOUTME: Tests for the jailcheck analyzer against clean and violating fixtures.
// ABOUTME: Pins the env-routed/read-only/annotated-fallback exemptions and the bypass detection.
package main

import (
	"sort"
	"testing"
)

func TestCheckDir_Clean(t *testing.T) {
	violations, err := checkDir("testdata/clean")
	if err != nil {
		t.Fatalf("checkDir: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("expected 0 violations in clean fixture, got %d: %+v", len(violations), violations)
	}
}

func TestCheckDir_Violation(t *testing.T) {
	violations, err := checkDir("testdata/violation")
	if err != nil {
		t.Fatalf("checkDir: %v", err)
	}

	got := make([]string, 0, len(violations))
	for _, v := range violations {
		got = append(got, v.Call)
	}
	sort.Strings(got)

	want := []string{"os.MkdirAll", "os.Remove", "os.WriteFile"}
	if len(got) != len(want) {
		t.Fatalf("expected %v, got %v (full: %+v)", want, got, violations)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("expected %v, got %v", want, got)
		}
	}

	// Every violation must carry a real file/line and the enclosing func name,
	// so the CI failure message points the contributor at the exact site.
	for _, v := range violations {
		if v.Line == 0 || v.File == "" || v.Func == "" {
			t.Errorf("violation missing location/func: %+v", v)
		}
	}
}

func TestCheckDir_AliasedImport(t *testing.T) {
	// An aliased os import (`stdos "os"`) must still be caught via import
	// resolution; the aliased read-only call must not be flagged.
	violations, err := checkDir("testdata/aliased")
	if err != nil {
		t.Fatalf("checkDir: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("expected 1 violation in aliased fixture, got %d: %+v", len(violations), violations)
	}
	if violations[0].Call != "os.WriteFile" || violations[0].Func != "rawWriteAliased" {
		t.Fatalf("expected os.WriteFile in rawWriteAliased, got %+v", violations[0])
	}
}

func TestCheckDir_FuncValueCapture(t *testing.T) {
	// Hoisting os.WriteFile into a variable (`wf := os.WriteFile; wf(...)`)
	// must still be caught: the lint matches the selector reference, not just
	// the call. The read-only capture must not be flagged.
	violations, err := checkDir("testdata/funcvalue")
	if err != nil {
		t.Fatalf("checkDir: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("expected 1 violation in funcvalue fixture, got %d: %+v", len(violations), violations)
	}
	if violations[0].Call != "os.WriteFile" || violations[0].Func != "captureWrite" {
		t.Fatalf("expected os.WriteFile in captureWrite, got %+v", violations[0])
	}
}

func TestCheckDir_DotImport(t *testing.T) {
	// A dot-import of os hides every mutating call behind a bare identifier;
	// the import itself must be flagged so CI fails fast.
	violations, err := checkDir("testdata/dotimport")
	if err != nil {
		t.Fatalf("checkDir: %v", err)
	}
	if len(violations) != 1 {
		t.Fatalf("expected 1 violation in dotimport fixture, got %d: %+v", len(violations), violations)
	}
	if violations[0].Call != `dot-import of "os"` {
		t.Fatalf("expected dot-import violation, got %+v", violations[0])
	}
}

func TestCheckDir_ReadOnlyNotFlagged(t *testing.T) {
	// The violation fixture's readOnly func uses os.ReadFile; it must not appear.
	violations, err := checkDir("testdata/violation")
	if err != nil {
		t.Fatalf("checkDir: %v", err)
	}
	for _, v := range violations {
		if v.Func == "readOnly" {
			t.Errorf("read-only os.* must not be flagged, got %+v", v)
		}
	}
}
