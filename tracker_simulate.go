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
// ctx is checked at entry so a caller that passes an already-cancelled
// context gets an immediate error instead of silent work. Full
// cancellation mid-parse would require threading ctx through
// parsePipelineSource → dippin-lang's parser, which is out of scope
// today (parses are fast and O(n) anyway). Nil is coalesced to
// context.Background().
func Simulate(ctx context.Context, source string) (*SimulateReport, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	format := detectSourceFormat(source)
	graph, err := parsePipelineSource(source, format)
	if err != nil {
		return nil, fmt.Errorf("parse pipeline: %w", err)
	}
	report, err := simulateFromGraph(ctx, graph)
	if err != nil {
		return nil, err
	}
	report.Format = format
	return report, nil
}

// SimulateGraph returns a SimulateReport for a pre-parsed graph. It is the
// graph-in variant of Simulate: callers that already parsed (e.g. the CLI's
// validate + simulate flow, or tooling that built a graph programmatically)
// avoid a second parse. Unlike Simulate, Format is left empty — there is no
// source string to inspect; the caller can set it if desired.
//
// ctx is honored at entry; nil is coalesced to context.Background(). The
// graph is consumed synchronously. For graphs built via the library's
// parsers or Graph.AddEdge, the BFS plan is O(nodes+edges); graphs that
// populate Graph.Edges directly without AddEdge leave the adjacency index
// unbuilt, and OutgoingEdges then scans all edges per lookup — callers
// that construct graphs by hand should prefer AddEdge to keep that bound.
func SimulateGraph(ctx context.Context, graph *pipeline.Graph) (*SimulateReport, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if graph == nil {
		return nil, fmt.Errorf("SimulateGraph: graph is nil")
	}
	return simulateFromGraph(ctx, graph)
}

// simulateFromGraph is the shared internal implementation used by both
// Simulate and SimulateGraph. It assumes ctx is non-nil and not cancelled.
func simulateFromGraph(_ context.Context, graph *pipeline.Graph) (*SimulateReport, error) {
	r := &SimulateReport{
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
