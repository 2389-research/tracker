// ABOUTME: Tests for CLI summary rendering — focuses on the Estimated-flag
// ABOUTME: surface introduced in #186 so mixed native+ACP runs render
// ABOUTME: clearly for operators.
package main

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/2389-research/tracker/pipeline"
)

// captureStdout runs fn with os.Stdout redirected to a buffer and returns
// the captured output. Used to assert print* helpers emit the right text.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.String()
	}()
	fn()
	_ = w.Close()
	os.Stdout = orig
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
