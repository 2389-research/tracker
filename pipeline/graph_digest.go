// ABOUTME: GraphDigest — a content hash of a loaded workflow (and its subgraphs),
// ABOUTME: so a paused run can refuse to resume against a workflow that changed.
package pipeline

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
)

// digestGraph is the part of a Graph that decides what a run does. Maps
// marshal with sorted keys and edges keep their order (it is their priority),
// so the encoding is deterministic.
type digestGraph struct {
	Name       string            `json:"name"`
	Attrs      map[string]string `json:"attrs"`
	Nodes      map[string]*Node  `json:"nodes"`
	Edges      []*Edge           `json:"edges"`
	StartNode  string            `json:"start"`
	ExitNode   string            `json:"exit"`
	ElseTarget string            `json:"else,omitempty"`
	Inputs     []InputSpec       `json:"inputs,omitempty"`
}

// GraphDigest returns a SHA-256 hex digest of g and its subgraphs as loaded:
// node attributes (prompt_file / command_file sidecars are resolved into them
// at load time, so a sidecar edit changes the digest), edges, graph attributes
// (params included) and every subgraph. Comment and formatting edits to the
// source that do not change the loaded graph do not change it.
func GraphDigest(g *Graph, subgraphs map[string]*Graph) string {
	all := map[string]digestGraph{"": digestOf(g)}
	for name, sg := range subgraphs {
		all["sub:"+name] = digestOf(sg)
	}
	data, _ := json.Marshal(all) // strings, maps and slices always marshal
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func digestOf(g *Graph) digestGraph {
	if g == nil {
		return digestGraph{}
	}
	attrs := make(map[string]string, len(g.Attrs))
	for k, v := range g.Attrs {
		if k != "workflow_dir" { // where the workflow sits, spelled per process (symlinks, cwd), not what it does
			attrs[k] = v
		}
	}
	return digestGraph{
		Name: g.Name, Attrs: attrs, Nodes: g.Nodes, Edges: g.Edges,
		StartNode: g.StartNode, ExitNode: g.ExitNode, ElseTarget: g.ElseTarget, Inputs: g.Inputs,
	}
}

// SecureInvocationPath returns <secureBase>/<runID>/invocation.json, beside the
// run's secure checkpoint: how a paused run was started, so `tracker answer`
// can rebuild it.
func SecureInvocationPath(runID string) (string, error) {
	if err := validateRunID(runID); err != nil {
		return "", fmt.Errorf("secure invocation path: %w", err)
	}
	base, err := secureActivityLogBase()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, runID, "invocation.json"), nil
}
