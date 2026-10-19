// ABOUTME: EventBackendResolved: the announcement text for each resolution and the
// ABOUTME: activity-log fields its payload maps to.
package pipeline

import "testing"

func TestBackendResolvedMessage(t *testing.T) {
	cases := []struct {
		name string
		d    BackendResolvedDetail
		want string
	}{
		{"claude-code", BackendResolvedDetail{Backend: "claude-code", ClaudePath: "/opt/homebrew/bin/claude", ClaudeVersion: "2.1.280"},
			"backend: auto → claude-code (claude 2.1.280 at /opt/homebrew/bin/claude; no API key needed)"},
		{"claude-code with a key too", BackendResolvedDetail{Backend: "claude-code", ClaudePath: "/usr/bin/claude", ClaudeVersion: "2.1.280", NativeAvailable: true},
			"backend: auto → claude-code (claude 2.1.280 at /usr/bin/claude; no API key needed) — set --backend native to use your API key instead"},
		{"native", BackendResolvedDetail{Backend: "native", Reason: "provider anthropic"},
			"backend: auto → native (provider anthropic)"},
		{"native after a slow claude", BackendResolvedDetail{Backend: "native", Reason: "provider anthropic; claude found at /usr/bin/claude but did not respond within 10s"},
			"backend: auto → native (provider anthropic; claude found at /usr/bin/claude but did not respond within 10s)"},
		{"recorded", BackendResolvedDetail{Backend: "claude-code", FromRecord: true},
			"backend: auto → claude-code (recorded in the checkpoint)"},
	}
	for _, c := range cases {
		if got := BackendResolvedMessage(c.d); got != c.want {
			t.Errorf("%s:\n got  %q\n want %q", c.name, got, c.want)
		}
	}
}

func TestBuildLogEntry_BackendResolved(t *testing.T) {
	d := &BackendResolvedDetail{Backend: "claude-code", Reason: "claude found", ClaudePath: "/usr/bin/claude", ClaudeVersion: "2.1.280", FromRecord: true}
	e := buildLogEntry(PipelineEvent{Type: EventBackendResolved, NodeID: "Answer", BackendResolved: d})
	if e.BackendResolved != "claude-code" || e.BackendReason != "claude found" || e.ClaudePath != "/usr/bin/claude" ||
		e.ClaudeVersion != "2.1.280" || !e.BackendFromRecord {
		t.Fatalf("log entry backend fields = %+v", e)
	}
}
