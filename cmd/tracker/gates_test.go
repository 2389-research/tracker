// ABOUTME: Tests --gates and the automatic pause mode: which gate mode a run gets
// ABOUTME: from the flag, stdin and --json, the conflicts, and the held answer.
package main

import (
	"strings"
	"testing"

	tracker "github.com/2389-research/tracker"
	"github.com/2389-research/tracker/pipeline/handlers"
)

func TestGateModeFor(t *testing.T) {
	cases := []struct {
		gates     string
		tty, json bool
		want      string
	}{
		{gatesAuto, true, false, gateModeTUI},
		{"", true, false, gateModeTUI},
		{gatesAuto, false, false, gatesPause},
		{gatesAuto, true, true, gatesPause},
		{gatesConsole, false, false, gatesConsole},
		{gatesConsole, true, false, gateModeTUI},
		{gatesConsole, false, true, gatesConsole},
		{gatesPause, true, false, gatesPause},
	}
	for _, c := range cases {
		if got := gateModeFor(c.gates, c.tty, c.json); got != c.want {
			t.Errorf("gateModeFor(%q, tty=%v, json=%v) = %q, want %q", c.gates, c.tty, c.json, got, c.want)
		}
	}
}

func TestFlags_GatesConflicts(t *testing.T) {
	bad := map[string][]string{
		"pause + auto-approve": {"--gates", "pause", "--auto-approve"},
		"console + webhook":    {"--gates", "console", "--webhook-url", "http://x"},
		"pause + autopilot":    {"--gates", "pause", "--autopilot", "mid"},
		"unknown value":        {"--gates", "bogus"},
	}
	for name, extra := range bad {
		args := append([]string{"tracker"}, extra...)
		args = append(args, "x.dip")
		if _, err := parseFlags(args); err == nil {
			t.Errorf("%s: accepted", name)
		} else if name != "unknown value" && !strings.Contains(err.Error(), "--gates") {
			t.Errorf("%s: error %q does not name --gates", name, err)
		}
	}
	cfg, err := parseFlags([]string{"tracker", "--gates", "pause", "x.dip"})
	if err != nil || cfg.gates != gatesPause {
		t.Fatalf("--gates pause: cfg.gates=%q err=%v", cfg.gates, err)
	}
	cfg, err = parseFlags([]string{"tracker", "--gates", "auto", "--auto-approve", "x.dip"})
	if err != nil {
		t.Errorf("--gates auto with --auto-approve should be fine: %v", err)
	}
	_ = cfg
}

func TestApplyInterviewer_PauseAndPendingAnswer(t *testing.T) {
	var cfg tracker.Config
	applyInterviewerToConfig(&cfg, &runOptions{gates: gatesPause}, true)
	if _, ok := cfg.Interviewer.(*handlers.PausingInterviewer); !ok {
		t.Fatalf("--gates pause at a TTY = %T, want a pausing interviewer", cfg.Interviewer)
	}

	ans := &handlers.PendingAnswer{NodeID: "Gate", Label: "approve"}
	cfg = tracker.Config{}
	applyInterviewerToConfig(&cfg, &runOptions{pendingAnswer: ans}, true)
	p, ok := cfg.Interviewer.(*handlers.PausingInterviewer)
	if !ok {
		t.Fatalf("a held answer = %T, want a pausing interviewer", cfg.Interviewer)
	}
	_ = p
}

func TestSelectAndRunMode_PauseImpliesNoTUI(t *testing.T) {
	for _, cfg := range []runConfig{{gates: gatesPause}, {jsonOut: true}} {
		var ranTUI, ranPlain bool
		deps := commandDeps{
			run:    func(*runOptions) error { ranPlain = true; return nil },
			runTUI: func(*runOptions) error { ranTUI = true; return nil },
		}
		_ = selectAndRunModeWithTTY(cfg, deps, &runOptions{}, true)
		if ranTUI || !ranPlain {
			t.Errorf("cfg %+v at a TTY: tui=%v plain=%v, want plain", cfg, ranTUI, ranPlain)
		}
	}
}
