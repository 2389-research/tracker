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
}

// NewGraph creates an empty Graph with the given name.
func NewGraph(name string) *Graph {
	return &Graph{
		Name:  name,
		Nodes: make(map[string]*Node),
		Attrs: make(map[string]string),
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
	explicitType := n.Attrs["type"]
	if explicitType != "" {
		n.Handler = explicitType
	} else if handler, ok := ShapeToHandler(n.Shape); ok {
		n.Handler = handler
	}
	// Diamond nodes with a tool_command should use the tool handler
	// regardless of shape (the generator sometimes uses diamond shape
	// for tool verification nodes). Skip if handler was set explicitly
	// via the type attribute to respect user intent.
	if explicitType == "" && n.Handler == "conditional" && n.Attrs["tool_command"] != "" {
		n.Handler = "tool"
	}
	// Diamond nodes with a prompt (but no tool_command) should use
	// codergen (LLM evaluation) instead of the no-op conditional handler.
	// Skip if handler was set explicitly via the type attribute.
	if explicitType == "" && n.Shape == "diamond" && n.Handler == "conditional" && n.Attrs["prompt"] != "" {
		n.Handler = "codergen"
		if n.Attrs["auto_status"] == "" {
			n.Attrs["auto_status"] = "true"
		}
	}
	g.Nodes[n.ID] = n

	switch n.Shape {
	case "Mdiamond":
		g.StartNode = n.ID
	case "Msquare":
		g.ExitNode = n.ID
	}
}

// AddEdge adds a directed edge to the graph.
// No referential integrity check is performed; use Validate to enforce that endpoints exist.
func (g *Graph) AddEdge(e *Edge) {
	if e.Attrs == nil {
		e.Attrs = make(map[string]string)
	}
	g.Edges = append(g.Edges, e)
}

// OutgoingEdges returns all edges originating from the given node ID.
func (g *Graph) OutgoingEdges(nodeID string) []*Edge {
	var result []*Edge
	for _, e := range g.Edges {
		if e.From == nodeID {
			result = append(result, e)
		}
	}
	return result
}

// IncomingEdges returns all edges terminating at the given node ID.
func (g *Graph) IncomingEdges(nodeID string) []*Edge {
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
