// ABOUTME: Tests that newRunOptions carries every per-invocation config field that
// ABOUTME: the former active* package globals used to smuggle from executeRun to run/runTUI.
package main

import (
	"testing"
	"time"

	"github.com/2389-research/tracker/pipeline"
)

// TestNewRunOptionsCarriesConfig asserts newRunOptions maps every config field
// that used to travel through the active* package globals. This is the seam the
// refactor replaces: configuration is threaded explicitly, not smuggled.
func TestNewRunOptionsCarriesConfig(t *testing.T) {
	cfg := runConfig{
		pipelineFile:     "pipeline.dip",
		workdir:          "/tmp/wd",
		format:           "dip",
		backend:          "claude-code",
		verbose:          true,
		jsonOut:          true,
		autopilot:        "hard",
		autoApprove:      false,
		maxTokens:        1000,
		maxCostCents:     250,
		maxWallTime:      5 * time.Minute,
		sleepAware:       true,
		failOnOverride:   true,
		params:           map[string]string{"foo": "bar"},
		gatewayURL:       "https://gw.example/v1/acc/slug",
		gatewayKind:      "bedrock",
		webhookURL:       "https://example/gate",
		gateCallbackAddr: ":8080",
		exportBundle:     "/tmp/out.bundle",
		artifactDir:      "/tmp/artifacts",
		bypassDenylist:   true,
		toolAllowlist:    []string{"make *"},
		toolDenylistAdd:  []string{"rm *"},
		maxOutputLimit:   131072,
		git:              "init",
		allowInit:        true,
	}
	resume := resumeInfo{CheckpointPath: "/tmp/wd/.tracker/runs/r1/checkpoint.json", RunID: "r1"}

	opts := newRunOptions(cfg, resume)

	if opts.pipelineFile != "pipeline.dip" || opts.workdir != "/tmp/wd" || opts.format != "dip" {
		t.Fatalf("invocation basics mismatch: %+v", opts)
	}
	if opts.backend != "claude-code" || !opts.verbose || !opts.jsonOut {
		t.Fatalf("backend/verbose/jsonOut mismatch: %+v", opts)
	}
	if opts.checkpoint != resume.CheckpointPath {
		t.Fatalf("checkpoint = %q, want %q", opts.checkpoint, resume.CheckpointPath)
	}
	if opts.autopilot.persona != "hard" || opts.autopilot.autoApprove {
		t.Fatalf("autopilot mismatch: %+v", opts.autopilot)
	}
	want := pipeline.BudgetLimits{MaxTotalTokens: 1000, MaxCostCents: 250, MaxWallTime: 5 * time.Minute, SleepAware: true}
	if opts.budget != want {
		t.Fatalf("budget = %+v, want %+v", opts.budget, want)
	}
	if !opts.failOnOverride {
		t.Fatal("failOnOverride not carried")
	}
	if opts.gatewayURL != cfg.gatewayURL || opts.gatewayKind != "bedrock" {
		t.Fatalf("gateway mismatch: %q/%q", opts.gatewayURL, opts.gatewayKind)
	}
	if opts.exportBundle != cfg.exportBundle || opts.artifactDir != cfg.artifactDir {
		t.Fatalf("bundle/artifact mismatch: %q/%q", opts.exportBundle, opts.artifactDir)
	}
	if opts.git.policy != "init" || !opts.git.allowInit {
		t.Fatalf("git mismatch: %+v", opts.git)
	}
	if opts.webhookGate == nil || opts.webhookGate.webhookURL != "https://example/gate" {
		t.Fatalf("webhookGate mismatch: %+v", opts.webhookGate)
	}
	if !opts.toolSafety.BypassDenylist || opts.toolSafety.MaxOutputLimit != 131072 {
		t.Fatalf("toolSafety mismatch: %+v", opts.toolSafety)
	}
	if len(opts.toolSafety.Allowlist) != 1 || opts.toolSafety.Allowlist[0] != "make *" {
		t.Fatalf("toolSafety allowlist mismatch: %+v", opts.toolSafety.Allowlist)
	}
	if len(opts.toolSafety.DenylistAdd) != 1 || opts.toolSafety.DenylistAdd[0] != "rm *" {
		t.Fatalf("toolSafety denylist mismatch: %+v", opts.toolSafety.DenylistAdd)
	}
	if opts.resume.RunID != "r1" {
		t.Fatalf("resume mismatch: %+v", opts.resume)
	}

	// params must be cloned, not aliased.
	opts.params["foo"] = "mutated"
	if cfg.params["foo"] != "bar" {
		t.Fatal("newRunOptions aliased cfg.params instead of cloning")
	}
}
