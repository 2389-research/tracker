// ABOUTME: Verifies the public diagnostic-sink seam routes library diagnostics off the global logger (#449).
package tracker

import (
	"bytes"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/2389-research/tracker/llm"
)

// diagModelSeq gives each EstimateCost probe a process-unique model name so the
// llm package's global unknownModelWarned dedupe never suppresses a warning on
// a repeated run (go test -count=N). package tracker cannot reach the unexported
// sync.Map directly, so uniqueness is the portable fix.
var diagModelSeq atomic.Uint64

func uniqueUnknownModel(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, diagModelSeq.Add(1))
}

// TestSetDiagnosticLogger asserts that a diagnostic which previously went to
// the process-global logger (llm.EstimateCost's unknown-model warning) reaches
// an slog.Logger injected through the public tracker.SetDiagnosticLogger seam,
// and that resetting to nil restores the silent default.
func TestSetDiagnosticLogger(t *testing.T) {
	var buf bytes.Buffer
	SetDiagnosticLogger(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { SetDiagnosticLogger(nil) })

	model := uniqueUnknownModel("unknown-model-for-tracker-diag-test")
	llm.EstimateCost(model, llm.Usage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15})

	got := buf.String()
	if !strings.Contains(got, model) {
		t.Fatalf("injected sink did not receive the diagnostic; got %q", got)
	}

	// Resetting to the no-op sink must silence subsequent diagnostics.
	SetDiagnosticLogger(nil)
	buf.Reset()
	llm.EstimateCost(uniqueUnknownModel("another-unknown-model-diag-test"), llm.Usage{InputTokens: 10, TotalTokens: 10})
	if buf.Len() != 0 {
		t.Fatalf("no-op sink still wrote to the previous buffer: %q", buf.String())
	}
}
