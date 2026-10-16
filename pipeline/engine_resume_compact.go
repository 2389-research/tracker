// ABOUTME: Fidelity-aware compaction of the run context on checkpoint resume, and the
// ABOUTME: routing hints and engine-structural keys it must always keep (#697).
package pipeline

import "strings"

// compactResumeContext applies fidelity-aware compaction when resuming from a
// checkpoint. It rewrites the run context itself (dropped keys become ""), not
// only a prompt view, so routing hints and engine-structural state are restored
// afterwards.
func (e *Engine) compactResumeContext(cp *Checkpoint, pctx *PipelineContext, runID string) {
	if cp.CurrentNode == "" || len(cp.CompletedNodes) == 0 {
		return
	}

	routingHints := captureRoutingHints(pctx)
	structural := captureResumeStructuralKeys(pctx)

	// Pin the declared reads of the node the run will actually re-enter — a
	// resume rewind (#651) may land on the fail-routed origin, not CurrentNode.
	currentNode := e.nodeOrDefault(e.planResume(cp).entry)
	fidelity := ResolveFidelity(currentNode, e.graph.Attrs)
	degraded := DegradeFidelity(fidelity)
	compacted := CompactContextWithPinnedKeys(
		pctx,
		cp.CompletedNodes,
		degraded,
		e.artifactDir,
		runID,
		ParseDeclaredKeys(currentNode.Attrs["reads"]),
	)

	replaceContextValues(pctx, compacted)
	restoreRoutingHints(pctx, routingHints)
	// State that later handlers read back must survive compaction (#697).
	// Compaction never writes these keys, so a plain overwrite is correct.
	for k, v := range structural {
		pctx.Set(k, v)
	}
}

// captureResumeStructuralKeys returns the context keys that resume compaction
// must never blank: engine-structural state consumed by later nodes rather than
// prompt history (#697).
func captureResumeStructuralKeys(pctx *PipelineContext) map[string]string {
	kept := make(map[string]string)
	for k, v := range pctx.Snapshot() {
		if isResumeStructuralKey(k) {
			kept[k] = v
		}
	}
	return kept
}

// isResumeStructuralKey reports whether k is engine-structural state: a
// parallel node's branch results (and the node-scoped copy
// node.<id>.parallel.results), a declared input (#553), or a graph attribute
// (authoritative from the live graph, re-seeded just before compaction).
func isResumeStructuralKey(k string) bool {
	if k == ContextKeyParallelResults || strings.HasPrefix(k, inputContextPrefix) || strings.HasPrefix(k, "graph.") {
		return true
	}
	return strings.HasPrefix(k, "node.") && strings.HasSuffix(k, "."+ContextKeyParallelResults)
}

// captureRoutingHints saves the current routing hint values from context.
func captureRoutingHints(pctx *PipelineContext) map[string]string {
	hints := make(map[string]string)
	for _, key := range []string{ContextKeyOutcome, ContextKeyPreferredLabel, ContextKeySuggestedNextNodes} {
		if val, ok := pctx.Get(key); ok && val != "" {
			hints[key] = val
		}
	}
	return hints
}

// replaceContextValues clears the context and repopulates it with compacted values.
func replaceContextValues(pctx *PipelineContext, compacted map[string]string) {
	for k := range pctx.Snapshot() {
		pctx.Set(k, "")
	}
	for k, v := range compacted {
		pctx.Set(k, v)
	}
}

// restoreRoutingHints re-applies routing hints that were cleared during compaction.
func restoreRoutingHints(pctx *PipelineContext, hints map[string]string) {
	for k, v := range hints {
		if existing, ok := pctx.Get(k); !ok || existing == "" {
			pctx.Set(k, v)
		}
	}
}
