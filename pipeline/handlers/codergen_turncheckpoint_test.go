// ABOUTME: Tests that the codergen handler wires the #427 turn_checkpoint attr
// ABOUTME: onto the native SessionConfig as a secure snapshot path (default-off).
package handlers

import (
	"path/filepath"
	"testing"

	"github.com/2389-research/tracker/agent"
	"github.com/2389-research/tracker/pipeline"
)

// runConfigFor builds the native run config for a node and returns the embedded
// SessionConfig after the turn-checkpoint wiring has run.
func sessionConfigAfterWiring(t *testing.T, h *CodergenHandler, node *pipeline.Node, pctx *pipeline.PipelineContext) *agent.SessionConfig {
	t.Helper()
	// A bare NativeBackend selects buildRunConfig's native branch (which places
	// *SessionConfig in Extra and runs the turn-checkpoint wiring); no LLM client
	// is needed for config assembly.
	runCfg, err := h.buildRunConfig(node, "prompt", NewNativeBackend(nil, nil), pctx)
	if err != nil {
		t.Fatalf("buildRunConfig: %v", err)
	}
	sc, ok := runCfg.Extra.(*agent.SessionConfig)
	if !ok {
		t.Fatalf("Extra is not *agent.SessionConfig: %T", runCfg.Extra)
	}
	return sc
}

func TestApplyTurnCheckpoint_SetsSecurePathWhenOptedIn(t *testing.T) {
	h := NewCodergenHandler(nil, t.TempDir())
	node := &pipeline.Node{ID: "Build", Handler: "codergen", Attrs: map[string]string{
		"prompt":          "do it",
		"turn_checkpoint": "true",
	}}
	pctx := pipeline.NewPipelineContext()
	pctx.SetInternal(pipeline.InternalKeyArtifactDir, filepath.Join(t.TempDir(), "run-xyz"))

	sc := sessionConfigAfterWiring(t, h, node, pctx)
	want, err := pipeline.SecureTurnCheckpointPath("run-xyz", "Build")
	if err != nil {
		t.Fatalf("SecureTurnCheckpointPath: %v", err)
	}
	if sc.TurnCheckpointPath != want {
		t.Errorf("TurnCheckpointPath = %q, want %q", sc.TurnCheckpointPath, want)
	}
}

func TestApplyTurnCheckpoint_DefaultOff(t *testing.T) {
	h := NewCodergenHandler(nil, t.TempDir())
	node := &pipeline.Node{ID: "Build", Handler: "codergen", Attrs: map[string]string{"prompt": "do it"}}
	pctx := pipeline.NewPipelineContext()
	pctx.SetInternal(pipeline.InternalKeyArtifactDir, filepath.Join(t.TempDir(), "run-xyz"))

	sc := sessionConfigAfterWiring(t, h, node, pctx)
	if sc.TurnCheckpointPath != "" {
		t.Errorf("TurnCheckpointPath = %q, want empty (feature off by default)", sc.TurnCheckpointPath)
	}
}

func TestApplyTurnCheckpoint_NoRunIDLeavesUnset(t *testing.T) {
	h := NewCodergenHandler(nil, t.TempDir())
	node := &pipeline.Node{ID: "Build", Handler: "codergen", Attrs: map[string]string{
		"prompt":          "do it",
		"turn_checkpoint": "true",
	}}
	pctx := pipeline.NewPipelineContext() // no artifact dir → no run id

	sc := sessionConfigAfterWiring(t, h, node, pctx)
	if sc.TurnCheckpointPath != "" {
		t.Errorf("TurnCheckpointPath = %q, want empty (no run id → degrade to node-boundary resume)", sc.TurnCheckpointPath)
	}
}
