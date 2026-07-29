// ABOUTME: Injectable diagnostic sink for tracker's library packages (#449).
// ABOUTME: Defaults to a no-op so embedders get silence unless they inject a logger.
package diag

import (
	"fmt"
	"log/slog"
	"sync/atomic"
)

// noop is the default sink: an embedded caller that injects nothing gets
// silence rather than writes to the process-global logger/stderr. This is the
// core of #449 — library diagnostics must not pollute a host service's logs.
var noop = slog.New(slog.DiscardHandler)

// current holds the active sink. A nil value means "use noop"; it is never
// written as a non-nil that must be honored except via SetLogger.
var current atomic.Pointer[slog.Logger]

// Logger returns the active diagnostic sink. It never returns nil — when no
// logger has been injected it returns the no-op sink, so callers can log
// unconditionally without a nil check.
func Logger() *slog.Logger {
	if l := current.Load(); l != nil {
		return l
	}
	return noop
}

// SetLogger installs the process-wide diagnostic sink used by tracker's
// library packages (pipeline, handlers, llm, root). Passing nil resets to the
// no-op sink. This is process-wide state, not per-engine: threading a logger
// through every free function (llm.EstimateCost, condition evaluation) is not
// feasible, so the library funnels all diagnostics through one injectable
// sink. tracker.Config.Logger sets this during engine construction; the CLI
// sets it once at startup to preserve its terminal output.
func SetLogger(l *slog.Logger) {
	current.Store(l)
}

// Warnf logs a formatted warning to the active sink. The printf-style shape
// mirrors the log.Printf call sites it replaces so the migration is a
// mechanical per-site swap that preserves the exact message text.
func Warnf(format string, args ...any) {
	Logger().Warn(fmt.Sprintf(format, args...))
}

// Infof logs a formatted informational note to the active sink.
func Infof(format string, args ...any) {
	Logger().Info(fmt.Sprintf(format, args...))
}

// Errorf logs a formatted error-level diagnostic to the active sink. Note this
// does NOT swallow an error return — call sites that previously logged context
// alongside returning/handling an error keep doing so; only the sink changes.
func Errorf(format string, args ...any) {
	Logger().Error(fmt.Sprintf(format, args...))
}
