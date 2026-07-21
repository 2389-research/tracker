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

	// GateRecheckPending tracks goal-gate nodes whose retry/fallback
	// redirect has fired but which have not re-executed since (#348
	// defect 1). Set when handleExitNode redirects away from an
	// unsatisfied gate; cleared when the gate node executes again. While
	// pending, the gate stays visible to the exit-time goal-gate check
	// even after clearDownstream removed it from CompletedNodes, and a
	// retry re-enters AT the gate so it re-evaluates the current tree
	// instead of replaying an escalation tail that routes around it.
	// Persisted so a resumed run replays the re-entry deterministically.
	GateRecheckPending map[string]bool `json:"gate_recheck_pending,omitempty"`

	// OverriddenGates records goal-gate node IDs whose last (failed) outcome
	// a human resolved by traversing an override edge from the gate's
	// escalation (#348 defect 2). An overridden gate is treated as satisfied
	// by the exit-time goal-gate check and is not re-entered; the run
	// completes validation_overridden. Cleared when the gate re-executes so a
	// fresh failure on new work re-prompts the human. Persisted so a resumed
	// run stays resolved.
	OverriddenGates map[string]bool `json:"overridden_gates,omitempty"`

	// WIPRefs maps a failed/exhausted node ID to the recoverable git ref
	// (a tag tracker/wip/<runID>/<nodeID>) where its uncommitted work was
	// preserved before the engine routed away from it (#302). Additive;
	// omitempty keeps older checkpoints without the field loading cleanly.
	WIPRefs map[string]string `json:"wip_refs,omitempty"`

	// BundleIdentity is the content-addressed identity of the .dipx bundle
	// the run was started against ("sha256:<hex>"). Empty for runs started
	// from a plain .dip file. Used for strict resume verification.
	BundleIdentity string `json:"bundle_identity,omitempty"`

	// ValidationOverrides persists the override sticky list across resume and
	// bundle export. Appended at the flip-point in advanceToNextNode whenever
	// an override edge is traversed; never cleared by clearDownstream or
	// handleLoopRestart. omitempty for backwards compat with pre-v0.35
	// checkpoints (absent = "no overrides happened").
	ValidationOverrides []OverrideDetail `json:"validation_overrides,omitempty"`

	// MemoEntries maps a content-hash memo key to a stored SUCCESSFUL outcome
	// projection (#421). Deliberately NOT cleared by clearDownstream — replay
	// must survive loop restarts; a changed input yields a different key and
	// misses naturally. The map round-trips natively through JSON (no derived
	// set to rebuild, unlike completedSet). omitempty keeps pre-feature
	// checkpoints byte-identical (default-off acceptance criterion).
	MemoEntries map[string]MemoEntry `json:"memo_entries,omitempty"`

	// completedSet provides O(1) lookup for IsCompleted. It is rebuilt from
	// CompletedNodes on deserialization and kept in sync by MarkCompleted.
	completedSet map[string]bool `json:"-"`
}

// MemoEntry is the JSON-tagged, serializable projection of a successful Outcome
// (which has no JSON tags) persisted in the checkpoint for memoization (#421).
// Status, ContextUpdates, and the routing hints (PreferredLabel,
// SuggestedNextNodes) are replayed — the hints are required so a memoized node
// that selected its next edge via them replays onto the SAME path the original
// execution took (#425 review). Other Outcome fields are accounting artifacts
// the replay path does not need.
type MemoEntry struct {
	Status             string            `json:"status"`
	ContextUpdates     map[string]string `json:"context_updates,omitempty"`
	PreferredLabel     string            `json:"preferred_label,omitempty"`
	SuggestedNextNodes []string          `json:"suggested_next_nodes,omitempty"`
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

// SetGateRecheckPending marks a goal-gate node as awaiting re-execution
// after a retry/fallback redirect (#348 defect 1).
func (cp *Checkpoint) SetGateRecheckPending(nodeID string) {
	if cp.GateRecheckPending == nil {
		cp.GateRecheckPending = make(map[string]bool)
	}
	cp.GateRecheckPending[nodeID] = true
}

// ClearGateRecheckPending records that a goal-gate node has re-executed,
// clearing its pending recheck (#348 defect 1).
func (cp *Checkpoint) ClearGateRecheckPending(nodeID string) {
	delete(cp.GateRecheckPending, nodeID)
}

// IsGateRecheckPending reports whether a goal-gate node is awaiting
// re-execution after a retry/fallback redirect (#348 defect 1).
func (cp *Checkpoint) IsGateRecheckPending(nodeID string) bool {
	return cp.GateRecheckPending[nodeID]
}

// MarkGateOverridden records that a human resolved a failed goal gate via an
// override edge from its escalation (#348 defect 2).
func (cp *Checkpoint) MarkGateOverridden(nodeID string) {
	if cp.OverriddenGates == nil {
		cp.OverriddenGates = make(map[string]bool)
	}
	cp.OverriddenGates[nodeID] = true
}

// ClearGateOverridden drops a gate's override when the gate re-executes, so a
// fresh failure on new work re-prompts the human (#348 defect 2).
func (cp *Checkpoint) ClearGateOverridden(nodeID string) {
	delete(cp.OverriddenGates, nodeID)
}

// IsGateOverridden reports whether a goal gate was human-overridden (#348
// defect 2). A nil map returns false.
func (cp *Checkpoint) IsGateOverridden(nodeID string) bool {
	return cp.OverriddenGates[nodeID]
}

// SetEdgeSelection records the selected outgoing edge for a completed node.
func (cp *Checkpoint) SetEdgeSelection(nodeID, edgeTo string) {
	if cp.EdgeSelections == nil {
		cp.EdgeSelections = make(map[string]string)
	}
	cp.EdgeSelections[nodeID] = edgeTo
}

// RecordWIPRef records the recoverable git ref where a failed/exhausted node's
// uncommitted work was preserved (#302).
func (cp *Checkpoint) RecordWIPRef(nodeID, ref string) {
	if cp.WIPRefs == nil {
		cp.WIPRefs = make(map[string]string)
	}
	cp.WIPRefs[nodeID] = ref
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

// PutMemo records a successful outcome under the given content-hash key (#421).
// ContextUpdates is deep-copied so a later pctx mutation cannot retroactively
// corrupt the stored record.
func (cp *Checkpoint) PutMemo(key string, o *Outcome) {
	if cp.MemoEntries == nil {
		cp.MemoEntries = make(map[string]MemoEntry)
	}
	cu := make(map[string]string, len(o.ContextUpdates))
	for k, v := range o.ContextUpdates {
		cu[k] = v
	}
	var snn []string
	if len(o.SuggestedNextNodes) > 0 {
		snn = append(snn, o.SuggestedNextNodes...)
	}
	cp.MemoEntries[key] = MemoEntry{
		Status:             string(o.Status),
		ContextUpdates:     cu,
		PreferredLabel:     o.PreferredLabel,
		SuggestedNextNodes: snn,
	}
}

// GetMemo returns the stored outcome projection for a key, if any (#421).
func (cp *Checkpoint) GetMemo(key string) (MemoEntry, bool) {
	if cp.MemoEntries == nil {
		return MemoEntry{}, false
	}
	e, ok := cp.MemoEntries[key]
	return e, ok
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

	// Write atomically: a crash *during* a plain WriteFile could truncate the
	// one file whose job is crash recovery, leaving LoadCheckpoint unable to
	// resume. Write a temp file, fsync it, then rename over the target — the
	// rename is atomic on POSIX same-directory, so a reader always sees either
	// the old-complete or new-complete checkpoint, never a partial one.
	if err := writeFileAtomic(path, data, 0o600); err != nil {
		return fmt.Errorf("write checkpoint: %w", err)
	}
	return nil
}

// writeFileAtomic writes data to a sibling temp file, then renames it over path.
// The rename is atomic on POSIX same-directory, so a reader (and a resume after a
// crash) always sees either the old-complete or new-complete file, never a
// partial one. This targets the stated threat — a *process* crash (OOM, SIGKILL,
// deploy) during a write — for which the surviving kernel page cache makes the
// rename sufficient; it deliberately does not fsync (power-loss durability is a
// non-goal here and the added latency is not worth it). Cleans up on error.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, perm); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
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
