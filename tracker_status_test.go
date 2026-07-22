// ABOUTME: Tests the status-update timeline reader (#494).
package tracker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunStatusTimeline(t *testing.T) {
	dir := t.TempDir()
	runDir := filepath.Join(dir, "run-status-xyz789")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	lines := []string{
		`{"ts":"2026-07-21T16:04:23.000Z","source":"agent","type":"status_update","node_id":"Implement","content":"finished the gh adapter; PR-count cases passing"}`,
		`{"ts":"2026-07-21T16:05:00.000Z","source":"agent","type":"tool_call_start","node_id":"Implement","content":"noise that must be filtered"}`,
		`{"ts":"2026-07-21T16:06:01.000Z","source":"agent","type":"status_update","node_id":"Verify","content":"milestone 3 of 7 verified — moving to OpenAI adapter"}`,
		`{"ts":"2026-07-21T16:07:00.000Z","type":"status_update","content":"   "}`, // empty → skipped
		`not json`, // skipped
	}
	if err := os.WriteFile(filepath.Join(runDir, "activity.jsonl"), []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}

	entries, err := RunStatusTimeline(runDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 status updates (noise/empty/non-json filtered), got %d: %+v", len(entries), entries)
	}
	if entries[0].NodeID != "Implement" || !strings.Contains(entries[0].Text, "gh adapter") {
		t.Errorf("entry 0 = %+v", entries[0])
	}
	if entries[1].NodeID != "Verify" || !strings.Contains(entries[1].Text, "milestone 3 of 7") {
		t.Errorf("entry 1 = %+v", entries[1])
	}
}

func TestRunStatusTimeline_NoLog(t *testing.T) {
	// A run dir with no activity log yields no entries, not an error.
	entries, err := RunStatusTimeline(filepath.Join(t.TempDir(), "run-none"))
	if err != nil || len(entries) != 0 {
		t.Errorf("expected (nil, nil), got (%v, %v)", entries, err)
	}
}

func TestParseStatusLine(t *testing.T) {
	if _, ok := parseStatusLine(`{"type":"status_update","content":"hi","node_id":"N"}`); !ok {
		t.Error("valid status_update should parse")
	}
	if _, ok := parseStatusLine(`{"type":"tool_call_end","content":"x"}`); ok {
		t.Error("non-status type should be skipped")
	}
	if _, ok := parseStatusLine(`{"type":"status_update","content":""}`); ok {
		t.Error("empty content should be skipped")
	}
	if _, ok := parseStatusLine(``); ok {
		t.Error("blank line should be skipped")
	}
}
