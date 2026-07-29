// ABOUTME: Public seam for injecting the library's diagnostic sink (#449).
// ABOUTME: Embedders redirect/suppress/level-filter tracker's internal diagnostics here.
package tracker

import (
	"log/slog"

	"github.com/2389-research/tracker/internal/diag"
)

// SetDiagnosticLogger installs the sink for tracker's library-internal
// diagnostics — LLM adapter warnings, pricing warnings, backend subprocess
// notes, condition-variable warnings, autopilot fallbacks. These are NOT
// pipeline-lifecycle events (those ride Config.EventHandler); they are the
// call sites that previously wrote to the process-global logger/stderr.
//
// By default the library discards them (a no-op sink) so an embedded control
// plane's structured logs stay clean. Pass a *slog.Logger to capture,
// redirect, or level-filter them; pass nil to reset to the no-op sink.
//
// The sink is process-wide, not per-engine: several diagnostics originate in
// free functions (e.g. llm.EstimateCost) that cannot carry a per-run logger,
// so the library funnels all of them through one injectable sink. Genuine
// per-run, lifecycle observability belongs on Config.EventHandler /
// Config.AgentEvents instead.
func SetDiagnosticLogger(logger *slog.Logger) {
	diag.SetLogger(logger)
}
