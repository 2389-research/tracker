// ABOUTME: Regression guard for #613 — the generated activity-log reader stays
// ABOUTME: byte-in-sync with its schema and keeps the decode contract (lossless,
// ABOUTME: unknown-field tolerant) that the single-source generation must uphold.
package tracker

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestActivitySchema_GeneratedFilesAreFresh regenerates the reader sources from
// the single field authority (scripts/gen/activitylog/schema.go) into a temp dir
// and asserts the committed files are byte-identical. This is the in-`go test`
// half of the freshness gate (scripts/docs/gate.sh activity-schema): it pins
// that activityRawLine, ActivityEntry, and toEntry are all derived from one
// schema and can never be hand-edited out of sync.
func TestActivitySchema_GeneratedFilesAreFresh(t *testing.T) {
	if testing.Short() {
		t.Skip("skips `go run` generation in -short mode; covered by the shell gate")
	}
	tmp := t.TempDir()
	cmd := exec.Command("go", "run", "./scripts/gen/activitylog")
	cmd.Env = append(os.Environ(), "GEN_ACTIVITY_OUT="+tmp)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("run generator: %v\n%s", err, out)
	}
	for _, name := range []string{"tracker_activity_raw_gen.go", "tracker_activity_entry_gen.go"} {
		want, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read committed %s: %v", name, err)
		}
		got, err := os.ReadFile(filepath.Join(tmp, name))
		if err != nil {
			t.Fatalf("read generated %s: %v", name, err)
		}
		if string(got) != string(want) {
			t.Errorf("%s is out of sync with scripts/gen/activitylog/schema.go — run `make gen-activity-schema` and commit.", name)
		}
	}
}

// TestParseActivityLine_IgnoresUnknownFields pins the forward-compatibility
// contract: a line written by a newer runtime carrying a key this build does not
// know still parses, and the known fields around it decode correctly. The
// unknown key must not fail the line (it is dropped, never an error).
func TestParseActivityLine_IgnoresUnknownFields(t *testing.T) {
	line := `{"ts":"2026-07-28T12:00:00.000Z","source":"pipeline","type":"decision_edge",` +
		`"node_id":"gen","edge_from":"a","edge_to":"b","future_field":{"nested":[1,2,3]},` +
		`"another_new_key":"ignored"}`
	entry, ok := ParseActivityLine(line)
	if !ok {
		t.Fatalf("ParseActivityLine rejected a line with unknown keys: %s", line)
	}
	if entry.Source != "pipeline" || entry.Type != "decision_edge" {
		t.Errorf("source/type = %q/%q, want pipeline/decision_edge", entry.Source, entry.Type)
	}
	if entry.EdgeFrom != "a" || entry.EdgeTo != "b" {
		t.Errorf("edge = %q->%q, want a->b", entry.EdgeFrom, entry.EdgeTo)
	}
}

// TestParseActivityLine_ByteLevelKnownFields decodes a fixed byte fixture that
// spans several field groups and asserts each datum lands on the exported
// ActivityEntry under the expected name and type — the generated toEntry copier
// must map every JSON key to the right Go field.
func TestParseActivityLine_ByteLevelKnownFields(t *testing.T) {
	line := []byte(`{"ts":"2026-07-28T12:00:00.000Z","source":"agent","type":"turn_metrics",` +
		`"run_id":"run-1","node_id":"build","session_id":"sess-7","turn_no":3,"call_id":"call-2",` +
		`"tool_name":"bash","tool_input":"{\"cmd\":\"go test\"}","token_input":120,"token_output":45,` +
		`"cache_read_tokens":900,"finish_reason":"stop","terminal_status":"success",` +
		`"resume_after":"2026-07-28T13:00:00Z","override_subgraph_path":["parent","child"]}`)
	entry, ok := ParseActivityLine(string(line))
	if !ok {
		t.Fatalf("ParseActivityLine rejected a well-formed fixture: %s", line)
	}
	checks := []struct {
		name string
		got  any
		want any
	}{
		{"RunID", entry.RunID, "run-1"},
		{"NodeID", entry.NodeID, "build"},
		{"SessionID", entry.SessionID, "sess-7"},
		{"TurnNo", entry.TurnNo, 3},
		{"CallID", entry.CallID, "call-2"},
		{"ToolName", entry.ToolName, "bash"},
		{"ToolInput", entry.ToolInput, `{"cmd":"go test"}`},
		{"TokenInput", entry.TokenInput, 120},
		{"TokenOutput", entry.TokenOutput, 45},
		{"CacheReadTokens", entry.CacheReadTokens, 900},
		{"FinishReason", entry.FinishReason, "stop"},
		{"TerminalStatus", entry.TerminalStatus, "success"},
		{"ResumeAfter", entry.ResumeAfter, "2026-07-28T13:00:00Z"},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
	// The defensive-copy slice field decodes with its elements intact.
	if len(entry.OverrideSubgraphPath) != 2 || entry.OverrideSubgraphPath[0] != "parent" || entry.OverrideSubgraphPath[1] != "child" {
		t.Errorf("OverrideSubgraphPath = %v, want [parent child]", entry.OverrideSubgraphPath)
	}
	if entry.Timestamp.IsZero() {
		t.Error("Timestamp was not parsed")
	}
}
