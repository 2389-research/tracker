// ABOUTME: --gates: how human gates are answered when no automation flag is set —
// ABOUTME: the TUI at a terminal, pause (tracker answer) without one, or stdin.
package main

import "fmt"

// --gates values, plus the resolved TUI mode.
const (
	gatesAuto    = "auto"    // TUI at a terminal; pause without one (or with --json)
	gatesConsole = "console" // today's interactive prompt: TUI at a terminal, stdin otherwise
	gatesPause   = "pause"   // every gate pauses the run; answer with `tracker answer`
	gateModeTUI  = "tui"
)

// gateModeFor resolves --gates against the terminal and --json: "tui" (the
// interactive TUI modal), gatesConsole (stdin) or gatesPause. A --json run
// pauses under auto because an agent harness may give it a PTY and still
// cannot answer an inline prompt.
func gateModeFor(gates string, stdinTTY, jsonOut bool) string {
	switch gates {
	case gatesPause:
		return gatesPause
	case gatesConsole:
		if stdinTTY && !jsonOut {
			return gateModeTUI
		}
		return gatesConsole
	default:
		if stdinTTY && !jsonOut {
			return gateModeTUI
		}
		return gatesPause
	}
}

// validateGatesFlag rejects an unknown --gates value and an explicit console /
// pause combined with a gate automation flag (they would silently lose).
func validateGatesFlag(cfg runConfig) error {
	switch cfg.gates {
	case "", gatesAuto:
		return nil
	case gatesConsole, gatesPause:
	default:
		return fmt.Errorf("--gates must be %q, %q or %q, got %q", gatesAuto, gatesConsole, gatesPause, cfg.gates)
	}
	for flag, set := range map[string]bool{
		"--auto-approve": cfg.autoApprove, "--webhook-url": cfg.webhookURL != "", "--autopilot": cfg.autopilot != "",
	} {
		if set {
			return fmt.Errorf("--gates %s and %s are mutually exclusive: choose one way to answer gates", cfg.gates, flag)
		}
	}
	return nil
}
