package handlers

import (
	"strings"
	"testing"
)

func TestFormatWritesError_TruncatesRawOutput(t *testing.T) {
	raw := strings.Repeat("x", maxWritesErrorRawLen+20)
	msg := formatWritesError("n1", []string{"a"}, "Tool stdout JSON", assertErr("bad json"), raw)
	if !strings.Contains(msg, "truncated") {
		t.Fatalf("expected truncation marker, got %q", msg)
	}
	if strings.Contains(msg, raw) {
		t.Fatalf("expected full raw output to be omitted")
	}
}

func TestFormatWritesError_LeavesShortRawOutput(t *testing.T) {
	raw := `{"a":"b"}`
	msg := formatWritesError("n1", []string{"a"}, "Tool stdout JSON", assertErr("bad json"), raw)
	if !strings.Contains(msg, raw) {
		t.Fatalf("expected short raw output to be preserved, got %q", msg)
	}
}

type assertErr string

func (e assertErr) Error() string { return string(e) }

