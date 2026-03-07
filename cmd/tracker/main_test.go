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
