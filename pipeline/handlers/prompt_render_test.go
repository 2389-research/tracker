// ABOUTME: Tests for PromptPlain word-wrapping (relocated from tui/render, #477).
package handlers

import "testing"

func TestPromptPlain(t *testing.T) {
	if got := PromptPlain("   ", 40); got != "" {
		t.Fatalf("blank prompt should render empty, got %q", got)
	}

	// Wraps at the width boundary.
	got := PromptPlain("one two three four", 7)
	want := "one two\nthree\nfour"
	if got != want {
		t.Fatalf("wrap mismatch:\n got %q\nwant %q", got, want)
	}

	// Preserves blank lines between paragraphs.
	got = PromptPlain("a\n\nb", 40)
	if want := "a\n\nb"; got != want {
		t.Fatalf("paragraph mismatch:\n got %q\nwant %q", got, want)
	}

	// Non-positive width falls back to a sane default (no panic, single line fits).
	if got := PromptPlain("short", 0); got != "short" {
		t.Fatalf("default width mismatch, got %q", got)
	}
}
