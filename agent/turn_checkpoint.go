// ABOUTME: Durable between-turns snapshot of a Session's conversational state (#427).
// ABOUTME: Serializes messages + episode log so an interrupted node resumes mid-node.
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/2389-research/tracker/llm"
)

// turnSnapshotSchema is the on-disk schema version. Bump on any breaking change
// to TurnSnapshot's shape; LoadTurnSnapshot refuses a version it does not know
// rather than silently mis-decoding a stale file (never silently swallow errors).
const turnSnapshotSchema = 1

// gitSHATimeout bounds the best-effort HEAD lookup so a wedged git invocation
// cannot stall a turn boundary.
const gitSHATimeout = 5 * time.Second

// TurnSnapshot is the durable, JSON-serializable projection of a Session's
// conversational state captured between turns. It carries exactly what a fresh
// Session needs to resume mid-node: the full message history (including
// tool-call and tool-result parts, which round-trip through llm.Message's JSON
// tags), the episode log, and the last completed turn number.
//
// WorkTreeSHA is a coarse consistency guard, not a content hash: it records the
// git HEAD commit the working directory was on when the snapshot was taken. On
// resume, a Session refuses to restore onto a tree whose HEAD has moved (a loop
// restart that committed between passes, or a different base) and starts the
// node fresh instead — this is the corruption safety valve. It does NOT capture
// uncommitted edits, so two different WIP states on the same HEAD both match;
// that is acceptable because a crash-resume replays the same on-disk WIP.
type TurnSnapshot struct {
	Schema      int            `json:"schema"`
	SessionID   string         `json:"session_id"`
	Turn        int            `json:"turn"` // last COMPLETED turn; resume runs Turn+1 onward
	Messages    []llm.Message  `json:"messages"`
	Episodes    []EpisodeEntry `json:"episodes,omitempty"`
	WorkTreeSHA string         `json:"work_tree_sha,omitempty"`
}

// Snapshot captures the Session's current conversational state as a durable
// TurnSnapshot at the given completed-turn boundary. workDir is captured for the
// WorkTreeSHA guard (empty/non-repo yields an empty SHA — the guard degrades to
// off, never a hard failure).
func (s *Session) Snapshot(turn int, workDir string) *TurnSnapshot {
	msgs := make([]llm.Message, len(s.messages))
	copy(msgs, s.messages)
	var eps []EpisodeEntry
	if len(s.episodeLog.Entries) > 0 {
		eps = make([]EpisodeEntry, len(s.episodeLog.Entries))
		copy(eps, s.episodeLog.Entries)
	}
	return &TurnSnapshot{
		Schema:      turnSnapshotSchema,
		SessionID:   s.id,
		Turn:        turn,
		Messages:    msgs,
		Episodes:    eps,
		WorkTreeSHA: captureWorkTreeSHA(workDir),
	}
}

// RestoreFrom seeds a fresh (not-yet-run) Session from a TurnSnapshot so its next
// Run resumes at Turn+1 with messages and episode log byte-identical to the
// pre-interrupt state. It adopts the snapshot's SessionID so a resumed run keeps
// a stable identity across the interruption.
func (s *Session) RestoreFrom(snap *TurnSnapshot) {
	if snap == nil {
		return
	}
	s.messages = make([]llm.Message, len(snap.Messages))
	copy(s.messages, snap.Messages)
	s.episodeLog.Entries = nil
	if len(snap.Episodes) > 0 {
		s.episodeLog.Entries = make([]EpisodeEntry, len(snap.Episodes))
		copy(s.episodeLog.Entries, snap.Episodes)
	}
	s.id = snap.SessionID
	s.resumeTurn = snap.Turn
}

// Save writes the snapshot to path atomically (temp file + rename), so a crash
// mid-write can never leave a truncated snapshot that would corrupt a later
// resume. Mirrors pipeline.SaveCheckpoint's write discipline: 0600, O_NOFOLLOW on
// the temp write, atomic same-directory rename.
func (snap *TurnSnapshot) Save(path string) error {
	// Plain Marshal (not MarshalIndent): a tool call's Arguments is a
	// json.RawMessage, which MarshalIndent would re-indent on every save. Because
	// the snapshot is rewritten every turn and reloaded on every resume, indenting
	// would compound whitespace across cycles and break byte-identical round-trip.
	// Compact output stays stable no matter how many times it is loaded and saved.
	data, err := json.Marshal(snap)
	if err != nil {
		return fmt.Errorf("marshal turn snapshot: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create turn-snapshot directory: %w", err)
	}
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC|turnCheckpointNoFollow, 0o600)
	if err != nil {
		return fmt.Errorf("open turn-snapshot temp: %w", err)
	}
	_ = f.Chmod(0o600) // O_CREATE perm is subject to umask; force-tighten.
	_, werr := f.Write(data)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("write turn-snapshot temp: %w", werr)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename turn-snapshot: %w", err)
	}
	return nil
}

// LoadTurnSnapshot reads a TurnSnapshot from path. A missing file returns
// (nil, nil) — "no snapshot" is the common, expected case, not an error. A
// present-but-unreadable or unknown-schema file returns an error rather than a
// silently mis-decoded snapshot.
func LoadTurnSnapshot(path string) (*TurnSnapshot, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read turn snapshot: %w", err)
	}
	var snap TurnSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return nil, fmt.Errorf("unmarshal turn snapshot: %w", err)
	}
	if snap.Schema != turnSnapshotSchema {
		return nil, fmt.Errorf("turn snapshot schema %d unsupported (want %d)", snap.Schema, turnSnapshotSchema)
	}
	return &snap, nil
}

// WorkTreeMatches reports whether the working directory's current git HEAD still
// equals the snapshot's captured SHA. An empty captured SHA (non-repo, or git
// unavailable when the snapshot was taken) matches unconditionally — the guard
// was never armed, so it cannot veto a resume.
func (snap *TurnSnapshot) WorkTreeMatches(workDir string) bool {
	if snap.WorkTreeSHA == "" {
		return true
	}
	return snap.WorkTreeSHA == captureWorkTreeSHA(workDir)
}

// maybeRestoreTurnCheckpoint attempts to resume this session from a durable turn
// snapshot at s.config.TurnCheckpointPath. It reports whether the session was
// restored (and therefore Run must skip conversation init + planning).
//
// It fails SAFE, never corrupt: a missing snapshot, a read/decode error, or a
// work-tree HEAD that has moved since the snapshot was taken all return false so
// the node runs fresh from scratch. The two failure cases (unreadable snapshot,
// stale work tree) emit a warning event — per CLAUDE.md we surface rather than
// swallow — and the stale-tree case removes the now-useless snapshot.
func (s *Session) maybeRestoreTurnCheckpoint() bool {
	if s.config.TurnCheckpointPath == "" {
		return false
	}
	snap, err := LoadTurnSnapshot(s.config.TurnCheckpointPath)
	if err != nil {
		s.emit(Event{Type: EventError, SessionID: s.id, Text: fmt.Sprintf("turn-checkpoint: cannot restore (%v); running node fresh", err)})
		return false
	}
	if snap == nil {
		return false // no prior snapshot — first run of this node
	}
	if !snap.WorkTreeMatches(s.config.WorkingDir) {
		s.emit(Event{Type: EventError, SessionID: s.id, Text: "turn-checkpoint: work tree moved since snapshot; running node fresh"})
		_ = os.Remove(s.config.TurnCheckpointPath)
		return false
	}
	s.RestoreFrom(snap)
	s.emit(Event{Type: EventCheckpoint, SessionID: s.id, Turn: snap.Turn, Text: fmt.Sprintf("turn-checkpoint: resumed mid-node at turn %d", snap.Turn+1)})
	return true
}

// persistTurnSnapshot writes the session's state as of the given completed turn
// to the durable snapshot path. No-op when the feature is off. A save failure is
// surfaced as a warning but does not fail the turn — losing durability degrades
// to node-boundary resume, it never breaks the run.
func (s *Session) persistTurnSnapshot(turn int) {
	if s.config.TurnCheckpointPath == "" {
		return
	}
	if err := s.Snapshot(turn, s.config.WorkingDir).Save(s.config.TurnCheckpointPath); err != nil {
		s.emit(Event{Type: EventError, SessionID: s.id, Text: fmt.Sprintf("turn-checkpoint: save failed (%v); continuing", err)})
	}
}

// clearTurnSnapshot removes the durable snapshot once the node reaches a terminal
// outcome, so a later loop-restart of the node begins fresh. No-op when the
// feature is off or no snapshot exists.
func (s *Session) clearTurnSnapshot() {
	if s.config.TurnCheckpointPath == "" {
		return
	}
	if err := os.Remove(s.config.TurnCheckpointPath); err != nil && !os.IsNotExist(err) {
		s.emit(Event{Type: EventError, SessionID: s.id, Text: fmt.Sprintf("turn-checkpoint: cleanup failed (%v)", err)})
	}
}

// captureWorkTreeSHA returns the git HEAD commit SHA for workDir, or "" when
// workDir is not a git repo, git is unavailable, or the command fails. Coarse and
// best-effort by design — see TurnSnapshot.WorkTreeSHA.
func captureWorkTreeSHA(workDir string) string {
	if workDir == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), gitSHATimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", workDir, "rev-parse", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
