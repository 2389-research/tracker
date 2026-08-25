// ABOUTME: Coverage-profile parsing, module/production-func indexing, and the
// ABOUTME: heuristic-3 test↔production re-implementation match for #532.
package tracker

import (
	"bufio"
	"bytes"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// reimplMinShingles is the size floor for a heuristic-3 match. A test-local helper
// must have at least this many canonical shingles before it can be reported as a
// re-implementation, so a tiny mirrored fixture/const doesn't false-fire.
const reimplMinShingles = 8

// funcRange is one function declaration's location, line span, and canonical
// structural shingles.
type funcRange struct {
	name       string
	file       string // absolute path
	line       int
	start, end int
	shingles   map[string]struct{}
}

func (f funcRange) key() string { return f.file + "\x00" + f.name }

// productionIndex holds the parsed function inventory of a module: production
// functions (for coverage attribution and heuristic 3) and test-local helper
// functions (heuristic 3 candidates).
type productionIndex struct {
	byFile  map[string][]funcRange // absFile → production funcs (coverage mapping)
	helpers []funcRange            // non-Test funcs declared in _test.go files
}

// funcByNameInDir returns the production function of the given name declared in
// pkgDir, if any.
func (idx *productionIndex) funcByNameInDir(pkgDir, name string) (funcRange, bool) {
	for file, fns := range idx.byFile {
		if filepath.Dir(file) != pkgDir {
			continue
		}
		for _, fn := range fns {
			if fn.name == name {
				return fn, true
			}
		}
	}
	return funcRange{}, false
}

// moduleRoot walks up from dir to the nearest go.mod and returns the directory,
// the declared module path, and any error. go.work-only roots are not supported
// for coverage path mapping (no single module path) and return an error → skip.
func moduleRoot(dir string) (root, modPath string, err error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", "", err
	}
	for {
		gomod := filepath.Join(abs, "go.mod")
		if data, rerr := os.ReadFile(gomod); rerr == nil {
			return abs, modulePathFromGoMod(data), nil
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			return "", "", errNoModuleRoot
		}
		abs = parent
	}
}

var errNoModuleRoot = fsErr("no go.mod found at or above the analyzed directory")

type fsErr string

func (e fsErr) Error() string { return string(e) }

func modulePathFromGoMod(data []byte) string {
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if rest, ok := strings.CutPrefix(line, "module "); ok {
			return strings.TrimSpace(rest)
		}
	}
	return ""
}

// buildProductionIndex parses every .go file under root, recording production
// functions (by file, for coverage mapping) and test-local helper functions
// (non-Test funcs in _test.go files, for heuristic 3). Parse errors are skipped.
func buildProductionIndex(root string) *productionIndex {
	idx := &productionIndex{byFile: map[string][]funcRange{}}
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		return walkGoEntry(idx, root, path, d, err)
	})
	return idx
}

// walkGoEntry indexes one filesystem entry, pruning vendored/hidden subtrees.
func walkGoEntry(idx *productionIndex, root, path string, d fs.DirEntry, err error) error {
	if err != nil {
		return err
	}
	if d.IsDir() {
		if path != root && skipDir(d.Name()) {
			return fs.SkipDir
		}
		return nil
	}
	if strings.HasSuffix(path, ".go") {
		indexGoFile(idx, path)
	}
	return nil
}

// indexGoFile adds one file's function declarations to the index.
func indexGoFile(idx *productionIndex, path string) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		return
	}
	isTest := strings.HasSuffix(path, "_test.go")
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil || fn.Recv != nil {
			continue
		}
		indexFuncDecl(idx, fset, path, fn, isTest)
	}
}

// indexFuncDecl records one function declaration into the index, as a production
// function or (for helper funcs in _test.go files) a heuristic-3 candidate.
func indexFuncDecl(idx *productionIndex, fset *token.FileSet, path string, fn *ast.FuncDecl, isTest bool) {
	if isTest && !isHelperFunc(fn.Name.Name) {
		return
	}
	fr := funcRange{
		name:     fn.Name.Name,
		file:     path,
		line:     fset.Position(fn.Pos()).Line,
		start:    fset.Position(fn.Body.Lbrace).Line,
		end:      fset.Position(fn.Body.Rbrace).Line,
		shingles: canonicalShingles(fset, fn),
	}
	if isTest {
		idx.helpers = append(idx.helpers, fr)
		return
	}
	idx.byFile[path] = append(idx.byFile[path], fr)
}

// isHelperFunc reports whether a _test.go function is a plain helper (not a Test,
// Benchmark, Fuzz, or Example entry point) — the heuristic-3 candidate shape.
func isHelperFunc(name string) bool {
	for _, prefix := range []string{"Test", "Benchmark", "Fuzz", "Example"} {
		if strings.HasPrefix(name, prefix) {
			return false
		}
	}
	return true
}

// canonicalShingles computes the heuristic-1 canonical shingle set for any
// function body (reused for cross-boundary heuristic-3 matching).
func canonicalShingles(fset *token.FileSet, fn *ast.FuncDecl) map[string]struct{} {
	if fn.Body == nil {
		return map[string]struct{}{}
	}
	locals := collectLocalNames(fn)
	return shingleSet(canonicalTokens(fset, fn.Body, locals))
}

// parseCoverProfile reads a Go cover profile and returns the number of covered
// production blocks (count>0) and the set of covered production function keys.
func parseCoverProfile(profPath, root, modPath string, idx *productionIndex) (blocks int, covered map[string]struct{}) {
	covered = map[string]struct{}{}
	data, err := os.ReadFile(profPath)
	if err != nil {
		return 0, covered
	}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "mode:") {
			continue
		}
		profPathField, startLine, count, ok := parseCoverLine(line)
		if !ok || count == 0 {
			continue
		}
		blocks++
		markCoveredFunc(profPathField, startLine, root, modPath, idx, covered)
	}
	return blocks, covered
}

// parseCoverLine parses "path/file.go:sl.sc,el.ec numStmt count" into the path,
// the start line, and the execution count.
func parseCoverLine(line string) (path string, startLine, count int, ok bool) {
	fields := strings.Fields(line)
	if len(fields) != 3 {
		return "", 0, 0, false
	}
	count, err := strconv.Atoi(fields[2])
	if err != nil {
		return "", 0, 0, false
	}
	colon := strings.LastIndex(fields[0], ":")
	if colon < 0 {
		return "", 0, 0, false
	}
	path = fields[0][:colon]
	span := fields[0][colon+1:] // "sl.sc,el.ec"
	dot := strings.IndexByte(span, '.')
	if dot < 0 {
		return "", 0, 0, false
	}
	startLine, err = strconv.Atoi(span[:dot])
	if err != nil {
		return "", 0, 0, false
	}
	return path, startLine, count, true
}

// markCoveredFunc maps a covered profile block to the production function that
// contains it and records that function as covered.
func markCoveredFunc(profPath string, startLine int, root, modPath string, idx *productionIndex, covered map[string]struct{}) {
	rel := profPath
	if modPath != "" {
		rel = strings.TrimPrefix(profPath, modPath+"/")
	}
	abs := filepath.Join(root, filepath.FromSlash(rel))
	for _, fn := range idx.byFile[abs] {
		if startLine >= fn.start && startLine <= fn.end {
			covered[fn.key()] = struct{}{}
			return
		}
	}
}

// testSkipped reports whether the `go test -json` stream marks the named test as
// skipped (t.Skip). A skipped test produces no coverage and must be excluded from
// the zero-coverage flag.
func testSkipped(jsonOut []byte, name string) bool {
	sc := bufio.NewScanner(bytes.NewReader(jsonOut))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		var ev struct {
			Action string `json:"Action"`
			Test   string `json:"Test"`
		}
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			continue
		}
		if ev.Test == name && ev.Action == "skip" {
			return true
		}
	}
	return false
}

// findReimplemented flags test-local helper functions that structurally duplicate
// a production function the whole suite never covers (heuristic 3, advisory). The
// coverage==0 corroboration excludes legitimate oracle tests, which DO call (and
// thus cover) the production function they compare against.
func findReimplemented(idx *productionIndex, coveredUnion map[string]struct{}) []ReimplementedFinding {
	var out []ReimplementedFinding
	for _, helper := range idx.helpers {
		if len(helper.shingles) < reimplMinShingles {
			continue
		}
		if best, sim, ok := bestUncoveredMatch(helper, idx, coveredUnion); ok {
			out = append(out, ReimplementedFinding{
				Helper:           TestLocation{Name: helper.name, File: helper.file, Line: helper.line},
				ProductionSymbol: best.name,
				ProductionFile:   best.file,
				ProductionLine:   best.line,
				Similarity:       sim,
			})
		}
	}
	return out
}

// bestUncoveredMatch returns the production function in the helper's package that
// is the closest structural match above threshold AND is uncovered by the suite.
func bestUncoveredMatch(helper funcRange, idx *productionIndex, coveredUnion map[string]struct{}) (funcRange, float64, bool) {
	pkgDir := filepath.Dir(helper.file)
	var best funcRange
	bestSim := structuralSimilarityThreshold
	found := false
	for file, fns := range idx.byFile {
		if filepath.Dir(file) != pkgDir {
			continue
		}
		if p, sim, ok := closestUncovered(helper, fns, coveredUnion, bestSim); ok {
			best, bestSim, found = p, sim, true
		}
	}
	return best, bestSim, found
}

// closestUncovered returns the uncovered production func in fns most similar to
// helper, if any clears minSim. Covered funcs are skipped (oracle-safe).
func closestUncovered(helper funcRange, fns []funcRange, coveredUnion map[string]struct{}, minSim float64) (funcRange, float64, bool) {
	var best funcRange
	found := false
	for _, p := range fns {
		if _, isCovered := coveredUnion[p.key()]; isCovered {
			continue
		}
		if sim := jaccard(helper.shingles, p.shingles); sim >= minSim {
			best, minSim, found = p, sim, true
		}
	}
	return best, minSim, found
}
