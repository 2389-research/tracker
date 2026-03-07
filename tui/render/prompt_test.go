// ABOUTME: Tests for prompt rendering with markdown and word wrapping.
// ABOUTME: Validates that Prompt produces wrapped, styled terminal output.
package render

import (
	"strings"
	"testing"
)

func TestPromptWrapsLongLines(t *testing.T) {
	long := strings.Repeat("word ", 40) // ~200 chars
	rendered := Prompt(long, 60)

	for _, line := range strings.Split(rendered, "\n") {
		plain := stripANSI(line)
		if len(plain) > 65 { // small margin for glamour padding
			t.Errorf("line too long (%d chars): %q", len(plain), plain)
		}
	}
}

func TestPromptPreservesMarkdownBold(t *testing.T) {
	input := "This has **bold text** in it."
	rendered := Prompt(input, 80)
	if !strings.Contains(rendered, "bold text") {
		t.Errorf("expected 'bold text' in rendered output, got:\n%s", rendered)
	}
}

func TestPromptHandlesCodeBlocks(t *testing.T) {
	input := "Here is code:\n```go\nfmt.Println(\"hello\")\n```"
	rendered := Prompt(input, 80)
	if !strings.Contains(rendered, "hello") {
		t.Errorf("expected code block content in rendered output, got:\n%s", rendered)
	}
}

func TestPromptHandlesEmptyString(t *testing.T) {
	rendered := Prompt("", 80)
	trimmed := strings.TrimSpace(rendered)
	if trimmed != "" {
		t.Errorf("expected empty output for empty input, got %q", trimmed)
	}
}

func TestPromptHandlesPlainText(t *testing.T) {
	input := "Just a simple question."
	rendered := Prompt(input, 80)
	if !strings.Contains(rendered, "Just a simple question") {
		t.Errorf("expected plain text in output, got:\n%s", rendered)
	}
}

// stripANSI removes ANSI escape sequences for length measurement.
func stripANSI(s string) string {
	var out strings.Builder
	inEscape := false
	for _, r := range s {
		if r == '\033' {
			inEscape = true
			continue
		}
		if inEscape {
			if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
				inEscape = false
			}
			continue
		}
		out.WriteRune(r)
	}
	return out.String()
}
