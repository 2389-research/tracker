// ABOUTME: Configurable fan-in aggregation policy shared by the parallel and fan-in handlers (#313).
// ABOUTME: Supports fan_in_policy: any (default, success-if-any), all, or quorum (with quorum: <n>).
package handlers

import (
	"fmt"
	"strings"

	"github.com/2389-research/tracker/pipeline"
)

// fanInPolicy is the resolved branch-aggregation policy for a parallel or
// fan-in node. Both aggregation code paths (ParallelHandler.aggregateStatus
// and FanInHandler) evaluate the same policy so a workflow can declare it on
// either node.
type fanInPolicy struct {
	name   string // "any", "all", or "quorum"
	quorum int    // required successes when name == "quorum"
}

// resolveFanInPolicy validates the node's fan_in_policy / quorum params and
// returns the effective policy. Unset means "any" (success-if-any) for
// back-compat. Unknown policies and quorum without a positive n are
// configuration errors — fail loudly rather than silently aggregating.
func resolveFanInPolicy(nodeID string, cfg pipeline.ParallelNodeConfig) (fanInPolicy, error) {
	switch cfg.FanInPolicy {
	case "", "any":
		return fanInPolicy{name: "any"}, nil
	case "all":
		return fanInPolicy{name: "all"}, nil
	case "quorum":
		if cfg.Quorum < 1 {
			return fanInPolicy{}, fmt.Errorf("node %q: fan_in_policy=quorum requires a positive quorum param", nodeID)
		}
		return fanInPolicy{name: "quorum", quorum: cfg.Quorum}, nil
	default:
		return fanInPolicy{}, fmt.Errorf("node %q: unknown fan_in_policy %q (want any, all, or quorum)", nodeID, cfg.FanInPolicy)
	}
}

// satisfied reports whether the policy is met by the given branch tally.
func (p fanInPolicy) satisfied(successes, total int) bool {
	switch p.name {
	case "all":
		return total > 0 && successes == total
	case "quorum":
		return successes >= p.quorum
	default: // any
		return successes > 0
	}
}

// detail renders a human-readable explanation of the policy evaluation for
// events and audit context, naming any failed branches.
func (p fanInPolicy) detail(successes, total int, failed []string) string {
	d := fmt.Sprintf("policy %s: %d/%d branches succeeded", p.name, successes, total)
	if p.name == "quorum" {
		d = fmt.Sprintf("policy quorum(%d): %d/%d branches succeeded", p.quorum, successes, total)
	}
	if len(failed) > 0 {
		d += "; failed: " + strings.Join(failed, ", ")
	}
	return d
}

// tallyBranches counts successful branches and collects the IDs of
// non-successful ones.
func tallyBranches(results []ParallelResult) (successes int, failed []string) {
	for _, r := range results {
		if r.Status == string(pipeline.OutcomeSuccess) {
			successes++
		} else {
			failed = append(failed, r.NodeID)
		}
	}
	return successes, failed
}
