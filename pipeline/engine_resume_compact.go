// ABOUTME: Fidelity-aware compaction of the run context on checkpoint resume, and the
// ABOUTME: routing hints and engine-structural keys it must always keep (#697).
package pipeline

import "strings"

// compactResumeContext applies fidelity-aware compaction when resuming from a
// checkpoint. It rewrites the run context itself (dropped keys become ""), not
// only a prompt view, so routing hints and engine-structural state are restored
// afterwards.
func (e *Engine) compactResumeContext(cp *Checkpoint, pctx *PipelineContext, runID string) {
	// #714: after compaction settles the context (deferred so it runs on both the
	// no-op and the full path, and after compaction's own Set calls), mark the
	// checkpoint's tool_command-safe keys shell-untrusted — file-sourced values
	// must not expand into a tool_command until a live node re-derives them. A
	// fresh run's empty checkpoint Context marks nothing.
	defer markRestoredSafeKeysShellUntrusted(cp, pctx)
	if cp.CurrentNode == "" || len(cp.CompletedNodes) == 0 {
		return
	}

	routingHints := captureRoutingHints(pctx)
	structural := captureResumeStructuralKeys(pctx, e.graph.Attrs)

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

// markRestoredSafeKeysShellUntrusted flags every tool_command-safe ctx key whose
// value originated in the checkpoint as shell-untrusted (#714), so tool_command
// expansion rejects it until a live node re-derives it. Provenance is checkpoint
// membership: only keys the checkpoint actually carried are marked. Called after
// compaction so no intervening Set clears the mark; a fresh run's empty
// checkpoint Context marks nothing.
func markRestoredSafeKeysShellUntrusted(cp *Checkpoint, pctx *PipelineContext) {
	if cp == nil || pctx == nil {
		return
	}
	for key := range toolCommandSafeCtxKeys {
		if _, ok := cp.Context[key]; ok {
			pctx.MarkShellUntrusted(key)
		}
	}
}

// captureResumeStructuralKeys returns the context keys that resume compaction
// must never blank: engine-structural state consumed by later nodes rather than
// prompt history (#697). liveAttrs is the live graph's attributes.
func captureResumeStructuralKeys(pctx *PipelineContext, liveAttrs map[string]string) map[string]string {
	kept := make(map[string]string)
	for k, v := range pctx.Snapshot() {
		if isResumeStructuralKey(k, liveAttrs) {
			kept[k] = v
		}
	}
	return kept
}

// isResumeStructuralKey reports whether k is engine-structural state: a
// parallel node's branch results (and the node-scoped copy
// node.<id>.parallel.results), a declared input (#553), or a graph attribute
// the live graph still declares (re-seeded from it just before compaction).
func isResumeStructuralKey(k string, liveAttrs map[string]string) bool {
	if k == ContextKeyParallelResults || strings.HasPrefix(k, inputContextPrefix) || isLiveGraphAttrKey(k, liveAttrs) {
		return true
	}
	return strings.HasPrefix(k, "node.") && strings.HasSuffix(k, "."+ContextKeyParallelResults)
}

// isLiveGraphAttrKey reports whether k is graph.<attr> for an attribute the
// live graph declares. A checkpoint can carry graph.* keys for attributes
// removed from the .dip since the run started; the re-seed doesn't overwrite
// those, so keeping them would resume with stale configuration.
func isLiveGraphAttrKey(k string, liveAttrs map[string]string) bool {
	attr, ok := strings.CutPrefix(k, "graph.")
	if !ok {
		return false
	}
	_, live := liveAttrs[attr]
	return live
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
