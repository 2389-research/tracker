// ABOUTME: Test-fidelity analysis — flags duplicate/near-duplicate Go test bodies
// ABOUTME: so a verify gate can't bless byte-for-byte-copied "distinct" tests (#489).
package tracker

import (
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
)

// TestFidelityReport lists groups of Go test functions that share a body — a
// fidelity red flag the structural VerifyMilestone checks are blind to (#489).
type TestFidelityReport struct {
	// DuplicateGroups are sets of ≥2 test functions with the same body. "identical"
	// = byte-for-byte equal bodies; "near-identical" = equal after normalizing
	// literal values (so tests differing only in a string/number collide).
	DuplicateGroups []DuplicateTestGroup `json:"duplicate_groups,omitempty"`
}

// DuplicateTestGroup is a set of test functions sharing a body.
type DuplicateTestGroup struct {
	Kind  string         `json:"kind"` // "identical" | "near-identical"
	Tests []TestLocation `json:"tests"`
}

// TestLocation identifies a test function.
type TestLocation struct {
	Name string `json:"name"`
	File string `json:"file"`
	Line int    `json:"line"`
}

// minTestBodyStmts is the smallest test body considered for duplication. Trivial
// stubs (a single assert, an empty body) collide legitimately and would be noise.
const minTestBodyStmts = 3

type testFunc struct {
	loc       TestLocation
	exactHash string
	normHash  string
}

// AnalyzeTestFidelity scans a directory tree for Go test files and reports test
// functions whose bodies duplicate one another. Unparseable files are skipped
// (best-effort), not fatal — the analysis is advisory. dir is walked recursively,
// skipping vendor/.git/node_modules.
func AnalyzeTestFidelity(dir string) (*TestFidelityReport, error) {
	funcs, err := collectTestFuncs(dir)
	if err != nil {
		return nil, err
	}
	return &TestFidelityReport{DuplicateGroups: groupDuplicates(funcs)}, nil
}

func collectTestFuncs(dir string) ([]testFunc, error) {
	var out []testFunc
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		return walkTestEntry(dir, path, d, err, &out)
	})
	return out, err
}

// walkTestEntry accumulates the test functions of one _test.go file, pruning
// vendored/hidden subtrees. Parse errors are skipped (best-effort).
func walkTestEntry(dir, path string, d fs.DirEntry, err error, out *[]testFunc) error {
	if err != nil {
		return err
	}
	if d.IsDir() {
		if path != dir && skipDir(d.Name()) {
			return fs.SkipDir
		}
		return nil
	}
	if !strings.HasSuffix(path, "_test.go") {
		return nil
	}
	if fns, ferr := testFuncsInFile(path); ferr == nil {
		*out = append(*out, fns...)
	}
	return nil
}

func skipDir(name string) bool {
	switch name {
	case "vendor", ".git", "node_modules", ".claude":
		return true
	default:
		return false
	}
}

func testFuncsInFile(path string) ([]testFunc, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		return nil, err
	}
	var out []testFunc
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || !isTestFunc(fn) {
			continue
		}
		exact, norm := bodyFingerprints(fset, fn)
		out = append(out, testFunc{
			loc:       TestLocation{Name: fn.Name.Name, File: path, Line: fset.Position(fn.Pos()).Line},
			exactHash: exact, normHash: norm,
		})
	}
	return out, nil
}

// isTestFunc reports whether fn is a top-level `func TestXxx(t *testing.T)` with a
// non-trivial body.
func isTestFunc(fn *ast.FuncDecl) bool {
	if fn.Recv != nil || fn.Body == nil || len(fn.Body.List) < minTestBodyStmts {
		return false
	}
	if !strings.HasPrefix(fn.Name.Name, "Test") {
		return false
	}
	return firstParamIsTestingT(fn)
}

func firstParamIsTestingT(fn *ast.FuncDecl) bool {
	if fn.Type.Params == nil || len(fn.Type.Params.List) == 0 {
		return false
	}
	star, ok := fn.Type.Params.List[0].Type.(*ast.StarExpr)
	if !ok {
		return false
	}
	sel, ok := star.X.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "testing" && sel.Sel.Name == "T"
}

// bodyFingerprints renders the exact body, then a literal-stripped body so tests
// differing only in literal values collide. Exact is rendered BEFORE the strip
// mutates the AST.
func bodyFingerprints(fset *token.FileSet, fn *ast.FuncDecl) (exact, norm string) {
	exact = renderNode(fset, fn.Body)
	stripLiterals(fn.Body)
	norm = renderNode(fset, fn.Body)
	return exact, norm
}

func renderNode(fset *token.FileSet, n ast.Node) string {
	var b strings.Builder
	_ = printer.Fprint(&b, fset, n)
	return b.String()
}

// stripLiterals replaces every basic literal's value with a placeholder so that
// bodies differing only in string/number/char literals normalize to the same
// shape (the #489 "copy the test, change the fixture string" pattern).
func stripLiterals(n ast.Node) {
	ast.Inspect(n, func(node ast.Node) bool {
		if lit, ok := node.(*ast.BasicLit); ok {
			lit.Value = "_"
		}
		return true
	})
}

func groupDuplicates(funcs []testFunc) []DuplicateTestGroup {
	var groups []DuplicateTestGroup
	exactMembers := map[string]bool{}
	for _, g := range dupsBy(funcs, func(f testFunc) string { return f.exactHash }) {
		for _, f := range g {
			exactMembers[locKey(f.loc)] = true
		}
		groups = append(groups, DuplicateTestGroup{Kind: "identical", Tests: toLocs(g)})
	}
	for _, g := range dupsBy(funcs, func(f testFunc) string { return f.normHash }) {
		if group, ok := nearIdenticalGroup(g, exactMembers); ok {
			groups = append(groups, group)
		}
	}
	return groups
}

// nearIdenticalGroup keeps the members of a same-normalized-body group that
// aren't already reported as byte-identical, and only reports them when at least
// two have *distinct* exact bodies (else they're identical, already covered).
func nearIdenticalGroup(g []testFunc, exactMembers map[string]bool) (DuplicateTestGroup, bool) {
	var fresh []testFunc
	distinctExact := map[string]bool{}
	for _, f := range g {
		if exactMembers[locKey(f.loc)] {
			continue
		}
		fresh = append(fresh, f)
		distinctExact[f.exactHash] = true
	}
	if len(fresh) < 2 || len(distinctExact) < 2 {
		return DuplicateTestGroup{}, false
	}
	return DuplicateTestGroup{Kind: "near-identical", Tests: toLocs(fresh)}, true
}

// dupsBy groups funcs by key(), returning only groups with ≥2 members, in
// first-appearance order with each group sorted by file+line (deterministic).
func dupsBy(funcs []testFunc, key func(testFunc) string) [][]testFunc {
	m := map[string][]testFunc{}
	var order []string
	for _, f := range funcs {
		k := key(f)
		if _, seen := m[k]; !seen {
			order = append(order, k)
		}
		m[k] = append(m[k], f)
	}
	var out [][]testFunc
	for _, k := range order {
		if len(m[k]) >= 2 {
			out = append(out, sortFuncs(m[k]))
		}
	}
	return out
}

func sortFuncs(fns []testFunc) []testFunc {
	sort.Slice(fns, func(i, j int) bool {
		if fns[i].loc.File != fns[j].loc.File {
			return fns[i].loc.File < fns[j].loc.File
		}
		return fns[i].loc.Line < fns[j].loc.Line
	})
	return fns
}

func toLocs(fns []testFunc) []TestLocation {
	locs := make([]TestLocation, len(fns))
	for i, f := range fns {
		locs[i] = f.loc
	}
	return locs
}

func locKey(l TestLocation) string {
	return l.File + ":" + l.Name
}
