// ABOUTME: Results writer for SWE-bench predictions — appends JSONL predictions and tracks completed instances.
// ABOUTME: Supports resumability by reading existing predictions on open, plus run stats and run metadata helpers.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// Prediction is one SWE-bench evaluation result line.
type Prediction struct {
	InstanceID      string `json:"instance_id"`
	ModelNameOrPath string `json:"model_name_or_path"`
	ModelPatch      string `json:"model_patch"`
}

// ResultsWriter appends predictions to a JSONL file and tracks which instances are done.
type ResultsWriter struct {
	path      string
	model     string
	file      *os.File
	completed map[string]struct{}
}

// NewResultsWriter opens (or creates) the predictions file at path, reads any existing predictions
// to build the completed set for resume support, and returns a writer ready to append.
func NewResultsWriter(path, model string) (*ResultsWriter, error) {
	completed := make(map[string]struct{})

	// Read existing predictions if the file already exists.
	if f, err := os.Open(path); err == nil {
		scanner := bufio.NewScanner(f)
		for scanner.Scan() {
			line := scanner.Text()
			if line == "" {
				continue
			}
			var p Prediction
			if jsonErr := json.Unmarshal([]byte(line), &p); jsonErr == nil && p.InstanceID != "" && p.ModelPatch != "" {
				completed[p.InstanceID] = struct{}{}
			}
		}
		f.Close()
	}

	// Open file in append mode, creating if necessary.
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open predictions file %q: %w", path, err)
	}

	return &ResultsWriter{
		path:      path,
		model:     model,
		file:      file,
		completed: completed,
	}, nil
}

// WritePrediction appends one prediction line and marks the instance as completed.
func (w *ResultsWriter) WritePrediction(instanceID, patch string) error {
	p := Prediction{
		InstanceID:      instanceID,
		ModelNameOrPath: w.model,
		ModelPatch:      patch,
	}
	data, err := json.Marshal(p)
	if err != nil {
		return fmt.Errorf("marshal prediction: %w", err)
	}
	data = append(data, '\n')
	if _, err := w.file.Write(data); err != nil {
		return fmt.Errorf("write prediction: %w", err)
	}
	// Only mark as completed if the patch is non-empty. Empty patches from
	// timeouts or errors should be retried on resume.
	if patch != "" {
		w.completed[instanceID] = struct{}{}
	}
	return nil
}

// IsCompleted reports whether instanceID has already been written.
func (w *ResultsWriter) IsCompleted(instanceID string) bool {
	_, ok := w.completed[instanceID]
	return ok
}

// CompletedCount returns the number of completed predictions tracked so far.
func (w *ResultsWriter) CompletedCount() int {
	return len(w.completed)
}

// Close flushes and closes the underlying file.
func (w *ResultsWriter) Close() error {
	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	return err
}

// RunStats holds counters and timing for a benchmark run.
type RunStats struct {
	Total        int
	Completed    int
	Skipped      int
	Errors       int
	TimedOut     int
	Patched      int
	InputTokens  int64
	OutputTokens int64
	StartTime    time.Time
}

// Summary returns a human-readable summary of the run.
func (s *RunStats) Summary() string {
	elapsed := time.Since(s.StartTime).Round(time.Second)

	patchPct := 0.0
	if s.Completed > 0 {
		patchPct = float64(s.Patched) / float64(s.Completed) * 100
	}
	completedPct := 0.0
	if s.Total > 0 {
		completedPct = float64(s.Completed) / float64(s.Total) * 100
	}

	inM := float64(s.InputTokens) / 1e6
	outM := float64(s.OutputTokens) / 1e6

	return fmt.Sprintf(
		"Run complete — elapsed: %s\n"+
			"  Total:     %d\n"+
			"  Completed: %d (%.1f%%)\n"+
			"  Skipped:   %d\n"+
			"  Errors:    %d\n"+
			"  Timed out: %d\n"+
			"  Patched:   %d (%.1f%% of completed)\n"+
			"  Tokens:    %.2fM in / %.2fM out",
		elapsed,
		s.Total,
		s.Completed, completedPct,
		s.Skipped,
		s.Errors,
		s.TimedOut,
		s.Patched, patchPct,
		inM, outM,
	)
}

// RunMeta holds metadata about the benchmark run written to a JSON file at the start.
type RunMeta struct {
	Model      string    `json:"model"`
	Provider   string    `json:"provider"`
	GatewayURL string    `json:"gateway_url,omitempty"`
	Dataset    string    `json:"dataset"`
	MaxTurns   int       `json:"max_turns"`
	Timeout    string    `json:"timeout"`
	StartedAt  time.Time `json:"started_at"`
	Commit     string    `json:"commit,omitempty"`
}

// WriteRunMeta writes meta as indented JSON to path. StartedAt is auto-filled with the current
// time when the zero value is passed.
func WriteRunMeta(path string, meta RunMeta) error {
	if meta.StartedAt.IsZero() {
		meta.StartedAt = time.Now().UTC()
	}
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal run meta: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write run meta %q: %w", path, err)
	}
	return nil
}
