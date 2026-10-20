// ABOUTME: Where a node runs inside a run: the subgraph nesting (on the Go
// ABOUTME: context) and the parallel branch (internal keys on the branch context).
package pipeline

import (
	"context"
	"strconv"
)

// Internal keys the parallel handler sets on each branch's context: the branch
// target's node ID and its 0-based index among the parallel node's targets.
// Internal keys are not copied back into the parent context at fan-in, so a
// node after the join never looks like it runs in a branch.
const (
	InternalKeyBranchScope = "_branch_scope"
	InternalKeyBranchIndex = "_branch_index"
)

type subgraphScopeKey struct{}

// WithSubgraphScope returns ctx with nodeID appended to its subgraph scope.
// The subgraph and manager_loop handlers wrap the context they run a child
// engine with, so a node inside the child can tell it is not top-level.
func WithSubgraphScope(ctx context.Context, nodeID string) context.Context {
	outer := SubgraphScope(ctx)
	scope := make([]string, 0, len(outer)+1)
	scope = append(append(scope, outer...), nodeID)
	return context.WithValue(ctx, subgraphScopeKey{}, scope)
}

// SubgraphScope returns the subgraph/manager_loop node IDs enclosing the
// current node, outermost first; empty at the top level.
func SubgraphScope(ctx context.Context) []string {
	scope, _ := ctx.Value(subgraphScopeKey{}).([]string)
	return append([]string(nil), scope...)
}

// BranchScope reports the parallel branch the current node runs in: the
// branch target's node ID and index, or ok=false outside a branch.
func BranchScope(pctx *PipelineContext) (branchID string, index int, ok bool) {
	if pctx == nil {
		return "", 0, false
	}
	branchID, ok = pctx.GetInternal(InternalKeyBranchScope)
	if !ok || branchID == "" {
		return "", 0, false
	}
	raw, _ := pctx.GetInternal(InternalKeyBranchIndex)
	index, _ = strconv.Atoi(raw)
	return branchID, index, true
}
