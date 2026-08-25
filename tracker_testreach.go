// ABOUTME: Coverage-attribution fidelity (#532 heuristics 2 & 3) — runs per-test
// ABOUTME: Go coverage to flag tests that reach no production code (unreached path)
// ABOUTME: and test-local functions that re-implement uncovered production logic.
package tracker

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// TestReachabilityReport is the outcome of the opt-in coverage-attribution pass.
// Like DetectTestRaces it is heavyweight (one `go test` run per test) and skips
// cleanly (Ran=false) rather than failing when it cannot run. Every finding is
// ADVISORY — reviewer-facing evidence, not a gate — because coverage cannot see
// subprocess/cgo execution and honest tests can legitimately trip an edge case.
type TestReachabilityReport struct {
	// Ran is false when the pass was skipped (no Go module, no toolchain, no
	// tests, or the test count exceeded the budget). A skip is not a failure.
	Ran        bool   `json:"ran"`
	SkipReason string `json:"skip_reason,omitempty"`
	// Tests is the per-test coverage summary (one entry per analyzed test).
	Tests []TestReachability `json:"tests,omitempty"`
	// ZeroCoverage lists non-skipped tests that executed ZERO production
	// (non-_test.go) statements — the strong, low-false-positive signal that a
	// test asserts on a re-implemented copy or a path guarded out (heuristic 2).
	ZeroCoverage []TestLocation `json:"zero_coverage,omitempty"`
	// NameUnreached lists tests whose name matches an exported production symbol
	// in the same package that the test never covers — a fuzzy, advisory-only
	// name-attribution signal.
	NameUnreached []NameAttribution `json:"name_unreached,omitempty"`
	// Reimplemented lists test-local functions that structurally duplicate a
	// production function the whole suite never covers (heuristic 3). The
	// coverage==0 corroboration excludes legitimate oracle/differential tests
	// (which DO call the production symbol). Advisory-only.
	Reimplemented []ReimplementedFinding `json:"reimplemented,omitempty"`
}

// TestReachability summarizes one test's production-coverage attribution.
type TestReachability struct {
	Test                    TestLocation `json:"test"`
	Skipped                 bool         `json:"skipped"`
	ProductionBlocksCovered int          `json:"production_blocks_covered"`
}

// NameAttribution is an advisory name-vs-coverage mismatch.
type NameAttribution struct {
	Test   TestLocation `json:"test"`
	Symbol string       `json:"symbol"`
}

// ReimplementedFinding is a test-local function that mirrors an uncovered
// production function (heuristic 3, advisory).
type ReimplementedFinding struct {
	Helper           TestLocation `json:"helper"`
	ProductionSymbol string       `json:"production_symbol"`
	ProductionFile   string       `json:"production_file"`
	ProductionLine   int          `json:"production_line"`
	Similarity       float64      `json:"similarity"`
}

// maxReachabilityTests caps how many `go test -run` invocations the pass will make.
// Beyond it the pass skips (skip-not-fail) rather than blowing a milestone's wall
// time. Generous by default; a huge suite is better served by CI coverage tooling.
const maxReachabilityTests = 200

// AnalyzeTestReachability runs per-test Go coverage under dir and reports tests
// that reach no production code (heuristic 2) plus test-local re-implementations
// of uncovered production functions (heuristic 3). It is opt-in and heavyweight,
// mirroring DetectTestRaces: Ran=false (a skip, not a failure) when there is no Go
// module, no toolchain, no tests, or the test count exceeds the budget.
func AnalyzeTestReachability(ctx context.Context, dir string) (*TestReachabilityReport, error) {
	if dir == "" {
		dir = "."
	}
	if skip := reachabilityPreflight(dir); skip != nil {
		return skip, nil
	}
	root, modPath, err := moduleRoot(dir)
	if err != nil {
		return &TestReachabilityReport{Ran: false, SkipReason: err.Error()}, nil
	}
	funcs, err := collectTestFuncs(dir)
	if err != nil {
		return nil, err
	}
	if len(funcs) == 0 {
		return &TestReachabilityReport{Ran: false, SkipReason: "no Go test functions under " + dir}, nil
	}
	if len(funcs) > maxReachabilityTests {
		return &TestReachabilityReport{Ran: false, SkipReason: "test count exceeds reachability budget"}, nil
	}
	idx := buildProductionIndex(root)
	return runReachability(ctx, root, modPath, funcs, idx), nil
}

// reachabilityPreflight returns a skip report when the environment can't run the
// pass, or nil to proceed. Mirrors DetectTestRaces' skip-not-fail discipline.
func reachabilityPreflight(dir string) *TestReachabilityReport {
	if !hasGoModule(dir) && !hasGoModuleAncestor(dir) {
		return &TestReachabilityReport{Ran: false, SkipReason: "no go.mod or go.work at or above " + dir}
	}
	if _, err := exec.LookPath("go"); err != nil {
		return &TestReachabilityReport{Ran: false, SkipReason: "go toolchain not found on PATH"}
	}
	return nil
}

// runReachability executes the per-test coverage pass and assembles the report.
func runReachability(ctx context.Context, root, modPath string, funcs []testFunc, idx *productionIndex) *TestReachabilityReport {
	rep := &TestReachabilityReport{Ran: true}
	coveredUnion := map[string]struct{}{}
	for _, f := range funcs {
		res := runOneTestCoverage(ctx, root, modPath, f, idx)
		rep.Tests = append(rep.Tests, TestReachability{
			Test: f.loc, Skipped: res.skipped, ProductionBlocksCovered: res.blocks,
		})
		if !res.skipped && res.blocks == 0 {
			rep.ZeroCoverage = append(rep.ZeroCoverage, f.loc)
		}
		if na, ok := nameAttribution(f, res.coveredNames, idx); ok {
			rep.NameUnreached = append(rep.NameUnreached, na)
		}
		for k := range res.coveredNames {
			coveredUnion[k] = struct{}{}
		}
	}
	rep.Reimplemented = findReimplemented(idx, coveredUnion)
	sortReachReport(rep)
	return rep
}

// coverageResult is one test's parsed coverage outcome.
type coverageResult struct {
	skipped      bool
	blocks       int                 // count of covered production blocks (count>0)
	coveredNames map[string]struct{} // "absFile\x00funcName" of covered production funcs
}

// runOneTestCoverage runs `go test -run '^Name$'` with a coverage profile for one
// test and parses ran/skip plus the covered production blocks.
func runOneTestCoverage(ctx context.Context, root, modPath string, f testFunc, idx *productionIndex) coverageResult {
	profile, err := os.CreateTemp("", "tracker-cover-*.out")
	if err != nil {
		return coverageResult{coveredNames: map[string]struct{}{}}
	}
	profPath := profile.Name()
	_ = profile.Close()
	defer os.Remove(profPath)

	target := packageTarget(root, f.loc.File)
	var stdout bytes.Buffer
	cmd := exec.CommandContext(ctx, "go", "test",
		"-run", "^"+f.loc.Name+"$", "-count=1",
		"-coverpkg=./...", "-coverprofile="+profPath, "-json", target)
	cmd.Dir = root
	cmd.Stdout = &stdout
	cmd.Stderr = &bytes.Buffer{}
	_ = cmd.Run() // a failing/zero-coverage test is expected; parse regardless.

	blocks, covered := parseCoverProfile(profPath, root, modPath, idx)
	return coverageResult{
		skipped:      testSkipped(stdout.Bytes(), f.loc.Name),
		blocks:       blocks,
		coveredNames: covered,
	}
}

// packageTarget returns the `go test` package argument for a test file, relative
// to the module root (e.g. "./pkg/sub" or ".").
func packageTarget(root, testFile string) string {
	rel, err := filepath.Rel(root, filepath.Dir(testFile))
	if err != nil || rel == "." || rel == "" {
		return "."
	}
	return "./" + filepath.ToSlash(rel)
}

// hasGoModuleAncestor reports whether any ancestor of dir has a go.mod/go.work.
func hasGoModuleAncestor(dir string) bool {
	_, _, err := moduleRoot(dir)
	return err == nil
}

// sortReachReport makes the report deterministic.
func sortReachReport(rep *TestReachabilityReport) {
	sort.Slice(rep.ZeroCoverage, func(i, j int) bool { return locLess(rep.ZeroCoverage[i], rep.ZeroCoverage[j]) })
	sort.Slice(rep.NameUnreached, func(i, j int) bool { return locLess(rep.NameUnreached[i].Test, rep.NameUnreached[j].Test) })
	sort.Slice(rep.Reimplemented, func(i, j int) bool { return locLess(rep.Reimplemented[i].Helper, rep.Reimplemented[j].Helper) })
}

func locLess(a, b TestLocation) bool {
	if a.File != b.File {
		return a.File < b.File
	}
	if a.Line != b.Line {
		return a.Line < b.Line
	}
	return a.Name < b.Name
}

// nameAttribution flags a test whose post-"Test" token names a production function
// in the same package that the test never covered (advisory, exact-name match only).
func nameAttribution(f testFunc, covered map[string]struct{}, idx *productionIndex) (NameAttribution, bool) {
	symbol := testNameSymbol(f.loc.Name)
	if symbol == "" {
		return NameAttribution{}, false
	}
	pkgDir := filepath.Dir(f.loc.File)
	fn, ok := idx.funcByNameInDir(pkgDir, symbol)
	if !ok {
		return NameAttribution{}, false // name doesn't match a real production symbol
	}
	if _, isCovered := covered[fn.key()]; isCovered {
		return NameAttribution{}, false
	}
	return NameAttribution{Test: f.loc, Symbol: symbol}, true
}

// testNameSymbol returns the production-symbol token a test name claims: the text
// after "Test", trimmed at the first subtest "_" separator. "TestParseConfig_Empty"
// → "ParseConfig". Returns "" for a bare "Test".
func testNameSymbol(name string) string {
	s := strings.TrimPrefix(name, "Test")
	if i := strings.IndexByte(s, '_'); i >= 0 {
		s = s[:i]
	}
	return s
}
