// ABOUTME: buildInitialContext seeds a run's PipelineContext from graph attrs and
// ABOUTME: the initial-context handoff, re-creating inherited shell-untrust marks (#714).
package pipeline

// buildInitialContext creates a PipelineContext seeded with graph and initial
// context values, then re-creates any inherited shell-untrust marks.
func (e *Engine) buildInitialContext() *PipelineContext {
	pctx := NewPipelineContext()
	for key, value := range e.graph.Attrs {
		pctx.Set("graph."+key, value)
	}
	for k, v := range e.initialContext {
		pctx.Set(k, v)
	}
	// #714: re-create inherited shell-untrust marks AFTER seeding values — the
	// Set calls above would otherwise leave an unmarked child context, letting a
	// checkpoint-borne safe value inherited by a subgraph / manager_loop child
	// expand into a child tool_command. A later same-value write keeps the mark.
	for _, k := range e.initialShellUntrusted {
		pctx.MarkShellUntrusted(k)
	}
	return pctx
}
