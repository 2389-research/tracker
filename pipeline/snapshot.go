// ABOUTME: Builds the RunSnapshot emitted on EventPipelineStarted.
// ABOUTME: Kept out of engine.go so that hot file stays under its size ceiling.
package pipeline

import "sort"

// buildRunSnapshot assembles the RunSnapshot emitted on EventPipelineStarted:
// the top-level node inventory (sorted by ID for a stable order) plus any
// resume state from the checkpoint. Lets a subscriber joining at run start seed
// its progress model without separate access to the graph or checkpoint.
func (e *Engine) buildRunSnapshot(s *runState) *RunSnapshot {
	nodes := make([]SnapshotNode, 0, len(e.graph.Nodes))
	for id, n := range e.graph.Nodes {
		nodes = append(nodes, SnapshotNode{ID: id, Label: n.Label, Handler: n.Handler})
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].ID < nodes[j].ID })

	snap := &RunSnapshot{
		Nodes:     nodes,
		StartNode: e.graph.StartNode,
		ExitNode:  e.graph.ExitNode,
	}
	if s.cp != nil {
		snap.CurrentNode = s.cp.CurrentNode
		snap.CompletedNodes = append([]string(nil), s.cp.CompletedNodes...)
	}
	return snap
}
