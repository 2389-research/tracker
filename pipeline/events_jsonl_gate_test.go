// ABOUTME: Tests that gate lifecycle events survive the activity.jsonl round trip (#509).
// ABOUTME: Pins gate_id/node_id correlation and payload fields on the on-disk log format.
package pipeline

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestJSONL_GateOpenedRoundTrip(t *testing.T) {
	ev := PipelineEvent{
		Type:      EventGateOpened,
		Timestamp: time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC),
		RunID:     "test-run",
		NodeID:    "ApprovePlan",
		Gate: &GateDetail{
			GateID:  "3f1c2b4a-0000-4000-8000-000000000001",
			Mode:    "choice",
			Label:   "Approve the plan?",
			Prompt:  "Approve the plan?\n\nHere is the plan…",
			Choices: []string{"approve", "revise"},
		},
	}
	entry := buildLogEntry(ev)

	if entry.NodeID != "ApprovePlan" {
		t.Errorf("NodeID = %q, want ApprovePlan", entry.NodeID)
	}
	if entry.GateID != ev.Gate.GateID {
		t.Errorf("GateID = %q, want %q", entry.GateID, ev.Gate.GateID)
	}
	if entry.GateMode != "choice" {
		t.Errorf("GateMode = %q, want choice", entry.GateMode)
	}
	if entry.GateLabel != "Approve the plan?" {
		t.Errorf("GateLabel = %q, want the node label", entry.GateLabel)
	}
	if !strings.Contains(entry.GatePrompt, "Here is the plan") {
		t.Errorf("GatePrompt = %q, want the resolved prompt body", entry.GatePrompt)
	}
	if len(entry.GateChoices) != 2 {
		t.Fatalf("GateChoices = %v, want 2 entries", entry.GateChoices)
	}

	// The copy must not alias the source slice.
	ev.Gate.Choices[0] = "mutated"
	if entry.GateChoices[0] != "approve" {
		t.Errorf("GateChoices aliased the source slice: %v", entry.GateChoices)
	}

	data, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded jsonlLogEntry
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.GateID != entry.GateID {
		t.Errorf("round-trip GateID: got %q want %q", decoded.GateID, entry.GateID)
	}
	if len(decoded.GateChoices) != 2 || decoded.GateChoices[1] != "revise" {
		t.Errorf("round-trip GateChoices = %v, want [approve revise]", decoded.GateChoices)
	}
}

func TestJSONL_GateResolvedRoundTrip(t *testing.T) {
	entry := buildLogEntry(PipelineEvent{
		Type:      EventGateResolved,
		Timestamp: time.Date(2026, 7, 28, 12, 0, 5, 0, time.UTC),
		RunID:     "test-run",
		NodeID:    "ApprovePlan",
		Gate: &GateDetail{
			GateID:   "3f1c2b4a-0000-4000-8000-000000000001",
			Mode:     "choice",
			Response: "approve",
			Outcome:  string(OutcomeSuccess),
			Actor:    ActorHuman,
		},
	})

	if entry.GateID != "3f1c2b4a-0000-4000-8000-000000000001" {
		t.Errorf("GateID = %q, want the opened gate's ID", entry.GateID)
	}
	if entry.GateResponse != "approve" {
		t.Errorf("GateResponse = %q, want approve", entry.GateResponse)
	}
	if entry.GateOutcome != string(OutcomeSuccess) {
		t.Errorf("GateOutcome = %q, want success", entry.GateOutcome)
	}
	if entry.GateActor != ActorHuman {
		t.Errorf("GateActor = %q, want %q", entry.GateActor, ActorHuman)
	}
	if entry.GateTimedOut {
		t.Error("GateTimedOut = true, want false")
	}
}

// TestJSONL_GateErrorSurfacesInEntryError pins that a gate that failed to
// collect an answer reports why, rather than logging a silent empty resolution.
func TestJSONL_GateErrorSurfacesInEntryError(t *testing.T) {
	entry := buildLogEntry(PipelineEvent{
		Type:      EventGateResolved,
		Timestamp: time.Now(),
		NodeID:    "ApprovePlan",
		Gate: &GateDetail{
			GateID:  "gate-1",
			Outcome: string(OutcomeFail),
			Error:   "interviewer does not support freeform input",
		},
	})
	if !strings.Contains(entry.Error, "does not support freeform") {
		t.Errorf("Error = %q, want the gate failure reason", entry.Error)
	}
	if entry.GateTimedOut {
		t.Error("GateTimedOut = true, want false")
	}
}

// TestJSONL_NilGateLeavesFieldsEmpty pins that every other event type is
// unaffected by the new fields (they are omitempty on the wire).
func TestJSONL_NilGateLeavesFieldsEmpty(t *testing.T) {
	entry := buildLogEntry(PipelineEvent{
		Type:      EventStageStarted,
		Timestamp: time.Now(),
		NodeID:    "Build",
	})
	if entry.GateID != "" || entry.GateMode != "" || entry.GateChoices != nil {
		t.Errorf("gate fields populated on a non-gate event: %+v", entry)
	}
	data, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(data), "gate_") {
		t.Errorf("non-gate event serialized gate fields: %s", data)
	}
}
