package main

import "testing"

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
