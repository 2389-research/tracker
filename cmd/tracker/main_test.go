package main

import (
	"testing"

	"github.com/2389-research/tracker/pipeline/handlers"
	"github.com/2389-research/tracker/tui"
)

func TestChooseInterviewerReturnsBubbleteaWhenTerminal(t *testing.T) {
	iv := chooseInterviewer(true)
	if _, ok := iv.(*tui.BubbleteaInterviewer); !ok {
		t.Errorf("expected *tui.BubbleteaInterviewer when terminal, got %T", iv)
	}
}

func TestChooseInterviewerReturnsConsoleWhenNotTerminal(t *testing.T) {
	iv := chooseInterviewer(false)
	if _, ok := iv.(*handlers.ConsoleInterviewer); !ok {
		t.Errorf("expected *handlers.ConsoleInterviewer when not terminal, got %T", iv)
	}
}

func TestParseFlagsEnablesVerbose(t *testing.T) {
	cfg, err := parseFlags([]string{"tracker", "--verbose", "pipe.dot"})
	if err != nil {
		t.Fatalf("parseFlags returned error: %v", err)
	}
	if !cfg.verbose {
		t.Fatal("expected verbose to be true")
	}
	if cfg.dotFile != "pipe.dot" {
		t.Fatalf("dotFile = %q, want %q", cfg.dotFile, "pipe.dot")
	}
}

func TestParseFlagsFlagsAfterDotFile(t *testing.T) {
	cfg, err := parseFlags([]string{"tracker", "pipeline.dot", "-c", "checkpoint.json"})
	if err != nil {
		t.Fatalf("parseFlags returned error: %v", err)
	}
	if cfg.dotFile != "pipeline.dot" {
		t.Fatalf("dotFile = %q, want %q", cfg.dotFile, "pipeline.dot")
	}
	if cfg.checkpoint != "checkpoint.json" {
		t.Fatalf("checkpoint = %q, want %q", cfg.checkpoint, "checkpoint.json")
	}
}

func TestParseFlagsFlagsBeforeDotFile(t *testing.T) {
	cfg, err := parseFlags([]string{"tracker", "-c", "checkpoint.json", "pipeline.dot"})
	if err != nil {
		t.Fatalf("parseFlags returned error: %v", err)
	}
	if cfg.dotFile != "pipeline.dot" {
		t.Fatalf("dotFile = %q, want %q", cfg.dotFile, "pipeline.dot")
	}
	if cfg.checkpoint != "checkpoint.json" {
		t.Fatalf("checkpoint = %q, want %q", cfg.checkpoint, "checkpoint.json")
	}
}

func TestParseFlagsMixedOrder(t *testing.T) {
	cfg, err := parseFlags([]string{"tracker", "--no-tui", "pipeline.dot", "-c", "cp.json", "--verbose"})
	if err != nil {
		t.Fatalf("parseFlags returned error: %v", err)
	}
	if cfg.dotFile != "pipeline.dot" {
		t.Fatalf("dotFile = %q, want %q", cfg.dotFile, "pipeline.dot")
	}
	if cfg.checkpoint != "cp.json" {
		t.Fatalf("checkpoint = %q, want %q", cfg.checkpoint, "cp.json")
	}
	if !cfg.noTUI {
		t.Fatal("expected noTUI to be true")
	}
	if !cfg.verbose {
		t.Fatal("expected verbose to be true")
	}
}

func TestParseFlagsDefaultIsTUI(t *testing.T) {
	cfg, err := parseFlags([]string{"tracker", "pipeline.dot"})
	if err != nil {
		t.Fatalf("parseFlags returned error: %v", err)
	}
	if cfg.noTUI {
		t.Fatal("expected noTUI to be false by default (TUI is the default)")
	}
}
