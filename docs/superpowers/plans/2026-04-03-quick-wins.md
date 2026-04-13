# Quick Wins Batch Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix auto-detection divergence, clean up code, add edge adjacency indexes, and fix DIP005 cycle validation in 3 example files.

**Architecture:** Four independent fixes. All mechanical — no design decisions needed.

**Tech Stack:** Go 1.25, standard library (`slices`, `strings`).

**Spec:** `docs/superpowers/specs/2026-04-03-quick-wins-design.md`

---

### Task 1: Fix auto-detection default and add format constants (#9)

**Files:**
- Modify: `cmd/tracker/loading.go:21` (change default)
- Modify: `tracker.go:29-41` (add constants)

- [ ] **Step 1: Change CLI default from "dot" to "dip"**

In `cmd/tracker/loading.go`, line 21, change:

```go
	return "dot" // default to DOT for .dot and unknown extensions
```

To:

```go
	return "dip" // default to .dip format for unknown extensions
```

- [ ] **Step 2: Add format constants to tracker.go**

In `tracker.go`, add before the Config struct:

```go
// Pipeline format identifiers.
const (
	FormatDip = "dip" // Dippin format (current, default)
	FormatDOT = "dot" // DOT/Graphviz format (deprecated)
)
```

- [ ] **Step 3: Build and test**

Run: `go build ./... && go test ./... -short`
Expected: All pass.

- [ ] **Step 4: Commit**

```bash
git add cmd/tracker/loading.go tracker.go
git commit -m "fix(cli): unify format detection default to dip, add format constants (#9)"
```

---

### Task 2: Code cleanup (#10)

**Files:**
- Modify: `pipeline/dippin_adapter.go:104-118` (unexport NodeKindToShape)
- Modify: `pipeline/graph.go:46` (remove make)
- Modify: `pipeline/dippin_adapter_test.go:442,781,1130-1142` (replace contains helper)
- Modify: `cmd/tracker/summary.go:82-89` (replace bubble sort)

- [ ] **Step 1: Unexport NodeKindToShape**

In `pipeline/dippin_adapter.go`:

Line 116-118 — rename function:
```go
// nodeKindToShape returns the DOT shape for a given NodeKind.
// Returns ("", false) if the kind is not recognized.
func nodeKindToShape(kind ir.NodeKind) (string, bool) {
```

Line 126 — update the call site in `convertNode`:
```go
	shape, ok := nodeKindToShape(irNode.Kind)
```

Line 104 — update the comment on the map:
```go
// nodeKindToShapeMap maps IR NodeKind to DOT shape strings.
```

- [ ] **Step 2: Remove unnecessary slice allocation**

In `pipeline/graph.go`, line 46, change:
```go
		Edges: make([]*Edge, 0),
```
To just remove that line entirely (nil slice is fine — append works on nil).

- [ ] **Step 3: Replace custom contains helper with strings.Contains**

In `pipeline/dippin_adapter_test.go`:

Add `"strings"` to imports if not present.

Line 442 — replace:
```go
	if !contains(params, "env=prod") || !contains(params, "timeout=30s") {
```
With:
```go
	if !strings.Contains(params, "env=prod") || !strings.Contains(params, "timeout=30s") {
```

Line 781 — replace:
```go
			if !contains(err.Error(), tt.wantErr) {
```
With:
```go
			if !strings.Contains(err.Error(), tt.wantErr) {
```

Delete lines 1130-1142 (the `contains` and `findSubstring` functions).

- [ ] **Step 4: Replace bubble sort with slices.SortFunc**

In `cmd/tracker/summary.go`, add `"slices"` to imports. Replace lines 82-89:

```go
	for i := 0; i < len(sorted); i++ {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[j].count > sorted[i].count ||
				(sorted[j].count == sorted[i].count && sorted[j].name < sorted[i].name) {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}
```

With:

```go
	slices.SortFunc(sorted, func(a, b toolCount) int {
		if a.count != b.count {
			return b.count - a.count // descending by count
		}
		if a.name < b.name {
			return -1 // ascending by name
		}
		if a.name > b.name {
			return 1
		}
		return 0
	})
```

- [ ] **Step 5: Build and test**

Run: `go build ./... && go test ./... -short`
Expected: All pass.

- [ ] **Step 6: Commit**

```bash
git add pipeline/dippin_adapter.go pipeline/graph.go pipeline/dippin_adapter_test.go cmd/tracker/summary.go
git commit -m "chore: unexport NodeKindToShape, replace bubble sort, remove custom contains helper (#10)"
```

---

### Task 3: Edge adjacency index for O(1) lookup (#31)

**Files:**
- Modify: `pipeline/graph.go:27-49,93-120` (Graph struct, NewGraph, AddEdge, OutgoingEdges, IncomingEdges)
- Test: `pipeline/graph_test.go` (if exists) or inline verification

- [ ] **Step 1: Write failing test**

Find or create `pipeline/graph_test.go`. Add:

```go
func TestOutgoingEdgesIndexed(t *testing.T) {
	g := NewGraph("test")
	g.AddNode(&Node{ID: "a", Shape: "box", Attrs: map[string]string{}})
	g.AddNode(&Node{ID: "b", Shape: "box", Attrs: map[string]string{}})
	g.AddNode(&Node{ID: "c", Shape: "box", Attrs: map[string]string{}})
	g.AddEdge(&Edge{From: "a", To: "b"})
	g.AddEdge(&Edge{From: "a", To: "c"})
	g.AddEdge(&Edge{From: "b", To: "c"})

	out := g.OutgoingEdges("a")
	if len(out) != 2 {
		t.Errorf("OutgoingEdges(a) = %d edges, want 2", len(out))
	}
	out = g.OutgoingEdges("b")
	if len(out) != 1 {
		t.Errorf("OutgoingEdges(b) = %d edges, want 1", len(out))
	}
	out = g.OutgoingEdges("c")
	if len(out) != 0 {
		t.Errorf("OutgoingEdges(c) = %d edges, want 0", len(out))
	}

	in := g.IncomingEdges("c")
	if len(in) != 2 {
		t.Errorf("IncomingEdges(c) = %d edges, want 2", len(in))
	}
	in = g.IncomingEdges("a")
	if len(in) != 0 {
		t.Errorf("IncomingEdges(a) = %d edges, want 0", len(in))
	}
}
```

- [ ] **Step 2: Run test (should pass with current O(E) implementation)**

Run: `go test ./pipeline/ -run TestOutgoingEdgesIndexed -v`
Expected: PASS (the test validates correctness, which the current implementation already provides).

- [ ] **Step 3: Add adjacency indexes to Graph struct**

In `pipeline/graph.go`, add two fields to the Graph struct:

```go
type Graph struct {
	Name      string
	Nodes     map[string]*Node
	Edges     []*Edge
	Attrs     map[string]string
	StartNode string
	ExitNode  string
	NodeOrder []string

	// Adjacency indexes for O(1) edge lookup. Built by AddEdge.
	outgoing map[string][]*Edge
	incoming map[string][]*Edge
}
```

- [ ] **Step 4: Initialize indexes in NewGraph**

```go
func NewGraph(name string) *Graph {
	return &Graph{
		Name:     name,
		Nodes:    make(map[string]*Node),
		Attrs:    make(map[string]string),
		outgoing: make(map[string][]*Edge),
		incoming: make(map[string][]*Edge),
	}
}
```

- [ ] **Step 5: Populate indexes in AddEdge**

```go
func (g *Graph) AddEdge(e *Edge) {
	if e.Attrs == nil {
		e.Attrs = make(map[string]string)
	}
	g.Edges = append(g.Edges, e)
	if g.outgoing == nil {
		g.outgoing = make(map[string][]*Edge)
	}
	if g.incoming == nil {
		g.incoming = make(map[string][]*Edge)
	}
	g.outgoing[e.From] = append(g.outgoing[e.From], e)
	g.incoming[e.To] = append(g.incoming[e.To], e)
}
```

Note: The nil checks on `outgoing`/`incoming` handle Graph structs created without `NewGraph` (e.g., struct literals in tests).

- [ ] **Step 6: Use indexes in OutgoingEdges/IncomingEdges**

```go
func (g *Graph) OutgoingEdges(nodeID string) []*Edge {
	if g.outgoing != nil {
		return g.outgoing[nodeID]
	}
	// Fallback for graphs built without AddEdge (e.g., deserialized).
	var result []*Edge
	for _, e := range g.Edges {
		if e.From == nodeID {
			result = append(result, e)
		}
	}
	return result
}

func (g *Graph) IncomingEdges(nodeID string) []*Edge {
	if g.incoming != nil {
		return g.incoming[nodeID]
	}
	var result []*Edge
	for _, e := range g.Edges {
		if e.To == nodeID {
			result = append(result, e)
		}
	}
	return result
}
```

- [ ] **Step 7: Run tests**

Run: `go test ./... -short`
Expected: All 14 packages pass.

- [ ] **Step 8: Commit**

```bash
git add pipeline/graph.go pipeline/graph_test.go
git commit -m "perf(pipeline): add edge adjacency indexes for O(1) OutgoingEdges/IncomingEdges (#31)"
```

---

### Task 4: Fix DIP005 cycle validation in example files (#2)

**Files:**
- Modify: `examples/ask_and_execute.dip`
- Modify: `examples/megaplan_quality.dip`
- Modify: `examples/sprint_exec.dip`

- [ ] **Step 1: Find the cycle-forming edges**

Run: `dippin doctor examples/ask_and_execute.dip examples/megaplan_quality.dip examples/sprint_exec.dip 2>&1`

Identify which edges cause DIP005 errors. These are retry/rework edges that form loops but are missing `restart: true`.

- [ ] **Step 2: Add restart: true to cycle edges**

For each file, find edges that form backward loops (e.g., `ReviewAnalysis -> ImplementClaude`) and add `restart: true` to them. The pattern is: any edge that goes from a later node back to an earlier node for rework/retry purposes.

Example fix in `ask_and_execute.dip`:
```
ReviewAnalysis -> ImplementClaude  when ctx.outcome = retry  label: rework  restart: true
```

Apply the same pattern to all cycle-forming edges in all 3 files.

- [ ] **Step 3: Validate**

Run: `dippin doctor examples/ask_and_execute.dip examples/megaplan_quality.dip examples/sprint_exec.dip`
Expected: A grade for all 3 files (or at minimum, no DIP005 errors).

- [ ] **Step 4: Run full build and tests**

Run: `go build ./... && go test ./... -short`
Expected: All pass. If any embed/sync tests reference these files, they should still work.

- [ ] **Step 5: Commit**

```bash
git add examples/ask_and_execute.dip examples/megaplan_quality.dip examples/sprint_exec.dip
git commit -m "fix(examples): add restart: true to cycle edges for DIP005 compliance (#2)"
```

---

## Task Dependency Graph

```text
Task 1 (auto-detection default — independent)
Task 2 (code cleanup — independent)
Task 3 (edge adjacency index — independent)
Task 4 (DIP005 cycle fixes — independent)
```

All four tasks are fully independent.
