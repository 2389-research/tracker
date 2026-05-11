// ABOUTME: Wraps a PipelineEventHandler to stamp .dipx bundle identity onto
// ABOUTME: handler-package emissions that bypass Engine.emit's chokepoint.
package handlers

import "github.com/2389-research/tracker/pipeline"

// stampingHandler wraps a PipelineEventHandler and injects BundleIdentity
// onto every emitted event whose identity is currently empty.
//
// This is the registry-side analogue of Engine.emit's stamping behavior —
// handler-package emissions (parallel.go, manager_loop.go) bypass
// Engine.emit but share the same destination JSONL writer, so they need
// their own stamping pass to keep activity.jsonl provenance complete.
//
// Empty inner.identity is a no-op: plain .dip runs see no change. The
// `if evt.BundleIdentity == ""` guard matches the engine so caller-set
// identities are preserved (matters when a sub-emission is itself
// already stamped via NodeScopedPipelineHandler or similar wrappers).
type stampingHandler struct {
	inner    pipeline.PipelineEventHandler
	identity string
}

func (s *stampingHandler) HandlePipelineEvent(evt pipeline.PipelineEvent) {
	if evt.BundleIdentity == "" {
		evt.BundleIdentity = s.identity
	}
	s.inner.HandlePipelineEvent(evt)
}
