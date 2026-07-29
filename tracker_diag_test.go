// ABOUTME: Verifies the public diagnostic-sink seam routes library diagnostics off the global logger (#449).
package tracker

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/2389-research/tracker/llm"
)

// TestSetDiagnosticLogger asserts that a diagnostic which previously went to
// the process-global logger (llm.EstimateCost's unknown-model warning) reaches
// an slog.Logger injected through the public tracker.SetDiagnosticLogger seam,
// and that resetting to nil restores the silent default.
func TestSetDiagnosticLogger(t *testing.T) {
	var buf bytes.Buffer
	SetDiagnosticLogger(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { SetDiagnosticLogger(nil) })

	model := "unknown-model-for-tracker-diag-test"
	llm.EstimateCost(model, llm.Usage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15})

	got := buf.String()
	if !strings.Contains(got, model) {
		t.Fatalf("injected sink did not receive the diagnostic; got %q", got)
	}

	// Resetting to the no-op sink must silence subsequent diagnostics.
	SetDiagnosticLogger(nil)
	buf.Reset()
	llm.EstimateCost("another-unknown-model-diag-test", llm.Usage{InputTokens: 10, TotalTokens: 10})
	if buf.Len() != 0 {
		t.Fatalf("no-op sink still wrote to the previous buffer: %q", buf.String())
	}
}
