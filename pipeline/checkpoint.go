// ABOUTME: Checkpoint serialization for pipeline execution resume support.
// ABOUTME: Tracks completed nodes, retry counts, and context state as JSON on disk.
package pipeline

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Checkpoint captures the execution state of a pipeline run for resume support.
type Checkpoint struct {
	RunID          string            `json:"run_id"`
	CurrentNode    string            `json:"current_node"`
	CompletedNodes []string          `json:"completed_nodes"`
	RetryCounts    map[string]int    `json:"retry_counts"`
	Context        map[string]string `json:"context"`
	Timestamp      time.Time         `json:"timestamp"`
	RestartCount   int               `json:"restart_count"`

	// EdgeSelections stores the selected outgoing edge target for each
	// completed node (nodeID -> selected edge To). Used on resume to
	// replay routing decisions instead of re-evaluating stale conditions.
	EdgeSelections map[string]string `json:"edge_selections,omitempty"`

	// FallbackTaken tracks which goal-gate nodes have already used their
	// one-shot fallback/escalation route. Persisted in checkpoint JSON so
	// the guard survives checkpoint save/restore cycles.
	FallbackTaken map[string]bool `json:"fallback_taken,omitempty"`

	// completedSet provides O(1) lookup for IsCompleted. It is rebuilt from
	// CompletedNodes on deserialization and kept in sync by MarkCompleted.
	completedSet map[string]bool `json:"-"`
}

// ensureSet lazily initializes the completed set from the slice.
func (cp *Checkpoint) ensureSet() {
	if cp.completedSet == nil {
		cp.completedSet = make(map[string]bool, len(cp.CompletedNodes))
		for _, id := range cp.CompletedNodes {
			cp.completedSet[id] = true
		}
	}
}

// IsCompleted returns true if the given node ID has been marked as completed.
func (cp *Checkpoint) IsCompleted(nodeID string) bool {
	cp.ensureSet()
	return cp.completedSet[nodeID]
}

// RetryCount returns the number of retries recorded for the given node.
// Returns 0 if the node has no retry history or if the map is nil.
func (cp *Checkpoint) RetryCount(nodeID string) int {
	if cp.RetryCounts == nil {
		return 0
	}
	return cp.RetryCounts[nodeID]
}

// IncrementRetry increments the retry counter for the given node by one.
func (cp *Checkpoint) IncrementRetry(nodeID string) {
	if cp.RetryCounts == nil {
		cp.RetryCounts = make(map[string]int)
	}
	cp.RetryCounts[nodeID]++
}

// MarkCompleted adds the given node ID to the completed nodes list.
// Duplicate IDs are ignored.
func (cp *Checkpoint) MarkCompleted(nodeID string) {
	cp.ensureSet()
	if cp.completedSet[nodeID] {
		return
	}
	cp.completedSet[nodeID] = true
	cp.CompletedNodes = append(cp.CompletedNodes, nodeID)
}

// SetEdgeSelection records the selected outgoing edge for a completed node.
func (cp *Checkpoint) SetEdgeSelection(nodeID, edgeTo string) {
	if cp.EdgeSelections == nil {
		cp.EdgeSelections = make(map[string]string)
	}
	cp.EdgeSelections[nodeID] = edgeTo
}

// GetEdgeSelection returns the stored edge selection for a node, if any.
func (cp *Checkpoint) GetEdgeSelection(nodeID string) (string, bool) {
	if cp.EdgeSelections == nil {
		return "", false
	}
	v, ok := cp.EdgeSelections[nodeID]
	return v, ok
}

// ClearCompleted removes a node from the completed set so it will re-execute.
func (cp *Checkpoint) ClearCompleted(nodeID string) {
	cp.ensureSet()
	if !cp.completedSet[nodeID] {
		return
	}
	delete(cp.completedSet, nodeID)
	for i, id := range cp.CompletedNodes {
		if id == nodeID {
			cp.CompletedNodes = append(cp.CompletedNodes[:i], cp.CompletedNodes[i+1:]...)
			break
		}
	}
}

// SaveCheckpoint writes the checkpoint to disk as JSON, creating directories as needed.
func SaveCheckpoint(cp *Checkpoint, path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create checkpoint directory: %w", err)
	}

	data, err := json.MarshalIndent(cp, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal checkpoint: %w", err)
	}

	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write checkpoint: %w", err)
	}
	return nil
}

// LoadCheckpoint reads a checkpoint from a JSON file on disk.
func LoadCheckpoint(path string) (*Checkpoint, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read checkpoint: %w", err)
	}

	var cp Checkpoint
	if err := json.Unmarshal(data, &cp); err != nil {
		return nil, fmt.Errorf("unmarshal checkpoint: %w", err)
	}
	return &cp, nil
}
