// ABOUTME: Wraps a PipelineEventHandler to stamp .dipx bundle identity onto
// ABOUTME: emissions that bypass Engine.emit's chokepoint (handler package + external).
package handlers

import "github.com/2389-research/tracker/pipeline"

// BundleIdentityStamper wraps a PipelineEventHandler and injects the
// .dipx bundle identity onto every emitted event whose identity is
// currently empty. Used by the handler registry to stamp emissions
// that bypass the engine's emit chokepoint (parallel, manager_loop),
// and available for library callers that build their own registries.
//
// Empty identity is a no-op: plain .dip runs see no change.
// Non-empty caller-set identities are preserved (the guard matches
// Engine.emit's behavior).
//
// Fields are exported so external callers can construct the wrapper
// directly. The type is small enough that field-access is fine and
// avoids needing a constructor function.
type BundleIdentityStamper struct {
	Inner    pipeline.PipelineEventHandler
	Identity string
}

func (s *BundleIdentityStamper) HandlePipelineEvent(evt pipeline.PipelineEvent) {
	if s == nil {
		return
	}
	if evt.BundleIdentity == "" {
		evt.BundleIdentity = s.Identity
	}
	if s.Inner == nil {
		return
	}
	s.Inner.HandlePipelineEvent(evt)
}
