// ABOUTME: resolveGateMode validation — pause mode needs an answerable checkpoint
// ABOUTME: copy, so an explicit user CheckpointDir on a fresh run is rejected (#734).
package tracker

import (
	"strings"
	"testing"
)

// TestResolveGateMode_RejectsPauseWithExplicitCheckpointDir: a fresh pause-mode
// run with an explicit CheckpointDir writes no secure checkpoint copy, so
// tracker.Answer / PendingGates cannot find the gate it pauses at. Reject it up
// front rather than stop at an unanswerable gate.
func TestResolveGateMode_RejectsPauseWithExplicitCheckpointDir(t *testing.T) {
	_, err := resolveGateMode(Config{GateMode: GateModePause, ArtifactDir: "/tmp/a", CheckpointDir: "/tmp/cp.json"})
	if err == nil {
		t.Fatal("expected error for pause mode combined with an explicit CheckpointDir")
	}
	if !strings.Contains(err.Error(), "CheckpointDir") {
		t.Errorf("error = %q, want it to mention CheckpointDir", err)
	}
}

// TestResolveGateMode_AllowsPauseResume: on a resume, applyResumeRunID populates
// CheckpointDir from the resolved run dir. That is answerable and must not be
// rejected (the #734 review caveat).
func TestResolveGateMode_AllowsPauseResume(t *testing.T) {
	iv, err := resolveGateMode(Config{GateMode: GateModePause, ArtifactDir: "/tmp/a", CheckpointDir: "/tmp/cp.json", ResumeRunID: "r1"})
	if err != nil {
		t.Fatalf("a pause-mode resume with a resolved CheckpointDir must be allowed: %v", err)
	}
	if iv == nil {
		t.Fatal("expected a pausing interviewer for a pause-mode resume")
	}
}
