// ABOUTME: The end-of-run notice: a workflow's short next-steps text from <run dir>/notice.txt,
// ABOUTME: printed after the summary, bounded and stripped of control characters.
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/2389-research/tracker/pipeline"
)

func TestPrintRunNotice(t *testing.T) {
	dir := t.TempDir()
	if got := captureStdout(t, func() { printRunNotice(dir) }); got != "" {
		t.Errorf("no notice.txt must print nothing, got %q", got)
	}
	body := "answer saved to x\nnext: make a small change with\n  tracker quickfix --param change=\"<one sentence>\"\n\x1b[31mred\x1b[0m\x07\n"
	if err := os.WriteFile(filepath.Join(dir, "notice.txt"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	got := captureStdout(t, func() { printRunNotice(dir) })
	for _, want := range []string{"answer saved to x", "  tracker quickfix --param change=\"<one sentence>\"", "red"} {
		if !strings.Contains(got, want) {
			t.Errorf("notice missing %q:\n%s", want, got)
		}
	}
	if strings.ContainsAny(got, "\x1b\x07") {
		t.Errorf("control characters must be stripped: %q", got)
	}
	big := strings.Repeat("x", 10000)
	if err := os.WriteFile(filepath.Join(dir, "notice.txt"), []byte(big), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := captureStdout(t, func() { printRunNotice(dir) }); len(got) > 4500 {
		t.Errorf("notice must be bounded, printed %d bytes", len(got))
	}
}

func TestSummaryClaudeCodeCostLabel(t *testing.T) {
	usage := &pipeline.UsageSummary{
		TotalInputTokens: 100, TotalOutputTokens: 50, TotalTokens: 150, TotalCostUSD: 0.04,
		ProviderTotals: map[string]pipeline.ProviderUsage{"claude-code": {InputTokens: 100, OutputTokens: 50, TotalTokens: 150}},
	}
	got := captureStdout(t, func() { printTotalTokens(usage) })
	if !strings.Contains(got, "~$0.04 usage (included in your Claude plan)") {
		t.Errorf("a claude-code-only run's cost must say it is plan usage:\n%s", got)
	}
	usage.ProviderTotals = map[string]pipeline.ProviderUsage{"anthropic": {}}
	usage.Estimated = true
	if got := captureStdout(t, func() { printTotalTokens(usage) }); strings.Contains(got, "Claude plan") {
		t.Errorf("an estimated metered run must not claim plan usage:\n%s", got)
	}
}
