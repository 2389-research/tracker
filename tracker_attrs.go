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
