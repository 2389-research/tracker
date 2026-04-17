package tracker

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveRunDir_ExactMatch(t *testing.T) {
	workdir := t.TempDir()
	runID := "2026-04-17T10-00-00"
	must(t, os.MkdirAll(filepath.Join(workdir, ".tracker", "runs", runID), 0o755))

	got, err := ResolveRunDir(workdir, runID)
	if err != nil {
		t.Fatalf("ResolveRunDir: %v", err)
	}
	want := filepath.Join(workdir, ".tracker", "runs", runID)
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestResolveRunDir_PrefixMatch(t *testing.T) {
	workdir := t.TempDir()
	full := "2026-04-17T10-00-00"
	must(t, os.MkdirAll(filepath.Join(workdir, ".tracker", "runs", full), 0o755))

	got, err := ResolveRunDir(workdir, "2026-04-17")
	if err != nil {
		t.Fatalf("ResolveRunDir: %v", err)
	}
	if filepath.Base(got) != full {
		t.Errorf("got base %q, want %q", filepath.Base(got), full)
	}
}

func TestResolveRunDir_AmbiguousPrefix(t *testing.T) {
	workdir := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(workdir, ".tracker", "runs", "2026-04-17T10-00-00"), 0o755))
	must(t, os.MkdirAll(filepath.Join(workdir, ".tracker", "runs", "2026-04-17T11-00-00"), 0o755))

	_, err := ResolveRunDir(workdir, "2026-04-17")
	if err == nil {
		t.Fatal("expected error for ambiguous prefix")
	}
}

func TestResolveRunDir_Empty(t *testing.T) {
	_, err := ResolveRunDir(t.TempDir(), "")
	if err == nil {
		t.Fatal("expected error for empty run ID")
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func TestParseActivityLine_RFC3339Nano(t *testing.T) {
	line := `{"ts":"2026-04-17T10:00:00.123456789Z","type":"stage_started","node_id":"N1"}`
	entry, ok := ParseActivityLine(line)
	if !ok {
		t.Fatal("expected parse success")
	}
	if entry.NodeID != "N1" || entry.Type != "stage_started" {
		t.Errorf("wrong fields: %+v", entry)
	}
}

func TestParseActivityLine_MillisFormat(t *testing.T) {
	line := `{"ts":"2026-04-17T10:00:00.123Z","type":"pipeline_completed"}`
	entry, ok := ParseActivityLine(line)
	if !ok {
		t.Fatal("expected parse success")
	}
	if entry.Type != "pipeline_completed" {
		t.Errorf("wrong type: %q", entry.Type)
	}
}

func TestParseActivityLine_MalformedJSON(t *testing.T) {
	if _, ok := ParseActivityLine(`not-json`); ok {
		t.Fatal("expected parse failure")
	}
}

func TestParseActivityLine_InvalidTimestamp(t *testing.T) {
	line := `{"ts":"not-a-time","type":"x"}`
	if _, ok := ParseActivityLine(line); ok {
		t.Fatal("expected parse failure on bad timestamp")
	}
}

func TestLoadActivityLog_Missing(t *testing.T) {
	entries, err := LoadActivityLog(t.TempDir())
	if err != nil {
		t.Fatalf("expected nil err for missing file, got %v", err)
	}
	if entries != nil {
		t.Errorf("expected nil entries, got %d", len(entries))
	}
}

func TestLoadActivityLog_SkipsBlankAndMalformed(t *testing.T) {
	dir := t.TempDir()
	content := `{"ts":"2026-04-17T10:00:00Z","type":"a"}
garbage
{"ts":"2026-04-17T10:00:01Z","type":"b"}
`
	must(t, os.WriteFile(filepath.Join(dir, "activity.jsonl"), []byte(content), 0o644))

	entries, err := LoadActivityLog(dir)
	if err != nil {
		t.Fatalf("LoadActivityLog: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2", len(entries))
	}
	if entries[0].Type != "a" || entries[1].Type != "b" {
		t.Errorf("wrong entries: %+v", entries)
	}
}
