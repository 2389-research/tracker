// ABOUTME: Small graph-attribute readers used to fall back to workflow-level
// ABOUTME: defaults (budget, timeouts, opt-in flags) when a Config field is zero.
package tracker

import (
	"strconv"
	"time"

	"github.com/2389-research/tracker/pipeline"
)

// positiveIntAttr returns the positive int value of graph.Attrs[key], or 0 when
// absent, unparseable, or non-positive (leaving the caller's field unchanged).
func positiveIntAttr(graph *pipeline.Graph, key string) int {
	if v, ok := graph.Attrs[key]; ok {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return 0
}

// positiveDurationAttr returns the positive duration value of graph.Attrs[key],
// or 0 when absent, unparseable, or non-positive.
func positiveDurationAttr(graph *pipeline.Graph, key string) time.Duration {
	if v, ok := graph.Attrs[key]; ok {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return 0
}

// boolAttr returns the parsed bool value of graph.Attrs[key], or false when
// absent or unparseable (malformed values stay default-off, never mis-enabled).
func boolAttr(graph *pipeline.Graph, key string) bool {
	if v, ok := graph.Attrs[key]; ok {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return false
}

// ResolveBudgetLimits fills any zero field on cfg from the matching
// workflow-level default in graph.Attrs. Config values take precedence —
// the graph attrs are only consulted for fields the caller left unset.
// Returns the original cfg unchanged if graph is nil or has no attrs.
//
// The graph-level keys consulted are max_total_tokens, max_cost_cents,
// max_wall_time, and stall_timeout, which the dippin adapter writes from
// WorkflowDefaults fields in v0.21.0+.
//
// Exported so the tracker CLI can merge its --max-* flag values with
// workflow defaults without re-implementing the same logic.
func ResolveBudgetLimits(cfg pipeline.BudgetLimits, graph *pipeline.Graph) pipeline.BudgetLimits {
	if graph == nil || len(graph.Attrs) == 0 {
		return cfg
	}
	if cfg.MaxTotalTokens == 0 {
		cfg.MaxTotalTokens = positiveIntAttr(graph, "max_total_tokens")
	}
	if cfg.MaxCostCents == 0 {
		cfg.MaxCostCents = positiveIntAttr(graph, "max_cost_cents")
	}
	if cfg.MaxWallTime == 0 {
		cfg.MaxWallTime = positiveDurationAttr(graph, "max_wall_time")
	}
	if cfg.StallTimeout == 0 {
		cfg.StallTimeout = positiveDurationAttr(graph, "stall_timeout")
	}
	// Opt-in sleep-awareness (#422). A bool can't use the == 0 zero gate, so a
	// Config value of true wins; otherwise consult the attr. A malformed bool
	// falls through to the default (off) — it does not silently mis-enable.
	if !cfg.SleepAware {
		cfg.SleepAware = boolAttr(graph, "sleep_aware_budget")
	}
	return cfg
}
