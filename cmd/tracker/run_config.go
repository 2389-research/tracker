// ABOUTME: Helpers that populate tracker.Config from the CLI's runOptions.
// ABOUTME: Interviewer selection, webhook-gate mapping, and herdr pane reporting.
package main

import (
	tracker "github.com/2389-research/tracker"
	"github.com/2389-research/tracker/herdr"
	"github.com/2389-research/tracker/pipeline"
	"github.com/2389-research/tracker/pipeline/handlers"
	"github.com/2389-research/tracker/tui"
)

// applyInterviewerToConfig translates the CLI's interviewer selection
// (auto-approve, webhook, autopilot persona, or interactive) into tracker.Config
// fields so the library owns the interviewer and its lifecycle/cleanup. Mirrors
// the priority in the former chooseInterviewer.
func applyInterviewerToConfig(cfg *tracker.Config, opts *runOptions, isTerminal bool) {
	switch {
	case opts.autopilot.autoApprove:
		cfg.AutoApprove = true
	case opts.webhookGate != nil:
		cfg.WebhookGate = toTrackerWebhookGate(opts.webhookGate)
	case opts.autopilot.persona != "":
		cfg.Autopilot = opts.autopilot.persona
	default:
		cfg.Interviewer = interactiveInterviewer(isTerminal)
	}
}

// interactiveInterviewer returns the human interviewer for an interactive plain
// run: an inline per-gate bubbletea modal on a TTY, else a stdin/stdout console.
func interactiveInterviewer(isTerminal bool) handlers.Interviewer {
	if isTerminal {
		return tui.NewMode1Interviewer()
	}
	return handlers.NewConsoleInterviewer()
}

// toTrackerWebhookGate maps the CLI webhook gate config to the library config.
func toTrackerWebhookGate(w *webhookGateCfg) *tracker.WebhookGateConfig {
	return &tracker.WebhookGateConfig{
		WebhookURL:    w.webhookURL,
		CallbackAddr:  w.gateCallbackAddr,
		Timeout:       w.gateTimeout,
		TimeoutAction: w.gateTimeoutAction,
		AuthHeader:    w.webhookAuthHeader,
	}
}

// humanAnswersGates reports whether a person answers this run's gates at the
// terminal. Auto-approve, webhook, and autopilot all resolve gates without
// anyone waiting, so a herdr pane should never show `blocked` for them. Mirrors
// the interviewer priority in applyInterviewerToConfig / chooseTUIInterviewer.
func humanAnswersGates(opts *runOptions) bool {
	return !opts.autopilot.autoApprove &&
		opts.webhookGate == nil &&
		opts.autopilot.persona == ""
}

// attachHerdr composes a herdr agent-state reporter into cfg.EventHandler when
// tracker runs inside a herdr terminal pane. It returns a release func to defer
// at run teardown; outside a herdr pane the reporter is nil and release is a
// no-op. The nil check is on the concrete *herdr.Reporter, before it can be
// wrapped in a non-nil interface value (the typed-nil trap PipelineMultiHandler
// cannot see through).
func attachHerdr(cfg *tracker.Config, humanGates bool) (release func()) {
	reporter := herdr.Detect(humanGates)
	if reporter == nil {
		return func() {}
	}
	if cfg.EventHandler != nil {
		cfg.EventHandler = pipeline.PipelineMultiHandler(cfg.EventHandler, reporter)
	} else {
		cfg.EventHandler = reporter
	}
	return reporter.Release
}
