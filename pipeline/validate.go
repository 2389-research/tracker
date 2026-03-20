// ABOUTME: Validates pipeline graph structure for correctness before execution.
// ABOUTME: Checks for single start/exit, no cycles, recognized shapes, and full reachability.
package pipeline

import (
	"fmt"
	"strings"
)

// ValidationError collects multiple validation failures and warnings into one error.
type ValidationError struct {
	Errors   []string
	Warnings []string
}

func (e *ValidationError) Error() string {
	var parts []string
	if len(e.Errors) > 0 {
		parts = append(parts, "errors: "+strings.Join(e.Errors, "; "))
	}
	if len(e.Warnings) > 0 {
		parts = append(parts, "warnings: "+strings.Join(e.Warnings, "; "))
	}
	return strings.Join(parts, " | ")
}

func (e *ValidationError) add(msg string) {
	e.Errors = append(e.Errors, msg)
}

func (e *ValidationError) addWarning(msg string) {
	e.Warnings = append(e.Warnings, msg)
}

func (e *ValidationError) hasErrors() bool {
	return len(e.Errors) > 0
}

func (e *ValidationError) hasWarnings() bool {
	return len(e.Warnings) > 0
}

// Validate checks a parsed Graph for structural correctness.
// Returns nil if the graph has no errors. Warning-only results return nil so
// that callers treating non-nil as fatal do not block valid graphs.
// Use ValidateAll to retrieve both errors and warnings.
func Validate(g *Graph) error {
	ve := validateGraph(g)
	if ve != nil && ve.hasErrors() {
		return ve
	}
	return nil
}

// ValidateAll checks a parsed Graph and returns a ValidationError containing
// both errors and warnings. Returns nil only if neither exists.
func ValidateAll(g *Graph) *ValidationError {
	ve := validateGraph(g)
	if ve != nil && (ve.hasErrors() || ve.hasWarnings()) {
		return ve
	}
	return nil
}

func validateGraph(g *Graph) *ValidationError {
	if g == nil {
		return &ValidationError{Errors: []string{"graph is nil"}}
	}
	ve := &ValidationError{}

	if len(g.Nodes) == 0 {
		ve.add("graph has no nodes")
		return ve
	}

	// Structural checks covered by Dippin's validator (DIP001–DIP006).
	// Skip when the graph was produced from already-validated Dippin IR.
	if !g.DippinValidated {
		validateStartExit(g, ve)
		validateEdgeEndpoints(g, ve)
		validateExitOutgoingEdges(g, ve)
		validateReachability(g, ve)
		validateNoCycles(g, ve)
	}

	// Tracker-specific checks always run.
	validateShapes(g, ve)
	validateNoDuplicateEdges(g, ve)
	validateConditionalFailEdges(g, ve)
	validateEdgeLabelConsistency(g, ve)

	return ve
}

// validateStartExit checks for exactly one start (Mdiamond) and one exit (Msquare) node.
func validateStartExit(g *Graph, ve *ValidationError) {
	var startCount, exitCount int
	for _, n := range g.Nodes {
		switch n.Shape {
		case "Mdiamond":
			startCount++
		case "Msquare":
			exitCount++
		}
	}

	if startCount == 0 {
		ve.add("graph has no start node (shape=Mdiamond)")
	} else if startCount > 1 {
		ve.add(fmt.Sprintf("graph has %d start nodes (shape=Mdiamond), expected exactly 1", startCount))
	}

	if exitCount == 0 {
		ve.add("graph has no exit node (shape=Msquare)")
	} else if exitCount > 1 {
		ve.add(fmt.Sprintf("graph has %d exit nodes (shape=Msquare), expected exactly 1", exitCount))
	}
}

// validateEdgeEndpoints checks that every edge references declared nodes.
func validateEdgeEndpoints(g *Graph, ve *ValidationError) {
	for _, e := range g.Edges {
		if _, ok := g.Nodes[e.From]; !ok {
			ve.add(fmt.Sprintf("edge %s->%s references undeclared source node %q", e.From, e.To, e.From))
		}
		if _, ok := g.Nodes[e.To]; !ok {
			ve.add(fmt.Sprintf("edge %s->%s references undeclared target node %q", e.From, e.To, e.To))
		}
	}
}

func validateExitOutgoingEdges(g *Graph, ve *ValidationError) {
	if g.ExitNode == "" {
		return
	}
	if outgoing := g.OutgoingEdges(g.ExitNode); len(outgoing) > 0 {
		ve.add(fmt.Sprintf("exit node %q must not have outgoing edges", g.ExitNode))
	}
}

// validateShapes checks that every node has a recognized shape.
func validateShapes(g *Graph, ve *ValidationError) {
	for _, n := range g.Nodes {
		if _, ok := ShapeToHandler(n.Shape); !ok {
			ve.add(fmt.Sprintf("node %q has unrecognized shape %q", n.ID, n.Shape))
		}
	}
}

// validateReachability checks that all nodes are reachable from the start node via BFS.
func validateReachability(g *Graph, ve *ValidationError) {
	if g.StartNode == "" {
		return
	}

	visited := make(map[string]bool)
	queue := []string{g.StartNode}
	visited[g.StartNode] = true

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		for _, e := range g.OutgoingEdges(current) {
			if !visited[e.To] {
				visited[e.To] = true
				queue = append(queue, e.To)
			}
		}
	}

	for id := range g.Nodes {
		if !visited[id] {
			ve.add(fmt.Sprintf("node %q is unreachable from start node", id))
		}
	}
}

// validateNoCycles uses DFS coloring to detect unconditional cycles in the graph.
// Conditional back-edges (retry loops) are allowed because they are guarded by
// runtime conditions and bounded by max_retries.
// White = unvisited, Gray = in current path, Black = fully processed.
func validateNoCycles(g *Graph, ve *ValidationError) {
	if g.StartNode == "" {
		return
	}

	// Build a set of unconditional edges for cycle detection.
	// Conditional edges form intentional retry loops and are excluded.
	type edgeKey struct{ from, to string }
	unconditional := make(map[edgeKey]bool)
	for _, e := range g.Edges {
		if e.Condition == "" {
			unconditional[edgeKey{e.From, e.To}] = true
		}
	}

	const (
		white = 0
		gray  = 1
		black = 2
	)

	color := make(map[string]int)
	for id := range g.Nodes {
		color[id] = white
	}

	var dfs func(nodeID string) bool
	dfs = func(nodeID string) bool {
		color[nodeID] = gray
		for _, e := range g.OutgoingEdges(nodeID) {
			if !unconditional[edgeKey{e.From, e.To}] {
				continue
			}
			switch color[e.To] {
			case gray:
				return true
			case white:
				if dfs(e.To) {
					return true
				}
			}
		}
		color[nodeID] = black
		return false
	}

	if dfs(g.StartNode) {
		ve.add("graph contains a cycle")
	}
}

// validateNoDuplicateEdges checks for edges with identical From, To, and Condition.
func validateNoDuplicateEdges(g *Graph, ve *ValidationError) {
	type edgeKey struct{ from, to, condition string }
	seen := make(map[edgeKey]bool)
	for _, e := range g.Edges {
		k := edgeKey{e.From, e.To, e.Condition}
		if seen[k] {
			ve.add(fmt.Sprintf("duplicate edge %s->%s (condition=%q)", e.From, e.To, e.Condition))
		}
		seen[k] = true
	}
}

// validateConditionalFailEdges warns when a diamond (conditional) node has no
// outgoing edge with a fail-like condition.
func validateConditionalFailEdges(g *Graph, ve *ValidationError) {
	for _, n := range g.Nodes {
		if n.Shape != "diamond" {
			continue
		}
		outgoing := g.OutgoingEdges(n.ID)
		hasFail := false
		for _, e := range outgoing {
			cond := strings.ToLower(e.Condition)
			if strings.Contains(cond, "fail") || strings.Contains(cond, "!=success") {
				hasFail = true
				break
			}
		}
		if !hasFail {
			ve.addWarning(fmt.Sprintf("conditional node %q has no fail edge", n.ID))
		}
	}
}

// validateEdgeLabelConsistency warns when a conditional (diamond) node has a mix
// of labeled and unlabeled outgoing edges.
func validateEdgeLabelConsistency(g *Graph, ve *ValidationError) {
	for _, n := range g.Nodes {
		if n.Shape != "diamond" {
			continue
		}
		outgoing := g.OutgoingEdges(n.ID)
		if len(outgoing) < 2 {
			continue
		}
		labeled := 0
		for _, e := range outgoing {
			if e.Label != "" {
				labeled++
			}
		}
		if labeled > 0 && labeled < len(outgoing) {
			ve.addWarning(fmt.Sprintf("conditional node %q has inconsistent edge label usage (%d/%d labeled)", n.ID, labeled, len(outgoing)))
		}
	}
}

// AutoFix applies automatic corrections to a graph and returns descriptions
// of each fix applied. Currently fixes conditional nodes missing fail edges
// by adding a self-referencing retry edge.
func AutoFix(g *Graph) []string {
	var fixes []string
	for _, n := range g.Nodes {
		if n.Shape != "diamond" {
			continue
		}
		outgoing := g.OutgoingEdges(n.ID)
		hasFail := false
		for _, e := range outgoing {
			cond := strings.ToLower(e.Condition)
			if strings.Contains(cond, "fail") || strings.Contains(cond, "!=success") {
				hasFail = true
				break
			}
		}
		if !hasFail {
			g.AddEdge(&Edge{
				From:      n.ID,
				To:        n.ID,
				Condition: "outcome=fail",
				Label:     "retry",
			})
			fixes = append(fixes, fmt.Sprintf("added fail edge %s->%s (condition=%q, label=%q)", n.ID, n.ID, "outcome=fail", "retry"))
		}
	}
	return fixes
}
