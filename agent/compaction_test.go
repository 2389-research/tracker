// ABOUTME: Tests for context compaction summary generation.
// ABOUTME: Verifies per-tool-type summary formats and edge cases.
package agent

import "testing"

func TestCompactSummary_ReadTool(t *testing.T) {
	content := "     1\tpackage main\n     2\t\n     3\tfunc main() {\n     4\t}\n"
	summary := compactSummary("read_file", content)
	expected := "[previously read: 4 lines. Re-read with read_file if needed.]"
	if summary != expected {
		t.Errorf("expected %q, got %q", expected, summary)
	}
}

func TestCompactSummary_ReadToolAltName(t *testing.T) {
	content := "     1\tpackage main\n"
	summary := compactSummary("read", content)
	expected := "[previously read: 1 lines. Re-read with read if needed.]"
	if summary != expected {
		t.Errorf("expected %q, got %q", expected, summary)
	}
}

func TestCompactSummary_GrepTool(t *testing.T) {
	content := "src/main.go:5:func main\nsrc/util.go:10:func helper\nsrc/lib.go:3:func init\n"
	summary := compactSummary("grep_search", content)
	expected := "[previously searched: 3 matches found. Re-run grep_search if needed.]"
	if summary != expected {
		t.Errorf("expected %q, got %q", expected, summary)
	}
}

func TestCompactSummary_BashTool(t *testing.T) {
	content := "go test ./... -v\nok  \tpackage1\nok  \tpackage2\n"
	summary := compactSummary("bash", content)
	expected := "[previously ran: go test ./... -v — Re-run if needed.]"
	if summary != expected {
		t.Errorf("expected %q, got %q", expected, summary)
	}
}

func TestCompactSummary_GenericTool(t *testing.T) {
	content := "some output that is 50 characters long or whateve"
	summary := compactSummary("list_files", content)
	expected := "[previous list_files result — 49 chars. Re-run if needed.]"
	if summary != expected {
		t.Errorf("expected %q, got %q", expected, summary)
	}
}

func TestCompactSummary_EmptyContent(t *testing.T) {
	summary := compactSummary("read_file", "")
	expected := "[previous read_file result — 0 chars. Re-run if needed.]"
	if summary != expected {
		t.Errorf("expected %q, got %q", expected, summary)
	}
}
