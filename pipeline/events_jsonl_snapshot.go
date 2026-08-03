// ABOUTME: Close-time snapshot of the secure activity log to the legacy run-dir path.
// ABOUTME: Strips the integrity sentinel and refuses symlinked destinations (#213).
package pipeline

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// refuseIfSymlink errors when path exists and is a symlink. Missing
// paths are OK (the snapshot creates them). Any other error from Lstat
// propagates so the snapshot bails out rather than continuing on
// uncertain state.
func refuseIfSymlink(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s is a symlink", path)
	}
	return nil
}

// writeSnapshot copies the secure log to <artifactDir>/<runID>/activity.jsonl
// with sentinel prefixes stripped, so existing tooling (bundle export,
// git_artifacts, anything that greps run dirs) continues to find a
// readable JSONL file at the legacy path. Errors are returned for the
// caller's logging convenience but do not fail Close — the secure file
// stays authoritative regardless of snapshot health.
//
// Caller must hold h.mu.
func (h *JSONLEventHandler) writeSnapshot() error {
	if h.artifactDir == "" || h.runID == "" || h.securePath == "" {
		return nil
	}
	legacyPath, err := h.prepareSnapshotDest()
	if err != nil {
		return err
	}

	src, err := os.Open(h.securePath)
	if err != nil {
		return fmt.Errorf("snapshot open secure: %w", err)
	}
	defer src.Close()

	// O_NOFOLLOW (unix builds) refuses to traverse a symlink at the
	// destination — a tool subprocess that pre-creates the legacy path
	// as a symlink to a sensitive location cannot redirect our write.
	// O_TRUNC overwrites any plain-file scratch the subprocess left.
	//
	// Mode 0o600: post-#519 the mirror holds verbatim provider request
	// bodies and full untruncated tool stdout/stderr, so it must not be
	// world-readable — matching the secure log's 0600-in-0700 model
	// (#213, #525). The bundle-export/git_artifacts consumers read this
	// path as the same UID, so tightening the mode doesn't affect them.
	dst, err := os.OpenFile(legacyPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC|snapshotNoFollow, 0o600)
	if err != nil {
		return fmt.Errorf("snapshot open legacy: %w", err)
	}
	defer dst.Close()

	// Force-tighten: the 0o600 in OpenFile only applies at creation, so a
	// pre-existing mirror from a prior run (O_TRUNC reuses it) could
	// retain wider permissions. Best-effort — the underlying access
	// control is same-UID; the mode is defense-in-depth against other
	// local users.
	_ = os.Chmod(legacyPath, 0o600)

	return copyStrippingSentinel(src, dst)
}

// prepareSnapshotDest creates <artifactDir>/<runID> and returns the path the
// snapshot should be written to.
//
// Pre-flight: if a tool subprocess swapped <artifactDir>/<runID> for a symlink
// during the run, MkdirAll would silently follow it and OpenFile's O_NOFOLLOW
// only guards the final component — the snapshot would land at the attacker's
// chosen target. Lstat catches that. The TOCTOU window between this check and
// MkdirAll is small and the snapshot is best-effort: refusing on suspicion is
// safer than silently mirroring elsewhere. Same defense for artifactDir itself.
func (h *JSONLEventHandler) prepareSnapshotDest() (string, error) {
	legacyDir := filepath.Join(h.artifactDir, h.runID)
	if err := refuseIfSymlink(h.artifactDir); err != nil {
		return "", fmt.Errorf("snapshot dest unsafe: %w", err)
	}
	if err := refuseIfSymlink(legacyDir); err != nil {
		return "", fmt.Errorf("snapshot dest unsafe: %w", err)
	}
	if err := os.MkdirAll(legacyDir, 0o700); err != nil {
		return "", fmt.Errorf("snapshot mkdir: %w", err)
	}
	// Force-tighten: MkdirAll is subject to umask and a pre-existing dir
	// keeps its old mode, either of which can leave the run dir wider
	// than 0o700. The mirror inside holds sensitive request/tool payloads
	// (#519, #525), so re-chmod best-effort — same-UID access is the real
	// gate; the mode is defense-in-depth against other local users.
	_ = os.Chmod(legacyDir, 0o700)
	return filepath.Join(legacyDir, "activity.jsonl"), nil
}

// copyStrippingSentinel copies src to dst line by line, removing the
// ActivityLogSentinel prefix from each line.
//
// Uses bufio.Reader.ReadBytes('\n') instead of bufio.Scanner so arbitrarily
// long lines survive. Agent/LLM events can produce JSONL entries that exceed
// bufio.Scanner's 1 MiB default (e.g. long ContextSnapshot maps or aggregated
// tool stdout in captured content fields); Scanner would have silently dropped
// those by erroring at scan-time.
func copyStrippingSentinel(src io.Reader, dst io.Writer) error {
	w := bufio.NewWriter(dst)
	r := bufio.NewReaderSize(src, 64*1024)
	for {
		done, err := copySentinelLine(r, w)
		if err != nil {
			return err
		}
		if done {
			break
		}
	}
	if err := w.Flush(); err != nil {
		return fmt.Errorf("snapshot flush: %w", err)
	}
	return nil
}

// copySentinelLine copies one sentinel-stripped line from r to w. Returns
// done=true when the source is exhausted.
func copySentinelLine(r *bufio.Reader, w *bufio.Writer) (bool, error) {
	line, err := r.ReadBytes('\n')
	if len(line) > 0 {
		stripped := bytes.TrimPrefix(line, []byte(ActivityLogSentinel))
		if _, wErr := w.Write(stripped); wErr != nil {
			return false, fmt.Errorf("snapshot write: %w", wErr)
		}
	}
	if err == io.EOF {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("snapshot read: %w", err)
	}
	return false, nil
}
