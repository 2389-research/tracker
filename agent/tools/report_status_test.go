// ABOUTME: Tests the report_status tool — emits the narration, validates input.
package tools

import (
	"context"
	"encoding/json"
	"testing"
)

func TestReportStatusTool_EmitsAndConfirms(t *testing.T) {
	var got string
	var calls int
	tool := NewReportStatusTool(func(s string) { got = s; calls++ })

	if tool.Name() != "report_status" {
		t.Errorf("name = %q", tool.Name())
	}
	// Parameters must be valid JSON schema.
	var schema map[string]any
	if err := json.Unmarshal(tool.Parameters(), &schema); err != nil {
		t.Fatalf("Parameters not valid JSON: %v", err)
	}

	out, err := tool.Execute(context.Background(), json.RawMessage(`{"status":"finished the OpenAI adapter; milestone 3 of 7"}`))
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || got != "finished the OpenAI adapter; milestone 3 of 7" {
		t.Errorf("emit got %q (calls=%d)", got, calls)
	}
	if out == "" {
		t.Error("expected a confirmation result")
	}
}

func TestReportStatusTool_RejectsEmptyAndBadInput(t *testing.T) {
	tool := NewReportStatusTool(func(string) {})
	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"status":"   "}`)); err == nil {
		t.Error("empty status should error")
	}
	if _, err := tool.Execute(context.Background(), json.RawMessage(`not json`)); err == nil {
		t.Error("invalid JSON should error")
	}
}

func TestReportStatusTool_NilEmitSafe(t *testing.T) {
	tool := NewReportStatusTool(nil)
	if _, err := tool.Execute(context.Background(), json.RawMessage(`{"status":"ok"}`)); err != nil {
		t.Errorf("nil emit should be safe, got %v", err)
	}
}
