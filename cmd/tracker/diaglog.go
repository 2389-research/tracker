// ABOUTME: Installs the CLI's diagnostic sink so library diagnostics still reach stderr (#449).
// ABOUTME: The library defaults to a no-op sink; the CLI restores the pre-#449 stderr behavior.
package main

import (
	"context"
	"log"
	"log/slog"

	"github.com/2389-research/tracker/internal/diag"
)

// installCLIDiagnostics routes tracker's library diagnostics (previously
// direct log.Printf calls) to stderr so the CLI's terminal output is
// unchanged by the #449 sink migration. Each record's fully-formatted message
// is handed to the standard log package, which — like the old call sites —
// writes to os.Stderr with the default date/time prefix. Embedders that never
// call this keep the library's no-op default (silence).
func installCLIDiagnostics() {
	diag.SetLogger(slog.New(stderrDiagHandler{}))
}

// stderrDiagHandler is a minimal slog.Handler that emits only the record's
// message via the standard log package, reproducing the pre-#449 log.Printf
// output byte-for-byte. Attributes/levels are not decorated because the
// migrated call sites carry their whole text in the message and no attrs; a
// TextHandler would change what a CLI user sees, which #449 forbids.
type stderrDiagHandler struct{}

func (stderrDiagHandler) Enabled(context.Context, slog.Level) bool { return true }

func (stderrDiagHandler) Handle(_ context.Context, r slog.Record) error {
	log.Print(r.Message)
	return nil
}

func (h stderrDiagHandler) WithAttrs([]slog.Attr) slog.Handler { return h }

func (h stderrDiagHandler) WithGroup(string) slog.Handler { return h }
