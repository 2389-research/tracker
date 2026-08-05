// ABOUTME: Branch-target fallback validation for the parallel handler (#313 defect 2).
// ABOUTME: Refuses a fallback_target declared on a fan-out branch, which would be silently inert.
package handlers

import (
	"fmt"
	"maps"
	"slices"

	"github.com/2389-research/tracker/pipeline"
)

// branchFallbackAttrs are the node-attr spellings the engine's
// findFallbackTarget consults: the raw "fallback_target" key and
// "fallback_retry_target", which is where the adapter's extractRetryAttrs
// stores a .dip node-level `fallback_target:` declaration.
var branchFallbackAttrs = []string{"fallback_target", "fallback_retry_target"}

// refuseBranchFallbackTargets fails fast when any branch-target node (or any
// branch.N.* override group) declares a node-level fallback target under
// either spelling. Branch nodes execute via registry.Execute inside runBranch
// and never reach the engine's strict-failure path, so the attr would do
// nothing at runtime — refusing loudly beats shipping a silently-inert
// failure route (#313 defect 2). The scan reads the raw indexed branch attrs
// rather than the collapsed target-keyed override map: with duplicate-target
// branches the collapse keeps only the highest index wholesale (#368), which
// would hide an earlier branch's equally-dead fallback declaration (Copilot,
// PR #377). Graph-level fallback_target (dippin `defaults: on_failure`) is
// unaffected: it lives on graph attrs and applies in the engine's main loop.
func refuseBranchFallbackTargets(parallelID string, edges []*pipeline.Edge, graph *pipeline.Graph, parallelAttrs map[string]string) error {
	indexed := indexBranchAttrs(parallelAttrs)
	declaredByTarget := collectDeclaredBranchFallbacks(indexed)
	return checkBranchFallbackTargets(parallelID, edges, graph, declaredByTarget)
}

// collectDeclaredBranchFallbacks scans branch.N.* attr groups for declared
// fallback targets, building target node ID → fallback attr spelling →
// declared value, across ALL branch.N.* groups naming that target (not just
// the surviving one — with duplicate-target branches the collapse keeps
// only the highest index wholesale (#368), which would hide an earlier
// branch's equally-dead fallback declaration, Copilot PR #377). Indices are
// visited in ascending order so that when several groups declare a fallback
// for the same target, the highest index's value is the one reported —
// deterministic output, matching the last-branch-wins duplicate-target
// convention (#368).
func collectDeclaredBranchFallbacks(indexed map[int]map[string]string) map[string]map[string]string {
	declaredByTarget := make(map[string]map[string]string)
	for _, idx := range slices.Sorted(maps.Keys(indexed)) {
		recordBranchFallbacks(declaredByTarget, indexed[idx])
	}
	return declaredByTarget
}

// recordBranchFallbacks copies every declared fallback attr from a single
// branch.N.* group onto declaredByTarget, keyed by the group's target node.
// No-op when the group names no target.
func recordBranchFallbacks(declaredByTarget map[string]map[string]string, branchAttrs map[string]string) {
	target := branchAttrs["target"]
	if target == "" {
		return
	}
	for _, attr := range branchFallbackAttrs {
		v := branchAttrs[attr]
		if v == "" {
			continue
		}
		if declaredByTarget[target] == nil {
			declaredByTarget[target] = make(map[string]string)
		}
		declaredByTarget[target][attr] = v
	}
}

// checkBranchFallbackTargets fails fast when any branch-target edge's
// target node (via graph attrs or a declared branch.N.* override) carries a
// node-level fallback target under either spelling in branchFallbackAttrs.
// Branch nodes execute via registry.Execute inside runBranch and never
// reach the engine's strict-failure path, so the attr would do nothing at
// runtime — refusing loudly beats shipping a silently-inert failure route
// (#313 defect 2). Graph-level fallback_target (dippin `defaults:
// on_failure`) is unaffected: it lives on graph attrs and applies in the
// engine's main loop.
func checkBranchFallbackTargets(parallelID string, edges []*pipeline.Edge, graph *pipeline.Graph, declaredByTarget map[string]map[string]string) error {
	for _, edge := range edges {
		if err := checkEdgeFallbackTargets(parallelID, edge, graph, declaredByTarget); err != nil {
			return err
		}
	}
	return nil
}

// checkEdgeFallbackTargets checks a single branch-target edge for a declared
// fallback under any spelling in branchFallbackAttrs, preferring the
// branch.N.* override over the target node's own graph attrs (matching the
// last-branch-wins convention used to build declaredByTarget).
func checkEdgeFallbackTargets(parallelID string, edge *pipeline.Edge, graph *pipeline.Graph, declaredByTarget map[string]map[string]string) error {
	for _, attr := range branchFallbackAttrs {
		declared := ""
		if tn, ok := graph.Nodes[edge.To]; ok {
			declared = tn.Attrs[attr]
		}
		if v := declaredByTarget[edge.To][attr]; v != "" {
			declared = v
		}
		if declared != "" {
			return fmt.Errorf("parallel node %q: branch target %q declares %s %q, which is never honored inside a parallel branch — remove it and route branch failure at the aggregating node via fan_in_policy (any|all|quorum) and a conditional fail edge", parallelID, edge.To, attr, declared)
		}
	}
	return nil
}
