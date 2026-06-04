// ABOUTME: jailcheck flags direct filesystem-mutation / subprocess calls in
// ABOUTME: agent/tools that bypass the ExecutionEnvironment seam guarding the writable_paths jail (#272/#275/#283).
//
// Background: agent tools must route every filesystem mutation and subprocess
// through the exec.ExecutionEnvironment interface (env.WriteFile /
// env.RemoveFile / env.ExecCommand). When a node sets writable_paths, that seam
// is the single choke point where Landlock + openat2 (writes) and the
// __jail-exec CommandWrapper (subprocesses) are wired (see
// pipeline/handlers/codergen_jail.go). A tool that calls os.WriteFile /
// os.Remove / os.MkdirAll — or exec.Command, ioutil.WriteFile, a mutating
// syscall.* — directly bypasses the jail entirely. That is exactly the bug the
// #275 audit caught in generate_code and write_enriched_sprint.
//
// This analyzer parses every non-test .go file in the target directory
// (default agent/tools) and reports any reference to a watched mutating
// function (filesystem write/delete or subprocess spawn) across the os,
// os/exec, io/ioutil, and syscall packages — resolving aliased imports and
// flagging dot-imports. The single legal exception is an env==nil fallback
// path, which can only run when no jail is active and therefore has nothing to
// bypass; such a function must carry the //jail:allow-unjailed-fallback marker.
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
	"strconv"
	"strings"
)

// mutatingFuncs maps a watched import path to the set of its functions that
// mutate the filesystem or spawn a subprocess OUTSIDE the ExecutionEnvironment
// seam. A reference to any of these from agent/tools/ bypasses the
// writable_paths jail (Landlock+openat2 for writes, CommandWrapper for
// subprocesses). Read-only entry points (os.ReadFile/Open/Stat, exec.LookPath,
// ioutil.ReadFile/ReadDir, syscall.Read/Stat) are intentionally absent: reads
// are an accepted residual risk of the jail design (it bounds writes, not
// reads), not a bypass.
var mutatingFuncs = map[string]map[string]bool{
	"os": {
		// filesystem writes / deletes / metadata
		"WriteFile": true, "Create": true, "CreateTemp": true, "OpenFile": true,
		"Mkdir": true, "MkdirAll": true, "MkdirTemp": true,
		"Remove": true, "RemoveAll": true, "Rename": true, "Truncate": true,
		"Symlink": true, "Link": true,
		"Chmod": true, "Chown": true, "Lchown": true, "Chtimes": true,
		// process spawn; and the os.Root handle whose methods escape selector
		// detection on later calls, so flag the entry points.
		"StartProcess": true, "OpenRoot": true, "NewRoot": true,
	},
	"os/exec": {
		// every *exec.Cmd starts here; Run/Start/Output are methods on the
		// returned value, so flagging the constructors covers them all.
		"Command": true, "CommandContext": true,
	},
	"io/ioutil": {
		// deprecated, but still compiles and bypasses env just like os.*.
		"WriteFile": true, "TempFile": true, "TempDir": true,
	},
	"syscall": {
		// low-level mutators that sidestep the os.* surface entirely.
		"Unlink": true, "Unlinkat": true, "Rmdir": true,
		"Rename": true, "Renameat": true,
		"Mkdir": true, "Mkdirat": true, "Link": true, "Linkat": true,
		"Symlink": true, "Symlinkat": true, "Truncate": true, "Ftruncate": true,
		"Chmod": true, "Fchmodat": true, "Chown": true, "Fchownat": true,
		"Creat": true, "Open": true, "Openat": true, "Mkfifo": true, "Mknod": true,
		"Exec": true, "ForkExec": true, "StartProcess": true,
	},
}

// allowMarker, when present in a comment inside (or on the doc comment of) the
// enclosing function, permits that function's watched mutating calls. It
// documents the one legal exception: a fallback that runs only when env == nil,
// where no jail can be active. See docs/architecture/agent-tool-jail-checklist.md.
const allowMarker = "jail:allow-unjailed-fallback"

// pkgBase returns the import path's final segment (the default package name):
// "os/exec" → "exec", "io/ioutil" → "ioutil", "os" → "os". Import paths always
// use "/" so this is OS-independent.
func pkgBase(importPath string) string {
	if i := strings.LastIndex(importPath, "/"); i >= 0 {
		return importPath[i+1:]
	}
	return importPath
}

// Violation is a single unguarded mutating reference (or a dot-import of a
// watched package, which defeats selector attribution wholesale).
type Violation struct {
	File string
	Line int
	Func string // enclosing function name, or a "<...>" pseudo-scope
	Call string // e.g. "os.WriteFile", "exec.Command", or `dot-import of "os"`
	Hint string // remediation hint; empty → the default ExecutionEnvironment hint
}

// report renders the one-line CI message for a violation.
func (v Violation) report() string {
	hint := v.Hint
	if hint == "" {
		hint = "route filesystem mutations and subprocesses through " +
			"exec.ExecutionEnvironment (env.WriteFile/RemoveFile/ExecCommand), or, " +
			"for an env==nil fallback, annotate the function with //" + allowMarker
	}
	return fmt.Sprintf("%s:%d: %s in %s — %s. "+
		"See docs/architecture/agent-tool-jail-checklist.md",
		v.File, v.Line, v.Call, v.Func, hint)
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
		fmt.Printf("jailcheck: ok — no unguarded filesystem-mutation/subprocess calls in %s\n", dir)
		return
	}

	for _, v := range violations {
		fmt.Fprintln(os.Stderr, v.report())
	}
	fmt.Fprintf(os.Stderr, "jailcheck: FAIL — %d unguarded jail-bypassing call(s) in %s\n", len(violations), dir)
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
	// Honor the documented contract explicitly rather than relying on
	// file-list order + AST-walk order happening to coincide with it.
	sort.Slice(violations, func(i, j int) bool {
		if violations[i].File != violations[j].File {
			return violations[i].File < violations[j].File
		}
		return violations[i].Line < violations[j].Line
	})
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

// checkFile reports every unguarded mutating reference in a parsed file.
func checkFile(fset *token.FileSet, f *ast.File) []Violation {
	if v, ok := dotImportViolation(fset, f); ok {
		// A dot-import of a watched package turns every WriteFile(...) into a
		// bare ident the selector matcher can't attribute — a wholesale bypass.
		// Flag the import itself and stop: per-call results would be misleading.
		return []Violation{v}
	}
	named := watchedLocalNames(f)
	if len(named) == 0 {
		return nil // no watched package imported — nothing can resolve here.
	}
	funcs := funcRanges(f)

	var violations []Violation
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := mutatingRef(n, named)
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
			Call: call,
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

// mutatingRef reports whether n is a *reference* to a watched mutating function
// and, if so, the canonical "pkg.Func" display string (e.g. "os.WriteFile",
// "exec.Command", "syscall.Unlink"). It matches the selector itself rather than
// the enclosing call, so a function-value capture (`wf := os.WriteFile;
// wf(...)`) or callback pass (`f(os.Remove)`) is flagged just like a direct
// call — a CI gate must not be bypassable by hoisting the symbol into a
// variable. named maps each local identifier to the watched import path it is
// bound to, so an aliased import (`stdos "os"` → `stdos.WriteFile`) is matched
// while a non-stdlib package happening to share a name is not.
func mutatingRef(n ast.Node, named map[string]string) (string, bool) {
	sel, ok := n.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok {
		return "", false
	}
	importPath, watched := named[pkg.Name]
	if !watched || !mutatingFuncs[importPath][sel.Sel.Name] {
		return "", false
	}
	return pkgBase(importPath) + "." + sel.Sel.Name, true
}

// watchedLocalNames maps each local identifier bound to a watched package
// (mutatingFuncs keys) to that package's import path. Handles the default name
// (`import "os"` → "os":"os") and aliases (`import stdos "os"` → "stdos":"os").
// Dot-imports are handled earlier by dotImportViolation; a blank import
// (`_ "os"`) maps "_", which can never form a `pkg.Sel` selector, so it is inert.
func watchedLocalNames(f *ast.File) map[string]string {
	named := map[string]string{}
	for _, imp := range f.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil || mutatingFuncs[path] == nil {
			continue
		}
		if imp.Name != nil {
			named[imp.Name.Name] = path
		} else {
			named[pkgBase(path)] = path
		}
	}
	return named
}

// dotImportViolation reports a dot-import of any watched package (e.g.
// `import . "os"`). Such an import makes every function a bare identifier
// (`WriteFile(...)`), which the selector-based matcher cannot attribute — a
// wholesale bypass. Flag it explicitly so CI fails fast rather than silently
// passing a file that has hidden a watched package's surface.
func dotImportViolation(fset *token.FileSet, f *ast.File) (Violation, bool) {
	for _, imp := range f.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil || mutatingFuncs[path] == nil || imp.Name == nil || imp.Name.Name != "." {
			continue
		}
		p := fset.Position(imp.Pos())
		return Violation{
			File: p.Filename,
			Line: p.Line,
			Func: "<imports>",
			Call: fmt.Sprintf("dot-import of %q", path),
			Hint: fmt.Sprintf("import %q under its normal name (no %q dot-import) so its mutating calls stay attributable to this lint", path, "."),
		}, true
	}
	return Violation{}, false
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
