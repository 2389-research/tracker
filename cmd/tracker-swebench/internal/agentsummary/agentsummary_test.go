// ABOUTME: Tests for the agent-runner summary helpers.
// ABOUTME: Covers the last-tool-calls window and the final-message rune cap.
package agentsummary

import (
	"strings"
	"testing"
)

func TestNormalizeLastToolCalls(t *testing.T) {
	got := NormalizeLastToolCalls(nil)
	if got == nil {
		t.Fatal("NormalizeLastToolCalls(nil) should return empty slice, not nil")
	}
	if len(got) != 0 {
		t.Fatalf("len(NormalizeLastToolCalls(nil)) = %d, want 0", len(got))
	}

	got = NormalizeLastToolCalls([]string{"a", "b", "c", "d"})
	if len(got) != 3 {
		t.Fatalf("len(NormalizeLastToolCalls(...)) = %d, want 3", len(got))
	}
	if got[0] != "b" || got[1] != "c" || got[2] != "d" {
		t.Fatalf("NormalizeLastToolCalls(...) = %#v, want [b c d]", got)
	}
}

func TestTruncateRunes(t *testing.T) {
	got := TruncateRunes(strings.Repeat("x", 450), MaxFinalMessageRunes)
	if len([]rune(got)) != MaxFinalMessageRunes {
		t.Fatalf("TruncateRunes length = %d, want %d", len([]rune(got)), MaxFinalMessageRunes)
	}
}
