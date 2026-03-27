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
		switch cfg := irNode.Config.(type) {
		case ir.FanInConfig:
			for _, source := range cfg.Sources {
				fanInBySource[source] = irNode.ID
			}
		case *ir.FanInConfig:
			for _, source := range cfg.Sources {
				fanInBySource[source] = irNode.ID
			}
		}
	}
	return fanInBySource
}

// synthesizeParallelEdges adds edges from a parallel node to its branch targets and fan-in join.
func synthesizeParallelEdges(g *Graph, irNode *ir.Node, cfg ir.ParallelConfig, fanInBySource map[string]string, existingEdges map[[2]string]bool) {
	for _, target := range cfg.Targets {
		key := [2]string{irNode.ID, target}
		if !existingEdges[key] {
			g.AddEdge(&Edge{From: irNode.ID, To: target})
			existingEdges[key] = true
		}
	}
	if len(cfg.Targets) > 0 {
		if joinID, ok := fanInBySource[cfg.Targets[0]]; ok {
			key := [2]string{irNode.ID, joinID}
			if !existingEdges[key] {
				g.AddEdge(&Edge{From: irNode.ID, To: joinID})
				existingEdges[key] = true
			}
			if node, ok := g.Nodes[irNode.ID]; ok {
				node.Attrs["parallel_join"] = joinID
			}
		}
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

// ensureStartExitNodes verifies that the start and exit nodes exist in the graph
// and have the correct shape/handler attributes.
func ensureStartExitNodes(g *Graph) error {
	if _, ok := g.Nodes[g.StartNode]; !ok {
		return fmt.Errorf("start node %q not found in graph", g.StartNode)
	}
	if _, ok := g.Nodes[g.ExitNode]; !ok {
		return fmt.Errorf("exit node %q not found in graph", g.ExitNode)
	}
	startNode := g.Nodes[g.StartNode]
	startNode.Shape = "Mdiamond"
	startNode.Handler = "start"
	exitNode := g.Nodes[g.ExitNode]
	exitNode.Shape = "Msquare"
	exitNode.Handler = "exit"
	return nil
}
