// ABOUTME: Shared helpers for resolving run directories and parsing activity.jsonl.
// ABOUTME: Promoted from cmd/tracker/ so library and CLI use one implementation.
package tracker

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
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
	matched, err := findRunDirMatchLib(runsDir, runID)
	if err != nil {
		return "", err
	}
	runDir := filepath.Join(runsDir, matched)
	absRunDir, err := filepath.Abs(runDir)
	if err != nil {
		return "", fmt.Errorf("cannot resolve absolute run directory path: %w", err)
	}
	return absRunDir, nil
}

func findRunDirMatchLib(runsDir, runID string) (string, error) {
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
	return pickRunDirMatch(runsDir, runID, matches)
}

// pickRunDirMatch resolves prefix matches to a single run directory name:
// none is an error, one wins outright, and several are only resolvable when
// one is an exact match for runID.
func pickRunDirMatch(runsDir, runID string, matches []string) (string, error) {
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
	return mostRecentRunID(workdir, io.Discard)
}

func mostRecentRunID(workdir string, logW io.Writer) (string, error) {
	runsDir := filepath.Join(workdir, ".tracker", "runs")
	entries, err := readRunsDir(runsDir)
	if err != nil {
		return "", err
	}
	var latestID string
	var latestTime time.Time
	for _, e := range entries {
		cp := loadRunCheckpoint(runsDir, e, logW)
		if cp != nil && cp.Timestamp.After(latestTime) {
			latestTime = cp.Timestamp
			latestID = e.Name()
		}
	}
	if latestID == "" {
		return "", fmt.Errorf("no runs found with valid checkpoints")
	}
	return latestID, nil
}

// readRunsDir lists the run directories under runsDir, distinguishing "no runs
// yet" from a genuine read failure.
func readRunsDir(runsDir string) ([]os.DirEntry, error) {
	entries, err := os.ReadDir(runsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("no runs found — run a pipeline first")
		}
		return nil, fmt.Errorf("cannot read runs directory: %w", err)
	}
	return entries, nil
}

// loadRunCheckpoint returns the checkpoint of one runs-dir entry, or nil when
// the entry is not a usable run directory. Invalid or missing checkpoints are
// skipped — the run directory may be partially written or belong to a
// different tool — with anything other than "missing" warned about on logW.
func loadRunCheckpoint(runsDir string, e os.DirEntry, logW io.Writer) *pipeline.Checkpoint {
	if !e.IsDir() {
		return nil
	}
	cpPath := filepath.Join(runsDir, e.Name(), "checkpoint.json")
	cp, err := pipeline.LoadCheckpoint(cpPath)
	if err != nil {
		if !os.IsNotExist(err) {
			fmt.Fprintf(logW, "warning: cannot load checkpoint for run %s: %v\n", e.Name(), err)
		}
		return nil
	}
	return cp
}

// ActivityEntry (the exported parsed-line type) and its private decode struct
// activityRawLine + toEntry copier are generated from the single field
// authority in scripts/gen/activitylog/schema.go. See
// tracker_activity_entry_gen.go and tracker_activity_raw_gen.go.

// ResolveActivityLogPath returns the on-disk location of the activity
// log for runDir. It prefers the integrity-protected secure path
// (#213) when present, falling back to <runDir>/activity.jsonl for
// pre-#213 runs and post-run snapshots. The returned secureUsed flag
// is true when the path came from the secure location — callers that
// validate the runtime sentinel should only do so in that case.
//
// runID is derived from runDir's basename, matching the
// .tracker/runs/<runID> layout enforced by ResolveRunDir.
func ResolveActivityLogPath(runDir string) (path string, secureUsed bool) {
	runID := filepath.Base(runDir)
	if runID != "" && runID != "." && runID != string(filepath.Separator) {
		if securePath, err := pipeline.SecureActivityLogPath(runID); err == nil {
			if _, statErr := os.Stat(securePath); statErr == nil {
				return securePath, true
			}
		}
	}
	return filepath.Join(runDir, "activity.jsonl"), false
}

// LoadActivityLog reads and parses the activity log for runDir, preferring
// the integrity-protected secure path with fallback to the legacy
// <runDir>/activity.jsonl. Returns (nil, nil) if neither location has a
// file. Malformed lines are skipped. Sentinel-stripped lines that don't
// parse as JSON are dropped silently — callers needing tamper-detection
// granularity should use ScanActivityLog (or the Diagnose path).
func LoadActivityLog(runDir string) ([]ActivityEntry, error) {
	scan, err := ScanActivityLog(runDir)
	if err != nil {
		return nil, err
	}
	return scan.Entries, nil
}

// ActivityLogScan is the structured result of reading an activity log.
// Path is the on-disk location read; SecureUsed reflects whether the
// integrity-protected secure log was the source; InjectedLines counts
// non-sentinel lines observed when reading from the secure path
// (always 0 when SecureUsed is false — legacy/snapshot files don't
// carry the runtime sentinel, so absence is not a signal).
type ActivityLogScan struct {
	Path          string
	SecureUsed    bool
	Entries       []ActivityEntry
	InjectedLines int
	TotalLines    int
	SentinelLines int
}

// ScanActivityLog is LoadActivityLog with tamper-detection counters
// exposed for callers (e.g. Diagnose) that need to surface injection
// signals. Lines without the runtime sentinel prefix in the secure
// file count toward InjectedLines; the line is still parsed best-effort
// so its content is visible to forensics.
func ScanActivityLog(runDir string) (*ActivityLogScan, error) {
	path, secureUsed := ResolveActivityLogPath(runDir)
	scan := &ActivityLogScan{Path: path, SecureUsed: secureUsed}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return scan, nil
		}
		return scan, fmt.Errorf("open activity log: %w", err)
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		scan.consumeLine(scanner.Text())
	}
	return scan, scanner.Err()
}

// consumeLine folds one raw log line into the scan: it strips the runtime
// sentinel, updates the integrity counters, and appends the parsed entry.
// Sentinel handling is unchanged from the inline version — the counters are
// bumped BEFORE the blank-line skip so a non-sentinel blank line on the secure
// file still increments InjectedLines (an attacker emitting blank padding
// shouldn't be able to hide from the integrity counter), and they are bumped
// only when the secure path was the source, because legacy/snapshot files
// carry no sentinel and their absence is not a signal.
func (s *ActivityLogScan) consumeLine(raw string) {
	line, hasSentinel := stripActivitySentinel(raw)
	s.TotalLines++
	if s.SecureUsed {
		if hasSentinel {
			s.SentinelLines++
		} else {
			s.InjectedLines++
		}
	}
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return
	}
	if entry, ok := ParseActivityLine(trimmed); ok {
		s.Entries = append(s.Entries, entry)
	}
}

// stripActivitySentinel removes the runtime sentinel prefix if present
// and reports whether it was found. Both signals matter: the parsed
// body for content, and the prefix flag for tamper detection.
func stripActivitySentinel(line string) (string, bool) {
	if strings.HasPrefix(line, pipeline.ActivityLogSentinel) {
		return line[len(pipeline.ActivityLogSentinel):], true
	}
	return line, false
}

// SortActivityByTime sorts entries ascending by Timestamp.
func SortActivityByTime(entries []ActivityEntry) {
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Timestamp.Before(entries[j].Timestamp)
	})
}

// ParseActivityLine decodes a single JSONL line. Returns (zero, false) on any
// parse error, including an unparseable timestamp. Unknown keys are ignored:
// a line written by a newer runtime still parses, minus the fields this build
// does not know. The decode target (activityRawLine) and the toEntry copier are
// generated into tracker_activity_raw_gen.go from scripts/gen/activitylog.
func ParseActivityLine(line string) (ActivityEntry, bool) {
	var raw activityRawLine
	if err := json.Unmarshal([]byte(line), &raw); err != nil {
		return ActivityEntry{}, false
	}
	ts, ok := parseActivityTimestamp(raw.Timestamp)
	if !ok {
		return ActivityEntry{}, false
	}
	return raw.toEntry(ts), true
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
