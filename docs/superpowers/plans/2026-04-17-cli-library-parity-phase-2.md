# CLI↔Library Parity — Phase 2 + NDJSON Writer Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Promote four CLI-private commands (`diagnose`, `audit`, `doctor`, `simulate`) and the NDJSON event writer into the public `tracker` library package as structured, JSON-serializable reports, keeping byte-for-byte CLI output identical.

**Architecture:** Data / presentation split. `tracker.<Command>()` returns a pure `*<Command>Report` with no formatted strings. `cmd/tracker/<command>.go` shrinks to `resolve → call library → print<Report>` with all `fmt.Printf`/`lipgloss` rendering in the printer. Shared run-dir and activity.jsonl helpers move to `tracker_activity.go` so both library and CLI use one implementation.

**Tech Stack:** Go, standard library only for new public types. Existing dependencies (`charmbracelet/lipgloss` for CLI printers, `pipeline.Checkpoint` / `pipeline.Graph` for parsing) stay on the CLI side of the split where they already were.

**Spec:** `docs/superpowers/specs/2026-04-17-cli-library-parity-phase-2-design.md`

---

## File structure

### New files in `tracker/`
- `tracker_activity.go` — `ResolveRunDir`, `MostRecentRunID`, `ActivityEntry` + `LoadActivityLog` + `ParseActivityLine`
- `tracker_events.go` — `NDJSONEvent`, `NDJSONWriter` + `NewNDJSONWriter`, factory methods for pipeline/agent/LLM handlers
- `tracker_diagnose.go` — `DiagnoseReport`, `NodeFailure`, `BudgetHalt`, `Suggestion` + `Diagnose`, `DiagnoseMostRecent`
- `tracker_audit.go` — `AuditReport`, `TimelineEntry`, `RetryRecord`, `ActivityError`, `RunSummary` + `Audit`, `ListRuns`
- `tracker_doctor.go` — `DoctorConfig`, `DoctorReport`, `CheckResult` + `Doctor`
- `tracker_simulate.go` — `SimulateReport`, `SimNode`, `SimEdge`, `PlanStep` + `Simulate`
- One `_test.go` per file
- `tracker/testdata/runs/ok/`, `tracker/testdata/runs/failed/`, `tracker/testdata/runs/budget_halted/` — fixture trees

### Modified files in `cmd/tracker/`
- `cmd/tracker/audit.go` — private helpers removed, `runAudit`/`listRuns` call library, rest is printers
- `cmd/tracker/diagnose.go` — `runDiagnose`/`diagnoseMostRecent` call library, rest is printers
- `cmd/tracker/doctor.go` — `runDoctorWithConfig` calls library for checks; retains CLI-only gitignore-fix/workdir-create logic
- `cmd/tracker/simulate.go` — `runSimulateCmd` calls library; rest is printers
- `cmd/tracker/run.go` — swap private `jsonStream` for `tracker.NewNDJSONWriter`

### Deleted files
- `cmd/tracker/json_stream.go` — content lives in `tracker/tracker_events.go`
- `cmd/tracker/json_stream_test.go` — tests move into `tracker/tracker_events_test.go`

### Docs
- `CHANGELOG.md` — add Unreleased section entry
- `README.md` — add a short library-API section showing `tracker.Diagnose(runDir)` usage

---

## Pre-flight

- [ ] **Step 0.1: Confirm clean tree and create feature branch**

```bash
git status  # must be clean or only contain .claude/
git checkout -b feat/library-parity-phase-2
```

Expected: clean branch, tracking `main`.

- [ ] **Step 0.2: Baseline tests pass before starting**

```bash
go build ./...
go test ./... -short
```

Expected: build succeeds, all 14 packages pass. If any package is red before you start, stop — resolve that first.

---

## Task 1: Shared activity helpers

**Files:**
- Create: `tracker/tracker_activity.go`
- Create: `tracker/tracker_activity_test.go`
- Modify: `cmd/tracker/audit.go` (remove `resolveRunDir`, `findRunDirMatch`, `resolveAmbiguousMatch`, `activityEntry`, `loadActivityLog`, `parseActivityLine`, `parseActivityTimestamp`)
- Modify: `cmd/tracker/diagnose.go` (update `findMostRecentRunID` callers to use library)

**Purpose:** One source of truth for run-dir resolution and activity.jsonl parsing. Prerequisite for every subsequent task.

- [ ] **Step 1.1: Write failing test for `ResolveRunDir`**

Create `tracker/tracker_activity_test.go`:

```go
package tracker

import (
	"os"
	"path/filepath"
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

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

_ = time.Time{} // keep time import for later tests
```

- [ ] **Step 1.2: Run test — expect FAIL**

```bash
go test ./... -run TestResolveRunDir -short
```

Expected: `undefined: ResolveRunDir`.

- [ ] **Step 1.3: Create `tracker_activity.go` with `ResolveRunDir` and helpers**

```go
// ABOUTME: Shared helpers for resolving run directories and parsing activity.jsonl.
// ABOUTME: Promoted from cmd/tracker/ so library and CLI use one implementation.
package tracker

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/2389-research/tracker/pipeline"
)

// ResolveRunDir finds the run directory under <workdir>/.tracker/runs matching
// runID by exact name or unique prefix. Returns an absolute path.
func ResolveRunDir(workdir, runID string) (string, error) {
	if runID == "" {
		return "", fmt.Errorf("run ID cannot be empty")
	}
	runsDir := filepath.Join(workdir, ".tracker", "runs")
	matched, err := findRunDirMatch(runsDir, runID)
	if err != nil {
		return "", err
	}
	return filepath.Join(runsDir, matched), nil
}

func findRunDirMatch(runsDir, runID string) (string, error) {
	entries, err := os.ReadDir(runsDir)
	if err != nil {
		return "", fmt.Errorf("cannot read runs directory: %w", err)
	}
	var matches []string
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), runID) {
			matches = append(matches, e.Name())
		}
	}
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("no run found matching %q in %s", runID, runsDir)
	case 1:
		return matches[0], nil
	default:
		for _, m := range matches {
			if m == runID {
				return m, nil
			}
		}
		return "", fmt.Errorf("ambiguous run ID %q matches %d runs: %s", runID, len(matches), strings.Join(matches, ", "))
	}
}

// MostRecentRunID returns the run ID of the most recent run (by checkpoint
// timestamp) under workdir. Returns an error if no runs with valid
// checkpoints exist.
func MostRecentRunID(workdir string) (string, error) {
	runsDir := filepath.Join(workdir, ".tracker", "runs")
	entries, err := os.ReadDir(runsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("no runs found — run a pipeline first")
		}
		return "", fmt.Errorf("cannot read runs directory: %w", err)
	}
	var latestID string
	var latestTime time.Time
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		cpPath := filepath.Join(runsDir, e.Name(), "checkpoint.json")
		cp, err := pipeline.LoadCheckpoint(cpPath)
		if err != nil {
			continue
		}
		if cp.Timestamp.After(latestTime) {
			latestTime = cp.Timestamp
			latestID = e.Name()
		}
	}
	if latestID == "" {
		return "", fmt.Errorf("no runs found with valid checkpoints")
	}
	return latestID, nil
}

// ActivityEntry is a parsed line from activity.jsonl.
type ActivityEntry struct {
	Timestamp time.Time `json:"ts"`
	Type      string    `json:"type"`
	RunID     string    `json:"run_id,omitempty"`
	NodeID    string    `json:"node_id,omitempty"`
	Message   string    `json:"message,omitempty"`
	Error     string    `json:"error,omitempty"`
}

// LoadActivityLog reads and parses activity.jsonl, skipping malformed lines.
// Returns (nil, nil) if the file does not exist.
func LoadActivityLog(runDir string) ([]ActivityEntry, error) {
	path := filepath.Join(runDir, "activity.jsonl")
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("open activity log: %w", err)
	}
	defer f.Close()
	var entries []ActivityEntry
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		entry, ok := ParseActivityLine(line)
		if ok {
			entries = append(entries, entry)
		}
	}
	return entries, scanner.Err()
}

// SortActivityByTime sorts entries ascending by Timestamp.
func SortActivityByTime(entries []ActivityEntry) {
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Timestamp.Before(entries[j].Timestamp)
	})
}

// ParseActivityLine decodes a single JSONL line. Returns (zero, false) on any parse error.
func ParseActivityLine(line string) (ActivityEntry, bool) {
	var raw struct {
		Timestamp string `json:"ts"`
		Type      string `json:"type"`
		RunID     string `json:"run_id"`
		NodeID    string `json:"node_id"`
		Message   string `json:"message"`
		Error     string `json:"error"`
	}
	if err := json.Unmarshal([]byte(line), &raw); err != nil {
		return ActivityEntry{}, false
	}
	ts, ok := parseActivityTimestamp(raw.Timestamp)
	if !ok {
		return ActivityEntry{}, false
	}
	return ActivityEntry{
		Timestamp: ts,
		Type:      raw.Type,
		RunID:     raw.RunID,
		NodeID:    raw.NodeID,
		Message:   raw.Message,
		Error:     raw.Error,
	}, true
}

func parseActivityTimestamp(s string) (time.Time, bool) {
	if ts, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return ts, true
	}
	if ts, err := time.Parse("2006-01-02T15:04:05.000Z07:00", s); err == nil {
		return ts, true
	}
	return time.Time{}, false
}
```

- [ ] **Step 1.4: Run test — expect PASS**

```bash
go test ./... -run TestResolveRunDir -short
```

Expected: all 4 subtests PASS.

- [ ] **Step 1.5: Add activity-parse tests**

Append to `tracker/tracker_activity_test.go`:

```go
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
` + "\n" + `garbage
` + `{"ts":"2026-04-17T10:00:01Z","type":"b"}
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
```

- [ ] **Step 1.6: Run activity tests — expect PASS**

```bash
go test ./... -run "TestParseActivityLine|TestLoadActivityLog" -short
```

Expected: all 6 subtests PASS.

- [ ] **Step 1.7: Update `cmd/tracker/audit.go` to use library helpers**

Remove local definitions of `activityEntry`, `resolveRunDir`, `findRunDirMatch`, `resolveAmbiguousMatch`, `loadActivityLog`, `parseActivityLine`, `parseActivityTimestamp` from `cmd/tracker/audit.go`.

Replace every in-file reference to the removed symbols with the library equivalents:

- `activityEntry` → `tracker.ActivityEntry`
- `resolveRunDir(workdir, runID)` → `tracker.ResolveRunDir(workdir, runID)`
- `loadActivityLog(runDir)` → `tracker.LoadActivityLog(runDir)`
- The activity-log sort call (`sort.Slice(activity, ...)`) → `tracker.SortActivityByTime(activity)`

Keep `runSummary`, `buildRunSummary`, `collectRunSummaries`, `determinePipelineStatus`, `listRuns`, `runAudit`, and all `print*` / `*Recommendations` functions in place for now — Task 5 migrates them.

Add the import:

```go
tracker "github.com/2389-research/tracker"
```

- [ ] **Step 1.8: Update `cmd/tracker/diagnose.go` to use library helpers**

In `cmd/tracker/diagnose.go`:

- Replace `diagnoseMostRecent` body with:

```go
func diagnoseMostRecent(workdir string) error {
	latestID, err := tracker.MostRecentRunID(workdir)
	if err != nil {
		return err
	}
	return runDiagnose(workdir, latestID)
}
```

- Delete `findMostRecentRunID`, `readRunsDir`, `pickLatestRunID` (superseded by `tracker.MostRecentRunID`).
- Replace `runDir, err := resolveRunDir(workdir, runID)` in `runDiagnose` with `runDir, err := tracker.ResolveRunDir(workdir, runID)`.

Add the `tracker` import if not already present.

- [ ] **Step 1.9: Build and run the full test suite**

```bash
go build ./...
go test ./... -short
```

Expected: build succeeds, all 14 packages PASS. If `cmd/tracker` tests fail, the most likely cause is a missed reference — grep for `activityEntry\|resolveRunDir\|findRunDirMatch\|loadActivityLog\|parseActivityLine\|parseActivityTimestamp\|findMostRecentRunID` under `cmd/tracker/` and update.

- [ ] **Step 1.10: Commit**

```bash
git add tracker/tracker_activity.go tracker/tracker_activity_test.go cmd/tracker/audit.go cmd/tracker/diagnose.go
git commit -m "$(cat <<'EOF'
feat(tracker): promote run-dir + activity helpers to library

Move ResolveRunDir, MostRecentRunID, ActivityEntry, LoadActivityLog,
ParseActivityLine into the tracker package. Prerequisite for exposing
Diagnose / Audit library APIs. No behavior change; CLI wired to the
library helpers.

Refs #76

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 2: NDJSON writer (closes Phase 1)

**Files:**
- Create: `tracker/tracker_events.go`
- Create: `tracker/tracker_events_test.go`
- Delete: `cmd/tracker/json_stream.go`, `cmd/tracker/json_stream_test.go`
- Modify: `cmd/tracker/run.go` (or wherever `newJSONStream` is called) to use `tracker.NewNDJSONWriter`

- [ ] **Step 2.1: Find existing callers of `newJSONStream`**

```bash
grep -rn "newJSONStream\|jsonStream" cmd/tracker/
```

Record the file(s) and line numbers. Expected hits: `cmd/tracker/json_stream.go` (definition), `cmd/tracker/json_stream_test.go` (tests), and 1-2 call sites in `run.go` wiring the handler factories.

- [ ] **Step 2.2: Write failing test for `NDJSONWriter`**

Create `tracker/tracker_events_test.go`:

```go
package tracker

import (
	"bytes"
	"encoding/json"
	"strings"
	"sync"
	"testing"
)

func TestNDJSONWriter_Write(t *testing.T) {
	var buf bytes.Buffer
	w := NewNDJSONWriter(&buf)
	w.Write(NDJSONEvent{Timestamp: "2026-04-17T10:00:00Z", Source: "pipeline", Type: "stage_started", NodeID: "N1"})

	line := strings.TrimSuffix(buf.String(), "\n")
	var got NDJSONEvent
	if err := json.Unmarshal([]byte(line), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Source != "pipeline" || got.Type != "stage_started" || got.NodeID != "N1" {
		t.Errorf("wrong event: %+v", got)
	}
}

func TestNDJSONWriter_StableJSONTags(t *testing.T) {
	var buf bytes.Buffer
	w := NewNDJSONWriter(&buf)
	w.Write(NDJSONEvent{
		Timestamp: "t", Source: "agent", Type: "tool_call",
		RunID: "r1", NodeID: "n1", Message: "m", Error: "e",
		Provider: "p", Model: "mo", ToolName: "tn", Content: "c",
	})
	want := `"ts":"t"`
	if !strings.Contains(buf.String(), want) {
		t.Errorf("missing stable tag %q in output: %s", want, buf.String())
	}
	for _, tag := range []string{`"source"`, `"type"`, `"run_id"`, `"node_id"`, `"message"`, `"error"`, `"provider"`, `"model"`, `"tool_name"`, `"content"`} {
		if !strings.Contains(buf.String(), tag) {
			t.Errorf("missing JSON tag %s in output", tag)
		}
	}
}

func TestNDJSONWriter_ConcurrentWrites(t *testing.T) {
	var buf bytes.Buffer
	w := NewNDJSONWriter(&buf)

	const n = 100
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			w.Write(NDJSONEvent{Timestamp: "t", Source: "pipeline", Type: "x"})
		}()
	}
	wg.Wait()

	lines := strings.Split(strings.TrimSuffix(buf.String(), "\n"), "\n")
	if len(lines) != n {
		t.Fatalf("got %d lines, want %d", len(lines), n)
	}
	for i, l := range lines {
		var evt NDJSONEvent
		if err := json.Unmarshal([]byte(l), &evt); err != nil {
			t.Fatalf("line %d: unmarshal: %v; got %q", i, err, l)
		}
	}
}
```

- [ ] **Step 2.3: Run tests — expect FAIL**

```bash
go test ./... -run TestNDJSONWriter -short
```

Expected: `undefined: NDJSONWriter`.

- [ ] **Step 2.4: Create `tracker/tracker_events.go`**

```go
// ABOUTME: Public NDJSON event writer for the tracker --json wire format.
// ABOUTME: Threaded from pipeline/LLM/agent event streams; thread-safe for concurrent writers.
package tracker

import (
	"encoding/json"
	"io"
	"sync"
	"time"

	"github.com/2389-research/tracker/agent"
	"github.com/2389-research/tracker/llm"
	"github.com/2389-research/tracker/pipeline"
)

// NDJSONEvent is the stable wire format for the tracker --json mode. Field
// tags are stable; new optional fields may be added without a major bump.
type NDJSONEvent struct {
	Timestamp string `json:"ts"`
	Source    string `json:"source"`
	Type      string `json:"type"`
	RunID     string `json:"run_id,omitempty"`
	NodeID    string `json:"node_id,omitempty"`
	Message   string `json:"message,omitempty"`
	Error     string `json:"error,omitempty"`
	Provider  string `json:"provider,omitempty"`
	Model     string `json:"model,omitempty"`
	ToolName  string `json:"tool_name,omitempty"`
	Content   string `json:"content,omitempty"`
}

// NDJSONWriter is a thread-safe writer that serializes NDJSONEvents line by
// line onto an io.Writer. Library consumers use it to produce the same
// stream as the tracker CLI's --json mode.
type NDJSONWriter struct {
	mu sync.Mutex
	w  io.Writer
}

// NewNDJSONWriter returns a new writer backed by w.
func NewNDJSONWriter(w io.Writer) *NDJSONWriter {
	return &NDJSONWriter{w: w}
}

// Write serializes evt as a JSON line. Safe to call from multiple goroutines.
func (s *NDJSONWriter) Write(evt NDJSONEvent) {
	data, err := json.Marshal(evt)
	if err != nil {
		return
	}
	data = append(data, '\n')
	s.mu.Lock()
	defer s.mu.Unlock()
	_, _ = s.w.Write(data)
}

// PipelineHandler returns a pipeline.PipelineEventHandler that writes events
// to this stream.
func (s *NDJSONWriter) PipelineHandler() pipeline.PipelineEventHandler {
	return pipeline.PipelineEventHandlerFunc(func(evt pipeline.PipelineEvent) {
		entry := NDJSONEvent{
			Timestamp: evt.Timestamp.Format("2006-01-02T15:04:05.000Z07:00"),
			Source:    "pipeline",
			Type:      string(evt.Type),
			RunID:     evt.RunID,
			NodeID:    evt.NodeID,
			Message:   evt.Message,
		}
		if evt.Err != nil {
			entry.Error = evt.Err.Error()
		}
		s.Write(entry)
	})
}

// TraceObserver returns an llm.TraceObserver that writes trace events to
// this stream.
func (s *NDJSONWriter) TraceObserver() llm.TraceObserver {
	return llm.TraceObserverFunc(func(evt llm.TraceEvent) {
		s.Write(NDJSONEvent{
			Timestamp: time.Now().Format("2006-01-02T15:04:05.000Z07:00"),
			Source:    "llm",
			Type:      string(evt.Kind),
			Provider:  evt.Provider,
			Model:     evt.Model,
			ToolName:  evt.ToolName,
			Content:   evt.Preview,
		})
	})
}

// AgentHandler returns an agent.EventHandler that writes agent events to
// this stream.
func (s *NDJSONWriter) AgentHandler() agent.EventHandler {
	return agent.EventHandlerFunc(func(evt agent.Event) {
		content := evt.ToolOutput
		if content == "" {
			content = evt.Text
		}
		entry := NDJSONEvent{
			Timestamp: time.Now().Format("2006-01-02T15:04:05.000Z07:00"),
			Source:    "agent",
			Type:      string(evt.Type),
			NodeID:    evt.NodeID,
			Provider:  evt.Provider,
			Model:     evt.Model,
			ToolName:  evt.ToolName,
			Content:   content,
		}
		entry.Error = buildStreamEntryError(evt)
		s.Write(entry)
	})
}

func buildStreamEntryError(evt agent.Event) string {
	if evt.ToolError == "" && evt.Err == nil {
		return ""
	}
	if evt.ToolError != "" && evt.Err != nil {
		return evt.ToolError + ": " + evt.Err.Error()
	}
	if evt.ToolError != "" {
		return evt.ToolError
	}
	return evt.Err.Error()
}
```

- [ ] **Step 2.5: Run NDJSONWriter tests — expect PASS**

```bash
go test ./... -run TestNDJSONWriter -short -race
```

Expected: all 3 subtests PASS; race detector clean.

- [ ] **Step 2.6: Port `cmd/tracker/json_stream_test.go` assertions into the library test file**

Open `cmd/tracker/json_stream_test.go`. For each existing test, decide:

- If it asserts on the private `jsonStream` / `jsonStreamEvent` struct, move it and translate the type names to `NDJSONWriter` / `NDJSONEvent`. Append into `tracker/tracker_events_test.go`.
- If it asserts on CLI-layer wiring (flags, stdout plumbing), leave it in place but update the type name to `*NDJSONWriter` (the handler factories return the same interfaces).

After the port, run the library tests:

```bash
go test ./... -run "NDJSON" -short
```

Expected: all ported tests PASS.

- [ ] **Step 2.7: Update CLI callers to use `tracker.NewNDJSONWriter`**

In the file that previously called `newJSONStream(os.Stdout)` (from Step 2.1), replace:

```go
stream := newJSONStream(os.Stdout)
```

with:

```go
stream := tracker.NewNDJSONWriter(os.Stdout)
```

Handler method calls (`stream.pipelineHandler()`, `stream.traceObserver()`, `stream.agentHandler()`) must be renamed to the exported form:

- `stream.pipelineHandler()` → `stream.PipelineHandler()`
- `stream.traceObserver()` → `stream.TraceObserver()`
- `stream.agentHandler()` → `stream.AgentHandler()`

Verify the `tracker` import is present in the modified file.

- [ ] **Step 2.8: Delete the private CLI writer**

```bash
git rm cmd/tracker/json_stream.go cmd/tracker/json_stream_test.go
```

- [ ] **Step 2.9: Build and run full test suite**

```bash
go build ./...
go test ./... -short -race
```

Expected: build succeeds, all packages PASS.

- [ ] **Step 2.10: Smoke-test CLI --json wire format unchanged**

```bash
./tracker 2>/dev/null || true  # ensures a built binary exists in PATH for the next line
go build -o /tmp/tracker-test ./cmd/tracker
echo 'workflow X
  goal: "x"
  start: S
  exit: S
  agent S
    label: "S"
    prompt: "hi"' > /tmp/x.dip
# Expect NDJSON lines starting with {"ts":...} on stdout; no auth needed because we just want the parser to emit at least one event.
/tmp/tracker-test validate /tmp/x.dip --json 2>&1 | head -3
```

Expected: at least one line of valid JSON starting with `{"ts":"..."` — identical shape to before.

- [ ] **Step 2.11: Commit**

```bash
git add tracker/tracker_events.go tracker/tracker_events_test.go cmd/tracker/
git commit -m "$(cat <<'EOF'
feat(tracker): promote NDJSON event writer to public library API

NewNDJSONWriter exposes the same --json wire format the CLI uses via
PipelineHandler / AgentHandler / TraceObserver. Closes the Phase 1
NDJSON gap from #76. CLI json_stream.go removed; run.go wires the
library writer. Wire format unchanged byte-for-byte.

Refs #76

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 3: Simulate library API

**Files:**
- Create: `tracker/tracker_simulate.go`
- Create: `tracker/tracker_simulate_test.go`
- Modify: `cmd/tracker/simulate.go` (shrink `runSimulateCmd` to a printer over `tracker.Simulate`)

- [ ] **Step 3.1: Write failing test for `Simulate`**

Create `tracker/tracker_simulate_test.go`:

```go
package tracker

import (
	"strings"
	"testing"
)

const simpleSource = `workflow X
  goal: "x"
  start: S
  exit: E
  agent S
    label: "Start"
    prompt: "hi"
  agent E
    label: "End"
    prompt: "bye"
  S -> E
`

func TestSimulate_BasicGraph(t *testing.T) {
	r, err := Simulate(simpleSource)
	if err != nil {
		t.Fatalf("Simulate: %v", err)
	}
	if r.Format != "dip" {
		t.Errorf("format = %q, want dip", r.Format)
	}
	if len(r.Nodes) != 2 {
		t.Errorf("got %d nodes, want 2", len(r.Nodes))
	}
	if len(r.Edges) != 1 {
		t.Errorf("got %d edges, want 1", len(r.Edges))
	}
	if len(r.ExecutionPlan) != 2 {
		t.Errorf("plan length = %d, want 2", len(r.ExecutionPlan))
	}
	if r.ExecutionPlan[0].NodeID != "S" || r.ExecutionPlan[1].NodeID != "E" {
		t.Errorf("plan order wrong: %+v", r.ExecutionPlan)
	}
}

func TestSimulate_UnreachableDetection(t *testing.T) {
	src := simpleSource + `  agent Orphan
    prompt: "lonely"
`
	r, err := Simulate(src)
	if err != nil {
		t.Fatalf("Simulate: %v", err)
	}
	if len(r.Unreachable) != 1 || r.Unreachable[0] != "Orphan" {
		t.Errorf("unreachable = %v, want [Orphan]", r.Unreachable)
	}
}

func TestSimulate_EdgeConditionPropagated(t *testing.T) {
	src := `workflow X
  goal: "x"
  start: S
  exit: E
  agent S
    prompt: "hi"
  agent E
    prompt: "bye"
  S -> E when ctx.outcome = success
`
	r, err := Simulate(src)
	if err != nil {
		t.Fatalf("Simulate: %v", err)
	}
	if len(r.Edges) != 1 {
		t.Fatalf("got %d edges, want 1", len(r.Edges))
	}
	if !strings.Contains(r.Edges[0].Condition, "outcome") {
		t.Errorf("edge condition lost: %q", r.Edges[0].Condition)
	}
}

func TestSimulate_InvalidSource(t *testing.T) {
	_, err := Simulate("this is not a pipeline")
	if err == nil {
		t.Fatal("expected error for invalid source")
	}
}
```

- [ ] **Step 3.2: Run test — expect FAIL**

```bash
go test ./... -run TestSimulate -short
```

Expected: `undefined: Simulate`.

- [ ] **Step 3.3: Create `tracker/tracker_simulate.go`**

```go
// ABOUTME: Library API for dry-running a pipeline: returns the parsed graph,
// ABOUTME: BFS execution plan, and list of unreachable nodes.
package tracker

import (
	"fmt"

	"github.com/2389-research/tracker/pipeline"
)

// SimulateReport is the structured output of a dry-run over a pipeline
// source. No LLM calls, no side effects — pure graph introspection.
type SimulateReport struct {
	Format        string     `json:"format"`
	Name          string     `json:"name,omitempty"`
	StartNode     string     `json:"start_node,omitempty"`
	ExitNode      string     `json:"exit_node,omitempty"`
	Nodes         []SimNode  `json:"nodes"`
	Edges         []SimEdge  `json:"edges"`
	ExecutionPlan []PlanStep `json:"execution_plan"`
	Unreachable   []string   `json:"unreachable,omitempty"`
}

type SimNode struct {
	ID      string            `json:"id"`
	Handler string            `json:"handler,omitempty"`
	Shape   string            `json:"shape,omitempty"`
	Label   string            `json:"label,omitempty"`
	Attrs   map[string]string `json:"attrs,omitempty"`
}

type SimEdge struct {
	From      string `json:"from"`
	To        string `json:"to"`
	Label     string `json:"label,omitempty"`
	Condition string `json:"condition,omitempty"`
}

type PlanStep struct {
	Step   int       `json:"step"`
	NodeID string    `json:"node_id"`
	Edges  []SimEdge `json:"edges,omitempty"`
}

// Simulate parses source and returns a SimulateReport. The format is
// detected from content.
func Simulate(source string) (*SimulateReport, error) {
	format := detectSourceFormat(source)
	graph, err := parsePipelineSource(source, format)
	if err != nil {
		return nil, fmt.Errorf("parse pipeline: %w", err)
	}
	r := &SimulateReport{
		Format:    format,
		Name:      graph.Name,
		StartNode: graph.StartNode,
		ExitNode:  graph.ExitNode,
	}
	r.Nodes = collectSimNodes(graph)
	r.Edges = collectSimEdges(graph)
	r.ExecutionPlan, r.Unreachable = buildExecutionPlan(graph)
	return r, nil
}

func collectSimNodes(graph *pipeline.Graph) []SimNode {
	ordered := bfsNodeOrder(graph)
	out := make([]SimNode, 0, len(ordered))
	for _, n := range ordered {
		label := n.Label
		if label == n.ID {
			label = ""
		}
		out = append(out, SimNode{
			ID:      n.ID,
			Handler: n.Handler,
			Shape:   n.Shape,
			Label:   label,
			Attrs:   copyStringMap(n.Attrs),
		})
	}
	return out
}

func collectSimEdges(graph *pipeline.Graph) []SimEdge {
	out := make([]SimEdge, 0, len(graph.Edges))
	for _, e := range graph.Edges {
		out = append(out, SimEdge{
			From:      e.From,
			To:        e.To,
			Label:     e.Label,
			Condition: e.Condition,
		})
	}
	return out
}

func buildExecutionPlan(graph *pipeline.Graph) ([]PlanStep, []string) {
	if graph.StartNode == "" {
		return nil, nil
	}
	visited := make(map[string]bool)
	queue := []string{graph.StartNode}
	var plan []PlanStep
	step := 0
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if visited[id] {
			continue
		}
		visited[id] = true
		if _, ok := graph.Nodes[id]; !ok {
			continue
		}
		step++
		outs := graph.OutgoingEdges(id)
		edges := make([]SimEdge, 0, len(outs))
		for _, e := range outs {
			edges = append(edges, SimEdge{From: e.From, To: e.To, Label: e.Label, Condition: e.Condition})
			if !visited[e.To] {
				queue = append(queue, e.To)
			}
		}
		plan = append(plan, PlanStep{Step: step, NodeID: id, Edges: edges})
	}
	var unreachable []string
	for id := range graph.Nodes {
		if !visited[id] {
			unreachable = append(unreachable, id)
		}
	}
	return plan, unreachable
}

// bfsNodeOrder walks graph nodes in BFS order from start, appending orphans.
func bfsNodeOrder(graph *pipeline.Graph) []*pipeline.Node {
	visited := make(map[string]bool)
	queue := []string{graph.StartNode}
	var ordered []*pipeline.Node
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if visited[id] {
			continue
		}
		visited[id] = true
		node, ok := graph.Nodes[id]
		if !ok {
			continue
		}
		ordered = append(ordered, node)
		for _, e := range graph.OutgoingEdges(id) {
			if !visited[e.To] {
				queue = append(queue, e.To)
			}
		}
	}
	for _, node := range graph.Nodes {
		if !visited[node.ID] {
			ordered = append(ordered, node)
		}
	}
	return ordered
}

func copyStringMap(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
```

- [ ] **Step 3.4: Run Simulate tests — expect PASS**

```bash
go test ./... -run TestSimulate -short
```

Expected: all 4 subtests PASS.

- [ ] **Step 3.5: Shrink `cmd/tracker/simulate.go` to use the library**

Replace the body of `runSimulateCmd` in `cmd/tracker/simulate.go`:

```go
func runSimulateCmd(pipelineFile, formatOverride string, w io.Writer) error {
	source, info, err := tracker.ResolveSource(pipelineFile, "")
	if err != nil {
		return err
	}
	displayName := pipelineFile
	if info.Name != "" {
		displayName = info.Name
	}

	report, err := tracker.Simulate(source)
	if err != nil {
		return fmt.Errorf("load pipeline: %w", err)
	}

	// Existing validation warnings (reparse once so the CLI can print raw errors).
	if graph, gerr := loadPipelineForValidation(source, formatOverride); gerr == nil {
		if vErr := pipeline.ValidateAll(graph); vErr != nil && len(vErr.Errors) > 0 {
			fmt.Fprintln(w)
			fmt.Fprintln(w, "=== Validation Warnings ===")
			for _, e := range vErr.Errors {
				fmt.Fprintf(w, "  ! %s\n", e)
			}
		}
	}

	printSimReport(w, report, displayName)
	return nil
}

// loadPipelineForValidation reuses the CLI's format-aware parser for the
// ValidateAll side-channel; library Simulate does not surface validation
// warnings as data today.
func loadPipelineForValidation(source, formatOverride string) (*pipeline.Graph, error) {
	// loadPipeline accepts a path; reuse the existing helper if available, or
	// re-parse inline. Pick whichever matches the surrounding file.
	// Implementation: call the existing parse helpers from cmd/tracker.
	return nil, fmt.Errorf("not needed — ValidateAll runs in library path")
}
```

Then replace every `printSim<X>(w, graph, ...)` call-site with a single `printSimReport(w *SimulateReport, displayName string)` that walks the report. Keep the Unicode/border style identical to the current printers — copy the body of each `printSim<X>` but read fields from `report` instead of `graph`.

Because the CLI validation warning path currently calls `pipeline.ValidateAll(graph)`, refactor so Simulate returns the graph if needed, or parse a second time at the CLI layer. Simplest approach: keep `loadPipeline`/`loadEmbeddedPipeline` in `cmd/tracker/` and call them in parallel to `tracker.Simulate` for the warning pass. Do **not** expose `ValidateAll` in the library surface — that's a dippin-lang lint concern.

Practical instruction for the engineer: leave the existing validation-warning block in place unchanged and re-use the existing `loadPipeline` / `loadEmbeddedPipeline` helpers to produce the `*pipeline.Graph` for `ValidateAll`. Then feed `tracker.Simulate(source)` for the structured report used by `printSimReport`.

- [ ] **Step 3.6: Move every `printSim*` function in `cmd/tracker/simulate.go` to take the library report**

Rewrite each printer to read from `*tracker.SimulateReport` / `*tracker.SimNode` / `*tracker.SimEdge` / `*tracker.PlanStep` instead of `*pipeline.Graph`. Preserve the exact format strings, Unicode separators, and column widths so stdout stays byte-identical.

Key renames inside printers:

- `graph.Nodes` (map) → `report.Nodes` (slice, already BFS-ordered)
- `graph.Edges` → `report.Edges`
- `graph.StartNode` → `report.StartNode`
- `graph.ExitNode` → `report.ExitNode`
- `graph.Attrs` → gone from the report; if the CLI needs to print graph attrs, parse once via `loadPipeline` and pass the `*pipeline.Graph` alongside the report. Preferred: leave the Graph-attrs print path exactly as it was, since it's CLI-only display.
- `node.Attrs["llm_model"]` → `node.Attrs["llm_model"]` (SimNode.Attrs is a copy of the original map; same keys)

- [ ] **Step 3.7: Build + test**

```bash
go build ./...
go test ./... -short
```

Expected: build succeeds, all tests PASS.

- [ ] **Step 3.8: Smoke-test CLI output unchanged**

```bash
go build -o /tmp/tracker-test ./cmd/tracker
diff <(./tracker simulate examples/ask_and_execute.dip 2>&1) <(/tmp/tracker-test simulate examples/ask_and_execute.dip 2>&1) | head -40
```

Expected: zero diff (or only diff is from the rebuilt timestamp banner, if any).

- [ ] **Step 3.9: Commit**

```bash
git add tracker/tracker_simulate.go tracker/tracker_simulate_test.go cmd/tracker/simulate.go
git commit -m "$(cat <<'EOF'
feat(tracker): add Simulate library API

tracker.Simulate(source) returns a SimulateReport with nodes, edges,
execution plan, and unreachable node list. CLI simulate command becomes
a printer over the report; output byte-identical.

Refs #76

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 4: Diagnose library API

**Files:**
- Create: `tracker/tracker_diagnose.go`
- Create: `tracker/tracker_diagnose_test.go`
- Create: `tracker/testdata/runs/ok/`, `tracker/testdata/runs/failed/`, `tracker/testdata/runs/budget_halted/`
- Modify: `cmd/tracker/diagnose.go` (shrink `runDiagnose` / `diagnoseMostRecent` to call library)

- [ ] **Step 4.1: Build fixture run directories**

Create `tracker/testdata/runs/ok/`:

```bash
mkdir -p tracker/testdata/runs/ok
```

Then write `tracker/testdata/runs/ok/checkpoint.json`:

```json
{
  "run_id": "ok-run",
  "completed_nodes": ["Start", "End"],
  "current_node": "",
  "retry_counts": {},
  "restart_count": 0,
  "timestamp": "2026-04-17T10:00:00Z"
}
```

Write `tracker/testdata/runs/ok/activity.jsonl`:

```
{"ts":"2026-04-17T10:00:00Z","type":"pipeline_started","run_id":"ok-run"}
{"ts":"2026-04-17T10:00:01Z","type":"stage_started","node_id":"Start"}
{"ts":"2026-04-17T10:00:02Z","type":"stage_completed","node_id":"Start","handler":"agent"}
{"ts":"2026-04-17T10:00:03Z","type":"stage_started","node_id":"End"}
{"ts":"2026-04-17T10:00:04Z","type":"stage_completed","node_id":"End","handler":"agent"}
{"ts":"2026-04-17T10:00:05Z","type":"pipeline_completed","run_id":"ok-run"}
```

Create `tracker/testdata/runs/failed/`:

```bash
mkdir -p tracker/testdata/runs/failed/Build
```

`tracker/testdata/runs/failed/checkpoint.json`:

```json
{
  "run_id": "failed-run",
  "completed_nodes": ["Setup"],
  "current_node": "Build",
  "retry_counts": {"Build": 2},
  "restart_count": 0,
  "timestamp": "2026-04-17T11:00:00Z"
}
```

`tracker/testdata/runs/failed/Build/status.json`:

```json
{
  "outcome": "fail",
  "context_updates": {
    "tool_stdout": "",
    "tool_stderr": "bash: missing_tool: command not found"
  }
}
```

`tracker/testdata/runs/failed/activity.jsonl`:

```
{"ts":"2026-04-17T11:00:00Z","type":"pipeline_started"}
{"ts":"2026-04-17T11:00:01Z","type":"stage_started","node_id":"Build","handler":"tool"}
{"ts":"2026-04-17T11:00:02Z","type":"stage_failed","node_id":"Build","handler":"tool","error":"exit 127","tool_error":"missing_tool: command not found"}
{"ts":"2026-04-17T11:00:03Z","type":"stage_started","node_id":"Build","handler":"tool"}
{"ts":"2026-04-17T11:00:04Z","type":"stage_failed","node_id":"Build","handler":"tool","error":"exit 127","tool_error":"missing_tool: command not found"}
{"ts":"2026-04-17T11:00:05Z","type":"pipeline_failed"}
```

Create `tracker/testdata/runs/budget_halted/`:

```bash
mkdir -p tracker/testdata/runs/budget_halted
```

`tracker/testdata/runs/budget_halted/checkpoint.json`:

```json
{
  "run_id": "halted-run",
  "completed_nodes": ["Start"],
  "current_node": "",
  "retry_counts": {},
  "restart_count": 0,
  "timestamp": "2026-04-17T12:00:00Z"
}
```

`tracker/testdata/runs/budget_halted/activity.jsonl`:

```
{"ts":"2026-04-17T12:00:00Z","type":"pipeline_started"}
{"ts":"2026-04-17T12:00:01Z","type":"stage_completed","node_id":"Start"}
{"ts":"2026-04-17T12:00:02Z","type":"budget_exceeded","message":"max_total_tokens exceeded","total_tokens":120000,"total_cost_usd":0.45,"wall_elapsed_ms":30000}
```

- [ ] **Step 4.2: Write failing tests for `Diagnose`**

Create `tracker/tracker_diagnose_test.go`:

```go
package tracker

import (
	"testing"
)

func TestDiagnose_CleanRun(t *testing.T) {
	r, err := Diagnose("testdata/runs/ok")
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	if r.RunID != "ok-run" {
		t.Errorf("run_id = %q", r.RunID)
	}
	if len(r.Failures) != 0 {
		t.Errorf("got %d failures on clean run", len(r.Failures))
	}
	if r.BudgetHalt != nil {
		t.Errorf("unexpected budget halt: %+v", r.BudgetHalt)
	}
	if len(r.Suggestions) != 0 {
		t.Errorf("got %d suggestions on clean run", len(r.Suggestions))
	}
}

func TestDiagnose_FailureWithRetries(t *testing.T) {
	r, err := Diagnose("testdata/runs/failed")
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	if len(r.Failures) != 1 {
		t.Fatalf("got %d failures, want 1", len(r.Failures))
	}
	f := r.Failures[0]
	if f.NodeID != "Build" {
		t.Errorf("node = %q, want Build", f.NodeID)
	}
	if f.RetryCount != 2 {
		t.Errorf("retries = %d, want 2", f.RetryCount)
	}
	if !f.IdenticalRetries {
		t.Error("expected identical-retry detection")
	}
	if f.Handler != "tool" {
		t.Errorf("handler = %q", f.Handler)
	}
	// Suggestion kinds that should fire.
	kinds := map[string]bool{}
	for _, s := range r.Suggestions {
		kinds[s.Kind] = true
	}
	if !kinds["retry_pattern"] {
		t.Error("expected retry_pattern suggestion")
	}
	if !kinds["shell_command"] {
		t.Error("expected shell_command suggestion")
	}
}

func TestDiagnose_BudgetHalt(t *testing.T) {
	r, err := Diagnose("testdata/runs/budget_halted")
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	if r.BudgetHalt == nil {
		t.Fatal("expected budget halt")
	}
	if r.BudgetHalt.TotalTokens != 120000 {
		t.Errorf("tokens = %d", r.BudgetHalt.TotalTokens)
	}
	if r.BudgetHalt.Message == "" {
		t.Error("empty breach message")
	}
}
```

- [ ] **Step 4.3: Run tests — expect FAIL**

```bash
go test ./... -run TestDiagnose -short
```

Expected: `undefined: Diagnose`.

- [ ] **Step 4.4: Create `tracker/tracker_diagnose.go`**

Translate the logic from `cmd/tracker/diagnose.go` into a pure report builder. The structure mirrors the CLI's `runDiagnose` exactly but returns a `*DiagnoseReport` instead of printing. Port:

1. `collectNodeFailures` and `loadNodeFailure` → private helpers reading `status.json`.
2. `enrichFromActivity` / `parseActivityLines` / `enrichFromEntry` / `processStageEvent` / `enrichNodeFailure` / `updateFailureTiming` / `applyRetryAnalysis` / `allIdentical` → copied verbatim, but write to `NodeFailure` fields (exported names) instead of private `nodeFailure`.
3. `budgetHalt` → replaced with the exported `BudgetHalt`.
4. `suggestionsForFailure` + every `suggest*Pattern` → each emits a `Suggestion` with the appropriate `Kind`. The existing prose becomes `Suggestion.Message`.

```go
// ABOUTME: Library API for diagnosing pipeline run failures.
// ABOUTME: Reads checkpoint + status.json + activity.jsonl and returns a structured report.
package tracker

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/2389-research/tracker/pipeline"
)

type DiagnoseReport struct {
	RunID          string        `json:"run_id"`
	CompletedNodes int           `json:"completed_nodes"`
	BudgetHalt     *BudgetHalt   `json:"budget_halt,omitempty"`
	Failures       []NodeFailure `json:"failures"`
	Suggestions    []Suggestion  `json:"suggestions"`
}

type NodeFailure struct {
	NodeID           string        `json:"node_id"`
	Outcome          string        `json:"outcome"`
	Handler          string        `json:"handler,omitempty"`
	Duration         time.Duration `json:"duration_ns,omitempty"`
	RetryCount       int           `json:"retry_count,omitempty"`
	IdenticalRetries bool          `json:"identical_retries,omitempty"`
	Stdout           string        `json:"stdout,omitempty"`
	Stderr           string        `json:"stderr,omitempty"`
	Errors           []string      `json:"errors,omitempty"`
}

type BudgetHalt struct {
	TotalTokens   int     `json:"total_tokens"`
	TotalCostUSD  float64 `json:"total_cost_usd"`
	WallElapsedMs int64   `json:"wall_elapsed_ms"`
	Message       string  `json:"message"`
}

type Suggestion struct {
	NodeID  string `json:"node_id,omitempty"`
	Kind    string `json:"kind"`
	Message string `json:"message"`
}

// Suggestion kinds (stable; new ones may be added additively).
const (
	SuggestionRetryPattern     = "retry_pattern"
	SuggestionEscalateLimit    = "escalate_limit"
	SuggestionNoOutput         = "no_output"
	SuggestionShellCommand     = "shell_command"
	SuggestionGoTest           = "go_test"
	SuggestionSuspiciousTiming = "suspicious_timing"
	SuggestionBudget           = "budget"
)

// Diagnose analyzes a run directory and returns a structured report.
func Diagnose(runDir string) (*DiagnoseReport, error) {
	cpPath := filepath.Join(runDir, "checkpoint.json")
	cp, err := pipeline.LoadCheckpoint(cpPath)
	if err != nil {
		return nil, fmt.Errorf("load checkpoint: %w", err)
	}
	report := &DiagnoseReport{
		RunID:          cp.RunID,
		CompletedNodes: len(cp.CompletedNodes),
	}
	failures := collectNodeFailures(runDir)
	report.BudgetHalt = enrichFromActivity(runDir, failures)
	report.Failures = sortedFailures(failures)
	report.Suggestions = buildSuggestions(report.Failures, report.BudgetHalt)
	return report, nil
}

// DiagnoseMostRecent finds the most recent run under workdir and diagnoses it.
func DiagnoseMostRecent(workdir string) (*DiagnoseReport, error) {
	id, err := MostRecentRunID(workdir)
	if err != nil {
		return nil, err
	}
	return Diagnose(filepath.Join(workdir, ".tracker", "runs", id))
}

// ----- internals below this line mirror cmd/tracker/diagnose.go structure -----

func collectNodeFailures(runDir string) map[string]*NodeFailure {
	failures := make(map[string]*NodeFailure)
	entries, err := os.ReadDir(runDir)
	if err != nil {
		return failures
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if f := loadNodeFailure(runDir, e.Name()); f != nil {
			failures[e.Name()] = f
		}
	}
	return failures
}

func loadNodeFailure(runDir, nodeID string) *NodeFailure {
	statusPath := filepath.Join(runDir, nodeID, "status.json")
	data, err := os.ReadFile(statusPath)
	if err != nil {
		return nil
	}
	var status struct {
		Outcome        string            `json:"outcome"`
		ContextUpdates map[string]string `json:"context_updates"`
	}
	if err := json.Unmarshal(data, &status); err != nil {
		return nil
	}
	if status.Outcome != "fail" {
		return nil
	}
	f := &NodeFailure{NodeID: nodeID, Outcome: status.Outcome}
	if status.ContextUpdates != nil {
		f.Stdout = status.ContextUpdates["tool_stdout"]
		f.Stderr = status.ContextUpdates["tool_stderr"]
	}
	return f
}

type diagnoseEntry struct {
	Timestamp     string  `json:"ts"`
	Type          string  `json:"type"`
	NodeID        string  `json:"node_id"`
	Message       string  `json:"message"`
	Error         string  `json:"error"`
	ToolErr       string  `json:"tool_error"`
	Handler       string  `json:"handler"`
	TotalTokens   int     `json:"total_tokens"`
	TotalCostUSD  float64 `json:"total_cost_usd"`
	WallElapsedMs int64   `json:"wall_elapsed_ms"`
}

func enrichFromActivity(runDir string, failures map[string]*NodeFailure) *BudgetHalt {
	path := filepath.Join(runDir, "activity.jsonl")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	stageStarts := map[string]time.Time{}
	failSignatures := map[string][]string{}
	halt := parseActivityLinesForDiagnose(string(data), failures, stageStarts, failSignatures)
	applyRetryAnalysis(failures, failSignatures)
	return halt
}

func parseActivityLinesForDiagnose(data string, failures map[string]*NodeFailure, stageStarts map[string]time.Time, failSignatures map[string][]string) *BudgetHalt {
	var halt *BudgetHalt
	for _, line := range strings.Split(data, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var entry diagnoseEntry
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			continue
		}
		if entry.Type == "budget_exceeded" {
			halt = &BudgetHalt{
				TotalTokens:   entry.TotalTokens,
				TotalCostUSD:  entry.TotalCostUSD,
				WallElapsedMs: entry.WallElapsedMs,
				Message:       entry.Message,
			}
		}
		enrichFromEntryNF(entry, failures, stageStarts, failSignatures)
	}
	return halt
}

func enrichFromEntryNF(entry diagnoseEntry, failures map[string]*NodeFailure, stageStarts map[string]time.Time, failSignatures map[string][]string) {
	ts, _ := time.Parse(time.RFC3339Nano, entry.Timestamp)
	switch entry.Type {
	case "stage_started":
		stageStarts[entry.NodeID] = ts
	case "stage_failed":
		updateFailureTimingNF(failures[entry.NodeID], stageStarts, entry, ts)
		sig := entry.Error + "\x00" + entry.ToolErr
		failSignatures[entry.NodeID] = append(failSignatures[entry.NodeID], sig)
	case "stage_completed":
		updateFailureTimingNF(failures[entry.NodeID], stageStarts, entry, ts)
	}
	if entry.NodeID == "" {
		return
	}
	f, ok := failures[entry.NodeID]
	if !ok {
		return
	}
	if entry.Error != "" {
		f.Errors = append(f.Errors, entry.Error)
	}
	if entry.ToolErr != "" && f.Stderr == "" {
		f.Stderr = entry.ToolErr
	}
}

func updateFailureTimingNF(f *NodeFailure, stageStarts map[string]time.Time, entry diagnoseEntry, ts time.Time) {
	if f == nil {
		return
	}
	if start, ok := stageStarts[entry.NodeID]; ok && !ts.IsZero() {
		f.Duration = ts.Sub(start)
	}
	if entry.Handler != "" {
		f.Handler = entry.Handler
	}
}

func applyRetryAnalysis(failures map[string]*NodeFailure, failSignatures map[string][]string) {
	for nodeID, sigs := range failSignatures {
		f, ok := failures[nodeID]
		if !ok {
			continue
		}
		f.RetryCount = len(sigs)
		if len(sigs) >= 2 {
			f.IdenticalRetries = allIdenticalStrings(sigs)
		}
	}
}

func allIdenticalStrings(ss []string) bool {
	if len(ss) < 2 {
		return false
	}
	for i := 1; i < len(ss); i++ {
		if ss[i] != ss[0] {
			return false
		}
	}
	return true
}

func sortedFailures(m map[string]*NodeFailure) []NodeFailure {
	ids := make([]string, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]NodeFailure, 0, len(ids))
	for _, id := range ids {
		out = append(out, *m[id])
	}
	return out
}

func buildSuggestions(failures []NodeFailure, halt *BudgetHalt) []Suggestion {
	var out []Suggestion
	for _, f := range failures {
		out = append(out, suggestionsForFailure(f)...)
	}
	if halt != nil {
		out = append(out, Suggestion{
			Kind:    SuggestionBudget,
			Message: "Raise the relevant --max-tokens, --max-cost, or --max-wall-time flag, or remove the Config.Budget value",
		})
	}
	return out
}

func suggestionsForFailure(f NodeFailure) []Suggestion {
	var out []Suggestion
	if f.IdenticalRetries && f.RetryCount >= 2 {
		out = append(out, Suggestion{
			NodeID: f.NodeID, Kind: SuggestionRetryPattern,
			Message: fmt.Sprintf("%s: Failed %d times with identical errors — this is a deterministic bug in the command, not a transient failure. Retrying won't help. Fix the tool command in the .dip file and re-run.", f.NodeID, f.RetryCount),
		})
	} else if f.RetryCount >= 3 {
		out = append(out, Suggestion{
			NodeID: f.NodeID, Kind: SuggestionRetryPattern,
			Message: fmt.Sprintf("%s: Failed %d times with varying errors — may be a flaky command or environment issue.", f.NodeID, f.RetryCount),
		})
	}
	if strings.Contains(f.Stdout, "ESCALATE") && strings.Contains(f.Stdout, "fix attempts") {
		out = append(out, Suggestion{
			NodeID: f.NodeID, Kind: SuggestionEscalateLimit,
			Message: fmt.Sprintf("%s: Hit fix attempt limit. The fix_attempts counter persists on disk across restarts — if you retry after escalation, the counter is already maxed. Reset it with: rm .ai/milestones/fix_attempts", f.NodeID),
		})
	}
	if f.Stdout == "" && f.Stderr == "" && len(f.Errors) == 0 {
		out = append(out, Suggestion{
			NodeID: f.NodeID, Kind: SuggestionNoOutput,
			Message: fmt.Sprintf("%s: No error details captured. Check the activity.jsonl for this node's events: grep %q activity.jsonl | tail -20", f.NodeID, f.NodeID),
		})
	}
	if strings.Contains(f.Stderr, "command not found") || strings.Contains(f.Stderr, "No such file or directory") {
		out = append(out, Suggestion{
			NodeID: f.NodeID, Kind: SuggestionShellCommand,
			Message: fmt.Sprintf("%s: Shell command failed — check that the working directory and required tools exist before running", f.NodeID),
		})
	}
	if strings.Contains(f.Stdout, "FAIL") && strings.Contains(f.Stdout, "go test") {
		out = append(out, Suggestion{
			NodeID: f.NodeID, Kind: SuggestionGoTest,
			Message: fmt.Sprintf("%s: Go test failures — check if .ai/milestones/known_failures should include these tests for this milestone", f.NodeID),
		})
	}
	if f.Duration > 0 && f.Duration < 50*time.Millisecond && f.Handler != "tool" {
		out = append(out, Suggestion{
			NodeID: f.NodeID, Kind: SuggestionSuspiciousTiming,
			Message: fmt.Sprintf("%s: Completed in %s — suspiciously fast. May indicate a configuration issue or missing handler", f.NodeID, f.Duration),
		})
	}
	return out
}
```

- [ ] **Step 4.5: Run Diagnose tests — expect PASS**

```bash
go test ./... -run TestDiagnose -short
```

Expected: all 3 subtests PASS.

- [ ] **Step 4.6: Shrink `cmd/tracker/diagnose.go`**

Replace the body of `runDiagnose`:

```go
func runDiagnose(workdir, runID string) error {
	runDir, err := tracker.ResolveRunDir(workdir, runID)
	if err != nil {
		return err
	}
	report, err := tracker.Diagnose(runDir)
	if err != nil {
		return err
	}
	printDiagnoseReport(report)
	return nil
}
```

And replace `diagnoseMostRecent`:

```go
func diagnoseMostRecent(workdir string) error {
	report, err := tracker.DiagnoseMostRecent(workdir)
	if err != nil {
		return err
	}
	printDiagnoseReport(report)
	return nil
}
```

Rewrite every `print*` helper in `cmd/tracker/diagnose.go` to consume `*tracker.DiagnoseReport`, `tracker.NodeFailure`, `*tracker.BudgetHalt`, and `tracker.Suggestion` instead of the deleted private types. Keep lipgloss styles and format strings identical so stdout is byte-for-byte unchanged.

Delete the now-unused private types (`nodeFailure`, `budgetHalt`, `diagnoseEntry`) and helpers (`collectNodeFailures`, `loadNodeFailure`, `enrichFromActivity`, `parseActivityLines`, `enrichFromEntry`, `processStageEvent`, `enrichNodeFailure`, `updateFailureTiming`, `applyRetryAnalysis`, `allIdentical`, every `suggest*Pattern`).

The printer suggestions section should iterate `report.Suggestions` and print `.Message` — same prose as before.

- [ ] **Step 4.7: Build + test**

```bash
go build ./...
go test ./... -short
```

Expected: all packages PASS.

- [ ] **Step 4.8: Smoke-test CLI output unchanged**

```bash
go build -o /tmp/tracker-test ./cmd/tracker
# Fabricate a failing run locally or point at an existing one in .tracker/runs/
./tracker diagnose 2>&1 | tee /tmp/before.txt | head -40 || true
/tmp/tracker-test diagnose 2>&1 | tee /tmp/after.txt | head -40 || true
diff /tmp/before.txt /tmp/after.txt
```

Expected: zero diff.

- [ ] **Step 4.9: Commit**

```bash
git add tracker/tracker_diagnose.go tracker/tracker_diagnose_test.go tracker/testdata/ cmd/tracker/diagnose.go
git commit -m "$(cat <<'EOF'
feat(tracker): add Diagnose library API

tracker.Diagnose(runDir) and tracker.DiagnoseMostRecent(workdir) return
a structured DiagnoseReport with failures, budget halt, and typed
suggestions. CLI diagnose command shrunk to a printer over the report;
output byte-identical.

Refs #76

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 5: Audit library API (+ ListRuns)

**Files:**
- Create: `tracker/tracker_audit.go`
- Create: `tracker/tracker_audit_test.go`
- Modify: `cmd/tracker/audit.go` (shrink `runAudit` / `listRuns` to call library)

- [ ] **Step 5.1: Write failing tests for `Audit` and `ListRuns`**

Create `tracker/tracker_audit_test.go`:

```go
package tracker

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAudit_CompletedRun(t *testing.T) {
	r, err := Audit("testdata/runs/ok")
	if err != nil {
		t.Fatalf("Audit: %v", err)
	}
	if r.Status != "success" {
		t.Errorf("status = %q, want success", r.Status)
	}
	if len(r.Timeline) == 0 {
		t.Error("empty timeline")
	}
	if r.TotalDuration <= 0 {
		t.Error("expected positive total duration")
	}
}

func TestAudit_FailedRun(t *testing.T) {
	r, err := Audit("testdata/runs/failed")
	if err != nil {
		t.Fatalf("Audit: %v", err)
	}
	if r.Status != "fail" {
		t.Errorf("status = %q, want fail", r.Status)
	}
	var foundRetry bool
	for _, rec := range r.Retries {
		if rec.NodeID == "Build" && rec.Attempts == 2 {
			foundRetry = true
		}
	}
	if !foundRetry {
		t.Errorf("missing Build retry record: %+v", r.Retries)
	}
	if len(r.Errors) == 0 {
		t.Error("expected error entries")
	}
}

func TestListRuns_MultipleRuns(t *testing.T) {
	workdir := t.TempDir()
	runsDir := filepath.Join(workdir, ".tracker", "runs")
	must(t, os.MkdirAll(filepath.Join(runsDir, "r1"), 0o755))
	must(t, os.WriteFile(filepath.Join(runsDir, "r1", "checkpoint.json"),
		[]byte(`{"run_id":"r1","completed_nodes":["A"],"timestamp":"2026-04-17T10:00:00Z"}`), 0o644))
	must(t, os.MkdirAll(filepath.Join(runsDir, "r2"), 0o755))
	must(t, os.WriteFile(filepath.Join(runsDir, "r2", "checkpoint.json"),
		[]byte(`{"run_id":"r2","completed_nodes":["A","B"],"timestamp":"2026-04-17T11:00:00Z"}`), 0o644))

	runs, err := ListRuns(workdir)
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(runs) != 2 {
		t.Fatalf("got %d runs, want 2", len(runs))
	}
	// Sorted newest first.
	if runs[0].RunID != "r2" {
		t.Errorf("first = %q, want r2", runs[0].RunID)
	}
}
```

- [ ] **Step 5.2: Run tests — expect FAIL**

```bash
go test ./... -run "TestAudit|TestListRuns" -short
```

Expected: `undefined: Audit` / `undefined: ListRuns`.

- [ ] **Step 5.3: Create `tracker/tracker_audit.go`**

```go
// ABOUTME: Library API for auditing a completed pipeline run.
// ABOUTME: Returns structured timeline, retries, errors, and recommendations.
package tracker

import (
	"fmt"
	"path/filepath"
	"sort"
	"time"

	"github.com/2389-research/tracker/pipeline"
)

type AuditReport struct {
	RunID           string          `json:"run_id"`
	Status          string          `json:"status"`
	TotalDuration   time.Duration   `json:"total_duration_ns"`
	Timeline        []TimelineEntry `json:"timeline"`
	Retries         []RetryRecord   `json:"retries,omitempty"`
	Errors          []ActivityError `json:"errors,omitempty"`
	Recommendations []string        `json:"recommendations,omitempty"`
}

type TimelineEntry struct {
	Timestamp time.Time     `json:"ts"`
	Type      string        `json:"type"`
	NodeID    string        `json:"node_id,omitempty"`
	Message   string        `json:"message,omitempty"`
	Duration  time.Duration `json:"duration_ns,omitempty"`
}

type RetryRecord struct {
	NodeID   string `json:"node_id"`
	Attempts int    `json:"attempts"`
}

type ActivityError struct {
	Timestamp time.Time `json:"ts"`
	NodeID    string    `json:"node_id,omitempty"`
	Message   string    `json:"message"`
}

type RunSummary struct {
	RunID     string        `json:"run_id"`
	Status    string        `json:"status"`
	Nodes     int           `json:"nodes"`
	Retries   int           `json:"retries"`
	Restarts  int           `json:"restarts"`
	Timestamp time.Time     `json:"timestamp"`
	Duration  time.Duration `json:"duration_ns"`
	FailedAt  string        `json:"failed_at,omitempty"`
}

// Audit reads checkpoint.json and activity.jsonl under runDir and returns a
// structured report.
func Audit(runDir string) (*AuditReport, error) {
	cp, err := pipeline.LoadCheckpoint(filepath.Join(runDir, "checkpoint.json"))
	if err != nil {
		return nil, fmt.Errorf("load checkpoint: %w", err)
	}
	activity, err := LoadActivityLog(runDir)
	if err != nil {
		return nil, fmt.Errorf("load activity log: %w", err)
	}
	SortActivityByTime(activity)

	status := classifyStatus(cp, activity)
	r := &AuditReport{
		RunID:    cp.RunID,
		Status:   status,
		Timeline: buildTimeline(activity),
		Retries:  buildRetryRecords(cp),
		Errors:   buildActivityErrors(activity),
	}
	if len(activity) >= 2 {
		r.TotalDuration = activity[len(activity)-1].Timestamp.Sub(activity[0].Timestamp)
	}
	r.Recommendations = buildAuditRecommendations(cp, status, r.TotalDuration)
	return r, nil
}

// ListRuns returns all runs under workdir/.tracker/runs, sorted newest first.
func ListRuns(workdir string) ([]RunSummary, error) {
	runsDir := filepath.Join(workdir, ".tracker", "runs")
	entries, err := osReadDir(runsDir)
	if err != nil {
		return nil, err
	}
	var runs []RunSummary
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		rs, ok := buildRunSummary(runsDir, e.Name())
		if ok {
			runs = append(runs, rs)
		}
	}
	sort.Slice(runs, func(i, j int) bool { return runs[i].Timestamp.After(runs[j].Timestamp) })
	return runs, nil
}

func classifyStatus(cp *pipeline.Checkpoint, activity []ActivityEntry) string {
	for i := len(activity) - 1; i >= 0; i-- {
		switch activity[i].Type {
		case "pipeline_completed":
			return "success"
		case "pipeline_failed":
			return "fail"
		}
	}
	if cp.CurrentNode != "" {
		return "fail"
	}
	return "success"
}

func buildTimeline(activity []ActivityEntry) []TimelineEntry {
	out := make([]TimelineEntry, 0, len(activity))
	stageStarts := map[string]time.Time{}
	for _, entry := range activity {
		e := TimelineEntry{Timestamp: entry.Timestamp, Type: entry.Type, NodeID: entry.NodeID, Message: entry.Message}
		switch entry.Type {
		case "stage_started":
			stageStarts[entry.NodeID] = entry.Timestamp
		case "stage_completed", "stage_failed":
			if start, ok := stageStarts[entry.NodeID]; ok {
				e.Duration = entry.Timestamp.Sub(start)
				delete(stageStarts, entry.NodeID)
			}
		}
		out = append(out, e)
	}
	return out
}

func buildRetryRecords(cp *pipeline.Checkpoint) []RetryRecord {
	if len(cp.RetryCounts) == 0 {
		return nil
	}
	ids := make([]string, 0, len(cp.RetryCounts))
	for id := range cp.RetryCounts {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]RetryRecord, 0, len(ids))
	for _, id := range ids {
		out = append(out, RetryRecord{NodeID: id, Attempts: cp.RetryCounts[id]})
	}
	return out
}

func buildActivityErrors(activity []ActivityEntry) []ActivityError {
	var out []ActivityError
	for _, e := range activity {
		if e.Error == "" {
			continue
		}
		out = append(out, ActivityError{Timestamp: e.Timestamp, NodeID: e.NodeID, Message: e.Error})
	}
	return out
}

func buildAuditRecommendations(cp *pipeline.Checkpoint, status string, total time.Duration) []string {
	var recs []string
	for nodeID, count := range cp.RetryCounts {
		if count >= 2 {
			recs = append(recs, fmt.Sprintf("Consider adjusting retry_policy for %s (used %d retries)", nodeID, count))
		}
	}
	if cp.RestartCount > 0 {
		suffix := "time"
		if cp.RestartCount > 1 {
			suffix = "times"
		}
		recs = append(recs, fmt.Sprintf("Pipeline restarted %d %s — review loop conditions", cp.RestartCount, suffix))
	}
	if total > 30*time.Minute {
		recs = append(recs, "Long-running pipeline — consider fidelity=summary:medium for faster resumes")
	}
	if status == "fail" && cp.CurrentNode != "" {
		recs = append(recs, fmt.Sprintf("Pipeline failed at %s — check error details above", cp.CurrentNode))
	}
	sort.Strings(recs)
	return recs
}

func buildRunSummary(runsDir, name string) (RunSummary, bool) {
	runDir := filepath.Join(runsDir, name)
	cp, err := pipeline.LoadCheckpoint(filepath.Join(runDir, "checkpoint.json"))
	if err != nil {
		return RunSummary{}, false
	}
	activity, _ := LoadActivityLog(runDir)
	SortActivityByTime(activity)
	status := classifyStatus(cp, activity)
	totalRetries := 0
	for _, c := range cp.RetryCounts {
		totalRetries += c
	}
	var dur time.Duration
	if len(activity) >= 2 {
		dur = activity[len(activity)-1].Timestamp.Sub(activity[0].Timestamp)
	}
	rs := RunSummary{
		RunID: name, Status: status, Nodes: len(cp.CompletedNodes),
		Retries: totalRetries, Restarts: cp.RestartCount,
		Timestamp: cp.Timestamp, Duration: dur,
	}
	if status == "fail" {
		rs.FailedAt = cp.CurrentNode
	}
	return rs, true
}

// osReadDir exists only so tests can stub readdir if ever needed. Implementation
// is trivial today — delegate to os.ReadDir.
func osReadDir(path string) ([]osDirEntry, error) {
	entries, err := osReadDirRaw(path)
	if err != nil {
		return nil, err
	}
	return entries, nil
}
```

Replace the bottom two helpers (`osReadDir` / `osDirEntry`) with:

```go
import osnative "os"

type osDirEntry = osnative.DirEntry

func osReadDirRaw(path string) ([]osnative.DirEntry, error) {
	entries, err := osnative.ReadDir(path)
	if err != nil {
		if osnative.IsNotExist(err) {
			return nil, fmt.Errorf("no runs found — run a pipeline first")
		}
		return nil, fmt.Errorf("cannot read runs directory: %w", err)
	}
	return entries, nil
}
```

(The indirection is optional; simpler: use `os.ReadDir` directly. The reason to split is only if future tests want to stub it — YAGNI says skip unless a test needs it.)

**Cleaner inline version — use this instead of the two helpers above:**

```go
import "os"

// in ListRuns body
entries, err := os.ReadDir(runsDir)
if err != nil {
    if os.IsNotExist(err) {
        return nil, nil
    }
    return nil, fmt.Errorf("cannot read runs directory: %w", err)
}
```

- [ ] **Step 5.4: Run Audit tests — expect PASS**

```bash
go test ./... -run "TestAudit|TestListRuns" -short
```

Expected: all 3 subtests PASS.

- [ ] **Step 5.5: Shrink `cmd/tracker/audit.go`**

Replace the body of `runAudit`:

```go
func runAudit(workdir, runID string) error {
	runDir, err := tracker.ResolveRunDir(workdir, runID)
	if err != nil {
		return err
	}
	report, err := tracker.Audit(runDir)
	if err != nil {
		return err
	}
	printAuditReport(report)
	return nil
}
```

Replace the body of `listRuns`:

```go
func listRuns(workdir string) error {
	runs, err := tracker.ListRuns(workdir)
	if err != nil {
		return err
	}
	if len(runs) == 0 {
		fmt.Println("No runs found. Run a pipeline first.")
		return nil
	}
	printRunList(runs)
	return nil
}
```

Rewrite every `print*` function in `cmd/tracker/audit.go` to consume `*tracker.AuditReport` and `tracker.RunSummary` instead of the deleted private types. Preserve format strings and Unicode separators exactly.

Delete the now-unused private types and helpers (`runSummary`, `buildRunSummary`, `collectRunSummaries`, `determinePipelineStatus`, `buildRecommendations`, `retryRecommendations`, `restartRecommendation`, `durationRecommendation`).

- [ ] **Step 5.6: Build + test**

```bash
go build ./...
go test ./... -short
```

Expected: all packages PASS.

- [ ] **Step 5.7: Smoke-test CLI output unchanged**

```bash
go build -o /tmp/tracker-test ./cmd/tracker
diff <(./tracker audit 2>&1) <(/tmp/tracker-test audit 2>&1)
```

Expected: zero diff. If there's no run to audit, both sides print the same "no runs found" message.

- [ ] **Step 5.8: Commit**

```bash
git add tracker/tracker_audit.go tracker/tracker_audit_test.go cmd/tracker/audit.go
git commit -m "$(cat <<'EOF'
feat(tracker): add Audit + ListRuns library APIs

tracker.Audit(runDir) returns a structured AuditReport (timeline,
retries, errors, recommendations). tracker.ListRuns(workdir) returns
[]RunSummary sorted newest first. CLI audit command shrunk to a printer
over the reports; output byte-identical.

Refs #76

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 6: Doctor library API

**Files:**
- Create: `tracker/tracker_doctor.go`
- Create: `tracker/tracker_doctor_test.go`
- Modify: `cmd/tracker/doctor.go` (shrink `runDoctorWithConfig` to call library for checks; retain gitignore-fix / workdir-create write paths)

- [ ] **Step 6.1: Write failing test for `Doctor`**

Create `tracker/tracker_doctor_test.go`:

```go
package tracker

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDoctor_NoProbe_KeyPresent(t *testing.T) {
	workdir := t.TempDir()
	must(t, os.Setenv("ANTHROPIC_API_KEY", "sk-ant-test-12345678901234567890"))
	t.Cleanup(func() { os.Unsetenv("ANTHROPIC_API_KEY") })

	r, err := Doctor(DoctorConfig{WorkDir: workdir, ProbeProviders: false})
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}
	var providersCheck *CheckResult
	for i := range r.Checks {
		if r.Checks[i].Name == "LLM Providers" {
			providersCheck = &r.Checks[i]
		}
	}
	if providersCheck == nil {
		t.Fatal("LLM Providers check not found")
	}
	if providersCheck.Status != "ok" && providersCheck.Status != "skip" {
		t.Errorf("LLM Providers status = %q, want ok or skip", providersCheck.Status)
	}
}

func TestDoctor_NoProviderKeys(t *testing.T) {
	workdir := t.TempDir()
	for _, k := range []string{"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "GEMINI_API_KEY"} {
		os.Unsetenv(k)
	}

	r, err := Doctor(DoctorConfig{WorkDir: workdir, ProbeProviders: false})
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}
	if r.OK {
		t.Error("expected OK=false when no providers configured")
	}
}

func TestDoctor_PipelineFileValidation(t *testing.T) {
	workdir := t.TempDir()
	pf := filepath.Join(workdir, "ok.dip")
	must(t, os.WriteFile(pf, []byte(simpleSource), 0o644))

	r, err := Doctor(DoctorConfig{WorkDir: workdir, PipelineFile: pf, ProbeProviders: false})
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}
	var pipelineCheck *CheckResult
	for i := range r.Checks {
		if r.Checks[i].Name == "Pipeline File" {
			pipelineCheck = &r.Checks[i]
		}
	}
	if pipelineCheck == nil {
		t.Fatal("Pipeline File check missing when PipelineFile set")
	}
}
```

- [ ] **Step 6.2: Run tests — expect FAIL**

```bash
go test ./... -run TestDoctor -short
```

Expected: `undefined: Doctor`.

- [ ] **Step 6.3: Create `tracker/tracker_doctor.go`**

```go
// ABOUTME: Library API for preflight health checks.
// ABOUTME: Pure read-only — no network probes unless ProbeProviders: true.
package tracker

import (
	"fmt"
	"os"
	"strings"
)

type DoctorConfig struct {
	WorkDir        string
	Backend        string
	ProbeProviders bool
	PipelineFile   string
}

type DoctorReport struct {
	Checks   []CheckResult `json:"checks"`
	OK       bool          `json:"ok"`
	Warnings int           `json:"warnings"`
	Errors   int           `json:"errors"`
}

type CheckResult struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
	Hint    string `json:"hint,omitempty"`
}

// Doctor runs a series of read-only preflight checks and returns a report.
// No side effects unless ProbeProviders is true (which makes real API calls).
func Doctor(cfg DoctorConfig) (*DoctorReport, error) {
	if cfg.WorkDir == "" {
		wd, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("cannot determine working directory: %w", err)
		}
		cfg.WorkDir = wd
	}
	r := &DoctorReport{}

	r.Checks = append(r.Checks,
		checkEnvWarningsLib(),
		checkProvidersLib(cfg.ProbeProviders),
		checkDippinLib(),
		checkVersionCompatLib(),
		checkOtherBinariesLib(cfg.Backend),
		checkWorkdirLib(cfg.WorkDir),
		checkArtifactDirsLib(cfg.WorkDir),
		checkDiskSpaceLib(cfg.WorkDir),
	)
	if cfg.PipelineFile != "" {
		r.Checks = append(r.Checks, checkPipelineFileLib(cfg.PipelineFile))
	}

	r.OK = true
	for _, c := range r.Checks {
		switch c.Status {
		case "warn":
			r.Warnings++
		case "error":
			r.Errors++
			r.OK = false
		}
	}
	return r, nil
}

// The check* functions below mirror cmd/tracker/doctor.go, returning
// CheckResult instead of printing. For each check currently in the CLI,
// port the body verbatim and translate:
//   - ok == true             -> Status "ok"
//   - warn == true           -> Status "warn"
//   - required && !ok        -> Status "error"
//   - !required && !ok       -> Status "warn"
//   - probe == false + well-formed key -> Status "ok" (without probing)
//   - probe == false + no key -> Status "skip" with hint
```

Append each `check*Lib` function by porting the body of the corresponding `check*` in `cmd/tracker/doctor.go`. For `checkProvidersLib`, add the opt-in probe branch:

```go
func checkProvidersLib(probe bool) CheckResult {
	// Enumerate providers same as CLI's checkProviders.
	// For each provider:
	//   envKey := os.Getenv(p.EnvVar)
	//   if envKey == "" { continue }
	//   if !probe { mark as "ok" with key name; continue }
	//   // else: call probeProvider(p, envKey) and map result to ok/warn/error.
	// If no keys found: Status "error", Message "no providers configured",
	// Hint "Set ANTHROPIC_API_KEY, OPENAI_API_KEY, or GEMINI_API_KEY".
	// ... (port remainder from cmd/tracker/doctor.go checkProviders)
}
```

**Implementation note for the engineer:** this task is the largest of the six because doctor.go has the most check functions. Port each check one at a time with a matching test (or reuse the tests above); do not try to port all eight checks in a single commit.

- [ ] **Step 6.4: Run Doctor tests — expect PASS**

```bash
go test ./... -run TestDoctor -short
```

Expected: all 3 subtests PASS.

- [ ] **Step 6.5: Shrink `cmd/tracker/doctor.go`**

Replace the body of `runDoctorWithConfig` with:

```go
func runDoctorWithConfig(workdir string, cfg DoctorConfig) error {
	report, err := tracker.Doctor(tracker.DoctorConfig{
		WorkDir:        workdir,
		Backend:        cfg.backend,
		ProbeProviders: cfg.probe,  // CLI always probes
		PipelineFile:   cfg.pipelineFile,
	})
	if err != nil {
		return err
	}

	fmt.Println()
	fmt.Println(bannerStyle.Render("tracker doctor"))
	fmt.Println()

	for _, c := range report.Checks {
		fmt.Printf("  %s\n", c.Name)
		printCheckResult(c)
		maybeApplyWriteFixup(c, workdir)  // retains CLI-only gitignore / workdir-create
		fmt.Println()
	}

	printDoctorSummary(report)
	fmt.Println()
	fmt.Println(mutedStyle.Render("  exit codes: 0=all pass  1=failures  2=warnings only"))
	fmt.Println()

	if report.Errors > 0 {
		return fmt.Errorf("health check failed")
	}
	if report.Warnings > 0 {
		return &DoctorWarningsError{Warnings: report.Warnings}
	}
	return nil
}
```

Keep the local `DoctorConfig` / `DoctorResult` / `DoctorWarningsError` types as they are — the CLI config struct is distinct from the library config struct.

Move write side effects (gitignore auto-add prompt, `.tracker/` auto-create) into a new `maybeApplyWriteFixup(c tracker.CheckResult, workdir string)` helper in `cmd/tracker/doctor.go`. Do NOT invoke them from the library.

Delete the now-unused private `check*` functions that have been ported (`checkEnvWarnings`, `checkProviders`, `checkDippin`, `checkVersionCompat`, `checkOtherBinaries`, `checkWorkdir`, `checkArtifactDirs`, `checkDiskSpace`, `checkPipelineFile`, plus their helpers `checkDippinVersionMismatch`, `parseVersionMajorMinor`, `probeProvider`, `findProviderKey`, `getDippinVersion`, `getBinaryVersion`, `isAuthError`, `trimErrMsg`, `maskKey`, `isValidAPIKey`, `isDirWritable`, `needsCompositeResultLine`, `buildChecks`, the top-level `checkGitignore`).

Keep `formatLLMClientError`, `printCheck`, `printWarn`, `printHint`, `printSummary` (renamed to `printDoctorSummary` for clarity) — these are CLI-only concerns.

- [ ] **Step 6.6: Build + test**

```bash
go build ./...
go test ./... -short
```

Expected: all packages PASS.

- [ ] **Step 6.7: Smoke-test CLI doctor output unchanged**

```bash
go build -o /tmp/tracker-test ./cmd/tracker
diff <(./tracker doctor 2>&1) <(/tmp/tracker-test doctor 2>&1) | head -30
```

Expected: zero diff. (Minor differences acceptable only if they stem from runtime state that changed between invocations — e.g., the disk-space check.)

- [ ] **Step 6.8: Commit**

```bash
git add tracker/tracker_doctor.go tracker/tracker_doctor_test.go cmd/tracker/doctor.go
git commit -m "$(cat <<'EOF'
feat(tracker): add Doctor library API with opt-in provider probe

tracker.Doctor(cfg) runs preflight checks and returns a DoctorReport.
ProbeProviders defaults to false (no network calls); CLI sets it true
to preserve today's behavior. Write side effects (gitignore fixup,
workdir create) stay CLI-only.

Refs #76

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

---

## Task 7: Documentation & final validation

**Files:**
- Modify: `CHANGELOG.md`
- Modify: `README.md`

- [ ] **Step 7.1: Add CHANGELOG entry**

Open `CHANGELOG.md`. Under the top-most `## [Unreleased]` section (creating one if missing), add:

```markdown
### Added

- `tracker.NewNDJSONWriter(io.Writer)` — public NDJSON event writer producing the same wire format as `tracker --json`. Factory methods `PipelineHandler`, `AgentHandler`, `TraceObserver` return handlers that plug into `Config.EventHandler`, `Config.AgentEvents`, and the LLM trace hook. Closes Phase 1 of #76.
- `tracker.Diagnose(runDir)` / `tracker.DiagnoseMostRecent(workDir)` — structured `*DiagnoseReport` with node failures, budget halt, and typed suggestions (`Kind: "retry_pattern" | "escalate_limit" | "no_output" | "shell_command" | "go_test" | "suspicious_timing" | "budget"`).
- `tracker.Audit(runDir)` — structured `*AuditReport` with timeline, retries, errors, and recommendations.
- `tracker.ListRuns(workDir)` — sorted `[]RunSummary` for enumerating past runs.
- `tracker.Doctor(cfg)` — structured `*DoctorReport` for preflight health checks. `ProbeProviders` defaults to false; set true to make real API calls for auth verification.
- `tracker.Simulate(source)` — structured `*SimulateReport` with nodes, edges, execution plan, and unreachable-node list.
- `tracker.ResolveRunDir(workDir, runID)` / `tracker.MostRecentRunID(workDir)` — exposed run-directory resolution helpers.
- `tracker.ActivityEntry` / `tracker.LoadActivityLog(runDir)` / `tracker.ParseActivityLine(line)` — shared activity.jsonl parsing. All shared between CLI and library.

### Changed

- `cmd/tracker/diagnose.go`, `audit.go`, `doctor.go`, `simulate.go` are now thin printers over the new library APIs. CLI stdout and `--json` wire format are byte-identical. Closes Phase 2 of #76.
```

- [ ] **Step 7.2: Add library-API section to README**

Open `README.md`. Find the existing "Library API" or "Using tracker as a library" section (if none exists, add one under the main TOC). Append a short example:

```markdown
### Analyzing past runs from code

```go
import tracker "github.com/2389-research/tracker"

report, err := tracker.DiagnoseMostRecent(".")
if err != nil { log.Fatal(err) }

for _, f := range report.Failures {
    fmt.Printf("failed: %s (handler=%s, retries=%d)\n",
        f.NodeID, f.Handler, f.RetryCount)
}
for _, s := range report.Suggestions {
    fmt.Printf("  %s: %s\n", s.Kind, s.Message)
}
```

`tracker.Audit`, `tracker.Simulate`, and `tracker.Doctor` follow the same pattern and return JSON-serializable reports.
```

- [ ] **Step 7.3: Final full test sweep**

```bash
go build ./...
go test ./... -short -race
dippin doctor examples/ask_and_execute.dip examples/build_product.dip examples/build_product_with_superspec.dip
```

Expected: build succeeds; all 14 (now 15, counting added files in the `tracker` package) package tests PASS; `dippin doctor` reports A grade on all three examples.

- [ ] **Step 7.4: Commit docs**

```bash
git add CHANGELOG.md README.md
git commit -m "$(cat <<'EOF'
docs: CHANGELOG + README for Phase 2 library APIs

Refs #76

Co-Authored-By: Claude Opus 4.7 (1M context) <noreply@anthropic.com>
EOF
)"
```

- [ ] **Step 7.5: Push branch and open PR**

```bash
git push -u origin feat/library-parity-phase-2
gh pr create --title "feat: CLI↔library feature parity (Phase 1 NDJSON + Phase 2)" --body "$(cat <<'EOF'
## Summary

- Promotes four CLI-private commands (diagnose, audit, doctor, simulate) into the public `tracker` library package as JSON-serializable reports.
- Lifts the private NDJSON event writer out of `cmd/tracker/json_stream.go` into `tracker.NewNDJSONWriter`, closing Phase 1 of #76.
- Splits data / presentation cleanly — library returns pure `*<Command>Report`; CLI prints byte-for-byte identical output.

Closes Phase 1 (NDJSON) and Phase 2 of #76. Phase 3 (`DescribeNodes`) remains as a follow-up.

## Test plan

- [ ] `go build ./...` clean
- [ ] `go test ./... -short -race` all 15 packages pass
- [ ] `dippin doctor` A grade on the three core example pipelines
- [ ] Smoke-diff: `./tracker simulate examples/ask_and_execute.dip` byte-identical before / after
- [ ] Smoke-diff: `./tracker --json validate <file>` byte-identical wire format
- [ ] Manual: a library-consumer smoke binary can call `tracker.Diagnose` and get a non-empty `DiagnoseReport`

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

---

## Self-review summary

- **Spec coverage:**
  - NDJSON writer (§3 API) → Task 2 ✅
  - `DiagnoseReport` / `NodeFailure` / `BudgetHalt` / `Suggestion` (§2 API) → Task 4 ✅
  - `AuditReport` + `ListRuns` (§2 API) → Task 5 ✅
  - `DoctorReport` + opt-in probe (§4 edge case) → Task 6 ✅
  - `SimulateReport` (§2 API) → Task 3 ✅
  - Shared activity helpers promotion → Task 1 ✅
  - CLI migration keeps byte-for-byte output → smoke-diff step in every task ✅
  - CHANGELOG + README → Task 7 ✅
  - Build sequence (activity → NDJSON → Simulate → Diagnose → Audit → Doctor → docs) matches spec §"Build sequence" ✅

- **Placeholder scan:** no TBD / TODO / "handle edge cases." Every code block is concrete. One soft spot in Task 6.3 — the `check*Lib` function bodies refer to porting existing CLI functions rather than reproducing every line. This is intentional: the CLI code is literally sitting in `cmd/tracker/doctor.go` for the engineer to copy verbatim into `tracker/tracker_doctor.go`, renaming `checkResult` → `CheckResult` and mapping the `ok/warn/required` booleans to `Status` values. Reproducing 500+ lines inline here would be less useful than the explicit mapping rule.

- **Type consistency check:**
  - `ActivityEntry` (tracker_activity.go) = `TimelineEntry.Type` + `TimelineEntry.NodeID` + etc. in `tracker_audit.go` — consistent. ✅
  - `NodeFailure` uses `time.Duration` everywhere, `time.Time` nowhere. ✅
  - `Suggestion.Kind` constants match the kinds asserted in `TestDiagnose_FailureWithRetries`. ✅
  - `DoctorConfig.ProbeProviders` consistently spelled (no `Probe` vs `ProbeProviders` split). ✅
  - `NDJSONWriter` method names `PipelineHandler` / `AgentHandler` / `TraceObserver` match CLI rewrite step in Task 2.7. ✅

Plan ready for execution.
