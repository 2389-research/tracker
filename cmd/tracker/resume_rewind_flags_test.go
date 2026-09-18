// ABOUTME: CLI flag tests for the #651 resume flags: --from <node> and --resume-no-rewind ride on -r,
// ABOUTME: are threaded to tracker.Config.ResumeFrom/ResumeExact, and are refused on a fresh run.
package main

import (
	"strings"
	"testing"
)

func TestParseFlagsResumeFromAndNoRewind(t *testing.T) {
	cfg, err := parseFlags([]string{"tracker", "-r", "abc123", "--from", "Setup", "--resume-no-rewind", "pipeline.dip"})
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	if cfg.resumeID != "abc123" || cfg.resumeFrom != "Setup" || !cfg.resumeExact {
		t.Fatalf("cfg = resumeID=%q from=%q exact=%v", cfg.resumeID, cfg.resumeFrom, cfg.resumeExact)
	}
	opts := newRunOptions(cfg, resumeInfo{CheckpointPath: "/tmp/cp.json", RunID: "abc123"})
	if opts.resumeFrom != "Setup" || !opts.resumeExact || opts.checkpoint != "/tmp/cp.json" {
		t.Fatalf("runOptions did not carry the resume flags: %+v", opts)
	}
}

func TestParseFlagsResumeFlagsRequireResume(t *testing.T) {
	cases := [][]string{
		{"tracker", "--from", "Setup", "pipeline.dip"},
		{"tracker", "--resume-no-rewind", "pipeline.dip"},
	}
	for _, args := range cases {
		_, err := parseFlags(args)
		if err == nil || !strings.Contains(err.Error(), "requires -r") {
			t.Errorf("%v: err=%v, want a 'requires -r' refusal", args, err)
		}
	}
}
