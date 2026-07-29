// ABOUTME: Verifies llm diagnostics route to the injected diag sink, not the global logger (#449).
package llm

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/2389-research/tracker/internal/diag"
)

// TestEstimateCostDiagnosticRoutesToInjectedSink pins the #449 contract for the
// llm layer: the "unknown model" warning that previously went to the global
// logger must now reach an injected slog.Logger and nothing else.
func TestEstimateCostDiagnosticRoutesToInjectedSink(t *testing.T) {
	var buf bytes.Buffer
	diag.SetLogger(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { diag.SetLogger(nil) })

	// Clear any prior dedupe entry so repeated runs (go test -count=N) don't
	// hit the package-global unknownModelWarned LoadOrStore short-circuit.
	model := "totally-unknown-model-449"
	unknownModelWarned.Delete(model)
	t.Cleanup(func() { unknownModelWarned.Delete(model) })

	cost := EstimateCost(model, Usage{InputTokens: 100, OutputTokens: 50, TotalTokens: 150})
	if cost != 0 {
		t.Fatalf("unknown model should price at 0, got %v", cost)
	}

	got := buf.String()
	if !strings.Contains(got, "unknown model") || !strings.Contains(got, model) {
		t.Fatalf("injected sink did not receive the unknown-model diagnostic; got %q", got)
	}
}
