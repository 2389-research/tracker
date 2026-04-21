package tracker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

func TestResolveRunDir_NoMatch(t *testing.T) {
	workdir := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(workdir, ".tracker", "runs", "2026-04-17T10-00-00"), 0o755))

	_, err := ResolveRunDir(workdir, "no-match")
	if err == nil {
		t.Fatal("expected no-match error")
	}
	if !strings.Contains(err.Error(), `no run found matching "no-match"`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestMostRecentRunID_SelectsLatestCheckpointTimestamp(t *testing.T) {
	workdir := t.TempDir()
	runsDir := filepath.Join(workdir, ".tracker", "runs")
	must(t, os.MkdirAll(filepath.Join(runsDir, "r1"), 0o755))
	must(t, os.WriteFile(filepath.Join(runsDir, "r1", "checkpoint.json"),
		[]byte(`{"run_id":"r1","timestamp":"2026-04-17T10:00:00Z"}`), 0o644))
	must(t, os.MkdirAll(filepath.Join(runsDir, "r2"), 0o755))
	must(t, os.WriteFile(filepath.Join(runsDir, "r2", "checkpoint.json"),
		[]byte(`{"run_id":"r2","timestamp":"2026-04-17T11:00:00Z"}`), 0o644))

	got, err := MostRecentRunID(workdir)
	if err != nil {
		t.Fatalf("MostRecentRunID: %v", err)
	}
	if got != "r2" {
		t.Fatalf("MostRecentRunID = %q, want r2", got)
	}
}

func TestMostRecentRunID_NoRunsFound(t *testing.T) {
	_, err := MostRecentRunID(t.TempDir())
	if err == nil {
		t.Fatal("expected no-runs error")
	}
	if !strings.Contains(err.Error(), "no runs found — run a pipeline first") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestMostRecentRunID_NoValidCheckpoints(t *testing.T) {
	workdir := t.TempDir()
	runsDir := filepath.Join(workdir, ".tracker", "runs")
	must(t, os.MkdirAll(filepath.Join(runsDir, "r1"), 0o755))
	must(t, os.MkdirAll(filepath.Join(runsDir, "r2"), 0o755))

	_, err := MostRecentRunID(workdir)
	if err == nil {
		t.Fatal("expected no-valid-checkpoints error")
	}
	if !strings.Contains(err.Error(), "no runs found with valid checkpoints") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestMostRecentRunID_WarnsOnMalformedCheckpointAndContinues(t *testing.T) {
	workdir := t.TempDir()
	runsDir := filepath.Join(workdir, ".tracker", "runs")
	must(t, os.MkdirAll(filepath.Join(runsDir, "bad"), 0o755))
	must(t, os.WriteFile(filepath.Join(runsDir, "bad", "checkpoint.json"), []byte(`{not json}`), 0o644))
	must(t, os.MkdirAll(filepath.Join(runsDir, "good"), 0o755))
	must(t, os.WriteFile(filepath.Join(runsDir, "good", "checkpoint.json"),
		[]byte(`{"run_id":"good","timestamp":"2026-04-17T11:00:00Z"}`), 0o644))

	var got string
	var err error
	stderr := captureStderr(t, func() {
		got, err = MostRecentRunID(workdir)
	})
	if err != nil {
		t.Fatalf("MostRecentRunID: %v", err)
	}
	if got != "good" {
		t.Fatalf("MostRecentRunID = %q, want good", got)
	}
	if !strings.Contains(stderr, "warning: cannot load checkpoint for run bad") {
		t.Fatalf("expected checkpoint warning on stderr, got: %q", stderr)
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

func TestSortActivityByTime_Order(t *testing.T) {
	t1 := time.Date(2026, 4, 17, 10, 0, 0, 0, time.UTC)
	t2 := t1.Add(time.Second)
	t3 := t2.Add(time.Second)
	entries := []ActivityEntry{
		{Timestamp: t3, Type: "c"},
		{Timestamp: t1, Type: "a"},
		{Timestamp: t2, Type: "b"},
	}
	SortActivityByTime(entries)
	if entries[0].Type != "a" || entries[1].Type != "b" || entries[2].Type != "c" {
		t.Errorf("wrong order: %+v", entries)
	}
}

func TestSortActivityByTime_EqualTimestamps(t *testing.T) {
	ts := time.Date(2026, 4, 17, 10, 0, 0, 0, time.UTC)
	entries := []ActivityEntry{
		{Timestamp: ts, Type: "x"},
		{Timestamp: ts, Type: "y"},
	}
	SortActivityByTime(entries)
	// Either order is acceptable — we just require no panic and preserved length.
	if len(entries) != 2 {
		t.Fatalf("lost entries: %d", len(entries))
	}
}
