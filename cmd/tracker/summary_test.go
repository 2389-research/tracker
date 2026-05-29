// ABOUTME: Tests for CLI summary rendering — focuses on the Estimated-flag
// ABOUTME: surface introduced in #186 so mixed native+ACP runs render
// ABOUTME: clearly for operators. Also covers Gap 5.2 Task 23 — the
// ABOUTME: validation_overridden status header with amber color treatment.
package main

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/2389-research/tracker/pipeline"
)

// captureStdout runs fn with os.Stdout redirected to a pipe and returns
// the captured output. Used to assert print* helpers emit the right text.
//
// Cleanup is deferred so a panic/t.Fatal inside fn still restores stdout
// and closes both pipe ends — otherwise a test package panic could leave
// the process-wide os.Stdout pointing at a closed pipe, which breaks
// every later test in the package. t.Cleanup covers the belt-and-braces
// case where a helper like this is extended and a later code path forgets
// to reach the explicit close.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w
	t.Cleanup(func() { os.Stdout = orig })
	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		_ = r.Close()
		done <- buf.String()
	}()
	fn()
	_ = w.Close()
	return <-done
}

// TestPrintTotalTokens_EstimatedRendersAsTilde pins that when any
// contributing session was estimated, the inline cost readout in the
// Totals section uses the "~$X.XX usage" form the TUI/summary share for
// heuristic cost, rather than the plain "($X.XX)" form used for metered
// spend. This is the signal operators read to know that --max-cost
// enforcement was comparing against a heuristic.
func TestPrintTotalTokens_EstimatedRendersAsTilde(t *testing.T) {
	usage := &pipeline.UsageSummary{
		TotalInputTokens:  1000,
		TotalOutputTokens: 500,
		TotalTokens:       1500,
		TotalCostUSD:      0.37,
		Estimated:         true,
		ProviderTotals: map[string]pipeline.ProviderUsage{
			"anthropic": {InputTokens: 800, OutputTokens: 400, TotalTokens: 1200},
			"acp":       {InputTokens: 200, OutputTokens: 100, TotalTokens: 300, Estimated: true},
		},
	}
	got := captureStdout(t, func() { printTotalTokens(usage) })
	if !strings.Contains(got, "~$0.37 usage") {
		t.Errorf("output missing ~$0.37 usage marker (want: estimated cost renders as tilde):\n%s", got)
	}
}

// TestPrintTotalTokens_AllMetered_RendersPlainCost pins the negative: a
// run with no estimated sessions gets the plain "($X.XX)" form.
func TestPrintTotalTokens_AllMetered_RendersPlainCost(t *testing.T) {
	usage := &pipeline.UsageSummary{
		TotalInputTokens:  1000,
		TotalOutputTokens: 500,
		TotalTokens:       1500,
		TotalCostUSD:      0.37,
		ProviderTotals: map[string]pipeline.ProviderUsage{
			"anthropic": {InputTokens: 800, OutputTokens: 400, TotalTokens: 1200},
			"openai":    {InputTokens: 200, OutputTokens: 100, TotalTokens: 300},
		},
	}
	got := captureStdout(t, func() { printTotalTokens(usage) })
	if strings.Contains(got, "~") {
		t.Errorf("output contains tilde on an all-metered run (want plain cost):\n%s", got)
	}
	if !strings.Contains(got, "($0.37)") {
		t.Errorf("output missing plain ($0.37) readout:\n%s", got)
	}
}

// TestPrintTotalTokens_ClaudeCodeOnly_RendersAsTilde pins that the
// pre-existing Max-subscription caveat still fires — the Estimated flag
// and the claude-code-only path both produce the ~$X marker; the test
// proves #186 didn't regress the former.
func TestPrintTotalTokens_ClaudeCodeOnly_RendersAsTilde(t *testing.T) {
	usage := &pipeline.UsageSummary{
		TotalInputTokens:  1000,
		TotalOutputTokens: 500,
		TotalTokens:       1500,
		TotalCostUSD:      0.37,
		ProviderTotals: map[string]pipeline.ProviderUsage{
			"claude-code": {InputTokens: 1000, OutputTokens: 500, TotalTokens: 1500},
		},
	}
	got := captureStdout(t, func() { printTotalTokens(usage) })
	if !strings.Contains(got, "~$0.37 usage") {
		t.Errorf("claude-code-only run did not render as tilde:\n%s", got)
	}
}

// --- Task 23: printRunHeader status switch — validation_overridden case ----------------

// TestPrintRunHeader_OverrideStatus_RendersGateAndLabel pins the format
// the summary header uses for the new validation_overridden terminal
// status: `● validation_overridden — at <gate> (label "<label>")` using
// the LATEST entry per spec D5a (the override that drove the run to its
// terminal exit). The amber color treatment (#D97706) is applied via
// overrideStyle; we don't assert ANSI bytes here, just the textual shape.
func TestPrintRunHeader_OverrideStatus_RendersGateAndLabel(t *testing.T) {
	res := &pipeline.EngineResult{
		RunID:  "abcd1234",
		Status: pipeline.OutcomeValidationOverridden,
		ValidationOverrides: []pipeline.OverrideDetail{
			{GateNodeID: "EscalateReview", Label: "accept", Actor: pipeline.ActorHuman},
		},
	}
	got := captureStdout(t, func() { printRunHeader(res) })
	if !strings.Contains(got, "validation_overridden") {
		t.Errorf("output missing validation_overridden status text:\n%s", got)
	}
	if !strings.Contains(got, "EscalateReview") {
		t.Errorf("output missing gate node ID:\n%s", got)
	}
	if !strings.Contains(got, `(label "accept")`) {
		t.Errorf("output missing quoted label:\n%s", got)
	}
	if !strings.Contains(got, "— at EscalateReview") {
		t.Errorf("output missing `— at <gate>` separator:\n%s", got)
	}
}

// TestPrintRunHeader_OverrideStatus_PicksLatestEntry pins D5a: when
// multiple overrides happened during the run, the headline is the most
// recent entry (which is the one that drove the terminal exit).
func TestPrintRunHeader_OverrideStatus_PicksLatestEntry(t *testing.T) {
	res := &pipeline.EngineResult{
		RunID:  "abcd1234",
		Status: pipeline.OutcomeValidationOverridden,
		ValidationOverrides: []pipeline.OverrideDetail{
			{GateNodeID: "EarlyGate", Label: "skip", Actor: pipeline.ActorAutopilot},
			{GateNodeID: "MiddleGate", Label: "next", Actor: pipeline.ActorHuman},
			{GateNodeID: "EscalateReview", Label: "accept", Actor: pipeline.ActorHuman},
		},
	}
	got := captureStdout(t, func() { printRunHeader(res) })
	if !strings.Contains(got, "EscalateReview") {
		t.Errorf("output missing latest gate (EscalateReview):\n%s", got)
	}
	if !strings.Contains(got, `(label "accept")`) {
		t.Errorf("output missing latest label (\"accept\"):\n%s", got)
	}
	if strings.Contains(got, "EarlyGate") || strings.Contains(got, "MiddleGate") {
		t.Errorf("output included a non-headline gate (should only show latest):\n%s", got)
	}
}

// TestPrintRunHeader_OverrideStatus_NoOverridesStillRenders pins the
// defensive case: if Status is validation_overridden but ValidationOverrides
// is empty (shouldn't happen in practice, but the code mustn't crash),
// the bare status text is rendered without the headline suffix.
func TestPrintRunHeader_OverrideStatus_NoOverridesStillRenders(t *testing.T) {
	res := &pipeline.EngineResult{
		RunID:  "abcd1234",
		Status: pipeline.OutcomeValidationOverridden,
	}
	got := captureStdout(t, func() { printRunHeader(res) })
	if !strings.Contains(got, "validation_overridden") {
		t.Errorf("output missing validation_overridden status text:\n%s", got)
	}
	if strings.Contains(got, "— at") {
		t.Errorf("output included headline suffix on empty overrides:\n%s", got)
	}
}

// TestPrintRunHeader_SuccessUnchanged pins that adding the override case
// did not regress the existing success path.
func TestPrintRunHeader_SuccessUnchanged(t *testing.T) {
	res := &pipeline.EngineResult{
		RunID:  "abcd1234",
		Status: pipeline.OutcomeSuccess,
	}
	got := captureStdout(t, func() { printRunHeader(res) })
	if !strings.Contains(got, "success") {
		t.Errorf("output missing success status text:\n%s", got)
	}
	if strings.Contains(got, "— at") {
		t.Errorf("success status should not include override headline suffix:\n%s", got)
	}
}

// TestPrintRunHeader_FailUnchanged pins that adding the override case
// did not regress the existing fail path.
func TestPrintRunHeader_FailUnchanged(t *testing.T) {
	res := &pipeline.EngineResult{
		RunID:  "abcd1234",
		Status: pipeline.OutcomeFail,
	}
	got := captureStdout(t, func() { printRunHeader(res) })
	if !strings.Contains(got, "fail") {
		t.Errorf("output missing fail status text:\n%s", got)
	}
	if strings.Contains(got, "— at") {
		t.Errorf("fail status should not include override headline suffix:\n%s", got)
	}
}
