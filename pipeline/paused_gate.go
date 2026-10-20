// ABOUTME: The paused-gate record (pause mode): what a waiting human gate asked,
// ABOUTME: its fingerprint, the PauseError that carries it, and the gate.json mirror.
package pipeline

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// GateMirrorFile is the run-dir file that mirrors Checkpoint.PausedGates for
// agents and scripts. It is a convenience copy in the tool-reachable run dir:
// Tracker never reads it back to decide anything — the checkpoint in the
// secure state dir is the authoritative record.
const GateMirrorFile = "gate.json"

// PausedGate records a human gate a run is waiting at (pause mode). It holds
// exactly what the gate asked — the arguments the human handler passed to the
// interviewer — so `tracker answer` can show and validate an answer against it
// without re-running anything.
type PausedGate struct {
	// TopNode is the node the engine paused at: the gate itself, or the
	// parallel node whose branch holds it. Set by the engine.
	TopNode string `json:"top_node"`
	// NodeID is the gate node.
	NodeID string `json:"node_id"`
	// ScopePath locates the gate inside the run: the parallel branch ID(s)
	// enclosing it, outermost first. Empty for a top-level gate.
	ScopePath []string `json:"scope_path,omitempty"`
	Mode      string   `json:"mode"`
	Label     string   `json:"label"`
	// Prompt is the full rendered prompt, never truncated.
	Prompt string `json:"prompt"`
	// Choices and Default are exactly what the gate passed to the interviewer.
	Choices   []string       `json:"choices,omitempty"`
	Default   string         `json:"default,omitempty"`
	Questions []GateQuestion `json:"questions,omitempty"`
	// BranchIndex orders gates from one parallel node (lowest first).
	BranchIndex int `json:"branch_index"`
	// Fingerprint identifies the question (GateFingerprint); an answer applies
	// only to a gate that asks the same question.
	Fingerprint string `json:"fingerprint"`
	// Nonce is random per pause, so a record replaced between `tracker answer`
	// reading it and resuming is detected.
	Nonce string `json:"nonce"`
	// Note is a message for the person answering, e.g. that an earlier answer
	// was dropped because the question changed.
	Note string    `json:"note,omitempty"`
	At   time.Time `json:"at"`
}

// GateFingerprint hashes what the gate asks — scope, node, mode, prompt,
// choices, default and interview questions — and nothing about when or how
// often it was asked (nonce, time, note, top node, branch index).
func GateFingerprint(g PausedGate) string {
	asked := struct {
		ScopePath []string       `json:"s"`
		NodeID    string         `json:"n"`
		Mode      string         `json:"m"`
		Prompt    string         `json:"p"`
		Choices   []string       `json:"c"`
		Default   string         `json:"d"`
		Questions []GateQuestion `json:"q"`
	}{g.ScopePath, g.NodeID, g.Mode, g.Prompt, g.Choices, g.Default, g.Questions}
	data, _ := json.Marshal(asked) // a struct of strings and slices always marshals
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// NewPausedGateError returns the pause a human gate raises when no one can
// answer it: status OutcomePausedGate carrying the waiting gate(s). Records
// without a Fingerprint get one.
func NewPausedGateError(gates ...PausedGate) *PauseError {
	out := make([]PausedGate, len(gates))
	for i, g := range gates {
		if g.Fingerprint == "" {
			g.Fingerprint = GateFingerprint(g)
		}
		out[i] = g
	}
	msg := "paused at a human gate"
	if len(out) > 0 {
		msg = fmt.Sprintf("paused at human gate %q", out[0].NodeID)
	}
	return &PauseError{Status: OutcomePausedGate, Err: fmt.Errorf("%s", msg), Gates: out}
}

// recordPause stamps a pause's state onto the checkpoint before it is saved:
// the waiting gates (their TopNode set to the paused node) and the run's
// cumulative usage, which a gate-paused resume seeds its budget from.
func (e *Engine) recordPause(s *runState, nodeID string, err error) {
	pe, ok := asPauseError(err)
	if !ok {
		return
	}
	for i := range pe.Gates {
		if pe.Gates[i].TopNode == "" {
			pe.Gates[i].TopNode = nodeID
		}
	}
	s.cp.PausedGates = append([]PausedGate(nil), pe.Gates...)
	s.cp.PriorUsage = cloneUsageSummary(e.combinedUsageForBudget(s))
}

// clearPausedGates drops the waiting-gate record once the node that paused
// completes, and removes the run-dir mirror. The checkpoint save after the
// node persists the cleared record, so a crash before it re-pauses at the
// same gate instead of losing it.
func (e *Engine) clearPausedGates(s *runState, nodeID string) {
	for _, g := range s.cp.PausedGates {
		if g.TopNode == nodeID {
			s.cp.PausedGates = nil
			removeGateMirror(s.pctx)
			return
		}
	}
}

// runDirOf returns the run's artifact dir, or "" when the run has none.
func runDirOf(pctx *PipelineContext) string {
	if pctx == nil {
		return ""
	}
	dir, _ := pctx.GetInternal(InternalKeyArtifactDir)
	return dir
}

// removeGateMirror deletes the run dir's gate.json, if any (best effort).
func removeGateMirror(pctx *PipelineContext) {
	if dir := runDirOf(pctx); dir != "" {
		_ = os.Remove(filepath.Join(dir, GateMirrorFile))
	}
}

// writeGateMirror writes gates to <runDir>/gate.json (0600). It refuses a
// symlink planted at the final component and replaces the file atomically, so
// a reader never sees a partial file.
func writeGateMirror(runDir string, gates []PausedGate) error {
	data, err := json.MarshalIndent(gates, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(runDir, GateMirrorFile)
	if err := refuseIfSymlink(path); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := refuseIfSymlink(tmp); err != nil {
		return err
	}
	if err := writeCaptureFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
