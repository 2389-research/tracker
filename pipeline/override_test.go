// ABOUTME: Tests for OverrideDetail JSON round-trip and Actor constants.
package pipeline

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestActor_StringCompat(t *testing.T) {
	cases := []struct {
		actor Actor
		want  string
	}{
		{ActorHuman, "human"},
		{ActorAutopilot, "autopilot"},
		{ActorWebhook, "webhook"},
		{ActorUnknown, "unknown"},
	}
	for _, tc := range cases {
		if string(tc.actor) != tc.want {
			t.Errorf("Actor %q != %q", tc.actor, tc.want)
		}
	}
}

func TestOverrideDetail_JSON(t *testing.T) {
	in := OverrideDetail{
		GateNodeID:   "EscalateReview",
		Label:        "accept",
		Actor:        ActorHuman,
		SubgraphPath: []string{"Outer", "Inner"},
		Timestamp:    time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC),
	}
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out OverrideDetail
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.GateNodeID != in.GateNodeID {
		t.Errorf("GateNodeID: got %q want %q", out.GateNodeID, in.GateNodeID)
	}
	if out.Label != in.Label {
		t.Errorf("Label: got %q want %q", out.Label, in.Label)
	}
	if out.Actor != in.Actor {
		t.Errorf("Actor: got %q want %q", out.Actor, in.Actor)
	}
	if len(out.SubgraphPath) != 2 || out.SubgraphPath[0] != "Outer" || out.SubgraphPath[1] != "Inner" {
		t.Errorf("SubgraphPath: got %v want [Outer Inner]", out.SubgraphPath)
	}
	if !out.Timestamp.Equal(in.Timestamp) {
		t.Errorf("Timestamp: got %v want %v", out.Timestamp, in.Timestamp)
	}
}

func TestOverrideDetail_OmitEmpty(t *testing.T) {
	in := OverrideDetail{
		GateNodeID: "Gate",
		Actor:      ActorAutopilot,
		Timestamp:  time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC),
	}
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(data)
	if strings.Contains(s, `"label"`) {
		t.Errorf("expected label omitted, got %s", s)
	}
	if strings.Contains(s, `"subgraph_path"`) {
		t.Errorf("expected subgraph_path omitted, got %s", s)
	}
}

func TestErrValidationOverridden_Is(t *testing.T) {
	if ErrValidationOverridden == nil {
		t.Fatal("ErrValidationOverridden is nil")
	}
	if ErrValidationOverridden.Error() == "" {
		t.Fatal("ErrValidationOverridden has empty message")
	}
	// The cobra exit-code-2 path in cmd/tracker depends on errors.Is matching
	// the sentinel through whatever wrapping interpretRunResult adds. Pin that
	// contract here so a refactor to errors.New (without %w support) would fail.
	wrapped := fmt.Errorf("interpretRunResult: %w", ErrValidationOverridden)
	if !errors.Is(wrapped, ErrValidationOverridden) {
		t.Fatal("errors.Is should match wrapped ErrValidationOverridden")
	}
}
