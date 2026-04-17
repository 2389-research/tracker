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
	matched, err := findRunDirMatchLib(runsDir, runID)
	if err != nil {
		return "", err
	}
	return filepath.Join(runsDir, matched), nil
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
	ts, ok := parseActivityTimestampLib(raw.Timestamp)
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

func parseActivityTimestampLib(s string) (time.Time, bool) {
	if ts, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return ts, true
	}
	if ts, err := time.Parse("2006-01-02T15:04:05.000Z07:00", s); err == nil {
		return ts, true
	}
	return time.Time{}, false
}
