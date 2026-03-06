// ABOUTME: Validates pipeline graph structure for correctness before execution.
// ABOUTME: Checks for single start/exit, no cycles, recognized shapes, and full reachability.
package pipeline

import (
	"fmt"
	"strings"
)

// ValidationError collects multiple validation failures into one error.
type ValidationError struct {
	Errors []string
}

func (e *ValidationError) Error() string {
	return strings.Join(e.Errors, "; ")
}

func (e *ValidationError) add(msg string) {
	e.Errors = append(e.Errors, msg)
}

func (e *ValidationError) hasErrors() bool {
	return len(e.Errors) > 0
}

// Validate checks a parsed Graph for structural correctness.
// Returns nil if the graph is valid, or a ValidationError listing all problems.
func Validate(g *Graph) error {
	if g == nil {
		return &ValidationError{Errors: []string{"graph is nil"}}
	}
	ve := &ValidationError{}

	if len(g.Nodes) == 0 {
		ve.add("graph has no nodes")
		return ve
	}

	validateStartExit(g, ve)
	validateShapes(g, ve)
	validateEdgeEndpoints(g, ve)
	validateExitOutgoingEdges(g, ve)
	validateReachability(g, ve)
	validateNoCycles(g, ve)

	if ve.hasErrors() {
		return ve
	}
	return nil
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
