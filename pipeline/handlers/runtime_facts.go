// ABOUTME: Runtime-facts block prepended to every codergen prompt (#347).
// ABOUTME: Surfaces working dir, current date, and run/node identity so agents never fill them with priors.
package handlers

import (
	"fmt"
	"strings"
	"time"
)

// prependRuntimeFacts prepends a machine-written "# Runtime" block to a
// codergen prompt (#347). It states the absolute working directory, the
// current date, and the run/node identity — facts an LLM will otherwise fill
// with priors (a hallucinated `cd` + clean tree once shipped an empty
// milestone). Empty workDir omits the line; empty runID omits the Run label.
// The block is always outermost: callers apply it after all other prompt
// assembly (context summary, variable expansion).
func prependRuntimeFacts(prompt, workDir, runID, nodeID string, now time.Time) string {
	var b strings.Builder
	b.WriteString("# Runtime\n")
	if workDir != "" {
		fmt.Fprintf(&b, "- Working directory (absolute): %s — all commands already run here; never cd elsewhere. A failed cd is a hard error, never evidence of completion.\n", absPathOrSelf(workDir))
	}
	fmt.Fprintf(&b, "- Current date: %s\n", now.Format("2006-01-02"))
	if runID != "" {
		fmt.Fprintf(&b, "- Run: %s, node: %s\n", runID, nodeID)
	} else {
		fmt.Fprintf(&b, "- Node: %s\n", nodeID)
	}
	return b.String() + "\n" + prompt
}
