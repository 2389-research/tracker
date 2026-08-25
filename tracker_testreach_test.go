// ABOUTME: Coverage-attribution fidelity tests (#532 H2/H3) — known-hollow tests
// ABOUTME: that MUST flag and known-good (real, skipped, oracle) that MUST NOT.
package tracker

import (
	"context"
	"path/filepath"
	"testing"
)

// writeReachModule builds a temp Go module from name→source and returns its dir.
func writeReachModule(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "go.mod"), "module reachfix\n\ngo 1.21\n")
	for name, src := range files {
		mustWrite(t, filepath.Join(dir, name), src)
	}
	return dir
}

func analyzeReach(t *testing.T, files map[string]string) *TestReachabilityReport {
	t.Helper()
	if testing.Short() {
		t.Skip("spawns per-test `go test`; skipped in -short")
	}
	dir := writeReachModule(t, files)
	rep, err := AnalyzeTestReachability(context.Background(), dir)
	if err != nil {
		t.Fatalf("AnalyzeTestReachability: %v", err)
	}
	if !rep.Ran {
		t.Skipf("reachability pass did not run: %s", rep.SkipReason)
	}
	return rep
}

func hasZeroCoverage(rep *TestReachabilityReport, name string) bool {
	for _, tl := range rep.ZeroCoverage {
		if tl.Name == name {
			return true
		}
	}
	return false
}

func hasReimpl(rep *TestReachabilityReport, helper string) bool {
	for _, r := range rep.Reimplemented {
		if r.Helper.Name == helper {
			return true
		}
	}
	return false
}

// KNOWN-HOLLOW: a non-skipped test that asserts on a test-local re-implementation
// of production `classify`, never touching production code. H2 must flag it as
// zero-coverage; H3 must flag the helper as a re-implementation of an uncovered
// production function.
var hollowFiles = map[string]string{
	"prod.go": `package reachfix

func classify(n int) string {
	if n > 0 {
		return "pos"
	}
	if n < 0 {
		return "neg"
	}
	return "zero"
}
`,
	"prod_test.go": `package reachfix

import "testing"

func localClassify(n int) string {
	if n > 0 {
		return "pos"
	}
	if n < 0 {
		return "neg"
	}
	return "zero"
}

func TestClassifyHollow(t *testing.T) {
	got := localClassify(5)
	if got != "pos" {
		t.Errorf("want pos, got %q", got)
	}
	if localClassify(-1) != "neg" {
		t.Errorf("neg case wrong")
	}
	if localClassify(0) != "zero" {
		t.Errorf("zero case wrong")
	}
}
`,
}

func TestReachability_FlagsHollowZeroCoverage(t *testing.T) {
	rep := analyzeReach(t, hollowFiles)
	if !hasZeroCoverage(rep, "TestClassifyHollow") {
		t.Errorf("hollow test asserting on a copy must be flagged zero-coverage; report=%+v", rep)
	}
}

func TestReachability_FlagsReimplementedLogic(t *testing.T) {
	rep := analyzeReach(t, hollowFiles)
	if !hasReimpl(rep, "localClassify") {
		t.Errorf("test-local re-implementation of uncovered production func must be flagged; reimpl=%+v", rep.Reimplemented)
	}
}

// KNOWN-GOOD: a real test that calls production code. Must NOT be zero-coverage.
var realFiles = map[string]string{
	"prod.go": `package reachfix

func Add(a, b int) int { return a + b }
func Mul(a, b int) int { return a * b }
`,
	"prod_test.go": `package reachfix

import "testing"

func TestAddReal(t *testing.T) {
	if Add(2, 3) != 5 {
		t.Fatal("add broken")
	}
	if Add(0, 0) != 0 {
		t.Fatal("zero broken")
	}
	if Mul(2, 3) != 6 {
		t.Fatal("mul broken")
	}
}
`,
}

func TestReachability_IgnoresRealTest(t *testing.T) {
	rep := analyzeReach(t, realFiles)
	if hasZeroCoverage(rep, "TestAddReal") {
		t.Errorf("a real test that calls production must not be flagged zero-coverage; report=%+v", rep)
	}
	if len(rep.Reimplemented) != 0 {
		t.Errorf("no re-implementation present; got %+v", rep.Reimplemented)
	}
}

// KNOWN-GOOD: a t.Skip test produces no coverage but must be EXCLUDED from the
// zero-coverage flag (a skip is not a hollow test).
var skippedFiles = map[string]string{
	"prod.go": `package reachfix

func feature() int { return 42 }
`,
	"prod_test.go": `package reachfix

import "testing"

func TestFeatureSkipped(t *testing.T) {
	t.Skip("not on this platform")
	got := feature()
	if got != 42 {
		t.Errorf("want 42, got %d", got)
	}
}
`,
}

func TestReachability_ExcludesSkippedTest(t *testing.T) {
	rep := analyzeReach(t, skippedFiles)
	if hasZeroCoverage(rep, "TestFeatureSkipped") {
		t.Errorf("a skipped test must not be flagged zero-coverage; report=%+v", rep)
	}
}

// KNOWN-GOOD (oracle/differential): the test re-implements a reference AND calls
// the production function, comparing the two. Because it COVERS production, H3
// must NOT flag the reference as a hollow re-implementation, and H2 must not flag
// the test as zero-coverage. This is the key false-positive discriminator.
var oracleFiles = map[string]string{
	"prod.go": `package reachfix

func classify(n int) string {
	if n > 0 {
		return "pos"
	}
	if n < 0 {
		return "neg"
	}
	return "zero"
}
`,
	"prod_test.go": `package reachfix

import "testing"

func oracleClassify(n int) string {
	if n > 0 {
		return "pos"
	}
	if n < 0 {
		return "neg"
	}
	return "zero"
}

func TestClassifyOracle(t *testing.T) {
	inputs := []int{-2, -1, 0, 1, 2}
	for _, n := range inputs {
		want := oracleClassify(n)
		got := classify(n)
		if want != got {
			t.Errorf("classify(%d) = %q, oracle = %q", n, got, want)
		}
	}
	if classify(100) != "pos" {
		t.Fatal("sanity")
	}
}
`,
}

func TestReachability_IgnoresOracleTest(t *testing.T) {
	rep := analyzeReach(t, oracleFiles)
	if hasZeroCoverage(rep, "TestClassifyOracle") {
		t.Errorf("an oracle test that calls production must not be zero-coverage; report=%+v", rep)
	}
	if hasReimpl(rep, "oracleClassify") {
		t.Errorf("an oracle reference that covers production must not be flagged reimplemented; got %+v", rep.Reimplemented)
	}
}

// KNOWN-GOOD name attribution: a test named for an exported production symbol it
// never covers is an advisory NameUnreached hit (exact-name match only).
var nameAttrFiles = map[string]string{
	"prod.go": `package reachfix

func ParseConfig(s string) int { return len(s) }
func Other() int               { return 1 }
`,
	"prod_test.go": `package reachfix

import "testing"

func fakeParse(s string) int { return len(s) }

func TestParseConfig(t *testing.T) {
	got := fakeParse("abc")
	if got != 3 {
		t.Errorf("want 3, got %d", got)
	}
	if fakeParse("") != 0 {
		t.Errorf("empty wrong")
	}
	if fakeParse("ab") != 2 {
		t.Errorf("two wrong")
	}
}
`,
}

func TestReachability_NameAttributionAdvisory(t *testing.T) {
	rep := analyzeReach(t, nameAttrFiles)
	found := false
	for _, na := range rep.NameUnreached {
		if na.Test.Name == "TestParseConfig" && na.Symbol == "ParseConfig" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected NameUnreached advisory for TestParseConfig→ParseConfig; got %+v", rep.NameUnreached)
	}
}

func TestReachability_SkipsNonGoDir(t *testing.T) {
	// Fast path: no go.mod → Ran=false, no subprocess. Runs even under -short.
	rep, err := AnalyzeTestReachability(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rep.Ran {
		t.Error("expected Ran=false for a dir with no go.mod")
	}
	if rep.SkipReason == "" {
		t.Error("expected a SkipReason when skipped")
	}
}
