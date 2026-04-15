// ABOUTME: Core data model for pipeline graphs: Graph, Node, Edge structs.
// ABOUTME: Provides shape-to-handler mapping and graph traversal helpers.
package pipeline

// shapeHandlerMap maps DOT node shapes to handler names.
var shapeHandlerMap = map[string]string{
	"Mdiamond":      "start",
	"Msquare":       "exit",
	"box":           "codergen",
	"hexagon":       "wait.human",
	"diamond":       "conditional",
	"component":     "parallel",
	"tripleoctagon": "parallel.fan_in",
	"parallelogram": "tool",
	"house":         "stack.manager_loop",
	"tab":           "subgraph",
}

// ShapeToHandler returns the handler name for a DOT node shape.
// Returns ("", false) if the shape is not recognized.
func ShapeToHandler(shape string) (string, bool) {
	h, ok := shapeHandlerMap[shape]
	return h, ok
}

// Graph represents a parsed pipeline as a directed graph.
type Graph struct {
	Name      string
	Nodes     map[string]*Node
	Edges     []*Edge
	Attrs     map[string]string
	StartNode string
	ExitNode  string

	// NodeOrder preserves the declaration order of nodes from the source file.
	// Used by the TUI to display nodes in a sensible order (declaration order)
	// rather than BFS order which puts "Done" in the middle.
	NodeOrder []string

	// DippinValidated is set to true when the graph was produced from a .dip
	// source that has already passed dippin-lang's structural validator
	// (DIP001–DIP009). Tracker's own validateGraph skips checks that overlap
	// with those diagnostics, preventing false positives and divergence between
	// `dippin doctor` and `tracker validate`.
	//
	// CONTRACT: this flag reflects the graph's state at the moment dippin
	// validation ran. If the graph is subsequently mutated (nodes or edges
	// added/removed programmatically), callers MUST clear this flag or
	// re-run dippin validation before calling Validate again — otherwise
	// tracker's structural checks will be skipped for a shape that has
	// changed since dippin last validated it.
	DippinValidated bool

	// Adjacency indexes for O(1) edge lookup. Built by AddEdge.
	outgoing map[string][]*Edge
	incoming map[string][]*Edge
}

// NewGraph creates an empty Graph with the given name.
func NewGraph(name string) *Graph {
	return &Graph{
		Name:     name,
		Nodes:    make(map[string]*Node),
		Attrs:    make(map[string]string),
		outgoing: make(map[string][]*Edge),
		incoming: make(map[string][]*Edge),
	}
}

// AddNode adds a node to the graph and resolves its handler from its shape.
// If the node has an Mdiamond shape, it is set as the start node.
// If the node has an Msquare shape, it is set as the exit node.
// Duplicate node IDs silently replace the previous node; use Validate to enforce uniqueness.
func (g *Graph) AddNode(n *Node) {
	if n.Attrs == nil {
		n.Attrs = make(map[string]string)
	}
	resolveNodeHandler(n)
	g.Nodes[n.ID] = n

	switch n.Shape {
	case "Mdiamond":
		g.StartNode = n.ID
	case "Msquare":
		g.ExitNode = n.ID
	}
}

// resolveNodeHandler assigns n.Handler based on shape, explicit type, and diamond overrides.
func resolveNodeHandler(n *Node) {
	explicitType := n.Attrs["type"]
	if explicitType != "" {
		n.Handler = explicitType
		return
	}
	if handler, ok := ShapeToHandler(n.Shape); ok {
		n.Handler = handler
	}
	applyDiamondOverrides(n)
}

// applyDiamondOverrides adjusts the handler for diamond-shaped nodes that carry
// a tool_command or prompt attribute. These are special cases where the DOT graph
// generator uses the diamond shape but intends tool or codergen semantics.
func applyDiamondOverrides(n *Node) {
	if n.Handler != "conditional" {
		return
	}
	// Diamond nodes with a tool_command should use the tool handler.
	if n.Attrs["tool_command"] != "" {
		n.Handler = "tool"
		return
	}
	// Diamond nodes with a prompt (but no tool_command) should use codergen.
	if n.Shape == "diamond" && n.Attrs["prompt"] != "" {
		n.Handler = "codergen"
		if n.Attrs["auto_status"] == "" {
			n.Attrs["auto_status"] = "true"
		}
	}
}

// AddEdge adds a directed edge to the graph.
// No referential integrity check is performed; use Validate to enforce that endpoints exist.
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

// OutgoingEdges returns all edges originating from the given node ID.
// Returns a copy to prevent callers from mutating internal state.
func (g *Graph) OutgoingEdges(nodeID string) []*Edge {
	if g.outgoing != nil {
		src := g.outgoing[nodeID]
		if len(src) == 0 {
			return nil
		}
		out := make([]*Edge, len(src))
		copy(out, src)
		return out
	}
	var result []*Edge
	for _, e := range g.Edges {
		if e.From == nodeID {
			result = append(result, e)
		}
	}
	return result
}

// IncomingEdges returns all edges terminating at the given node ID.
// Returns a copy to prevent callers from mutating internal state.
func (g *Graph) IncomingEdges(nodeID string) []*Edge {
	if g.incoming != nil {
		src := g.incoming[nodeID]
		if len(src) == 0 {
			return nil
		}
		out := make([]*Edge, len(src))
		copy(out, src)
		return out
	}
	var result []*Edge
	for _, e := range g.Edges {
		if e.To == nodeID {
			result = append(result, e)
		}
	}
	return result
}

// Node represents a single step in the pipeline.
type Node struct {
	ID      string
	Shape   string
	Label   string
	Attrs   map[string]string
	Handler string
}

// Edge represents a directed connection between two nodes.
type Edge struct {
	From      string
	To        string
	Label     string
	Condition string
	Attrs     map[string]string
}
