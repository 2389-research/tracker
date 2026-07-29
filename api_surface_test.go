// ABOUTME: Golden-snapshot guard over the exported surface of the root `tracker`
// ABOUTME: package — any add/remove/signature change must be an intentional golden update.
package tracker

import (
	"bytes"
	"flag"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// updateAPISurface regenerates testdata/api_surface.golden when set.
//
//	go test . -run APISurface -update
var updateAPISurface = flag.Bool("update", false, "update the API surface golden file")

const apiSurfaceGolden = "testdata/api_surface.golden"

// TestAPISurface pins the exported identifiers of package `tracker` to a
// committed golden file. It is the mechanical guard behind the API-stability
// policy (docs/api-stability.md): a new export, a removed export, or a changed
// signature all surface here as a readable diff, forcing a deliberate golden
// update (and a CHANGELOG note when the change is breaking).
func TestAPISurface(t *testing.T) {
	surface := enumerateAPISurface(t)
	got := strings.Join(surface, "\n") + "\n"

	if *updateAPISurface {
		if err := os.MkdirAll(filepath.Dir(apiSurfaceGolden), 0o755); err != nil {
			t.Fatalf("mkdir testdata: %v", err)
		}
		if err := os.WriteFile(apiSurfaceGolden, []byte(got), 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		t.Logf("updated %s (%d identifiers)", apiSurfaceGolden, len(surface))
		return
	}

	wantBytes, err := os.ReadFile(apiSurfaceGolden)
	if err != nil {
		t.Fatalf("read golden (%s): %v\nRun `go test . -run APISurface -update` to create it.", apiSurfaceGolden, err)
	}
	want := string(wantBytes)
	if got != want {
		t.Errorf("exported API surface of package tracker changed.\n"+
			"This is a public-API change — see docs/api-stability.md.\n"+
			"If intended, run `go test . -run APISurface -update` and note it in CHANGELOG.md.\n\n%s",
			diffLines(want, got))
	}
}

// enumerateAPISurface parses every non-test .go file of the root package and
// returns a sorted, deterministic list of its exported identifiers.
func enumerateAPISurface(t *testing.T) []string {
	t.Helper()
	fset := token.NewFileSet()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	var files []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		files = append(files, name)
	}
	sort.Strings(files)

	var lines []string
	for _, name := range files {
		f, err := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		if f.Name.Name != "tracker" {
			t.Fatalf("%s: unexpected package %q", name, f.Name.Name)
		}
		lines = append(lines, collectDecls(fset, f)...)
	}

	sort.Strings(lines)
	return dedupe(lines)
}

// collectDecls walks the top-level declarations of one file, emitting one line
// per exported identifier (functions, methods, types, fields, vars, consts).
func collectDecls(fset *token.FileSet, f *ast.File) []string {
	var out []string
	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			out = append(out, funcLines(fset, d)...)
		case *ast.GenDecl:
			out = append(out, genLines(fset, d)...)
		}
	}
	return out
}

// funcLines renders a function or method declaration.
func funcLines(fset *token.FileSet, d *ast.FuncDecl) []string {
	sig := "(" + renderFieldList(fset, d.Type.Params) + ")" + renderResults(fset, d.Type.Results)
	if d.Recv == nil {
		if !d.Name.IsExported() {
			return nil
		}
		return []string{"func " + d.Name.Name + sig}
	}
	recv := recvTypeName(d.Recv)
	// Methods on unexported types, or unexported methods, are not part of the surface.
	if !ast.IsExported(strings.TrimPrefix(recv, "*")) || !d.Name.IsExported() {
		return nil
	}
	return []string{"method (" + recv + ") " + d.Name.Name + sig}
}

// genLines renders type / var / const declarations.
func genLines(fset *token.FileSet, d *ast.GenDecl) []string {
	var out []string
	for _, spec := range d.Specs {
		switch s := spec.(type) {
		case *ast.TypeSpec:
			if !s.Name.IsExported() {
				continue
			}
			out = append(out, typeLines(fset, s)...)
		case *ast.ValueSpec:
			kw := "var"
			if d.Tok == token.CONST {
				kw = "const"
			}
			for _, name := range s.Names {
				if !name.IsExported() {
					continue
				}
				line := kw + " " + name.Name
				if s.Type != nil {
					line += " " + renderExpr(fset, s.Type)
				}
				out = append(out, line)
			}
		}
	}
	return out
}

// typeLines renders a type declaration plus its exported struct fields or
// interface methods.
func typeLines(fset *token.FileSet, s *ast.TypeSpec) []string {
	alias := ""
	if s.Assign.IsValid() {
		alias = "= "
	}
	name := s.Name.Name
	out := []string{"type " + name + " " + alias + typeKind(fset, s.Type)}
	switch t := s.Type.(type) {
	case *ast.StructType:
		for _, field := range t.Fields.List {
			ft := renderExpr(fset, field.Type)
			if len(field.Names) == 0 { // embedded
				out = append(out, "type "+name+".= embed "+ft)
				continue
			}
			for _, fn := range field.Names {
				if !fn.IsExported() {
					continue
				}
				out = append(out, "type "+name+"."+fn.Name+" field "+ft)
			}
		}
	case *ast.InterfaceType:
		for _, m := range t.Methods.List {
			if len(m.Names) == 0 { // embedded interface
				out = append(out, "type "+name+".= embed "+renderExpr(fset, m.Type))
				continue
			}
			for _, mn := range m.Names {
				if !mn.IsExported() {
					continue
				}
				if ft, ok := m.Type.(*ast.FuncType); ok {
					sig := "(" + renderFieldList(fset, ft.Params) + ")" + renderResults(fset, ft.Results)
					out = append(out, "type "+name+"."+mn.Name+" method "+sig)
				}
			}
		}
	}
	return out
}

// typeKind labels the underlying kind of a type spec for the snapshot header:
// "struct"/"interface" for composite types, otherwise the rendered underlying
// type (e.g. "string", "map[string]int").
func typeKind(fset *token.FileSet, expr ast.Expr) string {
	switch expr.(type) {
	case *ast.StructType:
		return "struct"
	case *ast.InterfaceType:
		return "interface"
	default:
		return renderExpr(fset, expr)
	}
}

func recvTypeName(recv *ast.FieldList) string {
	if recv == nil || len(recv.List) == 0 {
		return ""
	}
	switch t := recv.List[0].Type.(type) {
	case *ast.StarExpr:
		if id, ok := t.X.(*ast.Ident); ok {
			return "*" + id.Name
		}
	case *ast.Ident:
		return t.Name
	}
	return ""
}

func renderResults(fset *token.FileSet, results *ast.FieldList) string {
	if results == nil || len(results.List) == 0 {
		return ""
	}
	inner := renderFieldList(fset, results)
	if len(results.List) == 1 && len(results.List[0].Names) == 0 {
		return " " + inner
	}
	return " (" + inner + ")"
}

// renderFieldList renders params/results, dropping parameter names (only types
// are part of the signature contract).
func renderFieldList(fset *token.FileSet, fl *ast.FieldList) string {
	if fl == nil {
		return ""
	}
	var parts []string
	for _, field := range fl.List {
		typ := renderExpr(fset, field.Type)
		n := len(field.Names)
		if n == 0 {
			n = 1
		}
		for i := 0; i < n; i++ {
			parts = append(parts, typ)
		}
	}
	return strings.Join(parts, ", ")
}

// renderExpr prints an AST type expression to its canonical source form,
// collapsing internal whitespace so multi-line types render as one stable line.
func renderExpr(fset *token.FileSet, expr ast.Expr) string {
	var buf bytes.Buffer
	if err := printer.Fprint(&buf, fset, expr); err != nil {
		return "?"
	}
	return strings.Join(strings.Fields(buf.String()), " ")
}

func dedupe(in []string) []string {
	out := in[:0]
	var prev string
	for i, s := range in {
		if i == 0 || s != prev {
			out = append(out, s)
		}
		prev = s
	}
	return out
}

// diffLines produces a minimal line-oriented diff for the failure message.
func diffLines(want, got string) string {
	wl := strings.Split(strings.TrimRight(want, "\n"), "\n")
	gl := strings.Split(strings.TrimRight(got, "\n"), "\n")
	wset := map[string]bool{}
	for _, l := range wl {
		wset[l] = true
	}
	gset := map[string]bool{}
	for _, l := range gl {
		gset[l] = true
	}
	var b strings.Builder
	for _, l := range gl {
		if !wset[l] {
			b.WriteString("+ " + l + "\n")
		}
	}
	for _, l := range wl {
		if !gset[l] {
			b.WriteString("- " + l + "\n")
		}
	}
	if b.Len() == 0 {
		return "(only ordering changed)"
	}
	return b.String()
}
