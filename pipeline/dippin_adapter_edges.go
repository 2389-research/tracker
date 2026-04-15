// ABOUTME: Implicit edge synthesis for the dippin adapter.
// ABOUTME: Converts parallel/fan-in IR config into explicit graph edges.
package pipeline

import (
	"fmt"

	"github.com/2389-research/dippin-lang/ir"
)

// synthesizeImplicitEdges adds edges that dippin's IR stores as parallel/fan-in
// config rather than as explicit edges, but tracker's Graph.OutgoingEdges requires
// real Edge entries to traverse the graph.
func synthesizeImplicitEdges(g *Graph, workflow *ir.Workflow) {
	existingEdges := make(map[[2]string]bool)
	for _, e := range g.Edges {
		existingEdges[[2]string{e.From, e.To}] = true
	}

	fanInBySource := buildFanInSourceMap(workflow)

	for _, irNode := range workflow.Nodes {
		if irNode == nil {
			continue
		}
		switch cfg := irNode.Config.(type) {
		case ir.ParallelConfig:
			synthesizeParallelEdges(g, irNode, cfg, fanInBySource, existingEdges)
		case *ir.ParallelConfig:
			synthesizeParallelEdges(g, irNode, *cfg, fanInBySource, existingEdges)
		case ir.FanInConfig:
			synthesizeFanInEdges(g, irNode, cfg, existingEdges)
		case *ir.FanInConfig:
			synthesizeFanInEdges(g, irNode, *cfg, existingEdges)
		}
	}
}

// buildFanInSourceMap builds a lookup of source node ID -> fan-in node ID.
func buildFanInSourceMap(workflow *ir.Workflow) map[string]string {
	fanInBySource := make(map[string]string)
	for _, irNode := range workflow.Nodes {
		if irNode == nil {
			continue
		}
		indexFanInSources(fanInBySource, irNode)
	}
	return fanInBySource
}

// indexFanInSources registers fan-in sources for a node if it has a FanInConfig.
func indexFanInSources(fanInBySource map[string]string, irNode *ir.Node) {
	var sources []string
	switch cfg := irNode.Config.(type) {
	case ir.FanInConfig:
		sources = cfg.Sources
	case *ir.FanInConfig:
		sources = cfg.Sources
	}
	for _, source := range sources {
		fanInBySource[source] = irNode.ID
	}
}

// synthesizeParallelEdges adds edges from a parallel node to its branch targets and fan-in join.
func synthesizeParallelEdges(g *Graph, irNode *ir.Node, cfg ir.ParallelConfig, fanInBySource map[string]string, existingEdges map[[2]string]bool) {
	for _, target := range cfg.Targets {
		addEdgeIfNew(g, irNode.ID, target, existingEdges)
	}
	if len(cfg.Targets) > 0 {
		synthesizeJoinEdge(g, irNode, cfg.Targets[0], fanInBySource, existingEdges)
	}
}

// addEdgeIfNew adds an edge to the graph only if it hasn't been added before.
func addEdgeIfNew(g *Graph, from, to string, existingEdges map[[2]string]bool) {
	key := [2]string{from, to}
	if !existingEdges[key] {
		g.AddEdge(&Edge{From: from, To: to})
		existingEdges[key] = true
	}
}

// synthesizeJoinEdge links the parallel node to the fan-in join node derived from firstTarget.
func synthesizeJoinEdge(g *Graph, irNode *ir.Node, firstTarget string, fanInBySource map[string]string, existingEdges map[[2]string]bool) {
	joinID, ok := fanInBySource[firstTarget]
	if !ok {
		return
	}
	addEdgeIfNew(g, irNode.ID, joinID, existingEdges)
	if node, ok := g.Nodes[irNode.ID]; ok {
		node.Attrs["parallel_join"] = joinID
	}
}

// synthesizeFanInEdges adds edges from fan-in sources to the fan-in node.
func synthesizeFanInEdges(g *Graph, irNode *ir.Node, cfg ir.FanInConfig, existingEdges map[[2]string]bool) {
	for _, source := range cfg.Sources {
		key := [2]string{source, irNode.ID}
		if !existingEdges[key] {
			g.AddEdge(&Edge{From: source, To: irNode.ID})
			existingEdges[key] = true
		}
	}
}

// ensureStartExitNodes verifies that the start and exit nodes exist in the graph.
// The start/exit shapes (Mdiamond/Msquare) are always set so the validator can
// identify them. Only bare codergen (agent) nodes with no prompt are treated as
// passthrough placeholders and get the start/exit handler. Nodes with any other
// resolved handler (tool, wait.human, parallel, parallel.fan_in, conditional,
// subgraph, etc.) always keep their handler so user-defined start/exit logic
// executes correctly.
func ensureStartExitNodes(g *Graph) error {
	if _, ok := g.Nodes[g.StartNode]; !ok {
		return fmt.Errorf("start node %q not found in graph", g.StartNode)
	}
	if _, ok := g.Nodes[g.ExitNode]; !ok {
		return fmt.Errorf("exit node %q not found in graph", g.ExitNode)
	}
	startNode := g.Nodes[g.StartNode]
	startNode.Shape = "Mdiamond"
	if !nodeHasHandlerContent(startNode) {
		startNode.Handler = "start"
	}
	exitNode := g.Nodes[g.ExitNode]
	exitNode.Shape = "Msquare"
	if !nodeHasHandlerContent(exitNode) {
		exitNode.Handler = "exit"
	}
	return nil
}

// nodeHasHandlerContent reports whether a node has a real handler. Only two
// cases are treated as "bare" passthroughs suitable for start/exit handler
// assignment:
//   - codergen nodes with no prompt (a placeholder agent node)
//   - nodes with an empty/unresolved Handler (never dispatched, no content)
//
// Any other handler value (tool, wait.human, parallel, parallel.fan_in,
// conditional, subgraph, stack.manager_loop, etc.) means the node has real
// work to do and must keep its handler.
func nodeHasHandlerContent(n *Node) bool {
	if n.Handler != "" && n.Handler != "codergen" {
		return true
	}
	// Codergen (agent) nodes are passthrough placeholders unless they carry a prompt.
	return n.Attrs["prompt"] != ""
}
