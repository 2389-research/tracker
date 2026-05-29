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
			if !os.IsNotExist(err) {
				fmt.Fprintf(logW, "warning: cannot load checkpoint for run %s: %v\n", e.Name(), err)
			}
			// Skip invalid or missing checkpoints — the run directory may be
			// partially written or belong to a different tool.
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

// ActivityEntry is a parsed line from activity.jsonl. Populate via
// ParseActivityLine — ActivityEntry is not itself a JSON-wire type because
// tracker has historically used two timestamp formats and time.Time's
// default unmarshal handles only RFC3339Nano.
//
// Marshal/unmarshal contract: do not json.Marshal/json.Unmarshal ActivityEntry
// directly. Use ParseActivityLine and LoadActivityLog for decoding and map to
// your own wire type when encoding.
type ActivityEntry struct {
	Timestamp time.Time
	Type      string
	RunID     string
	NodeID    string
	Message   string
	Error     string
	// Override fields — populated for "validation_overridden" entries.
	// Mirror the wire-format fields written by the runtime's
	// jsonlLogEntry (see pipeline/events_jsonl.go): the gate that
	// produced the override, the label that selected the override
	// edge, who acted, and the subgraph_path when propagated up from
	// a child run. Empty for non-override entries.
	OverrideGate         string
	OverrideLabel        string
	OverrideActor        pipeline.Actor
	OverrideSubgraphPath []string
}

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
		raw := scanner.Text()
		line, hasSentinel := stripActivitySentinel(raw)
		// Count sentinel/injection BEFORE the blank-line skip so a
		// non-sentinel blank line on the secure file still increments
		// InjectedLines — an attacker emitting blank padding shouldn't
		// be able to hide from the integrity counter.
		scan.TotalLines++
		if secureUsed {
			if hasSentinel {
				scan.SentinelLines++
			} else {
				scan.InjectedLines++
			}
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if entry, ok := ParseActivityLine(trimmed); ok {
			scan.Entries = append(scan.Entries, entry)
		}
	}
	return scan, scanner.Err()
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

// ParseActivityLine decodes a single JSONL line. Returns (zero, false) on any parse error.
func ParseActivityLine(line string) (ActivityEntry, bool) {
	var raw struct {
		Timestamp            string         `json:"ts"`
		Type                 string         `json:"type"`
		RunID                string         `json:"run_id"`
		NodeID               string         `json:"node_id"`
		Message              string         `json:"message"`
		Error                string         `json:"error"`
		OverrideGate         string         `json:"override_gate"`
		OverrideLabel        string         `json:"override_label"`
		OverrideActor        pipeline.Actor `json:"override_actor"`
		OverrideSubgraphPath []string       `json:"override_subgraph_path"`
	}
	if err := json.Unmarshal([]byte(line), &raw); err != nil {
		return ActivityEntry{}, false
	}
	ts, ok := parseActivityTimestamp(raw.Timestamp)
	if !ok {
		return ActivityEntry{}, false
	}
	entry := ActivityEntry{
		Timestamp:     ts,
		Type:          raw.Type,
		RunID:         raw.RunID,
		NodeID:        raw.NodeID,
		Message:       raw.Message,
		Error:         raw.Error,
		OverrideGate:  raw.OverrideGate,
		OverrideLabel: raw.OverrideLabel,
		OverrideActor: raw.OverrideActor,
	}
	if len(raw.OverrideSubgraphPath) > 0 {
		// Defensive copy so callers can't mutate the parsed slice and
		// leak into another entry through aliased backing arrays.
		entry.OverrideSubgraphPath = append([]string(nil), raw.OverrideSubgraphPath...)
	}
	return entry, true
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
