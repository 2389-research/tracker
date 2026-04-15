// ABOUTME: Validates pipeline graph structure for correctness before execution.
// ABOUTME: Tracker-specific checks such as shapes and conditional routing always run.
// ABOUTME: Structural checks that dippin-lang already covers, including duplicate-edge checks, are skipped when DippinValidated=true.
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

// ValidateAllWithLint checks a parsed Graph for structural and semantic issues,
// including Dippin lint warnings. Returns a ValidationError with both errors and warnings.
func ValidateAllWithLint(g *Graph, registry *HandlerRegistry) *ValidationError {
	ve := validateGraph(g)
	if ve == nil {
		ve = &ValidationError{}
	}

	if registry != nil {
		applySemanticValidation(g, registry, ve)
	}

	if ve.hasErrors() || ve.hasWarnings() {
		return ve
	}
	return nil
}

// applySemanticValidation runs semantic validation and appends errors/warnings to ve.
func applySemanticValidation(g *Graph, registry *HandlerRegistry, ve *ValidationError) {
	err, lintWarnings := ValidateSemantic(g, registry)
	if err != nil {
		appendValidationErrors(ve, err)
	}
	ve.Warnings = append(ve.Warnings, lintWarnings...)
}

// appendValidationErrors merges err into ve, unwrapping *ValidationError if possible.
func appendValidationErrors(ve *ValidationError, err error) {
	if verr, ok := err.(*ValidationError); ok {
		ve.Errors = append(ve.Errors, verr.Errors...)
	} else {
		ve.Errors = append(ve.Errors, err.Error())
	}
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

	// Structural checks that overlap with dippin-lang's DIP001–DIP009.
	// For graphs produced from .dip sources, dippin-lang's validator already ran
	// these checks before conversion, so we skip them here to prevent
	// false-positive divergence between `dippin doctor` and `tracker validate`.
	// For DOT-format graphs (DippinValidated=false), we still run them because
	// no upstream validator has covered them.
	//
	// Dippin checks covered:
	//   DIP001 — start node missing
	//   DIP002 — exit node missing
	//   DIP003 — unknown node reference in edge
	//   DIP004 — unreachable node(s) from start
	//   DIP005 — unconditional cycle detected
	//   DIP006 — exit node has outgoing edges
	//   DIP009 — duplicate edge
	// Note: DIP001 and DIP002 are complete coverage for .dip workflows, which
	// come through FromDippinIR with exactly one start/exit node. The >1 start/exit
	// check in validateStartExit is primarily relevant for DOT graphs, which tracker
	// directly parses and may contain multiple structural variants.
	if !g.DippinValidated {
		validateStartExit(g, ve)
		validateEdgeEndpoints(g, ve)
		validateExitOutgoingEdges(g, ve)
		validateReachability(g, ve)
		validateNoCycles(g, ve)
		validateNoDuplicateEdges(g, ve)
	}

	// Tracker-specific checks — always run regardless of source format.
	// These cover concerns that dippin-lang does not validate:
	//   validateShapes: DOT shape → handler resolution (tracker internal concept)
	//   validateConditionalFailEdges: warns on diamond nodes missing a fail path
	//   validateEdgeLabelConsistency: warns on mixed labeled/unlabeled edges
	validateShapes(g, ve)
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

	visited := bfsVisitEdges(g)
	for id := range g.Nodes {
		if !visited[id] {
			ve.add(fmt.Sprintf("node %q is unreachable from start node", id))
		}
	}
}

// bfsVisitEdges performs BFS from g.StartNode following all outgoing edges.
// Returns the set of visited node IDs.
func bfsVisitEdges(g *Graph) map[string]bool {
	visited := map[string]bool{g.StartNode: true}
	queue := []string{g.StartNode}
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
	return visited
}

// unconditionalEdgeSet returns a set of edges that have no condition.
// These are the only edges that matter for cycle detection.
type unconditionalEdgeSet map[[2]string]bool

func buildUnconditionalEdgeSet(g *Graph) unconditionalEdgeSet {
	s := make(unconditionalEdgeSet)
	for _, e := range g.Edges {
		if e.Condition == "" {
			s[[2]string{e.From, e.To}] = true
		}
	}
	return s
}

// validateNoCycles uses DFS coloring to detect unconditional cycles in the graph.
// Conditional back-edges (retry loops) are allowed because they are guarded by
// runtime conditions and bounded by max_retries.
func validateNoCycles(g *Graph, ve *ValidationError) {
	if g.StartNode == "" {
		return
	}

	unconditional := buildUnconditionalEdgeSet(g)
	color := make(map[string]int, len(g.Nodes))

	if cyclesDFS(g.StartNode, g, unconditional, color) {
		ve.add("graph contains a cycle")
	}
}

// cyclesDFS performs DFS coloring to detect unconditional cycles.
// Returns true if a cycle is found. White=0, Gray=1, Black=2.
func cyclesDFS(nodeID string, g *Graph, unconditional unconditionalEdgeSet, color map[string]int) bool {
	const gray, black = 1, 2
	color[nodeID] = gray
	for _, e := range g.OutgoingEdges(nodeID) {
		if unconditional[[2]string{e.From, e.To}] && dfsVisitEdge(e.To, g, unconditional, color) {
			return true
		}
	}
	color[nodeID] = black
	return false
}

// dfsVisitEdge checks a single edge target for a cycle.
func dfsVisitEdge(target string, g *Graph, unconditional unconditionalEdgeSet, color map[string]int) bool {
	const gray = 1
	switch color[target] {
	case gray:
		return true
	case 0: // white
		return cyclesDFS(target, g, unconditional, color)
	}
	return false
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
		if !edgesHaveFailCondition(g.OutgoingEdges(n.ID)) {
			ve.addWarning(fmt.Sprintf("conditional node %q has no fail edge", n.ID))
		}
	}
}

// edgesHaveFailCondition returns true if any edge has a fail-like condition.
func edgesHaveFailCondition(edges []*Edge) bool {
	for _, e := range edges {
		cond := strings.ToLower(e.Condition)
		if strings.Contains(cond, "fail") || strings.Contains(cond, "!=success") {
			return true
		}
	}
	return false
}

// validateEdgeLabelConsistency warns when a conditional (diamond) node has a mix
// of labeled and unlabeled outgoing edges.
func validateEdgeLabelConsistency(g *Graph, ve *ValidationError) {
	for _, n := range g.Nodes {
		if n.Shape != "diamond" {
			continue
		}
		checkNodeEdgeLabelConsistency(g, n, ve)
	}
}

// checkNodeEdgeLabelConsistency checks a single diamond node for inconsistent edge label usage.
func checkNodeEdgeLabelConsistency(g *Graph, n *Node, ve *ValidationError) {
	outgoing := g.OutgoingEdges(n.ID)
	if len(outgoing) < 2 {
		return
	}
	labeled := countLabeledEdges(outgoing)
	if labeled > 0 && labeled < len(outgoing) {
		ve.addWarning(fmt.Sprintf("conditional node %q has inconsistent edge label usage (%d/%d labeled)", n.ID, labeled, len(outgoing)))
	}
}

// countLabeledEdges returns the number of edges with a non-empty label.
func countLabeledEdges(edges []*Edge) int {
	count := 0
	for _, e := range edges {
		if e.Label != "" {
			count++
		}
	}
	return count
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
		if !edgesHaveFailCondition(g.OutgoingEdges(n.ID)) {
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
