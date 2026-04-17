// ABOUTME: Library API for dry-running a pipeline: returns the parsed graph,
// ABOUTME: BFS execution plan, and list of unreachable nodes.
package tracker

import (
	"context"
	"fmt"
	"sort"

	"github.com/2389-research/tracker/pipeline"
)

// SimulateReport is the structured output of a dry-run over a pipeline
// source. No LLM calls, no side effects — pure graph introspection.
type SimulateReport struct {
	Format        string            `json:"format"`
	Name          string            `json:"name,omitempty"`
	StartNode     string            `json:"start_node,omitempty"`
	ExitNode      string            `json:"exit_node,omitempty"`
	GraphAttrs    map[string]string `json:"graph_attrs,omitempty"`
	Nodes         []SimNode         `json:"nodes"`
	Edges         []SimEdge         `json:"edges"`
	ExecutionPlan []PlanStep        `json:"execution_plan"`
	Unreachable   []string          `json:"unreachable,omitempty"`
}

// SimNode is a node in a SimulateReport.
type SimNode struct {
	ID      string            `json:"id"`
	Handler string            `json:"handler,omitempty"`
	Shape   string            `json:"shape,omitempty"`
	Label   string            `json:"label,omitempty"`
	Attrs   map[string]string `json:"attrs,omitempty"`
}

// SimEdge is an edge in a SimulateReport.
type SimEdge struct {
	From      string `json:"from"`
	To        string `json:"to"`
	Label     string `json:"label,omitempty"`
	Condition string `json:"condition,omitempty"`
}

// PlanStep is one step in an execution plan.
type PlanStep struct {
	Step   int       `json:"step"`
	NodeID string    `json:"node_id"`
	Edges  []SimEdge `json:"edges,omitempty"`
}

// Simulate parses source and returns a SimulateReport. Format is detected
// from content.
//
// ctx is accepted for future extensibility (e.g. cancelling a slow parse
// on very large graphs). A nil context is treated as context.Background().
func Simulate(ctx context.Context, source string) (*SimulateReport, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	_ = ctx // reserved for future use
	format := detectSourceFormat(source)
	graph, err := parsePipelineSource(source, format)
	if err != nil {
		return nil, fmt.Errorf("parse pipeline: %w", err)
	}
	r := &SimulateReport{
		Format:    format,
		Name:      graph.Name,
		StartNode: graph.StartNode,
		ExitNode:  graph.ExitNode,
	}
	r.GraphAttrs = copyStringMap(graph.Attrs)
	r.Nodes = collectSimNodes(graph)
	r.Edges = collectSimEdges(graph)
	r.ExecutionPlan, r.Unreachable = buildExecutionPlan(graph)
	return r, nil
}

func collectSimNodes(graph *pipeline.Graph) []SimNode {
	ordered := simBFSNodeOrder(graph)
	out := make([]SimNode, 0, len(ordered))
	for _, n := range ordered {
		label := n.Label
		if label == n.ID {
			label = ""
		}
		out = append(out, SimNode{
			ID:      n.ID,
			Handler: n.Handler,
			Shape:   n.Shape,
			Label:   label,
			Attrs:   copyStringMap(n.Attrs),
		})
	}
	return out
}

func collectSimEdges(graph *pipeline.Graph) []SimEdge {
	out := make([]SimEdge, 0, len(graph.Edges))
	for _, e := range graph.Edges {
		out = append(out, SimEdge{
			From:      e.From,
			To:        e.To,
			Label:     e.Label,
			Condition: e.Condition,
		})
	}
	return out
}

func buildExecutionPlan(graph *pipeline.Graph) ([]PlanStep, []string) {
	if graph.StartNode == "" {
		return []PlanStep{}, []string{}
	}
	visited := make(map[string]bool)
	queue := []string{graph.StartNode}
	plan := make([]PlanStep, 0)
	step := 0
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if visited[id] {
			continue
		}
		visited[id] = true
		if _, ok := graph.Nodes[id]; !ok {
			continue
		}
		step++
		outs := graph.OutgoingEdges(id)
		edges := make([]SimEdge, 0, len(outs))
		for _, e := range outs {
			edges = append(edges, SimEdge{From: e.From, To: e.To, Label: e.Label, Condition: e.Condition})
			if !visited[e.To] {
				queue = append(queue, e.To)
			}
		}
		plan = append(plan, PlanStep{Step: step, NodeID: id, Edges: edges})
	}
	var unreachable []string
	for id := range graph.Nodes {
		if !visited[id] {
			unreachable = append(unreachable, id)
		}
	}
	sort.Strings(unreachable)
	return plan, unreachable
}

// simBFSNodeOrder walks graph nodes in BFS order from start, appending orphans.
func simBFSNodeOrder(graph *pipeline.Graph) []*pipeline.Node {
	visited := make(map[string]bool)
	queue := []string{graph.StartNode}
	var ordered []*pipeline.Node
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if visited[id] {
			continue
		}
		visited[id] = true
		node, ok := graph.Nodes[id]
		if !ok {
			continue
		}
		ordered = append(ordered, node)
		for _, e := range graph.OutgoingEdges(id) {
			if !visited[e.To] {
				queue = append(queue, e.To)
			}
		}
	}
	var orphans []*pipeline.Node
	for _, node := range graph.Nodes {
		if !visited[node.ID] {
			orphans = append(orphans, node)
		}
	}
	sort.Slice(orphans, func(i, j int) bool {
		return orphans[i].ID < orphans[j].ID
	})
	ordered = append(ordered, orphans...)
	return ordered
}

func copyStringMap(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
