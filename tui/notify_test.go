// ABOUTME: Tests for the desktop notification utility.
// ABOUTME: Verifies TRACKER_NO_NOTIFY suppression and osascript escaping.
package tui

import (
	"os"
	"testing"
)

func TestNotificationSuppressed(t *testing.T) {
	os.Setenv("TRACKER_NO_NOTIFY", "1")
	defer os.Unsetenv("TRACKER_NO_NOTIFY")
	// Should not panic or send anything.
	SendNotification("Test", "Body")
}

func TestEscapeOsascript(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"hello", "hello"},
		{`say "hi"`, `say \"hi\"`},
		{`back\slash`, `back\\slash`},
		{"", ""},
	}
	for _, tt := range tests {
		got := escapeOsascript(tt.input)
		if got != tt.want {
			t.Errorf("escapeOsascript(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
