// ABOUTME: Interim guard for the env registry: every os.Getenv / os.LookupEnv literal in non-test Go code
// ABOUTME: must be registered. The AST gate tool (tools/envcheck) will replace this; until then this test is the fence.
package envpolicy

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// getenvScanExclusions are names a Getenv site reads that are deliberately
// NOT part of Tracker's runtime registry. Each needs a reason.
var getenvScanExclusions = map[string]string{
	// scripts/gen/*: build-time code generators run by `make gen-*`, never by
	// a shipped binary; their output-path overrides are developer tooling.
	"GEN_MODELS_OUT":   "scripts/gen/models output override (dev tooling)",
	"GEN_ACTIVITY_OUT": "scripts/gen/activitylog output override (dev tooling)",
}

// skippedScanDirs are directory names the walk never descends into.
var skippedScanDirs = map[string]bool{
	".git": true, ".claude": true, ".scratch": true, ".worktrees": true,
	"node_modules": true, "vendor": true, "site": true, "testdata": true,
}

func TestEveryGetenvLiteralIsRegistered(t *testing.T) {
	root := repoRoot(t)
	found := map[string][]string{} // name -> sites
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && (skippedScanDirs[d.Name()] || strings.HasPrefix(d.Name(), ".")) {
				return filepath.SkipDir
			}
			scanPackageDir(t, path, root, found)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	if len(found) == 0 {
		t.Fatal("scan found no os.Getenv literals — the walker is broken, refusing to pass vacuously")
	}
	names := make([]string, 0, len(found))
	for n := range found {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if _, excluded := getenvScanExclusions[n]; excluded {
			continue
		}
		if _, ok := Lookup(n); !ok {
			t.Errorf("os.Getenv(%q) at %s is not registered in internal/envpolicy — add a table row with Purpose and Source", n, strings.Join(found[n], ", "))
		}
	}
	for n := range getenvScanExclusions {
		if _, ok := Lookup(n); ok {
			t.Errorf("%s is both excluded and registered; drop one", n)
		}
	}
}

// scanPackageDir parses the non-test Go files of one directory, resolves
// package-level string constants, and records every os.Getenv / os.LookupEnv
// call whose argument is a string literal or such a constant.
func scanPackageDir(t *testing.T, dir, root string, found map[string][]string) {
	t.Helper()
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, func(fi fs.FileInfo) bool {
		return strings.HasSuffix(fi.Name(), ".go") && !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", dir, err)
	}
	for _, pkg := range pkgs {
		consts := packageStringConsts(pkg)
		for _, file := range pkg.Files {
			ast.Inspect(file, func(n ast.Node) bool {
				name, ok := getenvArgName(n, consts)
				if ok {
					pos := fset.Position(n.Pos())
					rel, _ := filepath.Rel(root, pos.Filename)
					found[name] = append(found[name], rel+":"+strconv.Itoa(pos.Line))
				}
				return true
			})
		}
	}
}

// packageStringConsts collects `const X = "literal"` declarations.
func packageStringConsts(pkg *ast.Package) map[string]string {
	consts := map[string]string{}
	for _, file := range pkg.Files {
		for _, decl := range file.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.CONST {
				continue
			}
			collectConstSpecs(gd, consts)
		}
	}
	return consts
}

func collectConstSpecs(gd *ast.GenDecl, consts map[string]string) {
	for _, spec := range gd.Specs {
		vs, ok := spec.(*ast.ValueSpec)
		if !ok {
			continue
		}
		for i, name := range vs.Names {
			if i >= len(vs.Values) {
				continue
			}
			if lit, ok := vs.Values[i].(*ast.BasicLit); ok && lit.Kind == token.STRING {
				if s, err := strconv.Unquote(lit.Value); err == nil {
					consts[name.Name] = s
				}
			}
		}
	}
}

// getenvArgName returns the variable name read by an os.Getenv / os.LookupEnv
// call when it is statically known (literal or package const).
func getenvArgName(n ast.Node, consts map[string]string) (string, bool) {
	call, ok := n.(*ast.CallExpr)
	if !ok || len(call.Args) != 1 {
		return "", false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	if pkg, ok := sel.X.(*ast.Ident); !ok || pkg.Name != "os" || (sel.Sel.Name != "Getenv" && sel.Sel.Name != "LookupEnv") {
		return "", false
	}
	switch arg := call.Args[0].(type) {
	case *ast.BasicLit:
		s, err := strconv.Unquote(arg.Value)
		return s, err == nil && arg.Kind == token.STRING
	case *ast.Ident:
		s, ok := consts[arg.Name]
		return s, ok
	}
	return "", false
}

// repoRoot walks up from the test's working directory to the go.mod.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above test directory")
		}
		dir = parent
	}
}
