// ABOUTME: Submit-time variable-availability validation (#505) — a graph walk that
// ABOUTME: flags ctx.<key> references no node in the pipeline can ever produce.
package pipeline

import (
	"fmt"
	"sort"
	"strings"
)

// ValidateVariableAvailability walks a graph and reports every `ctx.<key>`
// reference — edge `when` predicates, `${ctx.*}` interpolations in node
// attributes, and declared `reads:` entries — that no node can produce.
//
// # Reachability rule
//
// Plain directed-graph reachability (transitive closure over edges), NOT
// topological order. A producer P "can reach" a consumer C when some path
// P -> ... -> C exists in the edge set, and every node is treated as reaching
// itself. Cycles are therefore first-class: a fix-loop or a `manager_loop`
// that writes a key on one iteration legitimately makes it available to the
// nodes it loops back to. A topological/strictly-upstream rule would flag
// every loop in examples/build_product.dip.
//
// # Fail-open default
//
// Rejecting a valid pipeline is far worse than missing an invalid one, so only
// the unambiguous case is an error: the key has NO producer anywhere in the
// graph AND the reference is one the author clearly meant as a context
// variable (an explicitly `ctx.`/`context.`-prefixed condition operand, or a
// declared `reads:` entry). Everything softer is a warning:
//
//   - a producer exists but cannot reach the reference — engine restarts,
//     `restart_target`, checkpoint resume and `suggested_next_nodes` all move
//     control along paths the static edge set does not express;
//   - a bare (unprefixed) condition operand — it may be a literal, not a key;
//   - a `${ctx.x}` interpolation — it expands to the empty string at runtime
//     rather than mis-routing, and it is where external values injected via
//     the library's `Config.Context` realistically show up;
//   - a `node.<id>.<key>` reference whose `<id>` is not a node in this graph.
//
// Anything the walk cannot reason about at all is skipped outright: the
// engine-provided key set, every dynamic namespace (see ambientVarPrefixes),
// and any graph containing an opaque producer (`subgraph` / `stack.manager_loop`
// nodes write keys this graph cannot see) that reaches the reference.
//
// # Known limitation
//
// A graph can also receive keys from *outside* itself: the library's
// `Config.Context` seeds arbitrary keys, and a subgraph child runs with a
// snapshot of its parent's context. Neither is visible when validating that
// graph on its own, so a reference to such a key is reported (a warning when
// interpolated, an error when it is an explicit condition operand or a `reads:`
// entry). The fix in both cases is to name the key in the producing node's
// `writes:` so the contract is declared in the graph that consumes it.
//
// Returns (nil, nil) for a nil graph — nil-graph reporting belongs to the
// structural validators.
func ValidateVariableAvailability(g *Graph) (errs []string, warns []string) {
	if g == nil {
		return nil, nil
	}
	c := &varAvailChecker{graph: g, reach: newVarReach(g)}
	c.producers, c.opaque = collectVarProducers(g)
	for _, f := range c.findings() {
		if f.fatal {
			errs = append(errs, f.msg)
			continue
		}
		warns = append(warns, f.msg)
	}
	sort.Strings(errs)
	sort.Strings(warns)
	return errs, warns
}

// varFinding is one deduplicated (node, key) complaint.
type varFinding struct {
	msg   string
	fatal bool
}

// findings runs check over every reference and keeps at most one finding per
// (node, key) pair — the fatal one when the same key is both declared in
// reads: / used in a condition and interpolated into an attribute.
func (c *varAvailChecker) findings() []varFinding {
	index := make(map[string]int)
	var out []varFinding
	for _, ref := range collectVarRefs(c.graph) {
		msg, fatal, flagged := c.check(ref)
		if !flagged {
			continue
		}
		out = upsertVarFinding(out, index, ref.nodeID+"\x00"+ref.key, varFinding{msg: msg, fatal: fatal})
	}
	return out
}

// upsertVarFinding appends a first-seen finding, or upgrades an existing one
// from warning to error. Warnings never downgrade an error.
func upsertVarFinding(out []varFinding, index map[string]int, id string, f varFinding) []varFinding {
	i, seen := index[id]
	if !seen {
		index[id] = len(out)
		return append(out, f)
	}
	if f.fatal && !out[i].fatal {
		out[i] = f
	}
	return out
}

// alwaysAvailableVarKeys is the engine/handler-written key set. These are
// written by the runtime independently of graph position (the engine even
// seeds several to "" before every node), so they are never flagged.
var alwaysAvailableVarKeys = map[string]bool{
	ContextKeyOutcome:            true,
	ContextKeyPreferredLabel:     true,
	ContextKeyLastResponse:       true,
	ContextKeyHumanResponse:      true,
	ContextKeyToolStdout:         true,
	ContextKeyToolStderr:         true,
	ContextKeyToolMarker:         true,
	ContextKeyToolMarkerError:    true,
	ContextKeyToolRoute:          true,
	ContextKeySuggestedNextNodes: true,
	ContextKeyTurnLimitMsg:       true,
	ContextKeyTurnBreachClass:    true,
	ContextKeyNodeCostExceeded:   true,
	ContextKeyNodeNoProgress:     true,
	ContextKeyEpisodeSummary:     true,
	ContextKeyEpisodeSummaries:   true,
	ContextKeyInterviewQuestions: true,
	ContextKeyInterviewAnswers:   true,
	ContextKeyGoal:               true,
	"last_cost":                  true,
	"last_turns":                 true,
	"writes_error":               true,
	"writes_warning":             true,
	"completed_summary":          true,
}

// ambientVarPrefixes are namespaces whose members are produced dynamically by
// the runtime (graph attrs, workflow params, per-node aliases, fidelity
// summaries, manager-loop steering and child state, engine internals). Membership
// cannot be decided statically, so every key under them is skipped.
var ambientVarPrefixes = []string{
	"graph.",             // seeded from Graph.Attrs by buildInitialContext
	graphParamPrefix,     // workflow vars / --param overrides
	inputContextPrefix,   // declared inputs, seeded at run start (#553)
	"response.",          // per-node response snapshots
	"summary.",           // fidelity summary:high
	"steer.",             // manager_loop steer_context
	"stack.",             // manager_loop child state
	"parallel.",          // parallel.results
	"fan_in.",            // fan-in policy detail
	"internal.",          // engine bookkeeping namespace
	"_",                  // internal keys (e.g. _artifact_dir)
	ContextKeyNodePrefix, // handled by checkScopedRef before this list is consulted
}

// varOpaqueHandlers write keys this graph cannot enumerate: a subgraph runs a
// whole other workflow and merges its context back, and manager_loop returns a
// handler-computed context map. When one of them can reach a reference, the
// reference is unprovable and therefore never flagged.
var varOpaqueHandlers = map[string]bool{
	"subgraph":           true,
	"stack.manager_loop": true,
}

// varAvailChecker holds the per-graph indexes the per-reference check needs.
type varAvailChecker struct {
	graph     *Graph
	producers map[string][]string // key -> node IDs that declare writing it
	opaque    []string            // node IDs whose writes are unknowable
	reach     *varReach
}

// check classifies one reference. It returns the message, whether the finding
// is fatal, and whether anything was flagged at all.
func (c *varAvailChecker) check(ref varRef) (msg string, fatal, flagged bool) {
	if ref.key == "" {
		return "", false, false
	}
	if strings.HasPrefix(ref.key, ContextKeyNodePrefix) {
		return c.checkScopedRef(ref)
	}
	if varKeyIsAmbient(ref.key) {
		return "", false, false
	}
	if c.reachedByOpaque(ref.nodeID) {
		return "", false, false
	}
	owners := c.producers[ref.key]
	if len(owners) == 0 {
		return fmt.Sprintf(
			"node %q references context key %q in %s, but no node in this pipeline writes it "+
				"(fix the key name, or declare it in an upstream node's writes:)",
			ref.nodeID, ref.key, ref.where,
		), ref.fatalOrigin(), true
	}
	if c.anyReaches(owners, ref.nodeID) {
		return "", false, false
	}
	return fmt.Sprintf(
		"node %q references context key %q in %s, but its only producer(s) %s cannot reach it "+
			"along any edge path — verify the routing (restarts and resume can still deliver it)",
		ref.nodeID, ref.key, ref.where, strings.Join(sortedCopy(owners), ", "),
	), false, true
}

// checkScopedRef validates the node ID inside a node.<id>.<key> reference. The
// inner key is not checked: which keys get aliased depends on what the named
// node dirtied at runtime.
func (c *varAvailChecker) checkScopedRef(ref varRef) (string, bool, bool) {
	rest := strings.TrimPrefix(ref.key, ContextKeyNodePrefix)
	parts := strings.SplitN(rest, ".", 2)
	if len(parts) != 2 || parts[0] == "" {
		return "", false, false
	}
	if _, ok := c.graph.Nodes[parts[0]]; ok {
		return "", false, false
	}
	return fmt.Sprintf(
		"node %q references scoped context key %q in %s, but %q is not a node in this pipeline",
		ref.nodeID, ref.key, ref.where, parts[0],
	), false, true
}

// reachedByOpaque reports whether any opaque producer can reach nodeID.
func (c *varAvailChecker) reachedByOpaque(nodeID string) bool {
	return c.anyReaches(c.opaque, nodeID)
}

func (c *varAvailChecker) anyReaches(from []string, to string) bool {
	for _, id := range from {
		if c.reach.canReach(id, to) {
			return true
		}
	}
	return false
}

// varKeyIsAmbient reports whether a key is runtime-provided or lives in a
// namespace that cannot be resolved statically.
func varKeyIsAmbient(key string) bool {
	if alwaysAvailableVarKeys[key] {
		return true
	}
	for _, p := range ambientVarPrefixes {
		if strings.HasPrefix(key, p) {
			return true
		}
	}
	return false
}

// collectVarProducers indexes author-declared context writes. Only declared
// names are indexed — handler built-ins live in alwaysAvailableVarKeys, which
// keeps positional reasoning (and its false positives) off the built-ins.
func collectVarProducers(g *Graph) (map[string][]string, []string) {
	producers := make(map[string][]string)
	var opaque []string
	for _, id := range varNodeIDs(g) {
		node := g.Nodes[id]
		if varOpaqueHandlers[node.Handler] {
			opaque = append(opaque, id)
			continue
		}
		for _, key := range ParseDeclaredKeys(node.Attrs["writes"]) {
			producers[key] = append(producers[key], id)
		}
		// A human interview node writes its collected answers under answers_key.
		if ak := strings.TrimSpace(node.Attrs["answers_key"]); ak != "" {
			producers[ak] = append(producers[ak], id)
		}
	}
	return producers, opaque
}

// varReach answers "can node A reach node B" with a memoized forward BFS.
// Every node reaches itself (a node that writes a key and also references it
// is legal on a second visit, and self-reference is not worth a false positive).
type varReach struct {
	graph *Graph
	memo  map[string]map[string]bool
}

func newVarReach(g *Graph) *varReach {
	return &varReach{graph: g, memo: make(map[string]map[string]bool)}
}

func (r *varReach) canReach(from, to string) bool {
	set, ok := r.memo[from]
	if !ok {
		set = r.walk(from)
		r.memo[from] = set
	}
	return set[to]
}

func (r *varReach) walk(from string) map[string]bool {
	seen := map[string]bool{from: true}
	queue := []string{from}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, e := range r.graph.OutgoingEdges(cur) {
			if seen[e.To] {
				continue
			}
			seen[e.To] = true
			queue = append(queue, e.To)
		}
	}
	return seen
}
