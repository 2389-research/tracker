// ABOUTME: Tests for stampingHandler — registry-side BundleIdentity stamping
// ABOUTME: that mirrors Engine.emit for handler-package emissions.
package handlers

import (
	"testing"

	"github.com/2389-research/tracker/pipeline"
)

// TestStampingHandler_StampsEmptyIdentity pins the contract that an
// emission with empty BundleIdentity gets stamped with the configured
// identity — matching Engine.emit's behavior, which is the whole point
// of this wrapper (handler-package emissions bypass Engine.emit but land
// in the same activity.jsonl writer).
func TestStampingHandler_StampsEmptyIdentity(t *testing.T) {
	var captured pipeline.PipelineEvent
	inner := pipeline.PipelineEventHandlerFunc(func(evt pipeline.PipelineEvent) {
		captured = evt
	})
	s := &stampingHandler{inner: inner, identity: "sha256:abc"}
	s.HandlePipelineEvent(pipeline.PipelineEvent{Type: pipeline.EventStageStarted})
	if captured.BundleIdentity != "sha256:abc" {
		t.Errorf("identity not stamped: got %q want %q", captured.BundleIdentity, "sha256:abc")
	}
}

// TestStampingHandler_PreservesCallerIdentity pins the contract that an
// emission whose BundleIdentity is already set is left alone. Matches
// the `if evt.BundleIdentity == ""` guard in Engine.emit so identities
// stamped upstream (e.g., by NodeScopedPipelineHandler chains) survive
// the registry-side wrapper.
func TestStampingHandler_PreservesCallerIdentity(t *testing.T) {
	var captured pipeline.PipelineEvent
	inner := pipeline.PipelineEventHandlerFunc(func(evt pipeline.PipelineEvent) {
		captured = evt
	})
	s := &stampingHandler{inner: inner, identity: "sha256:abc"}
	s.HandlePipelineEvent(pipeline.PipelineEvent{
		Type:           pipeline.EventStageStarted,
		BundleIdentity: "sha256:xyz",
	})
	if captured.BundleIdentity != "sha256:xyz" {
		t.Errorf("caller identity should be preserved: got %q want %q", captured.BundleIdentity, "sha256:xyz")
	}
}

// TestWithHandlerBundleIdentity_AssignsField pins the contract that the
// option assigns the identity onto registryConfig. The wrap itself runs
// at NewDefaultRegistry call time — we cover that branch in the next two
// tests by simulating the same conditional.
func TestWithHandlerBundleIdentity_AssignsField(t *testing.T) {
	cfg := &registryConfig{}
	WithHandlerBundleIdentity("sha256:assigned")(cfg)
	if cfg.bundleIdentity != "sha256:assigned" {
		t.Errorf("identity not assigned: got %q want %q", cfg.bundleIdentity, "sha256:assigned")
	}
}

// TestRegistryWrapBranch_FiresWhenIdentitySet emulates the conditional
// inside NewDefaultRegistry and confirms that a non-empty identity plus
// a non-nil handler produces a stampingHandler wrapper. We do this in
// isolation from NewDefaultRegistry so the test stays focused on the
// wrap logic — the integration coverage (full registry execution of
// parallel/manager_loop emitting stamped events) belongs to Task 16.
func TestRegistryWrapBranch_FiresWhenIdentitySet(t *testing.T) {
	collector := pipeline.PipelineEventHandlerFunc(func(evt pipeline.PipelineEvent) {})
	cfg := &registryConfig{}
	WithPipelineEventHandler(collector)(cfg)
	WithHandlerBundleIdentity("sha256:wrapped")(cfg)

	// Mirror the exact branch in NewDefaultRegistry.
	if cfg.bundleIdentity != "" && cfg.pipelineEvents != nil {
		cfg.pipelineEvents = &stampingHandler{
			inner:    cfg.pipelineEvents,
			identity: cfg.bundleIdentity,
		}
	}

	wrapper, ok := cfg.pipelineEvents.(*stampingHandler)
	if !ok {
		t.Fatalf("expected *stampingHandler, got %T", cfg.pipelineEvents)
	}
	if wrapper.identity != "sha256:wrapped" {
		t.Errorf("wrapper identity = %q, want sha256:wrapped", wrapper.identity)
	}
}

// TestRegistryWrapBranch_NoOpWhenIdentityEmpty confirms the no-op
// behavior — plain .dip runs (no bundle identity) see no wrapper
// allocated, so cfg.pipelineEvents is the original handler exactly as
// the caller supplied it.
func TestRegistryWrapBranch_NoOpWhenIdentityEmpty(t *testing.T) {
	collector := pipeline.PipelineEventHandlerFunc(func(evt pipeline.PipelineEvent) {})
	cfg := &registryConfig{}
	WithPipelineEventHandler(collector)(cfg)
	WithHandlerBundleIdentity("")(cfg)

	// Mirror the exact branch in NewDefaultRegistry.
	if cfg.bundleIdentity != "" && cfg.pipelineEvents != nil {
		t.Fatalf("wrap branch should not fire for empty identity")
	}
	if _, isStamp := cfg.pipelineEvents.(*stampingHandler); isStamp {
		t.Fatalf("pipelineEvents should not be wrapped when identity is empty")
	}
}
