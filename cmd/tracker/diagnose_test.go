// ABOUTME: Tests for the diagnose subcommand render path — verifies stdout sections appear in expected order.
// ABOUTME: Override section is informational (spec §9.4) and appears between BudgetHalt and Failures.
package main

import (
	"io"
	"os"
	"strings"
	"testing"

	tracker "github.com/2389-research/tracker"
	"github.com/2389-research/tracker/pipeline"
)

// captureDiagnoseStdout runs fn while capturing os.Stdout and returns the bytes
// written. Mirrors the pattern used in audit_test.go but reads everything via
// io.ReadAll so reports larger than a fixed buffer don't get truncated.
func captureDiagnoseStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	defer func() { os.Stdout = old }()

	done := make(chan []byte, 1)
	go func() {
		buf, _ := io.ReadAll(r)
		done <- buf
	}()

	fn()
	w.Close()
	return string(<-done)
}

// TestPrintDiagnoseReport_RendersOverrideSection pins the "Validation Override"
// section's presence, fields, and placement between BudgetHalt and Failures.
// Per spec §9.4 this section is informational only — it must NOT short-circuit
// the early-return-on-clean-run path even when no failures are present.
func TestPrintDiagnoseReport_RendersOverrideSection(t *testing.T) {
	report := &tracker.DiagnoseReport{
		RunID:          "render-test",
		CompletedNodes: 5,
		ValidationOverrides: []pipeline.OverrideDetail{
			{
				GateNodeID:   "FinalSpecCheck",
				Label:        "mark spec done",
				Actor:        pipeline.ActorHuman,
				SubgraphPath: []string{"OuterBuild", "InnerVerify"},
			},
			{
				GateNodeID: "BudgetGate",
				Label:      "approve over-budget",
				Actor:      pipeline.ActorAutopilot,
			},
		},
		OverrideCount: 2,
	}

	out := captureDiagnoseStdout(t, func() { printDiagnoseReport(report) })

	if !strings.Contains(out, "Validation Override") {
		t.Fatalf("expected 'Validation Override' header, got:\n%s", out)
	}
	if !strings.Contains(out, "OuterBuild/InnerVerify/FinalSpecCheck") {
		t.Errorf("expected subgraph-joined gate path, got:\n%s", out)
	}
	if !strings.Contains(out, `"mark spec done"`) {
		t.Errorf("expected labeled override label in output, got:\n%s", out)
	}
	if !strings.Contains(out, "human") {
		t.Errorf("expected human actor in output, got:\n%s", out)
	}
	if !strings.Contains(out, "BudgetGate") {
		t.Errorf("expected second gate node ID, got:\n%s", out)
	}
	if !strings.Contains(out, "autopilot") {
		t.Errorf("expected autopilot actor in output, got:\n%s", out)
	}
	// Clean-run early-return must NOT fire when overrides are present.
	if strings.Contains(out, "No failures found") {
		t.Errorf("override-only report should not render clean-run early-return, got:\n%s", out)
	}
}

// TestPrintDiagnoseReport_OverrideSectionBetweenBudgetAndFailures verifies the
// section ordering: BudgetHalt → Validation Override → Failures.
func TestPrintDiagnoseReport_OverrideSectionBetweenBudgetAndFailures(t *testing.T) {
	report := &tracker.DiagnoseReport{
		RunID:          "render-order-test",
		CompletedNodes: 3,
		BudgetHalt: &tracker.BudgetHalt{
			TotalTokens: 100000,
			Message:     "max_total_tokens exceeded",
		},
		ValidationOverrides: []pipeline.OverrideDetail{
			{GateNodeID: "MidRun", Label: "continue", Actor: pipeline.ActorHuman},
		},
		OverrideCount: 1,
		Failures: []tracker.NodeFailure{
			{NodeID: "BrokenNode", Outcome: "fail"},
		},
	}

	out := captureDiagnoseStdout(t, func() { printDiagnoseReport(report) })

	idxBudget := strings.Index(out, "Budget halt detected")
	idxOverride := strings.Index(out, "Validation Override")
	idxFailures := strings.Index(out, "BrokenNode")

	if idxBudget < 0 || idxOverride < 0 || idxFailures < 0 {
		t.Fatalf("missing section in output (budget=%d override=%d failures=%d):\n%s",
			idxBudget, idxOverride, idxFailures, out)
	}
	if !(idxBudget < idxOverride && idxOverride < idxFailures) {
		t.Errorf("section ordering wrong: budget=%d override=%d failures=%d (want budget<override<failures)",
			idxBudget, idxOverride, idxFailures)
	}
}

// TestPrintDiagnoseReport_NoOverrideSectionWhenEmpty verifies the override
// header does NOT appear when ValidationOverrides is empty — keeping the
// clean-run report unchanged for non-override pipelines.
func TestPrintDiagnoseReport_NoOverrideSectionWhenEmpty(t *testing.T) {
	report := &tracker.DiagnoseReport{
		RunID:          "no-override-test",
		CompletedNodes: 4,
	}

	out := captureDiagnoseStdout(t, func() { printDiagnoseReport(report) })

	if strings.Contains(out, "Validation Override") {
		t.Errorf("override header should be absent when no overrides, got:\n%s", out)
	}
	if !strings.Contains(out, "No failures found") {
		t.Errorf("expected clean-run early-return, got:\n%s", out)
	}
}
