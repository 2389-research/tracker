// ABOUTME: Tests that the style registry exposes all required lamp characters and colors.
// ABOUTME: Ensures no empty strings for visual constants.
package tui

import "testing"

func TestLampCharacters(t *testing.T) {
	lamps := []struct{ name, char string }{
		{"Running", LampRunning}, {"Done", LampDone},
		{"Pending", LampPending}, {"Failed", LampFailed},
	}
	for _, l := range lamps {
		if l.char == "" {
			t.Errorf("lamp %s is empty", l.name)
		}
	}
}

func TestThinkingFrames(t *testing.T) {
	if len(ThinkingFrames) != 4 {
		t.Errorf("expected 4 thinking frames, got %d", len(ThinkingFrames))
	}
	for i, f := range ThinkingFrames {
		if f == "" {
			t.Errorf("thinking frame %d is empty", i)
		}
	}
}

func TestStatusLampRetrying(t *testing.T) {
	lamp, style := StatusLamp(NodeRetrying)
	if lamp != "↻" {
		t.Errorf("expected retry lamp '↻', got %q", lamp)
	}
	rendered := style.Render("x")
	if rendered == "" {
		t.Error("retrying lamp style renders empty")
	}
}

func TestStylesNotZero(t *testing.T) {
	s := Styles.NodeName.Render("test")
	if s == "" {
		t.Error("NodeName style renders empty")
	}
}
