// ABOUTME: Unit tests for `tracker answer`: argument parsing (incl. "--" text),
// ABOUTME: the answers-file schema, and listing / showing waiting runs.
package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tracker "github.com/2389-research/tracker"
)

func TestParseAnswerFlags(t *testing.T) {
	parse := func(args ...string) (answerFlags, runConfig, error) {
		cfg, err := parseFlags(append([]string{"tracker", "answer"}, args...))
		return cfg.answer, cfg, err
	}
	a, cfg, err := parse("abc", "approve")
	if err != nil || cfg.mode != modeAnswer || a.runID != "abc" || a.label != "approve" {
		t.Fatalf("label: %+v %v", a, err)
	}
	a, _, err = parse("abc", "--", "-1 is fine")
	if err != nil || a.text != "-1 is fine" || a.label != "" {
		t.Fatalf("-- text: %+v %v", a, err)
	}
	a, _, err = parse("--text", "hi there", "abc")
	if err != nil || a.text != "hi there" || a.runID != "abc" {
		t.Fatalf("--text before id: %+v %v", a, err)
	}
	a, cfg, err = parse("-w", "/x", "--artifact-dir", "/y", "--default", "--node", "B1/G", "abc")
	if err != nil || !a.useDefault || a.node != "B1/G" || cfg.workdir != "/x" || cfg.artifactDir != "/y" {
		t.Fatalf("flags: %+v %+v %v", a, cfg, err)
	}
	if a, _, err = parse(); err != nil || a.runID != "" || a.hasAnswer() {
		t.Fatalf("bare: %+v %v", a, err)
	}
	if _, _, err = parse("abc", "a", "b"); err == nil {
		t.Error("three positionals accepted")
	}
	if _, _, err = parse("abc", "approve", "--", "text too"); err == nil {
		t.Error("a label and -- text both accepted")
	}
}

func TestAnswer_FlagsConflict(t *testing.T) {
	for _, a := range []answerFlags{
		{runID: "r", label: "approve", useDefault: true},
		{runID: "r", text: "x", cancel: true},
	} {
		in, err := answerInputFrom(a)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tracker.ValidateAnswer(pausedResult().PausedGates[0], in); err == nil || !strings.Contains(err.Error(), "exactly one") {
			t.Errorf("%+v: err = %v, want 'exactly one'", a, err)
		}
	}
}

func TestAnswer_AnswersFileSchema(t *testing.T) {
	dir := t.TempDir()
	write := func(body string) string {
		p := filepath.Join(dir, "a.json")
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	got, err := readAnswersFile(write(`{"answers": {"q1": "pg", "q2": "orders"}}`))
	if err != nil || got["q1"] != "pg" || got["q2"] != "orders" {
		t.Fatalf("valid file: %v %v", got, err)
	}
	for name, body := range map[string]string{
		"unknown key": `{"answers": {"q1": "pg"}, "extra": 1}`,
		"no answers":  `{}`,
		"not json":    `answers: q1`,
		"wrong type":  `{"answers": ["pg"]}`,
	} {
		if _, err := readAnswersFile(write(body)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

const listDip = `workflow listtest
  start: Start
  exit: Done

  agent Start
    label: Start

  human Gate
    label: "Ship it?"
    default: reject

  tool Ship
    command: "true"

  tool Drop
    command: "true"

  agent Done
    label: Done

  edges
    Start -> Gate
    Gate -> Ship  label: "approve"
    Gate -> Drop  label: "reject"
    Ship -> Done
    Drop -> Done
`

func TestAnswer_ListAndShow(t *testing.T) {
	t.Setenv("TRACKER_AUDIT_DIR", t.TempDir())
	work := t.TempDir()
	runs := filepath.Join(work, ".tracker", "runs")
	res, _ := tracker.Run(context.Background(), listDip, tracker.Config{Format: "dip", WorkingDir: work, ArtifactDir: runs, GateMode: "pause"})
	if res == nil || res.Status != "paused_gate" {
		t.Fatalf("setup run = %+v", res)
	}

	var list bytes.Buffer
	if err := printWaitingRuns(&list, runs, ""); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(list.String(), res.RunID) || !strings.Contains(list.String(), "Gate") || !strings.Contains(list.String(), "Ship it?") {
		t.Errorf("list:\n%s", list.String())
	}

	_, gates, err := tracker.PendingGates(runs, res.RunID)
	if err != nil {
		t.Fatal(err)
	}
	var show bytes.Buffer
	printPendingGates(&show, res.RunID, gates, "-w /x")
	for _, want := range []string{"waiting at gate Gate", "options: approve · reject (default)", "tracker answer -w /x " + res.RunID + " reject"} {
		if !strings.Contains(show.String(), want) {
			t.Errorf("show lacks %q:\n%s", want, show.String())
		}
	}

	var empty bytes.Buffer
	if err := printWaitingRuns(&empty, t.TempDir(), ""); err != nil || !strings.Contains(empty.String(), "No runs are waiting") {
		t.Errorf("empty list = %q, %v", empty.String(), err)
	}
}
