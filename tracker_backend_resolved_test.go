// ABOUTME: backend: auto's announcement reaches the --json NDJSON stream and
// ABOUTME: tracker diagnose's report.
package tracker

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/2389-research/tracker/pipeline"
)

func TestNDJSON_BackendResolvedFields(t *testing.T) {
	var buf bytes.Buffer
	w := NewNDJSONWriter(&buf)
	w.PipelineHandler().HandlePipelineEvent(pipeline.PipelineEvent{
		Type: pipeline.EventBackendResolved, NodeID: "Answer", Timestamp: time.Now(),
		BackendResolved: &pipeline.BackendResolvedDetail{Backend: "claude-code", Reason: "claude found", ClaudePath: "/usr/bin/claude", ClaudeVersion: "2.1.280"},
	})
	var got map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &got); err != nil {
		t.Fatalf("decode %q: %v", buf.String(), err)
	}
	for k, want := range map[string]string{"backend_resolved": "claude-code", "backend_reason": "claude found", "claude_path": "/usr/bin/claude", "claude_version": "2.1.280"} {
		if got[k] != want {
			t.Errorf("%s = %v, want %q", k, got[k], want)
		}
	}
}

func TestDiagnose_ReportsBackendResolved(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{"activity.jsonl", "checkpoint.json"} {
		b, err := os.ReadFile(filepath.Join("testdata/runs/jail_degraded", f))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, f), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	line := `{"ts":"2026-09-17T10:00:01Z","type":"backend_resolved","run_id":"r","node_id":"Answer","backend_resolved":"claude-code","backend_reason":"claude found"}` + "\n"
	fh, err := os.OpenFile(filepath.Join(dir, "activity.jsonl"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fh.WriteString(line); err != nil {
		t.Fatal(err)
	}
	fh.Close()
	r, err := Diagnose(context.Background(), dir)
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	if r.BackendResolved != "claude-code" {
		t.Fatalf("DiagnoseReport.BackendResolved = %q, want claude-code", r.BackendResolved)
	}
}
