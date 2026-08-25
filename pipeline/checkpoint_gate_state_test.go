// ABOUTME: Tests for the unified per-gate checkpoint state machine (#602).
// ABOUTME: Covers phase transitions, the fallback latch, and one-way migration from the pre-#602 scattered maps.
package pipeline

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// TestGateState_TransitionTable exhaustively pins the phase transitions the
// central methods perform, so a future edit that reintroduces a contradictory
// combination (pending AND overridden at once) fails here (#602).
func TestGateState_TransitionTable(t *testing.T) {
	t.Run("fresh gate is Active with no recorded state", func(t *testing.T) {
		cp := &Checkpoint{}
		if cp.IsGateRecheckPending("g") || cp.IsGateOverridden("g") || cp.IsFallbackTaken("g") {
			t.Fatal("a gate with no state must report no pending/overridden/fallback")
		}
		if cp.GateOutcome("g") != "" {
			t.Fatalf("GateOutcome on a fresh gate = %q, want empty", cp.GateOutcome("g"))
		}
		// A read must not have created a map entry.
		if cp.GateStates != nil {
			t.Fatalf("read-only queries grew GateStates: %v", cp.GateStates)
		}
	})

	t.Run("pending set then cleared returns to Active", func(t *testing.T) {
		cp := &Checkpoint{}
		cp.SetGateRecheckPending("g")
		if !cp.IsGateRecheckPending("g") {
			t.Fatal("SetGateRecheckPending did not set pending")
		}
		cp.ClearGateRecheckPending("g")
		if cp.IsGateRecheckPending("g") {
			t.Fatal("ClearGateRecheckPending did not clear pending")
		}
	})

	t.Run("override replaces pending (override wins)", func(t *testing.T) {
		cp := &Checkpoint{}
		cp.SetGateRecheckPending("g")
		cp.MarkGateOverridden("g")
		if !cp.IsGateOverridden("g") {
			t.Fatal("MarkGateOverridden did not set overridden")
		}
		if cp.IsGateRecheckPending("g") {
			t.Fatal("a gate cannot be BOTH pending and overridden — override must replace pending")
		}
	})

	t.Run("clearing pending on an overridden gate leaves the override intact", func(t *testing.T) {
		cp := &Checkpoint{}
		cp.MarkGateOverridden("g")
		cp.ClearGateRecheckPending("g") // the defensive clear in markCoveredGoalGates
		if !cp.IsGateOverridden("g") {
			t.Fatal("ClearGateRecheckPending wrongly dropped an override")
		}
	})

	t.Run("clearing override on a pending gate leaves pending intact", func(t *testing.T) {
		cp := &Checkpoint{}
		cp.SetGateRecheckPending("g")
		cp.ClearGateOverridden("g")
		if !cp.IsGateRecheckPending("g") {
			t.Fatal("ClearGateOverridden wrongly dropped a pending recheck")
		}
	})

	t.Run("clearing both (gate re-executes) resets from either phase", func(t *testing.T) {
		for _, seed := range []func(*Checkpoint){
			func(cp *Checkpoint) { cp.SetGateRecheckPending("g") },
			func(cp *Checkpoint) { cp.MarkGateOverridden("g") },
		} {
			cp := &Checkpoint{}
			seed(cp)
			cp.ClearGateRecheckPending("g")
			cp.ClearGateOverridden("g")
			if cp.IsGateRecheckPending("g") || cp.IsGateOverridden("g") {
				t.Fatal("clearGoalGateFlagsOnExecute pair must reset the gate to Active")
			}
		}
	})
}

// TestGateState_FallbackLatchOrthogonal pins that the one-shot fallback latch is
// independent of the phase — a gate can be recheck-pending AND fallback-taken at
// once (the handleExitNode fallback path sets both), which the pre-#602 maps also
// allowed.
func TestGateState_FallbackLatchOrthogonal(t *testing.T) {
	cp := &Checkpoint{}
	cp.MarkFallbackTaken("g")
	cp.SetGateRecheckPending("g")
	if !cp.IsFallbackTaken("g") {
		t.Fatal("fallback latch lost when the phase changed")
	}
	if !cp.IsGateRecheckPending("g") {
		t.Fatal("phase lost when the fallback latch was set")
	}
	// The phase transition must not clear the latch.
	cp.MarkGateOverridden("g")
	if !cp.IsFallbackTaken("g") {
		t.Fatal("fallback latch must survive an override transition")
	}
}

// TestGateState_OutcomeRoundtrip pins that the durable last-outcome survives a
// plain JSON round-trip (the #533 property, now carried on GateState).
func TestGateState_OutcomeRoundtrip(t *testing.T) {
	cp := &Checkpoint{}
	cp.SetGateOutcome("gate", string(OutcomeSuccess))
	data, err := json.Marshal(cp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got Checkpoint
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.GateOutcome("gate") != string(OutcomeSuccess) {
		t.Fatalf("GateOutcome after round-trip = %q, want success", got.GateOutcome("gate"))
	}
}

// TestCheckpoint_EmptyHasNoGateStatesKey pins the omitempty backward-compat
// property: a run that touched no gate serializes byte-identically to a
// pre-feature checkpoint (no gate_states key).
func TestCheckpoint_EmptyHasNoGateStatesKey(t *testing.T) {
	data, _ := json.Marshal(&Checkpoint{})
	if strings.Contains(string(data), "gate_states") {
		t.Fatalf("empty checkpoint JSON contains gate_states: %s", data)
	}
}

// TestCheckpoint_LegacyMigration_FoldsAllFourMaps pins the one-way migration:
// a pre-#602 checkpoint's four scattered maps are folded into GateStates on
// load, and the legacy keys never reappear on re-save.
func TestCheckpoint_LegacyMigration_FoldsAllFourMaps(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "legacy.json")

	// A hand-crafted pre-#602 checkpoint. "gate1" genuinely passed; "gate2" is
	// recheck-pending AND took its one-shot fallback; "gate3" is overridden.
	legacy := `{
		"run_id": "legacy-602",
		"current_node": "done",
		"completed_nodes": ["start", "gate1"],
		"node_outcomes": {"start": "success", "gate1": "success", "gate2": "fail", "gate3": "fail"},
		"fallback_taken": {"gate2": true},
		"gate_recheck_pending": {"gate2": true},
		"overridden_gates": {"gate3": true}
	}`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatalf("write legacy checkpoint: %v", err)
	}

	cp, err := LoadCheckpoint(path)
	if err != nil {
		t.Fatalf("LoadCheckpoint: %v", err)
	}

	if cp.GateOutcome("gate1") != "success" {
		t.Errorf("gate1 LastOutcome = %q, want success (node_outcomes not migrated)", cp.GateOutcome("gate1"))
	}
	if !cp.IsGateRecheckPending("gate2") {
		t.Error("gate2 recheck-pending not migrated")
	}
	if !cp.IsFallbackTaken("gate2") {
		t.Error("gate2 fallback latch not migrated")
	}
	if !cp.IsGateOverridden("gate3") {
		t.Error("gate3 override not migrated")
	}

	// Re-save and confirm the legacy keys are gone (one-way migration).
	out := filepath.Join(dir, "resaved.json")
	if err := SaveCheckpoint(cp, out); err != nil {
		t.Fatalf("SaveCheckpoint: %v", err)
	}
	raw, _ := os.ReadFile(out)
	var topLevel map[string]json.RawMessage
	if err := json.Unmarshal(raw, &topLevel); err != nil {
		t.Fatalf("unmarshal re-saved checkpoint: %v", err)
	}
	// The legacy maps were top-level keys; "fallback_taken" also names a
	// GateState sub-field, so check the top level specifically rather than a
	// substring of the whole document.
	for _, key := range []string{"node_outcomes", "fallback_taken", "gate_recheck_pending", "overridden_gates"} {
		if _, present := topLevel[key]; present {
			t.Errorf("re-saved checkpoint still contains top-level legacy key %q: %s", key, raw)
		}
	}
	if _, present := topLevel["gate_states"]; !present {
		t.Errorf("re-saved checkpoint missing gate_states: %s", raw)
	}
}

// TestCheckpoint_LegacyMigration_OverrideWinsOverPending pins the precedence a
// legacy checkpoint must resolve to: a gate carrying BOTH gate_recheck_pending
// and overridden_gates folds to Overridden (override wins), matching the runtime
// rule in checkGoalGateNode.
func TestCheckpoint_LegacyMigration_OverrideWinsOverPending(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "legacy.json")
	legacy := `{
		"run_id": "legacy-both",
		"gate_recheck_pending": {"gate": true},
		"overridden_gates": {"gate": true}
	}`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	cp, err := LoadCheckpoint(path)
	if err != nil {
		t.Fatalf("LoadCheckpoint: %v", err)
	}
	if !cp.IsGateOverridden("gate") {
		t.Error("legacy pending+overridden gate did not fold to overridden")
	}
	if cp.IsGateRecheckPending("gate") {
		t.Error("legacy pending+overridden gate stayed pending — override must win")
	}
}

// TestGoalGateOverride_LegacyCheckpointResume is the end-to-end migration proof:
// a run resumed from a raw pre-#602 checkpoint (overridden_gates wire field, no
// gate_states) must not re-execute the resolved gate and must complete
// validation_overridden — identical routing to a new-format resume.
func TestGoalGateOverride_LegacyCheckpointResume(t *testing.T) {
	var attempts int
	var mu sync.Mutex
	g := overrideGateGraph(true)
	reg := failingGoalGateRegistry(t, &attempts, &mu, ActorHuman)

	dir := t.TempDir()
	cpPath := filepath.Join(dir, "cp.json")
	legacy := `{
		"run_id": "legacy-override",
		"current_node": "done",
		"completed_nodes": ["start", "gate", "escalate", "cleanup"],
		"overridden_gates": {"gate": true},
		"validation_overrides": [{"gate_node_id": "escalate", "label": "accept", "actor": "human", "covered_gates": ["gate"]}]
	}`
	if err := os.WriteFile(cpPath, []byte(legacy), 0o600); err != nil {
		t.Fatalf("write legacy checkpoint: %v", err)
	}

	result, err := NewEngine(g, reg, WithCheckpointPath(cpPath)).Run(context.Background())
	if err != nil {
		t.Fatalf("resume run: %v", err)
	}
	if attempts != 0 {
		t.Errorf("gate executed %d times on legacy resume, want 0 (already resolved)", attempts)
	}
	if result.Status != OutcomeValidationOverridden {
		t.Errorf("legacy resume Status = %q, want validation_overridden", result.Status)
	}
}

// TestGoalGatePassed_LegacyCheckpointResume is the #533 property proved from the
// legacy wire: a gate that genuinely passed (recorded only in the old
// node_outcomes map) stays satisfied on resume — no re-run, no escalation.
func TestGoalGatePassed_LegacyCheckpointResume(t *testing.T) {
	g := recheckTestGraph("")

	dir := t.TempDir()
	cpPath := filepath.Join(dir, "cp.json")
	legacy := `{
		"run_id": "legacy-533",
		"current_node": "done",
		"completed_nodes": ["start", "gate"],
		"context": {"outcome": "success"},
		"edge_selections": {"start": "gate", "gate": "done"},
		"node_outcomes": {"start": "success", "gate": "success"}
	}`
	if err := os.WriteFile(cpPath, []byte(legacy), 0o600); err != nil {
		t.Fatalf("write legacy checkpoint: %v", err)
	}

	reg := newTestRegistry()
	gateAttempts, escalateAttempts := 0, 0
	reg.Register(&testHandler{
		name: "codergen",
		executeFn: func(ctx context.Context, node *Node, pctx *PipelineContext) (Outcome, error) {
			switch node.ID {
			case "gate":
				gateAttempts++
			case "escalate":
				escalateAttempts++
			}
			return Outcome{Status: OutcomeSuccess}, nil
		},
	})

	result, err := NewEngine(g, reg, WithCheckpointPath(cpPath)).Run(context.Background())
	if err != nil {
		t.Fatalf("resume run: %v", err)
	}
	if result.Status != OutcomeSuccess {
		t.Errorf("status = %q, want success (a passed gate re-judged unsatisfied from legacy wire)", result.Status)
	}
	if gateAttempts != 0 {
		t.Errorf("gate re-executed %d time(s) on legacy resume; a passed gate must not re-run", gateAttempts)
	}
	if escalateAttempts != 0 {
		t.Errorf("escalation tail entered (%d) on legacy resume for an already-passed gate", escalateAttempts)
	}
}
