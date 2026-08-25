// ABOUTME: Structural near-duplicate detection (#532 heuristic 1) — flags test
// ABOUTME: bodies the exact/literal-strip hashes miss (reordered stmts, renamed
// ABOUTME: locals, wrapped asserts) that also call the same production symbols.
package tracker

import (
	"go/ast"
	"go/scanner"
	"go/token"
	"sort"
	"strings"
)

// structuralMinStmts is the statement floor for heuristic 1. Short bodies collide
// legitimately, so this floor is higher than the exact-duplicate minTestBodyStmts.
const structuralMinStmts = 5

// structuralSimilarityThreshold is the token-Jaccard cutoff above which two test
// bodies are considered structural near-duplicates. Tuned conservatively (≥0.85)
// so reordered/renamed/wrapped copies collide while genuinely different bodies do
// not. Paired with the shared-call-target guard for near-zero false positives.
const structuralSimilarityThreshold = 0.85

// callTargetJaccardThreshold requires the two bodies' production-call sets to
// overlap heavily. This is the primary false-positive guard: two structurally
// identical tests that call *different* production functions are testing
// different things and must not be flagged.
const callTargetJaccardThreshold = 0.5

// shingleK is the token-shingle width for the Jaccard similarity metric.
const shingleK = 3

// structuralSignature computes heuristic 1's signature for a test function: the
// body statement count, the set of 3-token shingles over its canonicalized token
// stream (locals alpha-renamed, literals stripped), and the set of production
// call targets. It reads a pristine body (before literal stripping mutates it).
func structuralSignature(fset *token.FileSet, fn *ast.FuncDecl) (stmtCount int, shingles, callTargets map[string]struct{}) {
	stmtCount = len(fn.Body.List)
	locals := collectLocalNames(fn)
	tokens := canonicalTokens(fset, fn.Body, locals)
	return stmtCount, shingleSet(tokens), collectCallTargets(fn.Body, locals)
}

// collectLocalNames gathers the identifiers a function body declares (params,
// results, :=-defined names, var/const/type specs, range vars, func-lit params).
// These are alpha-renamed in the canonical token stream so "renamed locals" copies
// collide; package/selector names are deliberately left alone.
func collectLocalNames(fn *ast.FuncDecl) map[string]struct{} {
	locals := map[string]struct{}{}
	addFieldNames(fn.Type.Params, locals)
	addFieldNames(fn.Type.Results, locals)
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.AssignStmt:
			if v.Tok == token.DEFINE {
				addExprNames(v.Lhs, locals)
			}
		case *ast.RangeStmt:
			if v.Tok == token.DEFINE {
				addExprNames([]ast.Expr{v.Key, v.Value}, locals)
			}
		case *ast.DeclStmt:
			addDeclNames(v.Decl, locals)
		case *ast.FuncLit:
			addFieldNames(v.Type.Params, locals)
			addFieldNames(v.Type.Results, locals)
		}
		return true
	})
	return locals
}

func addFieldNames(fl *ast.FieldList, out map[string]struct{}) {
	if fl == nil {
		return
	}
	for _, f := range fl.List {
		for _, name := range f.Names {
			out[name.Name] = struct{}{}
		}
	}
}

func addExprNames(exprs []ast.Expr, out map[string]struct{}) {
	for _, e := range exprs {
		if id, ok := e.(*ast.Ident); ok && id.Name != "_" {
			out[id.Name] = struct{}{}
		}
	}
}

func addDeclNames(decl ast.Decl, out map[string]struct{}) {
	gd, ok := decl.(*ast.GenDecl)
	if !ok {
		return
	}
	for _, spec := range gd.Specs {
		switch s := spec.(type) {
		case *ast.ValueSpec:
			for _, name := range s.Names {
				out[name.Name] = struct{}{}
			}
		case *ast.TypeSpec:
			out[s.Name.Name] = struct{}{}
		}
	}
}

// canonicalTokens renders the body and re-scans it into a canonical token stream:
// locally-declared identifiers become positional slots (v1, v2, …) in first-use
// order, literals collapse to "L", and every other token keeps its literal text.
// This tolerates reordered statements and renamed locals without AST mutation.
func canonicalTokens(fset *token.FileSet, body *ast.BlockStmt, locals map[string]struct{}) []string {
	src := renderNode(fset, body)
	var s scanner.Scanner
	fsFile := token.NewFileSet().AddFile("", -1, len(src))
	s.Init(fsFile, []byte(src), nil, 0)
	rename := map[string]string{}
	var toks []string
	for {
		_, tok, lit := s.Scan()
		if tok == token.EOF {
			break
		}
		toks = append(toks, canonicalToken(tok, lit, locals, rename))
	}
	return toks
}

func canonicalToken(tok token.Token, lit string, locals map[string]struct{}, rename map[string]string) string {
	switch tok {
	case token.IDENT:
		if _, isLocal := locals[lit]; isLocal {
			slot, ok := rename[lit]
			if !ok {
				slot = "v" + itoa(len(rename)+1)
				rename[lit] = slot
			}
			return slot
		}
		return lit
	case token.INT, token.FLOAT, token.IMAG, token.CHAR, token.STRING:
		return "L"
	default:
		if lit != "" {
			return lit
		}
		return tok.String()
	}
}

// itoa is a tiny int→string without pulling strconv into the hot path signature.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// shingleSet returns the set of contiguous k-token shingles of the token stream.
func shingleSet(tokens []string) map[string]struct{} {
	out := map[string]struct{}{}
	if len(tokens) < shingleK {
		// Fall back to the whole stream as one shingle so tiny bodies still match
		// exactly-equal streams (they are excluded elsewhere by the stmt floor).
		out[strings.Join(tokens, " ")] = struct{}{}
		return out
	}
	for i := 0; i+shingleK <= len(tokens); i++ {
		out[strings.Join(tokens[i:i+shingleK], " ")] = struct{}{}
	}
	return out
}

// collectCallTargets returns the set of production symbols a body calls, excluding
// test-framework calls (t.*, require.*, assert.*, etc.). A bare call Foo() yields
// "Foo"; a selector call pkg.Bar() yields "Bar"; a call on a local receiver
// v.Method() also yields "Method". Locals as bare callees are dropped (they name
// closures, not production functions).
func collectCallTargets(body *ast.BlockStmt, locals map[string]struct{}) map[string]struct{} {
	out := map[string]struct{}{}
	ast.Inspect(body, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			addCallTarget(call, locals, out)
		}
		return true
	})
	return out
}

// addCallTarget records one call's production target (if any) into out.
func addCallTarget(call *ast.CallExpr, locals, out map[string]struct{}) {
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		_, isLocal := locals[fun.Name]
		if !isLocal && !isBuiltinCall(fun.Name) {
			out[fun.Name] = struct{}{}
		}
	case *ast.SelectorExpr:
		if base, ok := fun.X.(*ast.Ident); ok && isTestFrameworkBase(base.Name) {
			return
		}
		out[fun.Sel.Name] = struct{}{}
	}
}

func isBuiltinCall(name string) bool {
	switch name {
	case "len", "cap", "make", "new", "append", "copy", "delete", "panic",
		"recover", "print", "println", "close", "complex", "real", "imag", "min", "max", "clear":
		return true
	default:
		return false
	}
}

// isTestFrameworkBase reports whether a selector base names the testing harness
// rather than production code. These calls (t.Errorf, require.Equal, …) are the
// shared shape of every test and must not count as production call targets.
func isTestFrameworkBase(name string) bool {
	switch name {
	case "t", "b", "tb", "testing", "require", "assert", "assertions",
		"is", "should", "must", "mock", "g", "gomega", "Expect", "ginkgo":
		return true
	default:
		return false
	}
}

// structuralGroups clusters test functions that are structural near-duplicates
// (heuristic 1). A pair qualifies when: both bodies clear the statement floor,
// their token-Jaccard ≥ threshold, their production-call sets overlap heavily and
// share ≥1 target, and they are NOT already reported as (near-)identical by the
// exact/literal-strip hashes. Clusters are connected components of qualifying
// pairs. Advisory-only — never affects the exit code.
func structuralGroups(funcs []testFunc) []StructuralTestGroup {
	cand := structuralCandidates(funcs)
	edges, minSim, shared := structuralEdges(cand)
	return buildStructuralClusters(cand, edges, minSim, shared)
}

// structuralCandidates keeps only funcs large enough and with a production-call
// set to corroborate against, preserving first-appearance order.
func structuralCandidates(funcs []testFunc) []testFunc {
	var cand []testFunc
	for _, f := range funcs {
		if f.stmtCount >= structuralMinStmts && len(f.callTargets) > 0 && len(f.shingles) > 0 {
			cand = append(cand, f)
		}
	}
	return cand
}

// structuralEdges computes the qualifying pairwise edges over the candidate set,
// returning adjacency, the per-edge similarity, and the per-edge shared calls.
func structuralEdges(cand []testFunc) (edges map[int][]int, minSim map[[2]int]float64, shared map[[2]int][]string) {
	edges = map[int][]int{}
	minSim = map[[2]int]float64{}
	shared = map[[2]int][]string{}
	for i := 0; i < len(cand); i++ {
		for j := i + 1; j < len(cand); j++ {
			sim, common, ok := qualifyStructuralPair(cand[i], cand[j])
			if !ok {
				continue
			}
			edges[i] = append(edges[i], j)
			edges[j] = append(edges[j], i)
			minSim[[2]int{i, j}] = sim
			shared[[2]int{i, j}] = common
		}
	}
	return edges, minSim, shared
}

// qualifyStructuralPair reports whether two candidates are a structural
// near-duplicate edge: not already (near-)identical, token-Jaccard ≥ threshold,
// and heavily-overlapping production-call sets sharing ≥1 target.
func qualifyStructuralPair(a, b testFunc) (sim float64, common []string, ok bool) {
	if a.normHash == b.normHash {
		return 0, nil, false // already covered by the (near-)identical detector
	}
	sim = jaccard(a.shingles, b.shingles)
	if sim < structuralSimilarityThreshold {
		return 0, nil, false
	}
	common = sortedIntersection(a.callTargets, b.callTargets)
	if len(common) == 0 || jaccard(a.callTargets, b.callTargets) < callTargetJaccardThreshold {
		return 0, nil, false
	}
	return sim, common, true
}

// buildStructuralClusters walks connected components of the edge graph and emits
// a StructuralTestGroup per component of ≥2 members.
func buildStructuralClusters(cand []testFunc, edges map[int][]int, minSim map[[2]int]float64, shared map[[2]int][]string) []StructuralTestGroup {
	var groups []StructuralTestGroup
	seen := map[int]bool{}
	for i := range cand {
		if seen[i] || len(edges[i]) == 0 {
			continue
		}
		members := componentMembers(i, edges, seen)
		if len(members) < 2 {
			continue
		}
		groups = append(groups, clusterGroup(cand, members, edges, minSim, shared))
	}
	return groups
}

// componentMembers returns the sorted node indices reachable from start.
func componentMembers(start int, edges map[int][]int, seen map[int]bool) []int {
	var members []int
	queue := []int{start}
	seen[start] = true
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		members = append(members, n)
		for _, m := range edges[n] {
			if !seen[m] {
				seen[m] = true
				queue = append(queue, m)
			}
		}
	}
	sort.Ints(members)
	return members
}

// clusterGroup assembles the report entry for one connected component: the
// minimum edge similarity within it and the union of shared call targets.
func clusterGroup(cand []testFunc, members []int, edges map[int][]int, minSim map[[2]int]float64, shared map[[2]int][]string) StructuralTestGroup {
	sim := 1.0
	sharedUnion := map[string]struct{}{}
	for _, a := range members {
		sim = aggregateEdges(a, edges[a], minSim, shared, sim, sharedUnion)
	}
	locs := make([]TestLocation, len(members))
	for i, m := range members {
		locs[i] = cand[m].loc
	}
	return StructuralTestGroup{Similarity: sim, SharedCalls: sortedKeys(sharedUnion), Tests: locs}
}

// aggregateEdges folds one node's outgoing edges (a<b only) into the running
// minimum similarity and the shared-call union.
func aggregateEdges(a int, neighbors []int, minSim map[[2]int]float64, shared map[[2]int][]string, sim float64, sharedUnion map[string]struct{}) float64 {
	for _, b := range neighbors {
		if a >= b {
			continue
		}
		key := [2]int{a, b}
		if s := minSim[key]; s < sim {
			sim = s
		}
		for _, c := range shared[key] {
			sharedUnion[c] = struct{}{}
		}
	}
	return sim
}

// jaccard is the set-Jaccard similarity |a∩b| / |a∪b| (0 when both are empty).
func jaccard(a, b map[string]struct{}) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 0
	}
	inter := 0
	for k := range a {
		if _, ok := b[k]; ok {
			inter++
		}
	}
	union := len(a) + len(b) - inter
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
}

func sortedIntersection(a, b map[string]struct{}) []string {
	var out []string
	for k := range a {
		if _, ok := b[k]; ok {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
