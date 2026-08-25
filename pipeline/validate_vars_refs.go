// ABOUTME: Reference extraction for the #505 variable-availability walk — pulls every
// ABOUTME: ctx.<key> reference out of node attributes, declared reads:, and edge conditions.
package pipeline

import (
	"fmt"
	"sort"
	"strings"
)

// varRefOrigin says where a reference was found, which fixes its severity.
type varRefOrigin int

const (
	// varOriginCondition is an edge `when` predicate operand. A key that never
	// resolves silently routes the wrong way, so this is the one hard error.
	varOriginCondition varRefOrigin = iota
	// varOriginReads is a declared `reads:` entry — an explicit author contract.
	varOriginReads
	// varOriginInterp is a `${ctx.key}` placeholder in a node attribute.
	varOriginInterp
)

// varRef is a single `ctx.<key>` reference attributed to the node that owns it.
type varRef struct {
	nodeID   string
	key      string
	origin   varRefOrigin
	where    string // human-readable location, e.g. `edge "A"->"B" condition`
	explicit bool   // written with an explicit ctx./context. prefix
}

// fatalOrigin reports whether an unproducible key at this origin is an error.
func (r varRef) fatalOrigin() bool {
	switch r.origin {
	case varOriginCondition:
		return r.explicit
	case varOriginReads:
		return true
	default:
		return false
	}
}

// collectVarRefs gathers every ctx.<key> reference in the graph, in a stable
// order (node declaration order, then edges).
func collectVarRefs(g *Graph) []varRef {
	var refs []varRef
	for _, id := range varNodeIDs(g) {
		refs = append(refs, nodeVarRefs(g.Nodes[id])...)
	}
	for _, e := range g.Edges {
		refs = append(refs, edgeVarRefs(e)...)
	}
	return refs
}

// nodeVarRefs extracts a node's declared reads: plus every ${ctx.*}
// interpolation in its attribute values.
func nodeVarRefs(node *Node) []varRef {
	var refs []varRef
	for _, raw := range ParseDeclaredKeys(node.Attrs["reads"]) {
		key, explicit := normalizeVarKey(raw)
		refs = append(refs, varRef{
			nodeID: node.ID, key: key, origin: varOriginReads,
			where: "its reads: declaration", explicit: explicit,
		})
	}
	for _, attr := range sortedCopy(mapKeys(node.Attrs)) {
		for _, key := range scanCtxInterpolations(node.Attrs[attr]) {
			refs = append(refs, varRef{
				nodeID: node.ID, key: key, origin: varOriginInterp,
				where: fmt.Sprintf("its %s attribute", attr), explicit: true,
			})
		}
	}
	return refs
}

// edgeVarRefs extracts the variable operands of an edge condition, attributed
// to the edge's source node (whose routing the condition governs).
func edgeVarRefs(e *Edge) []varRef {
	if e.Condition == "" {
		return nil
	}
	where := fmt.Sprintf("the condition on edge %q -> %q", e.From, e.To)
	var refs []varRef
	for _, operand := range conditionVarOperands(e.Condition) {
		refs = append(refs, varRef{
			nodeID: e.From, key: operand.key, origin: varOriginCondition,
			where: where, explicit: operand.explicit,
		})
	}
	return refs
}

// varOperand is a condition left-hand side after prefix normalization.
type varOperand struct {
	key      string
	explicit bool
}

// conditionVarOperands returns the left-hand operand of every clause in a
// condition expression. In this dialect the LHS is the variable and the RHS is
// a literal. Parsing goes through the shared ParseCondition model so the OR/AND
// split matches the runtime evaluator exactly. Malformed expressions yield
// nothing — validateConditionSyntax already reports those.
func conditionVarOperands(condition string) []varOperand {
	cc, err := ParseCondition(condition)
	if err != nil {
		return nil
	}
	var out []varOperand
	for _, branch := range cc.Branches {
		for _, clause := range branch.Clauses {
			if operand, ok := clauseVarOperand(clause); ok {
				out = append(out, operand)
			}
		}
	}
	return out
}

// clauseVarOperand isolates the variable operand of a single clause.
func clauseVarOperand(clause string) (varOperand, bool) {
	for strings.HasPrefix(clause, "not ") {
		clause = strings.TrimSpace(strings.TrimPrefix(clause, "not "))
	}
	op, ok, err := findConditionOperator(clause)
	if err != nil || !ok {
		return varOperand{}, false
	}
	lhs := strings.TrimSpace(clause[:op.index])
	if lhs == "" || strings.HasPrefix(lhs, `"`) {
		return varOperand{}, false
	}
	key, explicit := normalizeVarKey(lhs)
	return varOperand{key: key, explicit: explicit}, true
}

// normalizeVarKey strips the user-facing ctx./context. prefix and reports
// whether the reference carried one.
func normalizeVarKey(name string) (key string, explicit bool) {
	name = strings.TrimSpace(name)
	for _, p := range []string{"ctx.", "context."} {
		if strings.HasPrefix(name, p) {
			return strings.TrimPrefix(name, p), true
		}
	}
	return name, false
}

// scanCtxInterpolations returns the keys of every ${ctx.<key>} placeholder in
// text. Mirrors expandNextVariable's single-pass scan: an unterminated "${"
// ends the scan, and a placeholder without a namespace separator is ignored.
func scanCtxInterpolations(text string) []string {
	var keys []string
	for pos := 0; pos < len(text); {
		start := strings.Index(text[pos:], "${")
		if start == -1 {
			break
		}
		start += pos
		end := strings.Index(text[start+2:], "}")
		if end == -1 {
			break
		}
		end += start + 2
		if ns, key, found := strings.Cut(text[start+2:end], "."); found && ns == "ctx" && key != "" {
			keys = append(keys, key)
		}
		pos = end + 1
	}
	return keys
}

// varNodeIDs returns node IDs in declaration order when available, otherwise
// sorted, so findings are deterministic.
func varNodeIDs(g *Graph) []string {
	ids := make([]string, 0, len(g.Nodes))
	seen := make(map[string]bool, len(g.Nodes))
	for _, id := range g.NodeOrder {
		if _, ok := g.Nodes[id]; ok && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	var rest []string
	for id := range g.Nodes {
		if !seen[id] {
			rest = append(rest, id)
		}
	}
	sort.Strings(rest)
	return append(ids, rest...)
}

func sortedCopy(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}
