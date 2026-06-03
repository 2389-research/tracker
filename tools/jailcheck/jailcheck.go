// ABOUTME: jailcheck flags direct os.* filesystem mutations in agent/tools that
// ABOUTME: bypass the ExecutionEnvironment seam guarding the writable_paths jail (#272/#275/#283).
//
// Background: agent tools must route every filesystem mutation through the
// exec.ExecutionEnvironment interface (env.WriteFile / env.RemoveFile /
// env.ExecCommand). When a node sets writable_paths, that seam is the single
// choke point where Landlock + openat2 enforcement is wired (see
// pipeline/handlers/codergen_jail.go). A tool that calls os.WriteFile /
// os.Remove / os.MkdirAll directly bypasses the jail entirely — exactly the
// bug the #275 audit caught in generate_code and write_enriched_sprint.
//
// This analyzer parses every non-test .go file in the target directory
// (default agent/tools) and reports any call to a mutating os.* function.
// The single legal exception is an env==nil fallback path, which can only run
// when no jail is active and therefore has nothing to bypass; such a function
// must carry the //jail:allow-unjailed-fallback marker comment.
//
// Usage: go run ./tools/jailcheck [dir]   (exit 1 on any violation)
//
// Full rationale and the per-tool threat model:
// docs/architecture/agent-tool-jail-checklist.md
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// mutatingOSFuncs are os-package functions that mutate the filesystem. A call
// to any of these from agent/tools/ bypasses ExecutionEnvironment and so the
// writable_paths jail. Read-only os funcs (ReadFile, Open, Stat, ReadDir, ...)
// are intentionally absent: reads/exfil are an accepted residual risk of the
// jail design (the jail bounds writes, not reads), not a bypass.
var mutatingOSFuncs = map[string]bool{
	"WriteFile":  true,
	"MkdirAll":   true,
	"Mkdir":      true,
	"MkdirTemp":  true,
	"Remove":     true,
	"RemoveAll":  true,
	"Rename":     true,
	"Create":     true,
	"CreateTemp": true,
	"OpenFile":   true,
	"Truncate":   true,
	"Symlink":    true,
	"Link":       true,
	"Chmod":      true,
	"Chown":      true,
	"Lchown":     true,
	"Chtimes":    true,
}

// allowMarker, when present in a comment inside (or on the doc comment of) the
// enclosing function, permits that function's os.* mutations. It documents the
// one legal exception: a fallback that runs only when env == nil, where no jail
// can be active. See docs/architecture/agent-tool-jail-checklist.md.
const allowMarker = "jail:allow-unjailed-fallback"

// Violation is a single unguarded os.* mutation call.
type Violation struct {
	File string
	Line int
	Func string // enclosing function name, or "<file scope>"
	Call string // e.g. "os.WriteFile"
}

func main() {
	dir := "agent/tools"
	if len(os.Args) > 1 {
		dir = os.Args[1]
	}

	violations, err := checkDir(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "jailcheck: %v\n", err)
		os.Exit(2)
	}

	if len(violations) == 0 {
		fmt.Printf("jailcheck: ok — no unguarded os.* filesystem mutations in %s\n", dir)
		return
	}

	for _, v := range violations {
		fmt.Fprintf(os.Stderr,
			"%s:%d: %s called directly in %s — route filesystem mutations through "+
				"exec.ExecutionEnvironment (env.WriteFile/RemoveFile/ExecCommand), or, for an "+
				"env==nil fallback, annotate the function with //%s. "+
				"See docs/architecture/agent-tool-jail-checklist.md\n",
			v.File, v.Line, v.Call, v.Func, allowMarker)
	}
	fmt.Fprintf(os.Stderr, "jailcheck: FAIL — %d unguarded os.* filesystem mutation(s) in %s\n", len(violations), dir)
	os.Exit(1)
}

// checkDir parses every non-test .go file in dir and returns all violations,
// sorted by file then line.
func checkDir(dir string) ([]Violation, error) {
	files, err := goFiles(dir)
	if err != nil {
		return nil, err
	}

	fset := token.NewFileSet()
	var violations []Violation
	for _, path := range files {
		f, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		violations = append(violations, checkFile(fset, f)...)
	}
	return violations, nil
}

// goFiles lists non-test .go files directly under dir, sorted.
func goFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var files []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		files = append(files, filepath.Join(dir, name))
	}
	sort.Strings(files)
	return files, nil
}

// funcRange records a function's source span and name.
type funcRange struct {
	start, end token.Pos
	name       string
	allowed    bool
}

// checkFile reports every unguarded mutating os.* call in a parsed file.
func checkFile(fset *token.FileSet, f *ast.File) []Violation {
	funcs := funcRanges(f)

	var violations []Violation
	ast.Inspect(f, func(n ast.Node) bool {
		name, ok := mutatingOSCall(n)
		if !ok {
			return true
		}
		pos := n.Pos()
		fr := enclosingFunc(pos, funcs)
		if fr != nil && fr.allowed {
			return true
		}
		p := fset.Position(pos)
		violations = append(violations, Violation{
			File: p.Filename,
			Line: p.Line,
			Func: funcLabel(fr),
			Call: "os." + name,
		})
		return true
	})
	return violations
}

// funcRanges collects every top-level function/method span in the file, marking
// which carry the allow marker (in their doc comment or body).
func funcRanges(f *ast.File) []funcRange {
	var ranges []funcRange
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		ranges = append(ranges, funcRange{
			start:   fn.Pos(),
			end:     fn.End(),
			name:    fn.Name.Name,
			allowed: funcHasMarker(fn, f),
		})
	}
	return ranges
}

// mutatingOSCall reports whether n is a call to a mutating os.* function and,
// if so, the bare function name (e.g. "WriteFile").
func mutatingOSCall(n ast.Node) (string, bool) {
	call, ok := n.(*ast.CallExpr)
	if !ok {
		return "", false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok || pkg.Name != "os" {
		return "", false
	}
	if !mutatingOSFuncs[sel.Sel.Name] {
		return "", false
	}
	return sel.Sel.Name, true
}

// enclosingFunc returns the function span containing pos, or nil for file scope.
func enclosingFunc(pos token.Pos, funcs []funcRange) *funcRange {
	for i := range funcs {
		if pos >= funcs[i].start && pos < funcs[i].end {
			return &funcs[i]
		}
	}
	return nil
}

func funcLabel(fr *funcRange) string {
	if fr == nil {
		return "<file scope>"
	}
	return fr.name
}

// funcHasMarker reports whether the allow marker appears in the function's doc
// comment or anywhere inside its body.
func funcHasMarker(fn *ast.FuncDecl, f *ast.File) bool {
	if commentGroupHasMarker(fn.Doc) {
		return true
	}
	if fn.Body == nil {
		return false
	}
	for _, cg := range f.Comments {
		if cg.Pos() >= fn.Body.Lbrace && cg.End() <= fn.Body.Rbrace && commentGroupHasMarker(cg) {
			return true
		}
	}
	return false
}

func commentGroupHasMarker(cg *ast.CommentGroup) bool {
	if cg == nil {
		return false
	}
	for _, c := range cg.List {
		if strings.Contains(c.Text, allowMarker) {
			return true
		}
	}
	return false
}
